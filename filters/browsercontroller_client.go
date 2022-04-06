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

package filters

import (
	"context"
	errors2 "errors"
	"fmt"
	"github.com/nlnwa/veidemann-api/go/browsercontroller/v1"
	logV1 "github.com/nlnwa/veidemann-api/go/log/v1"
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
	cl  *logV1.CrawlLog
	err error
}

type doneMsg struct {
	resp *http.Response
	err  error
}

func (cs *ConnectionState) getBccSession() (*BccSession, error) {
	if cs.bcc != nil {
		return cs.bcc, nil
	}

	l := cs.LogWithContext("PROXY:BCC")

	cs.mutex.Lock()
	defer cs.mutex.Unlock()

	//cs.ctx = context.TODO()
	//parentSpan := opentracing.SpanFromContext(cs.ctx)
	//span := opentracing.StartSpan("Browser controller session", opentracing.FollowsFrom(parentSpan.Context()))
	span := opentracing.StartSpan("Browser controller session")
	bccCtx, cancel := context.WithCancel(context.Background())
	bccCtx = opentracing.ContextWithSpan(bccCtx, span)

	bcc, err := cs.conn.BrowserControllerClient().Do(bccCtx)
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
		var clStatusCode int32
		if b.complete != nil {
			if b.complete.err != nil {
				l.WithError(b.complete.err).Info("Session completed with error")
			}

			clStatusCode = b.complete.cl.StatusCode
			err := cs.saveCrawlLogUnlocked(b, b.complete.cl)

			if err != nil {
				l.WithError(err).Warn("Error writing to browser controller")
			}

			b.span.Finish()
		} else {
			l.Info("Browser controller client session canceled by client")

			clStatusCode = int32(errors.CanceledByBrowser)
			cs.CrawlLog.StatusCode = int32(errors.CanceledByBrowser)
			cs.CrawlLog.RecordType = constants.RecordResponse
			cs.CrawlLog.ContentType = ""
			e := errors.Error(errors.CanceledByBrowser, "CANCELED_BY_BROWSER", "Veidemann recorder proxy lost connection to client")
			cs.CrawlLog.Error = errors.AsCommonsError(e)

			err := cs.saveCrawlLogUnlocked(b, cs.CrawlLog)

			if err != nil {
				l.WithError(err).Warn("Error writing to browser controller")
			}

			b.span.Finish()
		}
		atomic.AddInt64(&closedSess, 1)
		l := cs.log.WithField("clStatusCode", clStatusCode)
		if resp != nil {
			l = l.WithField("statusCode", resp.StatusCode)
		}
		l.Infof("Session completed")
		cancel()
	}

	go func() {
		for {
			select {
			case <-cs.ctx.Done():
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
			if serr.Code() == codes.Unavailable {
				l.Debugf("browser controller unavailable: %v", err)
				b.msgChan <- doReply
				close(b.msgChan)
				return
			}
			if err != nil {
				l.Warnf("unknown error from browser controller %v, %v, %v\n", doReply, err, serr)
				cs.Error = fmt.Errorf("unknown error from browser controller: %v", err.Error())
				b.msgChan <- &browsercontroller.DoReply{Action: &browsercontroller.DoReply_Cancel{Cancel: cs.Error.Error()}}
				close(b.msgChan)
				return
			}
			switch doReply.Action.(type) {
			case *browsercontroller.DoReply_Cancel:
				if strings.Contains(doReply.GetCancel(), "robots.txt") {
					b.msgChan <- doReply
				} else {
					l.Info("Browser controller client session canceled by browser controller")
					_ = cs.CancelContentWriter("canceled by browser controller")

					cs.CrawlLog.StatusCode = int32(errors.CanceledByBrowser)
					cs.CrawlLog.RecordType = constants.RecordResponse
					cs.CrawlLog.ContentType = ""
					e := errors.Error(errors.CanceledByBrowser, "CANCELED_BY_BROWSER", "canceled by browser controller")
					cs.CrawlLog.Error = errors.AsCommonsError(e)

					cl := *cs.CrawlLog
					msg := &completeMsg{cl: &cl, err: e}
					b.completeChan <- msg
				}
			default:
				b.msgChan <- doReply
			}
		}
	}()

	cs.bcc = b
	span.LogFields(otLog.String("event", "Started BrowserControllerClient session"))
	return b, nil
}

func (cs *ConnectionState) ResponseCompleted(resp *http.Response, writeErr error) {
	if b, err := cs.getBccSession(); err == nil {
		b.done <- &doneMsg{resp: resp, err: err}
	}
	if writeErr != nil {
		cs.CancelContentWriter("Veidemann recorder proxy lost connection to client")
	}
}

func (cs *ConnectionState) WaitForCompleted() {
	if cs.cwc != nil {
		select {
		case <-cs.cwc.cwcCtx.Done():
		}
	}
	if cs.bcc != nil {
		select {
		case <-cs.bcc.bccCtx.Done():
		}
	}
}

func (cs *ConnectionState) SaveCrawlLog() error {
	b, err := cs.getBccSession()
	if err != nil {
		return err
	}

	b.m.Lock()
	defer b.m.Unlock()

	if b.complete != nil {
		return AlreadyCompleted
	}

	cl := *cs.CrawlLog
	msg := &completeMsg{cl: &cl}
	b.completeChan <- msg
	return nil
}

func (cs *ConnectionState) saveCrawlLogUnlocked(b *BccSession, cl *logV1.CrawlLog) (err error) {
	l := cs.LogWithContext("PROXY:BCC")

	fetchDurationMs := time.Now().Sub(cs.FetchTimesTamp).Nanoseconds() / 1000000
	cl.FetchTimeMs = fetchDurationMs
	cl.IpAddress = cs.Ip

	err = b.BrowserController_DoClient.Send(&browsercontroller.DoRequest{
		Action: &browsercontroller.DoRequest_Completed{
			Completed: &browsercontroller.Completed{
				CrawlLog: cl,
				Cached:   cs.FoundInCache,
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

func (cs *ConnectionState) SendRequestError(reqErr error) error {
	l := cs.LogWithContext("PROXY:BCC")

	if reqErr == nil {
		l.Panic("BUG: SendRequestError with nil error")
	}

	b, err := cs.getBccSession()
	if err != nil {
		return err
	} else {
		if b.complete != nil {
			if b.complete.err != nil {
				l.Debugf("Trying to send error, but another error was already sent. Previous error: %v, new error %v\n", b.complete.err, reqErr)
				return b.complete.err
			} else {
				return reqErr
			}
		}
	}

	err = cs.NotifyAllDataReceived()
	if err != nil {
		return errors.WrapInternalError(err, errors.RuntimeException, "error notifying browser controller", err.Error())
	}

	b.m.Lock()
	defer b.m.Unlock()

	if b.complete != nil {
		return errors.WrapInternalError(AlreadyCompleted, errors.RuntimeException, "error sending crawl log to browser controller", AlreadyCompleted.Error())
	}

	cs.CrawlLog.StatusCode = int32(errors.Code(reqErr))
	cs.CrawlLog.RecordType = constants.RecordResponse
	cs.CrawlLog.Error = errors.AsCommonsError(reqErr)

	cl := *cs.CrawlLog
	msg := &completeMsg{cl: &cl, err: reqErr}
	b.completeChan <- msg
	return reqErr
}

func (cs *ConnectionState) SendResponseError(respErr error) error {
	l := cs.LogWithContext("PROXY:BCC")

	if respErr == nil {
		l.Panic("BUG: SendResponseError with nil error")
	}

	b, err := cs.getBccSession()
	if err != nil {
		return err
	}

	b.m.Lock()
	defer b.m.Unlock()

	if b.complete != nil {
		return errors.WrapInternalError(AlreadyCompleted, errors.RuntimeException, "error sending crawl log to browser controller", AlreadyCompleted.Error())
	}

	cs.CrawlLog.StatusCode = int32(errors.Code(respErr))
	cs.CrawlLog.RecordType = constants.RecordResponse
	cs.CrawlLog.ContentType = ""
	cs.CrawlLog.Error = errors.AsCommonsError(respErr)

	cl := *cs.CrawlLog
	msg := &completeMsg{cl: &cl, err: respErr}
	b.completeChan <- msg
	return respErr
}

func (cs *ConnectionState) RegisterNewRequest() error {
	b, err := cs.getBccSession()
	if err != nil {
		return err
	}

	l := cs.LogWithContext("PROXY:BCC")

	b.m.Lock()
	defer b.m.Unlock()

	if b.complete != nil {
		return AlreadyCompleted
	}

	bccRequest := &browsercontroller.DoRequest{
		Action: &browsercontroller.DoRequest_New{
			New: &browsercontroller.RegisterNew{
				ProxyId:          cs.ProxyId,
				Method:           cs.Method,
				Uri:              cs.Uri.String(),
				RequestId:        cs.RequestId,
				CrawlExecutionId: cs.CrawlExecId,
				JobExecutionId:   cs.JobExecId,
				CollectionRef:    cs.CollectionRef,
			},
		},
	}

	lf := []otLog.Field{
		otLog.String("event", "Send BrowserController New request"),
		otLog.Int32("ProxyId", cs.ProxyId),
		otLog.String("Uri", cs.Uri.String()),
		otLog.String("RequestId", cs.RequestId),
		otLog.String("CrawlExecutionId", cs.CrawlExecId),
		otLog.String("JobExecutionId", cs.JobExecId),
	}
	if cs.CollectionRef != nil {
		lf = append(lf, otLog.String("CollectionId", cs.CollectionRef.Id))
	} else {
		lf = append(lf, otLog.String("CollectionId", ""))
	}
	b.span.LogFields(lf...)

	err = b.Send(bccRequest)
	if err != nil {
		l.WithError(err).Info("Error register with browser controller")
		err = errors.WrapInternalError(err, errors.RuntimeException, "Error register with browser controller", err.Error())
	}

	bcReply, ok := <-b.msgChan
	if !ok || bcReply == nil {
		return errors.Error(errors.CanceledByBrowser, "CANCELLED_BY_BROWSER", "Browser controller closed connection")
	}
	switch v := bcReply.Action.(type) {
	case *browsercontroller.DoReply_Cancel:
		if v.Cancel == "Blocked by robots.txt" {
			cs.PrecludedByRobots = true
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
		cs.JobExecId = v.New.JobExecutionId
		cs.CrawlExecId = v.New.CrawlExecutionId
		cs.CollectionRef = v.New.CollectionRef
		cs.ReplacementScript = v.New.ReplacementScript

		cs.CrawlLog.JobExecutionId = cs.JobExecId
		cs.CrawlLog.ExecutionId = cs.CrawlExecId
	}

	return nil
}

func (cs *ConnectionState) NotifyDataReceived() error {
	return cs.notifyDataReceived(browsercontroller.NotifyActivity_DATA_RECEIVED)
}

func (cs *ConnectionState) NotifyAllDataReceived() error {
	return cs.notifyDataReceived(browsercontroller.NotifyActivity_ALL_DATA_RECEIVED)
}

func (cs *ConnectionState) notifyDataReceived(activity browsercontroller.NotifyActivity_Activity) error {
	b, err := cs.getBccSession()
	if err != nil {
		return err
	}

	l := cs.LogWithContext("PROXY:BCC")

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

func (cs *ConnectionState) RegisterConnectRequest(conn *serviceconnections.Connections, proxyId int32, req *http.Request, uri *url.URL) {
	l := cs.LogWithContext("PROXY:BCC")

	//resolveIdsFromHttpHeader(ctx, req)

	bccRequest := &browsercontroller.DoRequest{
		Action: &browsercontroller.DoRequest_New{
			New: &browsercontroller.RegisterNew{
				ProxyId:          proxyId,
				Method:           "CONNECT",
				Uri:              uri.String(),
				RequestId:        cs.RequestId,
				CrawlExecutionId: cs.CrawlExecId,
				JobExecutionId:   cs.JobExecId,
				CollectionRef:    cs.CollectionRef,
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
	if err != nil {
		l.WithError(err).Errorf("Failed getting register response from browser controller %v", err)
	} else {
		switch v := doReply.Action.(type) {
		case *browsercontroller.DoReply_New:
			cs.JobExecId = v.New.JobExecutionId
			cs.CrawlExecId = v.New.CrawlExecutionId
			cs.CollectionRef = v.New.CollectionRef
		}
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
