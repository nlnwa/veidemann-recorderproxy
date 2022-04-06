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
	"github.com/nlnwa/veidemann-api/go/contentwriter/v1"
	"github.com/nlnwa/veidemann-recorderproxy/constants"
	"github.com/nlnwa/veidemann-recorderproxy/errors"
	"github.com/nlnwa/veidemann-recorderproxy/filters"
	"github.com/nlnwa/veidemann-recorderproxy/logger"
	"hash"
	"io"
	"strings"
	"sync"
)

type wrappedResponseBody struct {
	io.ReadCloser
	cs                *filters.ConnectionState
	recNum            int32
	size              int64
	blockCrc          hash.Hash
	recordMeta        *contentwriter.WriteRequestMeta_RecordMeta
	replacementReader io.Reader
	mutex             sync.Mutex
	eof               bool
	allDataSent       bool
	log               *logger.Logger
}

func WrapResponseBody(cs *filters.ConnectionState, body io.ReadCloser, statusCode int32, contentType string,
	recordType contentwriter.RecordType, prolog []byte) (*wrappedResponseBody, error) {

	b := &wrappedResponseBody{
		ReadCloser: body,
		cs:         cs,
		recNum:     1,
		blockCrc:   sha1.New(),
	}
	b.log = cs.LogWithContext("BODY:resp").WithField("url", b.cs.Uri.String())

	b.recordMeta = &contentwriter.WriteRequestMeta_RecordMeta{
		RecordNum: b.recNum,
		Type:      recordType,
	}
	b.recordMeta.RecordContentType = constants.RecordContentTypeResponse
	b.cs.Meta.Meta.RecordMeta[b.recNum] = b.recordMeta
	b.cs.CrawlLog.StatusCode = statusCode
	b.cs.CrawlLog.ContentType = contentType

	b.size = int64(len(prolog))
	b.blockCrc.Write(prolog)

	if !b.cs.FoundInCache {
		err := b.cs.SendProtocolHeader(b.recNum, prolog)
		if err != nil {
			return nil, fmt.Errorf("error writing payload to content writer: %v", err)
		}
	}

	return b, nil
}

func (b *wrappedResponseBody) Close() (err error) {
	err = b.ReadCloser.Close()
	b.log.WithError(err).Debug("Close body")
	return
}

func (b *wrappedResponseBody) Read(p []byte) (n int, err error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if b.cs.ReplacementScript == nil {
		// Send original content to client
		return b.innerRead(b.ReadCloser, p)
	} else {
		// Replace content sent to client while still storing original content
		if b.replacementReader == nil {
			buf := make([]byte, 64*1024)
			for {
				_, err := b.innerRead(b.ReadCloser, buf)
				if err == io.EOF {
					break
				}
				if err != nil {
					return 0, err
				}
			}
			b.replacementReader = bytes.NewReader([]byte(b.cs.ReplacementScript.Script))
		}
		n, err = b.replacementReader.Read(p)
		return
	}
}

func (b *wrappedResponseBody) innerRead(r io.Reader, p []byte) (n int, err error) {
	if b.eof {
		n, err = r.Read(p)
		return 0, io.EOF
	}

	n, err = r.Read(p)
	if err != nil && err != io.EOF {
		b.log.WithError(err).Warnf("Inner read %d", n)
	} else {
		b.log.Tracef("Inner read %d", n)
	}

	if n > 0 {
		if !b.cs.FoundInCache {
			_ = b.cs.NotifyDataReceived()

			b.size += int64(n)
			d := p[:n]
			b.writeCrc(d)
			err2 := b.cs.SendPayload(b.recNum, d)
			if err2 != nil {
				b.log.WithError(err2).Errorf("Error writing payload")
			}
		}
	}
	if err == io.EOF {
		b.eof = true
		if b.cs.FoundInCache {
			b.handleCachedContent()
			return
		}

		_ = b.cs.NotifyAllDataReceived()

		blockDigest := fmt.Sprintf("sha1:%x", b.blockCrc.Sum(nil))
		b.recordMeta.Size = b.size
		b.recordMeta.BlockDigest = blockDigest

		cwReply, err2 := b.cs.SendMeta()
		if err2 != nil {
			err2 = b.cs.SendResponseError(errors.Wrap(err2, errors.RuntimeException, "Error writing to content writer", err2.Error()))
			return
		}
		if cwReply == nil {
			return
		}

		cl := b.cs.CrawlLog
		cl.CollectionFinalName = cwReply.Meta.RecordMeta[b.recNum].CollectionFinalName
		cl.WarcId = cwReply.Meta.RecordMeta[b.recNum].WarcId
		cl.StorageRef = cwReply.Meta.RecordMeta[b.recNum].StorageRef
		cl.WarcRefersTo = cwReply.Meta.RecordMeta[b.recNum].RevisitReferenceId
		cl.Size = b.size
		cl.RecordType = strings.ToLower(cwReply.Meta.RecordMeta[b.recNum].Type.String())
		cl.BlockDigest = blockDigest
		cl.PayloadDigest = cwReply.Meta.RecordMeta[b.recNum].PayloadDigest

		err3 := b.cs.SaveCrawlLog()
		if err3 != nil {
			b.log.WithError(err3).Errorf("Error saving crawllog")
		}
	}
	return
}

func (b *wrappedResponseBody) writeCrc(d []byte) error {
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

func (b *wrappedResponseBody) handleCachedContent() {
	cl := b.cs.CrawlLog
	cl.Size = b.size

	_ = b.cs.SaveCrawlLog()
	_ = b.cs.CancelContentWriter("OK: Loaded from cache")
}
