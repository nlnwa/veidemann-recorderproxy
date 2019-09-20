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
	"fmt"
	"github.com/nlnwa/veidemann-api-go/browsercontroller/v1"
	"github.com/nlnwa/veidemann-api-go/commons/v1"
	"github.com/nlnwa/veidemann-api-go/config/v1"
	"github.com/nlnwa/veidemann-api-go/contentwriter/v1"
	"github.com/nlnwa/veidemann-api-go/frontier/v1"
	"github.com/nlnwa/veidemann-recorderproxy/logger"
	"github.com/nlnwa/veidemann-recorderproxy/serviceconnections"
	"github.com/opentracing/opentracing-go"
	otLog "github.com/opentracing/opentracing-go/log"
	log "github.com/sirupsen/logrus"
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

// session variable must be aligned in i386
// see http://golang.org/src/pkg/sync/atomic/doc.go#L41
var sess int64

type RecordContext struct {
	Error error

	CloseFunc func()

	// Will connect a request to a response
	session int64

	Cwc               contentwriter.ContentWriter_WriteClient
	Bcc               browsercontroller.BrowserController_DoClient
	BccMsgChan        chan *browsercontroller.DoReply
	Uri               *url.URL
	IP                string
	FetchTimesTamp    time.Time
	CollectionRef     *config.ConfigRef
	Meta              *contentwriter.WriteRequest_Meta
	CrawlLog          *frontier.CrawlLog
	ReplacementScript *config.BrowserScript
	closed            bool
	FoundInCache      bool
	PrecludedByRobots bool
	done              bool
	mutex             sync.Mutex
}

// NewRecordContext creates a new RecordContext
func NewRecordContext() *RecordContext {
	rc := &RecordContext{
		session: atomic.AddInt64(&sess, 1),
	}

	return rc
}

// cleanup waits for cancellation and eventually calls the Close function
func (rc *RecordContext) cleanup(ctx context.Context) {
	select {
	case <-ctx.Done():
		if rc.CloseFunc != nil {
			rc.CloseFunc()
		}
		rc.closed = true
		//rc.Bcc.CloseSend()
		//rc.Cwc.CloseAndRecv()

		//rc.Close()
		rc.SessionLogger().Info("Closed recordContext ********************")
		return
	}
}

func (rc *RecordContext) IsClosed() bool {
	return rc.closed
}

func (rc *RecordContext) IsDone() bool {
	rc.mutex.Lock()
	defer rc.mutex.Unlock()
	return rc.done
}

func (rc *RecordContext) SetDone() {
	rc.mutex.Lock()
	defer rc.mutex.Unlock()
	rc.done = true
}

func (rc *RecordContext) Init(conn *serviceconnections.Connections, req *http.Request) *RecordContext {
	span := opentracing.SpanFromContext(req.Context())

	cwc, err := conn.ContentWriterClient().Write(req.Context())
	span.LogFields(otLog.String("event", "Started ContentWriterClient session"), otLog.Error(err))
	if err != nil {
		rc.SessionLogger().Warnf("Error connecting to content writer, cause: %v", err)
		return nil
	}

	bcc, err := conn.BrowserControllerClient().Do(req.Context())
	span.LogFields(otLog.String("event", "Started BrowserControllerClient session"), otLog.Error(err))
	if err != nil {
		rc.SessionLogger().Warnf("Error connecting to browser controller, cause: %v", err)
		return nil
	}

	rc.Cwc = cwc
	rc.Bcc = bcc
	rc.BccMsgChan = make(chan *browsercontroller.DoReply)

	// Handle messages from browser controller
	go func() {
		for {
			doReply, err := bcc.Recv()
			if err == io.EOF {
				// read done.
				rc.BccMsgChan <- doReply
				close(rc.BccMsgChan)
				rc.Close()
				return
			}
			serr := status.Convert(err)
			if serr.Code() == codes.Canceled {
				rc.SessionLogger().Debugf("context canceled %v\n", serr)
				rc.SessionLogger().Panicf("????????????context canceled %v\n", serr)
				rc.BccMsgChan <- doReply
				close(rc.BccMsgChan)
				rc.Close()
				return
			}
			if serr.Code() == codes.DeadlineExceeded {
				rc.SessionLogger().Debugf("context deadline exeeded %v\n", err)
				rc.SessionLogger().Errorf("context deadline exeeded %v\n", err)
				rc.BccMsgChan <- doReply
				close(rc.BccMsgChan)
				rc.Close()
				return
			}
			if err != nil {
				rc.SessionLogger().Warnf("unknown error from browser controller %v, %v, %v\n", doReply, err, serr)
				rc.Error = fmt.Errorf("unknown error from browser controller: %v", err.Error())
				rc.BccMsgChan <- &browsercontroller.DoReply{Action: &browsercontroller.DoReply_Cancel{Cancel: rc.Error.Error()}}
				close(rc.BccMsgChan)
				rc.Close()
				return
			}
			switch doReply.Action.(type) {
			case *browsercontroller.DoReply_Cancel:
				if doReply.GetCancel() == "Blocked by robots.txt" {
					rc.SendError(&commons.Error{
						Code:   -9998,
						Msg:    "PRECLUDED_BY_ROBOTS",
						Detail: "Robots.txt rules precluded fetch",
					})
					rc.BccMsgChan <- doReply
				} else {
					rc.SendError(&commons.Error{
						Code:   -5011,
						Msg:    "CANCELED_BY_BROWSER",
						Detail: "cancelled by browser controller",
					})
					rc.BccMsgChan <- doReply
				}
			default:
				rc.BccMsgChan <- doReply
			}
		}
	}()

	return rc
}

func (rc *RecordContext) SaveCrawlLog(crawlLog *frontier.CrawlLog) {
	if rc.closed {
		return
	}
	err := rc.Bcc.Send(&browsercontroller.DoRequest{
		Action: &browsercontroller.DoRequest_Completed{
			Completed: &browsercontroller.Completed{
				CrawlLog: crawlLog,
				Cached:   rc.FoundInCache,
			},
		},
	})
	rc.HandleErr("Error sending crawl log to browser controller", err)
	err = rc.Bcc.CloseSend()
	fmt.Printf("SAVE CRAWL LOG ERROR?: %v\n", err)
	<-rc.BccMsgChan
}

func (rc *RecordContext) SendUnknownError(err error) {
	rc.SendErrorCode(-5, "RecorderProxy internal failure", err.Error())
}

func (rc *RecordContext) SendErrorCode(code int32, msg string, detail string) {
	rc.SendError(&commons.Error{
		Code:   code,
		Msg:    msg,
		Detail: detail,
	})
}

func (rc *RecordContext) SendError(err *commons.Error) {
	if rc.closed {
		return
	}
	defer rc.Close()
	if rc.CrawlLog != nil {
		cl := rc.CrawlLog
		if cl.FetchTimeMs == 0 {
			cl.FetchTimeMs = time.Now().Sub(rc.FetchTimesTamp).Nanoseconds() / 1000000
		}
		cl.StatusCode = err.Code
		if cl.RecordType == "" {
			cl.RecordType = strings.ToLower("response")
		}
		cl.Error = err

		rc.SaveCrawlLog(cl)
	}
	e := rc.Cwc.Send(&contentwriter.WriteRequest{Value: &contentwriter.WriteRequest_Cancel{Cancel: err.Detail}})
	if e != nil {
		s, ok := status.FromError(e)
		if ok && s.Code() == codes.Internal && s.Message() == "SendMsg called after CloseSend" {
			// Content writer session is already closed
			return
		}

		log.Errorf("Could not write error to content writer: %v.\nError was: %v *** %T", e, err, e)
	}
}

func (rc *RecordContext) HandleErr(msg string, err error) bool {
	if err != nil {
		log.Warnf("%s, cause: %v", msg, err)
		defer rc.Close()
		return true
	}
	return false
}

func (rc *RecordContext) Close() {
	rc.SessionLogger().Info("Closing RecordContext")
	if rc.closed {
		return
	}
	//ctx.closed = true
	//ctx.Bcc.CloseSend()
	//ctx.Cwc.CloseAndRecv()
}

func (rc *RecordContext) SessionLogger() *logger.Logger {
	return &logger.Logger{FieldLogger: log.WithFields(
		log.Fields{
			"session":   rc.session,
			"component": "PROXY",
		},
	)}
}

//var charsetFinder = regexp.MustCompile("charset=([^ ;]*)")

// Will try to infer the character set of the request from the headers.
// Returns the empty string if we don't know which character set it used.
// Currently it will look for charset=<charset> in the Content-Type header of the request.
//func (ctx *RecordContext) Charset() string {
//	charsets := charsetFinder.FindStringSubmatch(ctx.Resp.Header.Get("Content-Type"))
//	if charsets == nil {
//		return ""
//	}
//	return charsets[1]
//}
