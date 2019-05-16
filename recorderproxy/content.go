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
	"bytes"
	"crypto/sha1"
	"fmt"
	"github.com/elazarl/goproxy"
	"github.com/nlnwa/veidemann-api-go/browsercontroller/v1"
	"github.com/nlnwa/veidemann-api-go/contentwriter/v1"
	"github.com/nlnwa/veidemann-api-go/frontier/v1"
	log "github.com/sirupsen/logrus"
	"hash"
	"io"
	"strings"
	"time"
)

type wrappedBody struct {
	io.ReadCloser
	ctx               *goproxy.ProxyCtx
	recordContext     *recordContext
	recNum            int32
	size              int64
	blockCrc          hash.Hash
	bodyCrc           hash.Hash
	recordMeta        *contentwriter.WriteRequestMeta_RecordMeta
	recordType        contentwriter.RecordType
	statusCode        int32
	replacementReader io.Reader
}

func WrapBody(body io.ReadCloser, ctx *goproxy.ProxyCtx, recNum int32, statusCode int32, contentType string,
	recordType contentwriter.RecordType, prolog []byte) *wrappedBody {

	b := &wrappedBody{
		ReadCloser:    body,
		ctx:           ctx,
		recordContext: ctx.UserData.(*recordContext),
		recNum:        recNum,
		statusCode:    statusCode,
		blockCrc:      sha1.New(),
		recordType:    recordType,
	}

	b.recordMeta = &contentwriter.WriteRequestMeta_RecordMeta{
		RecordNum:         recNum,
		Type:              recordType,
		RecordContentType: contentType,
	}
	b.recordContext.meta.Meta.RecordMeta[recNum] = b.recordMeta

	b.size = int64(len(prolog))
	b.blockCrc.Write(prolog)
	err2 := b.sendProtocolHeader(prolog)
	if err2 != nil {
		log.Fatalf("Error writing payload to content writer: %v", err2)
	}

	return b
}

func (b *wrappedBody) Read(p []byte) (n int, err error) {
	if b.recordType != contentwriter.RecordType_RESPONSE || b.recordContext.replacementScript == nil {
		return b.innerRead(b.ReadCloser, p)
	} else {
		if b.replacementReader == nil {
			fmt.Printf("======\n")
			for {
				_, err := b.innerRead(b.ReadCloser, p)
				if err == io.EOF {
					break
				}
				if err != nil {
					return 0, err
				}
			}
			b.replacementReader = bytes.NewReader([]byte(b.recordContext.replacementScript.Script))
		}
		n, err = b.replacementReader.Read(p)
		fmt.Printf("N: %v, ERR: %v, P: %s\n", n, err, p[:n])
		return
	}
}

func (b *wrappedBody) innerRead(r io.Reader, p []byte) (n int, err error) {
	n, err = r.Read(p)
	//fmt.Printf("inner N: %v, ERR: %v, P: %s\n", n, err, p[:n])
	if n > 0 {
		if b.bodyCrc == nil {
			b.bodyCrc = sha1.New()
		}
		b.notifyBc(browsercontroller.NotifyActivity_DATA_RECEIVED)

		b.size += int64(n)
		d := p[:n]
		b.blockCrc.Write(d)
		b.bodyCrc.Write(d)
		err2 := b.sendPayload(d)
		b.handleErr("Error writing payload to content writer", err2)
	}
	if err == io.EOF {
		fetchDurationMs := time.Now().Sub(b.recordContext.FetchTimesTamp).Nanoseconds() / 1000
		b.notifyBc(browsercontroller.NotifyActivity_ALL_DATA_RECEIVED)

		var payloadDigest string
		blockDigest := fmt.Sprintf("sha1:%x", b.blockCrc.Sum(nil))
		b.recordMeta.Size = b.size
		if b.bodyCrc != nil {
			payloadDigest = fmt.Sprintf("sha1:%x", b.bodyCrc.Sum(nil))
		}
		b.recordMeta.PayloadDigest = payloadDigest
		b.recordMeta.BlockDigest = blockDigest

		switch b.recordType {
		case contentwriter.RecordType_RESPONSE:
			fallthrough
		case contentwriter.RecordType_REVISIT:
			fallthrough
		case contentwriter.RecordType_RESOURCE:
			cwReply, err2 := b.sendMeta()
			if err2 != nil {
				b.handleErr("Error writing payload to content writer", err2)
			}

			cl := b.recordContext.crawlLog
			cl.FetchTimeMs = fetchDurationMs
			cl.StatusCode = b.statusCode
			cl.CollectionFinalName = cwReply.Meta.RecordMeta[b.recNum].CollectionFinalName
			cl.WarcId = cwReply.Meta.RecordMeta[b.recNum].WarcId
			cl.StorageRef = cwReply.Meta.RecordMeta[b.recNum].StorageRef
			cl.WarcRefersTo = cwReply.Meta.RecordMeta[b.recNum].RevisitReferenceId
			cl.Size = b.size
			cl.ContentType = b.recordMeta.RecordContentType
			cl.RecordType = strings.ToLower(cwReply.Meta.RecordMeta[b.recNum].Type.String())
			cl.BlockDigest = blockDigest
			cl.PayloadDigest = payloadDigest
			b.saveCrawlLog(cl)

			b.recordContext.Close()
		}
	}
	return
}

func (b *wrappedBody) notifyBc(activity browsercontroller.NotifyActivity_Activity) {
	err := b.recordContext.bcc.Send(&browsercontroller.DoRequest{
		Action: &browsercontroller.DoRequest_Notify{
			Notify: &browsercontroller.NotifyActivity{
				Activity: activity,
			},
		},
	})
	b.handleErr("Error notifying browser controller", err)
}

func (b *wrappedBody) saveCrawlLog(crawlLog *frontier.CrawlLog) {
	err := b.recordContext.bcc.Send(&browsercontroller.DoRequest{
		Action: &browsercontroller.DoRequest_Completed{
			Completed: &browsercontroller.Completed{
				CrawlLog: crawlLog,
			},
		},
	})
	b.handleErr("Error sending crawl log to browser controller", err)
}

func (b *wrappedBody) sendProtocolHeader(p []byte) error {
	payloadRequest := &contentwriter.WriteRequest{
		Value: &contentwriter.WriteRequest_ProtocolHeader{
			ProtocolHeader: &contentwriter.Data{
				RecordNum: b.recNum,
				Data:      p,
			},
		},
	}

	return b.recordContext.cwc.Send(payloadRequest)
}

func (b *wrappedBody) sendPayload(p []byte) error {
	payloadRequest := &contentwriter.WriteRequest{
		Value: &contentwriter.WriteRequest_Payload{
			Payload: &contentwriter.Data{
				RecordNum: b.recNum,
				Data:      p,
			},
		},
	}

	return b.recordContext.cwc.Send(payloadRequest)
}

func (b *wrappedBody) sendMeta() (reply *contentwriter.WriteReply, err error) {
	metaRequest := &contentwriter.WriteRequest{
		Value: b.recordContext.meta,
	}

	err = b.recordContext.cwc.Send(metaRequest)
	if b.handleErr("Error writing meta record to content writer", err) {
		return nil, err
	}

	reply, err = b.recordContext.cwc.CloseAndRecv()
	if b.handleErr("Error closing content writer", err) {
		return nil, err
	}

	return

}

func (b *wrappedBody) handleErr(msg string, err error) bool {
	if err != nil {
		log.Warnf("%s, cause: %v", msg, err)
		b.recordContext.SendError(-5, msg, err.Error())
		return true
	}
	return false
}
