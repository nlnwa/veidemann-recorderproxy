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
	"github.com/nlnwa/veidemann-api-go/frontier/v1"
	"github.com/nlnwa/veidemann-recorderproxy/constants"
	"github.com/nlnwa/veidemann-recorderproxy/errors"
	"github.com/nlnwa/veidemann-recorderproxy/serviceconnections"
	"github.com/opentracing/opentracing-go"
	otLog "github.com/opentracing/opentracing-go/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	"net/http"
	"net/url"
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
	done         chan *doneMsg
	complete     *completeMsg
	m            sync.Mutex
	bccCtx       context.Context
}

type completeMsg struct {
	cl  *frontier.CrawlLog
	err error
}

type doneMsg struct {
	resp *http.Response
	err  error
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
	b.done = make(chan *doneMsg)

	finish := func(resp *http.Response) {
		b.m.Lock()
		defer b.m.Unlock()
		if GetRecordContext(rc.ctx) != nil {
			var clStatusCode int32
			if b.complete != nil {
				if b.complete.err != nil {
					l.WithError(b.complete.err).Info("Session completed with error")
				}

				clStatusCode = b.complete.cl.StatusCode
				err := rc.saveCrawlLogUnlocked(b, b.complete.cl)

				if err != nil {
					l.WithError(err).Warn("Error writing to browser controller")
				}

				b.span.Finish()
			} else {
				l.Info("Browser controller client session canceled by client")

				clStatusCode = int32(errors.CanceledByBrowser)
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
			l := rc.log.WithField("clStatusCode", clStatusCode)
			if resp != nil {
				l = l.WithField("statusCode", resp.StatusCode)
			}
			l.Infof("Session completed")
			cancel()
		}
	}

	go func() {
		for {
			select {
			case <-rc.ctx.Done():
				finish(nil)
				return
			case doneMsg := <-b.done:
				if doneMsg.err == nil {
					l.Debugf("Finished sending response downstream.")
				} else {
					l.Debugf("Finished sending response downstream with error.")
				}
				finish(doneMsg.resp)
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

func (rc *RecordContext) ResponseCompleted(resp *http.Response, writeErr error) {
	if b, err := rc.getBccSession(); err == nil {
		b.done <- &doneMsg{resp: resp, err: err}
	}
	if writeErr != nil {
		rc.CancelContentWriter("Veidemann recorder proxy lost connection to client")
	}
}

func (rc *RecordContext) WaitForCompleted() {
	if rc.cwc != nil {
		select {
		case <-rc.cwc.cwcCtx.Done():
		}
	}
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
	cl.IpAddress = GetIp(rc.ctx)

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
			l.Debugf("Trying to send error, but another error was already sent. Previous error: %v, new error %v\n", bb.complete.err, reqErr)
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
		l.Panic("BUG: SendResponseError with nil error")
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
				Method:           rc.Method,
				Uri:              rc.Uri.String(),
				RequestId:        GetRequestId(rc.ctx),
				CrawlExecutionId: GetCrawlExecutionId(rc.ctx),
				JobExecutionId:   GetJobExecutionId(rc.ctx),
				CollectionRef:    GetCollectionRef(rc.ctx),
			},
		},
	}

	lf := []otLog.Field{
		otLog.String("event", "Send BrowserController New request"),
		otLog.Int32("ProxyId", rc.ProxyId),
		otLog.String("Uri", rc.Uri.String()),
		otLog.String("RequestId", GetRequestId(rc.ctx)),
		otLog.String("CrawlExecutionId", GetCrawlExecutionId(rc.ctx)),
		otLog.String("JobExecutionId", GetJobExecutionId(rc.ctx)),
	}
	if GetCollectionRef(rc.ctx) != nil {
		lf = append(lf, otLog.String("CollectionId", GetCollectionRef(rc.ctx).Id))
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
		SetJobExecutionId(rc.ctx, v.New.JobExecutionId)
		SetCrawlExecutionId(rc.ctx, v.New.CrawlExecutionId)
		SetCollectionRef(rc.ctx, v.New.CollectionRef)
		rc.ReplacementScript = v.New.ReplacementScript

		rc.CrawlLog.JobExecutionId = GetJobExecutionId(rc.ctx)
		rc.CrawlLog.ExecutionId = GetCrawlExecutionId(rc.ctx)
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

func RegisterConnectRequest(ctx filters.Context, conn *serviceconnections.Connections, proxyId int32, req *http.Request, uri *url.URL) {
	l := LogWithContext(ctx, "PROXY:BCC")

	resolveIdsFromHttpHeader(ctx, req)

	bccRequest := &browsercontroller.DoRequest{
		Action: &browsercontroller.DoRequest_New{
			New: &browsercontroller.RegisterNew{
				ProxyId:          proxyId,
				Method:           "CONNECT",
				Uri:              uri.String(),
				RequestId:        GetRequestId(ctx),
				CrawlExecutionId: GetCrawlExecutionId(ctx),
				JobExecutionId:   GetJobExecutionId(ctx),
				CollectionRef:    GetCollectionRef(ctx),
			},
		},
	}

	bccCtx, cancel := context.WithCancel(context.Background())
	bcc, err := conn.BrowserControllerClient().Do(bccCtx)
	if err != nil {
		l.WithError(err).Warn("Error connecting to browser controller")
		err = errors.WrapInternalError(err, errors.RuntimeException, "Error connecting to browser controller", err.Error())
		cancel()
		return
	}

	err = bcc.Send(bccRequest)
	if err != nil {
		l.WithError(err).Info("Error register with browser controller")
		err = errors.WrapInternalError(err, errors.RuntimeException, "Error register with browser controller", err.Error())
	}
	doReply, err := bcc.Recv()

	switch v := doReply.Action.(type) {
	case *browsercontroller.DoReply_New:
		SetJobExecutionId(ctx, v.New.JobExecutionId)
		SetCrawlExecutionId(ctx, v.New.CrawlExecutionId)
		SetCollectionRef(ctx, v.New.CollectionRef)
	}

	_ = bcc.CloseSend()
	for {
		_, e := bcc.Recv()
		if e != nil {
			break
		}
	}
	cancel()
}
