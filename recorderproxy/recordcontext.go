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
	"github.com/dustin/go-broadcast"
	"github.com/elazarl/goproxy"
	"github.com/nlnwa/veidemann-api-go/browsercontroller/v1"
	"github.com/nlnwa/veidemann-api-go/commons/v1"
	"github.com/nlnwa/veidemann-api-go/config/v1"
	"github.com/nlnwa/veidemann-api-go/contentwriter/v1"
	"github.com/nlnwa/veidemann-api-go/frontier/v1"
	log "github.com/sirupsen/logrus"
	"io"
	"net/url"
	"strings"
	"time"
)

type recordContext struct {
	session           int64
	conn              Connections
	cancel            context.CancelFunc
	cwc               contentwriter.ContentWriter_WriteClient
	bcc               browsercontroller.BrowserController_DoClient
	bccMsgChan        chan *browsercontroller.DoReply
	uri               *url.URL
	IP                string
	FetchTimesTamp    time.Time
	collectionRef     *config.ConfigRef
	meta              *contentwriter.WriteRequest_Meta
	crawlLog          *frontier.CrawlLog
	replacementScript *config.BrowserScript
	pubsubChan        chan interface{}
	pubsub            broadcast.Broadcaster
}

func NewRecordContext(proxy *RecorderProxy, session int64) *recordContext {
	ctx, cancel := context.WithTimeout(context.Background(), proxy.ConnectionTimeout)

	cwc, err := proxy.conn.ContentWriterClient().Write(ctx)
	if err != nil {
		log.Fatalf("Error connecting to content writer, cause: %v", err)
		return nil
	}

	bcc, err := proxy.conn.BrowserControllerClient().Do(context.Background())
	if err != nil {
		log.Fatalf("Error connecting to browser controller, cause: %v", err)
		return nil
	}
	msgc := make(chan *browsercontroller.DoReply)
	go func() {
		for {
			doReply, err := bcc.Recv()
			if err == io.EOF {
				// read done.
				close(msgc)
				return
			}
			msgc <- doReply
		}
	}()

	rCtx := &recordContext{
		session:    session,
		conn:       proxy.conn,
		cancel:     cancel,
		cwc:        cwc,
		bcc:        bcc,
		bccMsgChan: msgc,
		pubsubChan: make(chan interface{}),
		pubsub:     proxy.pubsub,
	}

	rCtx.pubsub.Register(rCtx.pubsubChan)

	// Handle log events sent to the broadcaster
	go func() {
		for v := range rCtx.pubsubChan {
			m := v.(*msg)
			if m.session == rCtx.session {
				switch f := m.format; {
				case strings.Contains(f, "Cannot write TLS response header from mitm'd client"):
					err := &commons.Error{
						Code:   -5011,
						Msg:    "CANCELED_BY_BROWSER",
						Detail: "Veidemann recorder proxy lost connection to client",
					}
					rCtx.SendError(err)
				}
			}
		}
	}()

	return rCtx
}

func (r *recordContext) saveCrawlLog(crawlLog *frontier.CrawlLog) {
	err := r.bcc.Send(&browsercontroller.DoRequest{
		Action: &browsercontroller.DoRequest_Completed{
			Completed: &browsercontroller.Completed{
				CrawlLog: crawlLog,
			},
		},
	})
	defer r.bcc.CloseSend()
	r.handleErr("Error sending crawl log to browser controller", err)
}

func (r *recordContext) SendUnknownError(err error) {
	r.SendErrorCode(-5, "RecorderProxy internal failure", err.Error())
}

func (r *recordContext) SendErrorCode(code int32, msg string, detail string) {
	r.SendError(&commons.Error{
		Code:   code,
		Msg:    msg,
		Detail: detail,
	})
}

func (r *recordContext) SendError(err *commons.Error) {
	defer r.cwc.CloseSend()
	defer r.bcc.CloseSend()
	cl := r.crawlLog
	if cl.FetchTimeMs == 0 {
		cl.FetchTimeMs = time.Now().Sub(r.FetchTimesTamp).Nanoseconds() / 1000000
	}
	cl.StatusCode = err.Code
	if cl.RecordType == "" {
		cl.RecordType = strings.ToLower("response")
	}
	cl.Error = err
	r.saveCrawlLog(cl)

	e := r.cwc.Send(&contentwriter.WriteRequest{Value: &contentwriter.WriteRequest_Cancel{Cancel: err.Detail}})
	if e != nil {
		log.Errorf("Could not write error to browser controller: %v.\nError was: %v", e, err)
	}
}

func (r *recordContext) handleErr(msg string, err error) bool {
	if err != nil {
		log.Warnf("%s, cause: %v", msg, err)
		defer r.Close()
		return true
	}
	return false
}

func (r *recordContext) Close() {
	r.pubsub.Unregister(r.pubsubChan)
	r.bcc.CloseSend()
	<-r.bccMsgChan
	r.cwc.CloseSend()
	if r.cancel != nil {
		r.cancel()
	}
}

type LogStealer struct {
	goproxy.Logger
	pubsub broadcast.Broadcaster
}

func (l *LogStealer) Printf(format string, v ...interface{}) {
	l.Logger.Printf(format, v...)
	m := &msg{
		session: v[0].(int64),
		format:  format,
		v:       v[1:],
	}
	l.pubsub.Submit(m)
}

type msg struct {
	session int64
	format  string
	v       []interface{}
}
