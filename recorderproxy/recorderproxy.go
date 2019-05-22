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
	"github.com/dustin/go-broadcast"
	"github.com/elazarl/goproxy"
	"github.com/golang/protobuf/ptypes"
	"github.com/nlnwa/veidemann-api-go/browsercontroller/v1"
	"github.com/nlnwa/veidemann-api-go/commons/v1"
	"github.com/nlnwa/veidemann-api-go/contentwriter/v1"
	"github.com/nlnwa/veidemann-api-go/dnsresolver/v1"
	"github.com/nlnwa/veidemann-api-go/frontier/v1"
	log "github.com/sirupsen/logrus"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"strconv"
	"time"
)

const (
	ENCODING         = "Accept-Encoding"
	EXECUTION_ID     = "veidemann_eid"
	JOB_EXECUTION_ID = "veidemann_jeid"
)

var proxyCount int32

type RecorderProxy struct {
	id                int32
	proxy             *goproxy.ProxyHttpServer
	addr              string
	conn              Connections
	ConnectionTimeout time.Duration
	pubsub            broadcast.Broadcaster
}

func (r *RecorderProxy) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.proxy.ServeHTTP(w, req)
}

func NewRecorderProxy(port int, conn Connections, connectionTimeout time.Duration) *RecorderProxy {
	r := &RecorderProxy{
		id:                proxyCount,
		conn:              conn,
		proxy:             goproxy.NewProxyHttpServer(),
		addr:              ":" + strconv.Itoa(port),
		ConnectionTimeout: connectionTimeout,
		pubsub:            broadcast.NewBroadcaster(64),
	}

	r.proxy.Logger = &LogStealer{Logger: r.proxy.Logger, pubsub: r.pubsub}

	r.proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)

	r.proxy.OnRequest().Do(r.RecordRequest())

	r.proxy.OnResponse().Do(r.RecordResponse())

	proxyCount++
	return r
}

func (r *RecorderProxy) Start() {
	fmt.Printf("Starting proxy %v...\n", r.id)

	go func() {
		log.Fatalf("Proxy with addr %v: %v", r.addr, http.ListenAndServe(r.addr, r.proxy))
	}()

	fmt.Printf("Proxy %v started on port %v\n", r.id, r.addr)
}

func (r *RecorderProxy) SetVerbose(v bool) {
	r.proxy.Verbose = v
}

func (r *RecorderProxy) RecordRequest() goproxy.ReqHandler {
	return goproxy.FuncReqHandler(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		ctx.RoundTripper = &RoundTripper{r.proxy.Tr.RoundTrip}
		var prolog bytes.Buffer
		writeRequestProlog(req, &prolog)

		rCtx := NewRecordContext(r, ctx.Session)

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

		req.Header.Set(ENCODING, "identity")
		req.Header.Set(EXECUTION_ID, executionId)
		req.Header.Set(JOB_EXECUTION_ID, jobExecutionId)

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
		bodyWrapper, err := WrapBody(req.Body, REQUEST, ctx, 0, -1, "", contentwriter.RecordType_REQUEST, prolog.Bytes())
		if err != nil {
			return req, NewResponse(req, goproxy.ContentTypeText, http.StatusBadGateway, "FAIL1: "+err.Error())
		}
		req.Body = bodyWrapper

		return req, nil
	})
}

func (r *RecorderProxy) RecordResponse() goproxy.RespHandler {
	return goproxy.FuncRespHandler(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		if resp == nil {
			ctx.UserData.(*recordContext).Close()
			panic(http.ErrAbortHandler)
		}

		var prolog bytes.Buffer
		writeResponseProlog(resp, &prolog)
		contentType := resp.Header.Get("Content-Type")
		statusCode := int32(resp.StatusCode)
		var err error
		bodyWrapper, err := WrapBody(resp.Body, RESPONSE, ctx, 1, statusCode, contentType, contentwriter.RecordType_RESPONSE, prolog.Bytes())
		if err != nil {
			ctx.UserData.(*recordContext).Close()
			return NewResponse(resp.Request, goproxy.ContentTypeText, http.StatusBadGateway, "Veidemann proxy lost connection to GRPC services\n"+err.Error())
		}

		resp.Body = bodyWrapper

		return resp
	})
}

func NewResponse(r *http.Request, contentType string, status int, body string) *http.Response {
	resp := &http.Response{}
	resp.ProtoMajor = r.ProtoMajor
	resp.ProtoMinor = r.ProtoMinor
	resp.Request = r
	resp.TransferEncoding = r.TransferEncoding
	resp.Header = make(http.Header)
	if contentType != "" {
		resp.Header.Add("Content-Type", contentType)
	}
	resp.StatusCode = status
	resp.Status = http.StatusText(status)
	buf := bytes.NewBufferString(body)
	resp.ContentLength = int64(buf.Len())
	resp.Body = ioutil.NopCloser(buf)
	return resp
}

type RoundTripper struct {
	wrapped func(req *http.Request) (response *http.Response, e error)
}

func (r *RoundTripper) RoundTrip(req *http.Request, ctx *goproxy.ProxyCtx) (response *http.Response, e error) {
	response, e = r.wrapped(req)

	if e != nil {
		err := &commons.Error{}
		if ctx.Error != nil {
			err.Detail = ctx.Error.Error()
		} else {
			err.Detail = e.Error()
		}
		switch e {
		case io.EOF:
			err.Code = 504
			err.Msg = "GATEWAY_TIMEOUT"
			err.Detail = "Veidemann recorder proxy lost connection to upstream server"
		case context.Canceled:
			err.Code = -5011
			err.Msg = "CANCELED_BY_BROWSER"
			err.Detail = "Veidemann recorder proxy lost connection to client"
		default:
			err.Code = -5
			err.Msg = "RUNTIME_EXCEPTION"
		}
		ctx.UserData.(*recordContext).SendError(err)
	}
	return
}
