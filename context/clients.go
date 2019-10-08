/*
 * Copyright 2019 National Library of Norway.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *       http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package context

import (
	"context"
	errors2 "errors"
	"fmt"
	"github.com/getlantern/proxy/filters"
	"github.com/nlnwa/veidemann-api-go/browsercontroller/v1"
	"github.com/nlnwa/veidemann-api-go/contentwriter/v1"
	"github.com/nlnwa/veidemann-api-go/frontier/v1"
	"github.com/nlnwa/veidemann-recorderproxy/constants"
	"github.com/nlnwa/veidemann-recorderproxy/errors"
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	otLog "github.com/opentracing/opentracing-go/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var AlreadyCompleted = errors2.New("already completed")

type BccSession struct {
	browsercontroller.BrowserController_DoClient
	msgChan      chan *browsercontroller.DoReply
	completeChan chan *completeMsg
	span         opentracing.Span
	done         chan bool
	complete     *completeMsg
	m            sync.Mutex
	bccCtx       context.Context
}

type completeMsg struct {
	cl  *frontier.CrawlLog
	err error
}

func (rc *RecordContext) getBccSession() (*BccSession, error) {
	if rc.bcc != nil {
		return rc.bcc, nil
	}

	l := LogWithContext(rc.ctx, "PROXY:BCC")

	rc.mutex.Lock()
	defer rc.mutex.Unlock()

	parentSpan := opentracing.SpanFromContext(rc.ctx)
	span := opentracing.StartSpan("Browser controller session", opentracing.FollowsFrom(parentSpan.Context()))
	bccCtx, cancel := context.WithCancel(context.Background())
	bccCtx = opentracing.ContextWithSpan(bccCtx, span)

	bcc, err := rc.conn.BrowserControllerClient().Do(bccCtx)
	span.LogFields(otLog.String("event", "Started BrowserControllerClient session"), otLog.Error(err))
	if err != nil {
		l.WithError(err).Warn("Error connecting to browser controller")
		span.LogFields(otLog.String("event", "Failed starting BrowserControllerClient session"), otLog.Error(err))
		err = errors.WrapInternalError(err, errors.RuntimeException, "Error connecting to browser controller", err.Error())
		cancel()
		return nil, err
	}

	b := &BccSession{BrowserController_DoClient: bcc, span: span, bccCtx: bccCtx}

	b.msgChan = make(chan *browsercontroller.DoReply)
	b.completeChan = make(chan *completeMsg)
	b.done = make(chan bool)

	finish := func() {
		b.m.Lock()
		defer b.m.Unlock()
		if GetRecordContext(rc.ctx) != nil {
			if b.complete != nil {
				if b.complete.err != nil {
					l.WithError(b.complete.err).Info("Session completed with error")
				}

				err := rc.saveCrawlLogUnlocked(b, b.complete.cl)

				if err != nil {
					l.WithError(err).Warn("Error writing to browser controller")
				}

				b.span.Finish()
			} else {
				l.Info("Browser controller client session canceled by client")

				rc.CrawlLog.StatusCode = int32(errors.CanceledByBrowser)
				rc.CrawlLog.RecordType = constants.RecordResponse
				rc.CrawlLog.ContentType = ""
				e := errors.Error(errors.CanceledByBrowser, "CANCELED_BY_BROWSER", "Veidemann recorder proxy lost connection to client")
				rc.CrawlLog.Error = errors.AsCommonsError(e)

				err := rc.saveCrawlLogUnlocked(b, rc.CrawlLog)

				if err != nil {
					l.WithError(err).Warn("Error writing to browser controller")
				}

				b.span.Finish()
			}
			SetRecordContext(rc.ctx, nil)
			atomic.AddInt64(&closedSess, 1)
			rc.log.Infof("Session completed")
			cancel()
		}
	}

	go func() {
		for {
			select {
			case <-rc.ctx.Done():
				finish()
				return
			case normalExit := <-b.done:
				if normalExit {
					l.Debugf("Finished sending response downstream.")
				} else {
					l.Debugf("Finished sending response downstream with error.")
				}
				finish()
				return
			case completeMsg := <-b.completeChan:
				if b.complete != nil {
					if b.complete.err != nil {
						l.Debugf("Session completed with error '%v', but error '%v' was already sent", completeMsg.err, b.complete.err)
					}
				} else {
					b.complete = completeMsg
				}
			}
		}
	}()

	// Handle messages from browser controller
	go func() {
		for {
			doReply, err := bcc.Recv()
			if err == io.EOF {
				// read done.
				b.msgChan <- doReply
				close(b.msgChan)
				return
			}
			serr := status.Convert(err)
			if serr.Code() == codes.Canceled {
				l.Debugf("context canceled %v\n", serr)
				b.msgChan <- doReply
				close(b.msgChan)
				return
			}
			if serr.Code() == codes.DeadlineExceeded {
				l.Debugf("context deadline exeeded %v\n", err)
				b.msgChan <- doReply
				close(b.msgChan)
				return
			}
			if err != nil {
				l.Warnf("unknown error from browser controller %v, %v, %v\n", doReply, err, serr)
				rc.Error = fmt.Errorf("unknown error from browser controller: %v", err.Error())
				b.msgChan <- &browsercontroller.DoReply{Action: &browsercontroller.DoReply_Cancel{Cancel: rc.Error.Error()}}
				close(b.msgChan)
				return
			}
			switch doReply.Action.(type) {
			case *browsercontroller.DoReply_Cancel:
				if strings.Contains(doReply.GetCancel(), "robots.txt") {
					b.msgChan <- doReply
				} else {
					l.Info("Browser controller client session canceled by browser controller")
					_ = rc.CancelContentWriter("canceled by browser controller")

					rc.CrawlLog.StatusCode = int32(errors.CanceledByBrowser)
					rc.CrawlLog.RecordType = constants.RecordResponse
					rc.CrawlLog.ContentType = ""
					e := errors.Error(errors.CanceledByBrowser, "CANCELED_BY_BROWSER", "canceled by browser controller")
					rc.CrawlLog.Error = errors.AsCommonsError(e)

					cl := *rc.CrawlLog
					msg := &completeMsg{cl: &cl, err: e}
					b.completeChan <- msg
				}
			default:
				b.msgChan <- doReply
			}
		}
	}()

	rc.bcc = b
	span.LogFields(otLog.String("event", "Started BrowserControllerClient session"))
	return b, nil
}

type CwcSession struct {
	contentwriter.ContentWriter_WriteClient
	span     opentracing.Span
	done     bool
	canceled bool
	m        sync.Mutex
}

func (rc *RecordContext) getCwcSession() (*CwcSession, error) {
	if rc.cwc != nil {
		return rc.cwc, nil
	}

	l := LogWithContext(rc.ctx, "PROXY:CWC")

	rc.mutex.Lock()
	defer rc.mutex.Unlock()

	parentSpan := opentracing.SpanFromContext(rc.ctx)
	span := opentracing.StartSpan("ContentWriter session", opentracing.FollowsFrom(parentSpan.Context()))
	cwcCtx, cancel := context.WithCancel(context.Background())
	cwcCtx = opentracing.ContextWithSpan(cwcCtx, span)

	cwc, err := rc.conn.ContentWriterClient().Write(cwcCtx)
	span.LogFields(otLog.String("event", "Started ContentWriter client session"), otLog.Error(err))
	if err != nil {
		l.WithError(err).Warn("Error connecting to ContentWriter")
		span.LogFields(otLog.String("event", "Failed starting ContentWriter session"), otLog.Error(err))
		err = errors.WrapInternalError(err, errors.RuntimeException, "Error connecting to ContentWriter", err.Error())
		cancel()
		return nil, err
	}

	c := &CwcSession{ContentWriter_WriteClient: cwc, span: span}

	go func() {
		select {
		case <-rc.ctx.Done():
			c.m.Lock()
			defer c.m.Unlock()
			if !c.done {
				c.done = true
				l.Info("ContentWriter client session canceled by client")
				err := rc.cwc.Send(&contentwriter.WriteRequest{
					Value: &contentwriter.WriteRequest_Cancel{Cancel: "Veidemann recorder proxy lost connection to client"},
				})
				if err != nil {
					l.WithError(err).Warn("Error writing to ContentWriter")
				}
				_, err = rc.cwc.CloseAndRecv()
				if err != nil {
					l.WithError(err).Warn("Error closing from ContentWriter")
				}
				span.Finish()
			}
			cancel()
		}
	}()

	rc.cwc = c
	span.LogFields(otLog.String("event", "Started ContentWriter session"))
	return c, nil
}

func (rc *RecordContext) ResponseCompleted(writeErr error) {
	if b, err := rc.getBccSession(); err == nil {
		if writeErr == nil {
			b.done <- true
		} else {
			b.done <- false
		}
	}
}

func (rc *RecordContext) WaitForCompleted() {
	if rc.bcc != nil {
		select {
		case <-rc.bcc.bccCtx.Done():
		}
	}
}

func (rc *RecordContext) SaveCrawlLog() error {
	b, err := rc.getBccSession()
	if err != nil {
		return err
	}

	b.m.Lock()
	defer b.m.Unlock()

	if b.complete != nil {
		return AlreadyCompleted
	}

	cl := *rc.CrawlLog
	msg := &completeMsg{cl: &cl}
	b.completeChan <- msg
	return nil
}

func (rc *RecordContext) saveCrawlLogUnlocked(b *BccSession, cl *frontier.CrawlLog) (err error) {
	l := LogWithContext(rc.ctx, "PROXY:BCC")

	fetchDurationMs := time.Now().Sub(rc.FetchTimesTamp).Nanoseconds() / 1000000
	cl.FetchTimeMs = fetchDurationMs
	cl.IpAddress = rc.IP

	err = b.BrowserController_DoClient.Send(&browsercontroller.DoRequest{
		Action: &browsercontroller.DoRequest_Completed{
			Completed: &browsercontroller.Completed{
				CrawlLog: cl,
				Cached:   rc.FoundInCache,
			},
		},
	})
	if err != nil {
		l.WithError(err).Info("Error sending crawl log to browser controller")
	}
	err = b.CloseSend()
	if err != nil {
		l.WithError(err).Info("Error closing browser controller client")
	}

	for range b.msgChan {
	}

	return err
}

func (rc *RecordContext) SendRequestError(ctx filters.Context, reqErr error) error {
	l := LogWithContext(rc.ctx, "PROXY:BCC")

	if reqErr == nil {
		l.Panic("BUG: SendRequestError with nil error")
	}

	bb, _ := rc.getBccSession()
	if bb.complete != nil {
		if bb.complete.err != nil {
			fmt.Printf("EXISTING ERROR *** %v, NEW ERROR *** %v\n", bb.complete.err, reqErr)
			return bb.complete.err
		} else {
			return reqErr
		}
	}

	err := rc.NotifyAllDataReceived()
	if err != nil {
		return errors.WrapInternalError(err, errors.RuntimeException, "error notifying browser controller", err.Error())
	}

	b, err := rc.getBccSession()
	if err != nil {
		return err
	}

	b.m.Lock()
	defer b.m.Unlock()

	if b.complete != nil {
		return errors.WrapInternalError(AlreadyCompleted, errors.RuntimeException, "error sending crawl log to browser controller", AlreadyCompleted.Error())
	}

	rc.CrawlLog.StatusCode = int32(errors.Code(reqErr))
	rc.CrawlLog.RecordType = constants.RecordResponse
	rc.CrawlLog.Error = errors.AsCommonsError(reqErr)

	cl := *rc.CrawlLog
	msg := &completeMsg{cl: &cl, err: reqErr}
	b.completeChan <- msg
	return reqErr
}

func (rc *RecordContext) SendResponseError(ctx filters.Context, respErr error) error {
	l := LogWithContext(rc.ctx, "PROXY:BCC")

	if respErr == nil {
		l.Panic("BUG: SendRequestError with nil error")
	}

	b, err := rc.getBccSession()
	if err != nil {
		return err
	}

	b.m.Lock()
	defer b.m.Unlock()

	if b.complete != nil {
		return errors.WrapInternalError(AlreadyCompleted, errors.RuntimeException, "error sending crawl log to browser controller", AlreadyCompleted.Error())
	}

	rc.CrawlLog.StatusCode = int32(errors.Code(respErr))
	rc.CrawlLog.RecordType = constants.RecordResponse
	rc.CrawlLog.ContentType = ""
	rc.CrawlLog.Error = errors.AsCommonsError(respErr)

	cl := *rc.CrawlLog
	msg := &completeMsg{cl: &cl, err: respErr}
	b.completeChan <- msg
	return respErr
}

func (rc *RecordContext) RegisterNewRequest(ctx filters.Context) error {
	b, err := rc.getBccSession()
	if err != nil {
		return err
	}

	l := LogWithContext(rc.ctx, "PROXY:BCC")

	b.m.Lock()
	defer b.m.Unlock()

	if b.complete != nil {
		return AlreadyCompleted
	}

	bccRequest := &browsercontroller.DoRequest{
		Action: &browsercontroller.DoRequest_New{
			New: &browsercontroller.RegisterNew{
				ProxyId:          rc.ProxyId,
				Uri:              rc.Uri.String(),
				CrawlExecutionId: rc.CrawlExecutionId,
				JobExecutionId:   rc.JobExecutionId,
				CollectionRef:    rc.CollectionRef,
			},
		},
	}

	lf := []otLog.Field{
		otLog.String("event", "Send BrowserController New request"),
		otLog.Int32("ProxyId", rc.ProxyId),
		otLog.String("Uri", rc.Uri.String()),
		otLog.String("CrawlExecutionId", rc.CrawlExecutionId),
		otLog.String("JobExecutionId", rc.JobExecutionId),
	}
	if rc.CollectionRef != nil {
		lf = append(lf, otLog.String("CollectionId", rc.CollectionRef.Id))
	} else {
		lf = append(lf, otLog.String("CollectionId", ""))
	}
	b.span.LogFields(lf...)

	err = b.Send(bccRequest)
	if err != nil {
		l.WithError(err).Info("Error register with browser controller")
		err = errors.WrapInternalError(err, errors.RuntimeException, "Error register with browser controller", err.Error())
	}

	bcReply := <-b.msgChan

	switch v := bcReply.Action.(type) {
	case *browsercontroller.DoReply_Cancel:
		if v.Cancel == "Blocked by robots.txt" {
			rc.PrecludedByRobots = true
		}
		b.span.LogKV("event", "ResponseFromNew", "responseType", "Cancel")
		return errors.Error(errors.PrecludedByRobots, "PRECLUDED_BY_ROBOTS", "Robots.txt rules precluded fetch")
	case *browsercontroller.DoReply_New:
		b.span.LogKV("event", "ResponseFromNew", "responseType", "New",
			"JobExecutionId", v.New.JobExecutionId,
			"CrawlExecutionId", v.New.CrawlExecutionId,
			"CollectionId", v.New.CollectionRef.Id,
			"ReplacementScript", v.New.ReplacementScript,
		)
		rc.CrawlExecutionId = v.New.CrawlExecutionId
		rc.JobExecutionId = v.New.GetJobExecutionId()
		rc.CollectionRef = v.New.CollectionRef
		rc.ReplacementScript = v.New.ReplacementScript

		rc.CrawlLog.JobExecutionId = rc.JobExecutionId
		rc.CrawlLog.ExecutionId = rc.CrawlExecutionId
	}

	return nil
}

func (rc *RecordContext) NotifyDataReceived() error {
	return rc.notifyDataReceived(browsercontroller.NotifyActivity_DATA_RECEIVED)
}

func (rc *RecordContext) NotifyAllDataReceived() error {
	return rc.notifyDataReceived(browsercontroller.NotifyActivity_ALL_DATA_RECEIVED)
}

func (rc *RecordContext) notifyDataReceived(activity browsercontroller.NotifyActivity_Activity) error {
	b, err := rc.getBccSession()
	if err != nil {
		b.span.LogFields(otLog.String("event", "Notify data received"), otLog.Error(err))
		return err
	}

	l := LogWithContext(rc.ctx, "PROXY:BCC")

	b.m.Lock()
	defer b.m.Unlock()

	if b.complete != nil {
		return AlreadyCompleted
	}

	err = b.Send(&browsercontroller.DoRequest{
		Action: &browsercontroller.DoRequest_Notify{
			Notify: &browsercontroller.NotifyActivity{
				Activity: activity,
			},
		},
	})
	if err != nil {
		b.span.LogFields(otLog.String("event", "Notify data received"), otLog.Error(err))
		l.WithError(err).Infof("Error notify data sent to browser controller, Activity: %v", activity)
	} else {
		b.span.LogFields(otLog.String("event", "Notify data received"))
	}
	return err
}

func (rc *RecordContext) CancelContentWriter(msg string) error {
	if rc.cwc == nil {
		// No ContentWriter session to cancel
		return nil
	}

	l := LogWithContext(rc.ctx, "PROXY:CWC")

	cwc, err := rc.getCwcSession()
	cwc.canceled = true
	if err != nil {
		cwc.span.LogFields(otLog.String("event", "Cancel content writer"), otLog.String("message", msg), otLog.Error(err))
		return err
	}

	cwc.m.Lock()
	defer cwc.m.Unlock()
	if !cwc.done {
		cwc.done = true

		err = cwc.Send(&contentwriter.WriteRequest{Value: &contentwriter.WriteRequest_Cancel{Cancel: msg}})
		if err != nil {
			cwc.span.LogFields(otLog.String("event", "Cancel content writer"), otLog.String("message", msg), otLog.Error(err))
			l.WithError(err).Info("Error sending ContentWriter cancel")
		}
		reply, err := cwc.CloseAndRecv()
		if err != nil {
			cwc.span.LogFields(otLog.String("event", "Cancel content writer"), otLog.String("message", msg), otLog.Error(err))
			l.WithError(err).Info("Error sending ContentWriter cancel")
		} else {
			cwc.span.LogFields(otLog.String("event", "Cancel content writer"), otLog.String("message", msg), otLog.String("reply", reply.String()))
		}
	}
	return err
}

func (rc *RecordContext) SendProtocolHeader(recNum int32, p []byte) error {
	l := LogWithContext(rc.ctx, "PROXY:CWC")

	otEvent := otLog.String("event", "sendProtocolHeader")
	otRecNum := otLog.Int32("recNum", recNum)

	cwc, err := rc.getCwcSession()
	if err != nil {
		cwc.span.LogFields(otEvent, otRecNum, otLog.Error(err))
		return err
	}

	if cwc.canceled {
		return nil
	}

	protocolHeaderRequest := &contentwriter.WriteRequest{
		Value: &contentwriter.WriteRequest_ProtocolHeader{
			ProtocolHeader: &contentwriter.Data{
				RecordNum: recNum,
				Data:      p,
			},
		},
	}

	err = cwc.Send(protocolHeaderRequest)
	if err != nil {
		l.WithError(err).Info("Error sending ContentWriter protocol header")
		cwc.span.LogFields(otEvent, otRecNum, otLog.Error(err))
	} else {
		cwc.span.LogFields(otEvent, otRecNum)
	}
	return err
}

func (rc *RecordContext) SendPayload(recNum int32, p []byte) error {
	l := LogWithContext(rc.ctx, "PROXY:CWC")

	otEvent := otLog.String("event", "sendPayload")
	otRecNum := otLog.Int32("recNum", recNum)

	cwc, err := rc.getCwcSession()
	if err != nil {
		cwc.span.LogFields(otEvent, otRecNum, otLog.Error(err))
		return err
	}

	if cwc.canceled {
		return nil
	}

	payloadRequest := &contentwriter.WriteRequest{
		Value: &contentwriter.WriteRequest_Payload{
			Payload: &contentwriter.Data{
				RecordNum: recNum,
				Data:      p,
			},
		},
	}

	err = cwc.Send(payloadRequest)
	if err != nil {
		l.WithError(err).Info("Error sending ContentWriter payload")
		cwc.span.LogFields(otEvent, otRecNum, otLog.Error(err))
	} else {
		cwc.span.LogFields(otEvent, otRecNum)
	}
	return err
}

func (rc *RecordContext) SendMeta() (reply *contentwriter.WriteReply, err error) {
	l := LogWithContext(rc.ctx, "PROXY:CWC")

	cwc, err := rc.getCwcSession()
	if err != nil {
		cwc.span.LogFields(otLog.String("event", "sendMeta"), otLog.String("http.url", rc.Meta.Meta.TargetUri), otLog.Error(err))
		return nil, err
	}

	if cwc.canceled {
		return nil, nil
	}

	cwc.m.Lock()
	defer cwc.m.Unlock()
	if !cwc.done {
		cwc.done = true
		sendMetaSpan := opentracing.StartSpan("ContentWriter sendMeta", opentracing.ChildOf(cwc.span.Context()))
		defer sendMetaSpan.Finish()
		ext.HTTPUrl.Set(sendMetaSpan, rc.Meta.Meta.TargetUri)
		ext.Component.Set(sendMetaSpan, "contentWriterClient")

		metaRequest := &contentwriter.WriteRequest{
			Value: rc.Meta,
		}

		err = cwc.Send(metaRequest)
		if err != nil {
			l.WithError(err).Info("Error sending ContentWriter meta")
			ext.Error.Set(sendMetaSpan, true)
			sendMetaSpan.LogFields(otLog.String("event", "sendMeta"), otLog.Error(err))
			return nil, err
		}

		reply, err = cwc.CloseAndRecv()
		if err != nil {
			l.WithError(err).Info("Error receiving ContentWriter meta response")
			ext.Error.Set(sendMetaSpan, true)
			cwc.span.LogFields(otLog.String("event", "receiveMeta"), otLog.Error(err))
		}

		cwc.span.Finish()
	}
	return
}
