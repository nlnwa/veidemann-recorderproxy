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
	"github.com/nlnwa/veidemann-api-go/browsercontroller/v1"
	"github.com/nlnwa/veidemann-api-go/commons/v1"
	"github.com/nlnwa/veidemann-api-go/config/v1"
	"github.com/nlnwa/veidemann-api-go/contentwriter/v1"
	"github.com/nlnwa/veidemann-api-go/frontier/v1"
	log "github.com/sirupsen/logrus"
	"io"
	"net/url"
	"time"
)

type recordContext struct {
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
}

func NewRecordContext(conn Connections, uri *url.URL) *recordContext {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)

	cwc, err := conn.ContentWriterClient().Write(ctx)
	if err != nil {
		log.Fatalf("Error connecting to content writer, cause: %v", err)
		return nil
	}

	bcc, err := conn.BrowserControllerClient().Do(context.Background())
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

	return &recordContext{
		conn:       conn,
		cancel:     cancel,
		cwc:        cwc,
		bcc:        bcc,
		bccMsgChan: msgc,
	}
}

func (u *recordContext) SendUnknownError(err error) {
	u.SendError(-5, "RecorderProxy internal failure", err.Error())
}

func (u *recordContext) SendError(code int32, msg string, detail string) {
	err := &commons.Error{
		Code:   code,
		Msg:    msg,
		Detail: detail,
	}

	e := u.bcc.Send(&browsercontroller.DoRequest{Action: &browsercontroller.DoRequest_Error{Error: err}})

	if e != nil {
		log.Errorf("Could not write error to browser controller: %v.\nError was: %v", e, err)
	}
}

func (u *recordContext) Close() {
	u.bcc.CloseSend()
	<-u.bccMsgChan
	if u.cancel != nil {
		u.cancel()
	}
}
