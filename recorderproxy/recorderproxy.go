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
	"context"
	"fmt"
	"github.com/elazarl/goproxy"
	"github.com/golang/protobuf/ptypes"
	"github.com/nlnwa/veidemann-api-go/browsercontroller/v1"
	"github.com/nlnwa/veidemann-api-go/contentwriter/v1"
	"github.com/nlnwa/veidemann-api-go/dnsresolver/v1"
	"github.com/nlnwa/veidemann-api-go/frontier/v1"
	log "github.com/sirupsen/logrus"
	"net"
	"net/http"
	"strconv"
	"time"
)

var proxyCount int32

type RecorderProxy struct {
	id    int32
	Proxy *goproxy.ProxyHttpServer
	addr  string
	conn  Connections
}

func NewRecorderProxy(port int, conn Connections) *RecorderProxy {
	r := &RecorderProxy{
		id:    proxyCount,
		conn:  conn,
		Proxy: goproxy.NewProxyHttpServer(),
		addr:  ":" + strconv.Itoa(port),
	}

	r.Proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)

	r.Proxy.OnRequest().Do(r.RecordRequest())

	r.Proxy.OnResponse().Do(r.RecordResponse())

	proxyCount++
	return r
}

func (r *RecorderProxy) Start() {
	fmt.Printf("Starting proxy %v...\n", r.id)

	go func() {
		log.Fatalf("Proxy with addr %v: %v", r.addr, http.ListenAndServe(r.addr, r.Proxy))
	}()

	fmt.Printf("Proxy %v started on port %v\n", r.id, r.addr)
}

func (r *RecorderProxy) SetVerbose(v bool) {
	r.Proxy.Verbose = v
}

func (r *RecorderProxy) RecordRequest() goproxy.ReqHandler {
	return goproxy.FuncReqHandler(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		var prolog bytes.Buffer
		writeRequestProlog(req, &prolog)

		rCtx := NewRecordContext(r.conn, req.URL)

		err := rCtx.bcc.Send(&browsercontroller.DoRequest{
			Action: &browsercontroller.DoRequest_New{
				New: &browsercontroller.RegisterNew{
					ProxyId: r.id,
					Uri:     req.URL.String(),
				},
			},
		})
		if err != nil {
			log.Fatalf("Error register with browser controller, cause: %v", err)
		}

		bcReply := <-rCtx.bccMsgChan

		executionId := bcReply.GetNew().CrawlExecutionId
		jobExecutionId := bcReply.GetNew().GetJobExecutionId()
		collectionRef := bcReply.GetNew().CollectionRef
		rCtx.replacementScript = bcReply.GetNew().ReplacementScript
		uri := req.URL

		host, ps, err := net.SplitHostPort(uri.Host)
		if err != nil {
			log.Fatalf("Error looking up %v, cause: %v", uri.Host, err)
		}
		port, err := strconv.Atoi(ps)
		if err != nil {
			log.Fatalf("Error looking up %v, cause: %v", uri.Host, err)
		}
		dnsReq := &dnsresolver.ResolveRequest{
			CollectionRef: collectionRef,
			Host:          host,
			Port:          int32(port),
		}
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		dnsRep, err := r.conn.DnsResolverClient().Resolve(c, dnsReq)
		if err != nil {
			log.Fatalf("Error looking up %v, cause: %v", uri.Host, err)
		}

		rCtx.FetchTimesTamp = time.Now()
		fetchTimeStamp, _ := ptypes.TimestampProto(rCtx.FetchTimesTamp)

		rCtx.meta = &contentwriter.WriteRequest_Meta{
			Meta: &contentwriter.WriteRequestMeta{
				RecordMeta:     map[int32]*contentwriter.WriteRequestMeta_RecordMeta{},
				TargetUri:      req.URL.String(),
				ExecutionId:    executionId,
				IpAddress:      dnsRep.TextualIp,
				CollectionRef:  collectionRef,
				FetchTimeStamp: fetchTimeStamp,
			},
		}

		rCtx.crawlLog = &frontier.CrawlLog{
			JobExecutionId: jobExecutionId,
			ExecutionId:    executionId,
			IpAddress:      dnsRep.TextualIp,
			RequestedUri:   req.URL.String(),
			FetchTimeStamp: fetchTimeStamp,
		}

		ctx.UserData = rCtx
		req.Body = WrapBody(req.Body, ctx, 0, -1, "", contentwriter.RecordType_REQUEST, prolog.Bytes())

		return req, nil
	})
}

func (r *RecorderProxy) RecordResponse() goproxy.RespHandler {
	return goproxy.FuncRespHandler(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		var prolog bytes.Buffer
		writeResponseProlog(resp, &prolog)
		contentType := resp.Header.Get("Content-Type")
		statusCode := int32(resp.StatusCode)
		resp.Body = WrapBody(resp.Body, ctx, 1, statusCode, contentType, contentwriter.RecordType_RESPONSE, prolog.Bytes())
		return resp
	})
}
