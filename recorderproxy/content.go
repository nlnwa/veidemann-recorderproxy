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
	"github.com/nlnwa/veidemann-api-go/browsercontroller/v1"
	"github.com/nlnwa/veidemann-api-go/contentwriter/v1"
	"hash"
	"io"
	"strings"
	"time"
)

const (
	REQUEST                      = "request"
	RESPONSE                     = "response"
	CRLF                         = "\r\n"
	RECORD_CONTENT_TYPE_REQUEST  = "application/http; msgtype=request"
	RECORD_CONTENT_TYPE_RESPONSE = "application/http; msgtype=response"
)

type wrappedBody struct {
	io.ReadCloser
	bodyType           string
	recordContext      *RecordContext
	recNum             int32
	size               int64
	blockCrc           hash.Hash
	separatorAdded     bool
	recordMeta         *contentwriter.WriteRequestMeta_RecordMeta
	recordType         contentwriter.RecordType
	statusCode         int32
	replacementReader  io.Reader
	payloadContentType string
}

func WrapBody(body io.ReadCloser, bodyType string, ctx *RecordContext, recNum int32, statusCode int32, contentType string,
	recordType contentwriter.RecordType, prolog []byte) (*wrappedBody, error) {

	b := &wrappedBody{
		ReadCloser:         body,
		bodyType:           bodyType,
		recordContext:      ctx,
		recNum:             recNum,
		statusCode:         statusCode,
		blockCrc:           sha1.New(),
		recordType:         recordType,
		payloadContentType: contentType,
	}

	b.recordMeta = &contentwriter.WriteRequestMeta_RecordMeta{
		RecordNum: recNum,
		Type:      recordType,
	}
	switch b.recordType {
	case contentwriter.RecordType_REQUEST:
		b.recordMeta.RecordContentType = RECORD_CONTENT_TYPE_REQUEST
	case contentwriter.RecordType_RESPONSE:
		b.recordMeta.RecordContentType = RECORD_CONTENT_TYPE_RESPONSE
	}

	b.recordContext.meta.Meta.RecordMeta[recNum] = b.recordMeta

	b.size = int64(len(prolog))
	b.blockCrc.Write(prolog)

	if !b.recordContext.foundInCache {
		err := b.sendProtocolHeader(prolog)
		if err != nil {
			return nil, fmt.Errorf("error writing payload to content writer: %v", err)
		}
	}

	return b, nil
}

func (b *wrappedBody) Read(p []byte) (n int, err error) {
	if b.recordType != contentwriter.RecordType_RESPONSE || b.recordContext.replacementScript == nil {
		// Send original content to client
		return b.innerRead(b.ReadCloser, p)
	} else {
		// Replace content sent to client while still storing original content
		if b.replacementReader == nil {
			buf := make([]byte, 64000)
			for {
				_, err := b.innerRead(b.ReadCloser, buf)
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
		return
	}
}

func (b *wrappedBody) innerRead(r io.Reader, p []byte) (n int, err error) {
	n, err = r.Read(p)
	if n > 0 {
		if !b.recordContext.foundInCache {
			if !b.separatorAdded {
				b.size += 2 // Add size for header and payload separator (\r\n)
				b.blockCrc.Write([]byte(CRLF))
				b.separatorAdded = true
			}
			if b.bodyType == RESPONSE {
				b.notifyBc(browsercontroller.NotifyActivity_DATA_RECEIVED)
			}

			b.size += int64(n)
			d := p[:n]
			writeCrc(b.blockCrc, d)
			err2 := b.sendPayload(d)
			b.recordContext.handleErr("Error writing payload to content writer", err2)
		}
	}
	if err == io.EOF {
		fetchDurationMs := time.Now().Sub(b.recordContext.FetchTimesTamp).Nanoseconds() / 1000000

		if b.recordContext.foundInCache {
			if !b.recordContext.closed {
				cl := b.recordContext.crawlLog
				cl.FetchTimeMs = fetchDurationMs
				cl.StatusCode = b.statusCode
				cl.Size = b.size
				cl.ContentType = b.payloadContentType
				b.recordContext.saveCrawlLog(cl)
			}
			ee := b.recordContext.cwc.Send(&contentwriter.WriteRequest{Value: &contentwriter.WriteRequest_Cancel{Cancel: "OK: Loaded from cache"}})
			if ee != nil {
				fmt.Println("Could not cancel", ee)
			}
			b.recordContext.cwc.CloseAndRecv()

			b.recordContext.Close()
			return
		}

		if b.bodyType == RESPONSE {
			b.notifyBc(browsercontroller.NotifyActivity_ALL_DATA_RECEIVED)
		}

		blockDigest := fmt.Sprintf("sha1:%x", b.blockCrc.Sum(nil))
		b.recordMeta.Size = b.size
		b.recordMeta.BlockDigest = blockDigest

		if b.bodyType == RESPONSE {
			cwReply, err2 := b.sendMeta()
			if err2 != nil {
				b.recordContext.SendErrorCode(-5, "Error writing to content writer", err2.Error())
				b.recordContext.Close()
				return
			}

			if !b.recordContext.closed {
				cl := b.recordContext.crawlLog
				cl.FetchTimeMs = fetchDurationMs
				cl.StatusCode = b.statusCode
				cl.CollectionFinalName = cwReply.Meta.RecordMeta[b.recNum].CollectionFinalName
				cl.WarcId = cwReply.Meta.RecordMeta[b.recNum].WarcId
				cl.StorageRef = cwReply.Meta.RecordMeta[b.recNum].StorageRef
				cl.WarcRefersTo = cwReply.Meta.RecordMeta[b.recNum].RevisitReferenceId
				cl.Size = b.size
				cl.ContentType = b.payloadContentType
				cl.RecordType = strings.ToLower(cwReply.Meta.RecordMeta[b.recNum].Type.String())
				cl.BlockDigest = blockDigest
				cl.PayloadDigest = cwReply.Meta.RecordMeta[b.recNum].PayloadDigest
				b.recordContext.saveCrawlLog(cl)
			}

			b.recordContext.Close()
		}
	}
	return
}

func (b *wrappedBody) notifyBc(activity browsercontroller.NotifyActivity_Activity) {
	if b.recordContext.closed {
		return
	}
	err := b.recordContext.bcc.Send(&browsercontroller.DoRequest{
		Action: &browsercontroller.DoRequest_Notify{
			Notify: &browsercontroller.NotifyActivity{
				Activity: activity,
			},
		},
	})
	b.recordContext.handleErr("Error notifying browser controller", err)
}

func (b *wrappedBody) sendProtocolHeader(p []byte) error {
	if b.recordContext.closed {
		return nil
	}
	protocolHeaderRequest := &contentwriter.WriteRequest{
		Value: &contentwriter.WriteRequest_ProtocolHeader{
			ProtocolHeader: &contentwriter.Data{
				RecordNum: b.recNum,
				Data:      p,
			},
		},
	}

	return b.recordContext.cwc.Send(protocolHeaderRequest)
}

func (b *wrappedBody) sendPayload(p []byte) error {
	if b.recordContext.closed {
		return nil
	}
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
	if b.recordContext.closed {
		return
	}
	metaRequest := &contentwriter.WriteRequest{
		Value: b.recordContext.meta,
	}

	err = b.recordContext.cwc.Send(metaRequest)
	if err != nil {
		return nil, err
	}

	reply, err = b.recordContext.cwc.CloseAndRecv()

	return
}

func writeCrc(h hash.Hash, d []byte) error {
	l := len(d)
	c := 0
	for c < l {
		n, err := h.Write(d[c:])
		if err != nil {
			return err
		}
		c += n
	}
	return nil
}
