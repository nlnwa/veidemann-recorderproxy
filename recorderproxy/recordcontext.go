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

package recorderproxy

import (
	"context"
	"fmt"
	"github.com/nlnwa/veidemann-api-go/browsercontroller/v1"
	"github.com/nlnwa/veidemann-api-go/commons/v1"
	"github.com/nlnwa/veidemann-api-go/config/v1"
	"github.com/nlnwa/veidemann-api-go/contentwriter/v1"
	"github.com/nlnwa/veidemann-api-go/frontier/v1"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

type RecordContext struct {
	// Will contain the client request from the proxy
	Req *http.Request
	// Will contain the remote server's response (if available. nil if the request wasn't send yet)
	Resp *http.Response
	//RoundTripper RoundTripper
	// will contain the recent error that occurred while trying to send receive or parse traffic
	Error error
	// Will connect a request to a response
	session int64
	proxy   *RecorderProxy

	conn              *Connections
	cwcCancelFunc     context.CancelFunc
	cwc               contentwriter.ContentWriter_WriteClient
	bcc               browsercontroller.BrowserController_DoClient
	bccCancelFunc     context.CancelFunc
	bccMsgChan        chan *browsercontroller.DoReply
	uri               *url.URL
	IP                string
	FetchTimesTamp    time.Time
	collectionRef     *config.ConfigRef
	meta              *contentwriter.WriteRequest_Meta
	crawlLog          *frontier.CrawlLog
	replacementScript *config.BrowserScript
	closed            bool
	foundInCache      bool
	precludedByRobots bool
}

var i int

func NewRecordContext(proxy *RecorderProxy) *RecordContext {
	ctx := &RecordContext{
		session: atomic.AddInt64(&proxy.sess, 1),
		proxy:   proxy,
		conn:    proxy.conn,
	}

	return ctx
}

func (ctx *RecordContext) init(req *http.Request) *RecordContext {
	cwcCtx, cwcCancel := context.WithTimeout(context.Background(), ctx.proxy.ConnectionTimeout)
	cwc, err := ctx.proxy.conn.ContentWriterClient().Write(cwcCtx)
	if err != nil {
		ctx.SessionLogger().Warnf("Error connecting to content writer, cause: %v", err)
		cwcCancel()
		return nil
	}

	bccCtx, bccCancel := context.WithTimeout(context.Background(), ctx.proxy.ConnectionTimeout)
	bcc, err := ctx.proxy.conn.BrowserControllerClient().Do(bccCtx)
	if err != nil {
		ctx.SessionLogger().Warnf("Error connecting to browser controller, cause: %v", err)
		cwcCancel()
		bccCancel()
		return nil
	}

	ctx.Req = req
	ctx.cwcCancelFunc = cwcCancel
	ctx.cwc = cwc
	ctx.bcc = bcc
	ctx.bccCancelFunc = bccCancel
	ctx.bccMsgChan = make(chan *browsercontroller.DoReply)

	// Handle messages from browser controller
	go func() {
		i++
		for {
			doReply, err := bcc.Recv()
			if err == io.EOF {
				// read done.
				ctx.bccMsgChan <- doReply
				close(ctx.bccMsgChan)
				ctx.Close()
				ctx.bccCancelFunc()
				return
			}
			serr := status.Convert(err)
			if serr.Code() == codes.Canceled {
				ctx.SessionLogger().Debugf("context canceled %v\n", serr)
				ctx.bccMsgChan <- doReply
				close(ctx.bccMsgChan)
				ctx.Close()
				ctx.bccCancelFunc()
				return
			}
			if serr.Code() == codes.DeadlineExceeded {
				ctx.SessionLogger().Debugf("context deadline exeeded %v\n", err)
				ctx.bccMsgChan <- doReply
				close(ctx.bccMsgChan)
				ctx.Close()
				ctx.bccCancelFunc()
				return
			}
			if err != nil {
				ctx.SessionLogger().Warnf("unknown error from browser controller %v, %v, %v\n", doReply, err, serr)
				ctx.Error = fmt.Errorf("unknown error from browser controller: %v", err.Error())
				ctx.bccMsgChan <- &browsercontroller.DoReply{Action: &browsercontroller.DoReply_Cancel{Cancel: ctx.Error.Error()}}
				close(ctx.bccMsgChan)
				ctx.Close()
				ctx.bccCancelFunc()
				return
			}
			switch doReply.Action.(type) {
			case *browsercontroller.DoReply_Cancel:
				if doReply.GetCancel() == "Blocked by robots.txt" {
					ctx.SendError(&commons.Error{
						Code:   -9998,
						Msg:    "PRECLUDED_BY_ROBOTS",
						Detail: "Robots.txt rules precluded fetch",
					})
					ctx.bccMsgChan <- doReply
				} else {
					ctx.SendError(&commons.Error{
						Code:   -5011,
						Msg:    "CANCELED_BY_BROWSER",
						Detail: "cancelled by browser controller",
					})
					ctx.bccMsgChan <- doReply
				}
			default:
				ctx.bccMsgChan <- doReply
			}
		}
	}()

	return ctx
}

func (ctx *RecordContext) saveCrawlLog(crawlLog *frontier.CrawlLog) {
	if ctx.closed {
		return
	}
	err := ctx.bcc.Send(&browsercontroller.DoRequest{
		Action: &browsercontroller.DoRequest_Completed{
			Completed: &browsercontroller.Completed{
				CrawlLog: crawlLog,
				Cached:   ctx.foundInCache,
			},
		},
	})
	ctx.handleErr("Error sending crawl log to browser controller", err)
}

func (ctx *RecordContext) SendUnknownError(err error) {
	ctx.SendErrorCode(-5, "RecorderProxy internal failure", err.Error())
}

func (ctx *RecordContext) SendErrorCode(code int32, msg string, detail string) {
	ctx.SendError(&commons.Error{
		Code:   code,
		Msg:    msg,
		Detail: detail,
	})
}

func (ctx *RecordContext) SendError(err *commons.Error) {
	if ctx.closed {
		return
	}
	defer ctx.Close()
	if ctx.crawlLog != nil {
		cl := ctx.crawlLog
		if cl.FetchTimeMs == 0 {
			cl.FetchTimeMs = time.Now().Sub(ctx.FetchTimesTamp).Nanoseconds() / 1000000
		}
		cl.StatusCode = err.Code
		if cl.RecordType == "" {
			cl.RecordType = strings.ToLower("response")
		}
		cl.Error = err
		ctx.saveCrawlLog(cl)
	}
	e := ctx.cwc.Send(&contentwriter.WriteRequest{Value: &contentwriter.WriteRequest_Cancel{Cancel: err.Detail}})
	if e != nil {
		s, ok := status.FromError(e)
		if ok && s.Code() == codes.Internal && s.Message() == "SendMsg called after CloseSend" {
			// Content writer session is already closed
			return
		}

		log.Errorf("Could not write error to content writer: %v.\nError was: %v *** %T", e, err, e)
	}
}

func (ctx *RecordContext) handleErr(msg string, err error) bool {
	if err != nil {
		log.Warnf("%s, cause: %v", msg, err)
		defer ctx.Close()
		return true
	}
	return false
}

func (ctx *RecordContext) Close() {
	if ctx.closed {
		return
	}
	ctx.closed = true
	ctx.bcc.CloseSend()
	ctx.cwc.CloseAndRecv()
	if ctx.cwcCancelFunc != nil {
		ctx.cwcCancelFunc()
	}
}

func (ctx *RecordContext) SessionLogger() *Logger {
	return &Logger{log.WithFields(
		log.Fields{
			"session":   ctx.session,
			"component": "PROXY",
		},
	)}
}

var charsetFinder = regexp.MustCompile("charset=([^ ;]*)")

// Will try to infer the character set of the request from the headers.
// Returns the empty string if we don't know which character set it used.
// Currently it will look for charset=<charset> in the Content-Type header of the request.
func (ctx *RecordContext) Charset() string {
	charsets := charsetFinder.FindStringSubmatch(ctx.Resp.Header.Get("Content-Type"))
	if charsets == nil {
		return ""
	}
	return charsets[1]
}
