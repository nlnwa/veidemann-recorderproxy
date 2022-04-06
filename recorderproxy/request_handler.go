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
	"crypto/sha1"
	"fmt"
	"github.com/nlnwa/veidemann-api/go/contentwriter/v1"
	"github.com/nlnwa/veidemann-recorderproxy/constants"
	"github.com/nlnwa/veidemann-recorderproxy/errors"
	"github.com/nlnwa/veidemann-recorderproxy/filters"
	"github.com/nlnwa/veidemann-recorderproxy/logger"
	"hash"
	"io"
	"net/http"
	"sync"
)

// handleRequestError creates a short circuit response for requests that fail before or in request handling.
// Only CrawlLog is sent, nothing is written to content writer.
func handleRequestError(cs *filters.ConnectionState, req *http.Request, reqErr error) (*http.Response, *filters.ConnectionState, error) {
	l := cs.LogWithContextAndRequest(req, "REQH")
	l.WithError(reqErr).Debug("handling request error")
	e := cs.SendRequestError(reqErr)
	_ = cs.CancelContentWriter(errors.Detail(e))
	return errorResponse(cs, req, e)
}

// errorResponse creates a response from an error and populates Veidemann specific headers
func errorResponse(cs *filters.ConnectionState, req *http.Request, err error) (*http.Response, *filters.ConnectionState, error) {
	resp, c, err := filters.Fail(cs, req, errors.HttpStatusCode(err), err)
	resp.Header.Add(constants.HeaderProxyErrorCode, errors.Code(err).String())
	resp.Header.Add(constants.HeaderProxyError, errors.Message(err))
	return resp, c, err
}

type wrappedRequestBody struct {
	io.ReadCloser
	cs         *filters.ConnectionState
	recNum     int32
	size       int64
	blockCrc   hash.Hash
	recordMeta *contentwriter.WriteRequestMeta_RecordMeta
	mutex      sync.Mutex
	eof        bool
}

func WrapRequestBody(cs *filters.ConnectionState, body io.ReadCloser, contentType string,
	prolog []byte) (*wrappedRequestBody, error) {

	b := &wrappedRequestBody{
		ReadCloser: body,
		cs:         cs,
		recNum:     0,
		blockCrc:   sha1.New(),
	}

	b.recordMeta = &contentwriter.WriteRequestMeta_RecordMeta{
		RecordNum: b.recNum,
		Type:      contentwriter.RecordType_REQUEST,
	}
	b.recordMeta.RecordContentType = constants.RecordContentTypeRequest
	b.cs.Meta.Meta.RecordMeta[b.recNum] = b.recordMeta
	b.cs.CrawlLog.StatusCode = -1
	b.cs.CrawlLog.ContentType = contentType

	b.size = int64(len(prolog))
	b.blockCrc.Write(prolog)

	err := b.cs.SendProtocolHeader(b.recNum, prolog)
	if err != nil {
		return nil, fmt.Errorf("error writing payload to content writer: %v", err)
	}

	return b, nil
}

func (b *wrappedRequestBody) Close() (err error) {
	err = b.ReadCloser.Close()
	logger.LogWithComponent("BODY:req").WithError(err).Debug("Close body")
	return
}

func (b *wrappedRequestBody) Read(p []byte) (n int, err error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if b.eof {
		return 0, io.EOF
	}

	n, err = b.ReadCloser.Read(p)
	if n > 0 {
		b.size += int64(n)
		d := p[:n]
		b.writeCrc(d)
		err2 := b.cs.SendPayload(b.recNum, d)
		if err2 != nil {
			logger.Log.Errorf("Error writing payload: %v", err2)
		}
		//b.recordContext.HandleErr("Error writing payload to content writer", err2)
	}
	if err == io.EOF {
		b.eof = true

		blockDigest := fmt.Sprintf("sha1:%x", b.blockCrc.Sum(nil))
		b.recordMeta.Size = b.size
		b.recordMeta.BlockDigest = blockDigest
	}
	return
}

func (b *wrappedRequestBody) writeCrc(d []byte) error {
	l := len(d)
	c := 0
	for c < l {
		n, err := b.blockCrc.Write(d[c:])
		if err != nil {
			return err
		}
		c += n
	}
	return nil
}
