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
	"github.com/golang/protobuf/ptypes"
	"github.com/nlnwa/veidemann-api/go/config/v1"
	"github.com/nlnwa/veidemann-api/go/contentwriter/v1"
	"github.com/nlnwa/veidemann-api/go/frontier/v1"
	"github.com/nlnwa/veidemann-recorderproxy/constants"
	"github.com/nlnwa/veidemann-recorderproxy/logger"
	"github.com/nlnwa/veidemann-recorderproxy/serviceconnections"
	log "github.com/sirupsen/logrus"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// session variable must be aligned in i386
// see http://golang.org/src/pkg/sync/atomic/doc.go#L41
var sess int64
var closedSess int64

func OpenSessions() int64 {
	return sess - closedSess
}

type RecordContext struct {
	Error error

	CloseFunc func()

	// Will connect a request to a response
	session int64

	conn              *serviceconnections.Connections
	ctx               context.Context
	cwc               *CwcSession
	bcc               *BccSession
	Method            string
	Uri               *url.URL
	FetchTimesTamp    time.Time
	Meta              *contentwriter.WriteRequest_Meta
	CrawlLog          *frontier.CrawlLog
	ReplacementScript *config.BrowserScript
	closed            bool
	FoundInCache      bool
	PrecludedByRobots bool
	done              bool
	mutex             sync.Mutex
	InitDone          bool
	ProxyId           int32
	log               *logger.Logger
}

// NewRecordContext creates a new RecordContext
func NewRecordContext() *RecordContext {
	rc := &RecordContext{
		session: atomic.AddInt64(&sess, 1),
	}

	return rc
}

func (rc *RecordContext) Init(proxyId int32, conn *serviceconnections.Connections, req *http.Request, uri *url.URL) *RecordContext {
	rc.conn = conn
	rc.ctx = req.Context()
	rc.ProxyId = proxyId
	rc.Method = req.Method
	rc.Uri = uri

	resolveIdsFromHttpHeader(rc.ctx, req)

	req.Header.Del(constants.HeaderRequestId)
	req.Header.Del(constants.HeaderCrawlExecutionId)
	req.Header.Del(constants.HeaderJobExecutionId)
	req.Header.Del(constants.HeaderCollectionId)

	rc.FetchTimesTamp = time.Now()
	fetchTimeStamp, _ := ptypes.TimestampProto(rc.FetchTimesTamp)

	rc.CrawlLog = &frontier.CrawlLog{
		JobExecutionId: GetJobExecutionId(rc.ctx),
		ExecutionId:    GetCrawlExecutionId(rc.ctx),
		FetchTimeStamp: fetchTimeStamp,
		RequestedUri:   uri.String(),
		Method:         rc.Method,
		IpAddress:      GetIp(rc.ctx),
	}

	rc.InitDone = true

	rc.log = logger.Log.WithFields(log.Fields{
		"component": "PROXY",
		"method":    req.Method,
		"url":       uri.String(),
		"session":   rc.Session(),
	})

	rc.log.Infof("New session")
	return rc
}

func (rc *RecordContext) Session() int64 {
	return rc.session
}

func LogWithRecordContext(rc *RecordContext, componentName string) *logger.Logger {
	return rc.log.WithField("component", componentName)
}

func LogWithContext(ctx context.Context, componentName string) *logger.Logger {
	var l *logger.Logger
	rc := GetRecordContext(ctx)
	if rc != nil {
		l = rc.log
	} else {
		l = logger.Log
	}
	l = l.WithField("component", componentName)
	return l
}

func LogWithContextAndRequest(ctx context.Context, req *http.Request, componentName string) *logger.Logger {
	var l *logger.Logger

	rc := GetRecordContext(ctx)
	if rc != nil {
		l = rc.log
	} else {
		l = logger.Log.WithFields(log.Fields{
			"method": req.Method,
			"url":    req.URL.String(),
		})
	}
	l = l.WithField("component", componentName)
	return l
}

func resolveIdsFromHttpHeader(ctx context.Context, req *http.Request) {
	jid := req.Header.Get(constants.HeaderJobExecutionId)
	eid := req.Header.Get(constants.HeaderCrawlExecutionId)
	reqid := req.Header.Get(constants.HeaderRequestId)
	SetJobExecutionId(ctx, jid)
	SetCrawlExecutionId(ctx, eid)
	SetRequestId(ctx, reqid)

	if req.Header.Get(constants.HeaderCollectionId) != "" {
		cid := req.Header.Get(constants.HeaderCollectionId)
		SetCollectionRef(ctx, &config.ConfigRef{
			Kind: config.Kind_collection,
			Id:   cid,
		})
	}
	return
}
