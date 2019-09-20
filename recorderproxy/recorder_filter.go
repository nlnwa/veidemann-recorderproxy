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
	"fmt"
	"github.com/getlantern/proxy/filters"
	"github.com/golang/protobuf/ptypes"
	"github.com/nlnwa/veidemann-api-go/browsercontroller/v1"
	"github.com/nlnwa/veidemann-api-go/config/v1"
	"github.com/nlnwa/veidemann-api-go/contentwriter/v1"
	"github.com/nlnwa/veidemann-api-go/dnsresolver/v1"
	dnsresolverV1 "github.com/nlnwa/veidemann-api-go/dnsresolver/v1"
	"github.com/nlnwa/veidemann-api-go/frontier/v1"
	context2 "github.com/nlnwa/veidemann-recorderproxy/context"
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/log"
	"github.com/sirupsen/logrus"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RecorderFilter is a filter which returns an error if the proxy is accessed as if it where a web server and not a proxy.
type RecorderFilter struct {
	proxyId           int32
	DnsResolverClient dnsresolverV1.DnsResolverClient
}

func (f *RecorderFilter) Apply(ctx filters.Context, req *http.Request, next filters.Next) (resp *http.Response, context filters.Context, err error) {
	if req.Method != http.MethodConnect {
		fmt.Printf("NOT SHORT CIRCUT %v %v %v\n", req.URL, ctx.IsMITMing(), ctx.RequestNumber())

		rc := context2.GetRecordContext(ctx)
		span := opentracing.SpanFromContext(ctx)
		//defer span.Finish()

		fmt.Printf("REQ HEADERS:\n%v\n", req.Header)
		span.LogFields(log.String("event", "rec upstream request"))

		req, resp = f.filterRequest(ctx, span, req, rc)
		if resp != nil {
			fmt.Printf("SHORT")
			return resp, ctx, nil
		}

		roundTripSpan, roundtripCtx := opentracing.StartSpanFromContext(ctx, "Roundtrip upstream")
		resp, context, err = next(filters.AdaptContext(roundtripCtx), req)
		roundTripSpan.Finish()
		if err != nil {
			fmt.Printf("ERROR IN RECORDER FILTER %v\n", err)
		}

		resp = f.filterResponse(span, resp, rc)

		span.LogFields(log.String("event", "rec upstream response"))
	} else {
		// Handle HTTPS CONNECT
		resp, context, err = next(ctx, req)
		if err != nil {
			logrus.Errorf("Could not CONNECT to upstream server: %v. Error: %v\n", req.Host, err)
			resp, ctx, err = filters.ShortCircuit(ctx, req, &http.Response{
				StatusCode: http.StatusOK,
			})
		}
	}
	return
}

func (f *RecorderFilter) filterRequest(c filters.Context, span opentracing.Span, req *http.Request, ctx *context2.RecordContext) (*http.Request, *http.Response) {
	span.LogKV("event", "StartFilterRequest")

	var prolog bytes.Buffer
	errr := writeRequestProlog(req, &prolog)
	fmt.Printf("Write Prolog error: %v\n", errr)
	fmt.Printf("Prolog: %v\n", prolog.String())

	var collectionRef *config.ConfigRef

	executionId := req.Header.Get(CRAWL_EXECUTION_ID)
	jobExecutionId := req.Header.Get(JOB_EXECUTION_ID)

	if req.Header.Get(COLLECTION_ID) != "" {
		collectionRef = &config.ConfigRef{
			Kind: config.Kind_collection,
			Id:   req.Header.Get(COLLECTION_ID),
		}
		span.LogKV("event", "CollectionIdFromHeader", "CollectionId", collectionRef.Id)
	}

	ctx.FetchTimesTamp = time.Now()
	fetchTimeStamp, _ := ptypes.TimestampProto(ctx.FetchTimesTamp)

	ctx.CrawlLog = &frontier.CrawlLog{
		RequestedUri:   req.URL.String(),
		FetchTimeStamp: fetchTimeStamp,
	}

	bccRequest := &browsercontroller.DoRequest{
		Action: &browsercontroller.DoRequest_New{
			New: &browsercontroller.RegisterNew{
				ProxyId:          f.proxyId,
				Uri:              req.URL.String(),
				CrawlExecutionId: executionId,
				JobExecutionId:   jobExecutionId,
				CollectionRef:    collectionRef,
			},
		},
	}

	lf := []log.Field{
		log.String("event", "Send BrowserController New request"),
		log.Int32("ProxyId", f.proxyId),
		log.String("Uri", req.URL.String()),
		log.String("CrawlExecutionId", executionId),
		log.String("JobExecutionId", jobExecutionId),
	}
	if collectionRef != nil {
		lf = append(lf, log.String("CollectionId", collectionRef.Id))
	} else {
		lf = append(lf, log.String("CollectionId", ""))
	}
	span.LogFields(lf...)

	err := ctx.Bcc.Send(bccRequest)
	if err != nil {
		logrus.Fatalf("Error register with browser controller, cause: %v", err)
	}

	bcReply := <-ctx.BccMsgChan

	switch v := bcReply.Action.(type) {
	case *browsercontroller.DoReply_Cancel:
		if v.Cancel == "Blocked by robots.txt" {
			ctx.PrecludedByRobots = true
		}
		span.LogKV("event", "ResponseFromNew", "responseType", "Cancel")
		return req, NewResponse(req, ContentTypeText, 403, v.Cancel)
	case *browsercontroller.DoReply_New:
		span.LogKV("event", "ResponseFromNew", "responseType", "New",
			"JobExecutionId", v.New.JobExecutionId,
			"CrawlExecutionId", v.New.CrawlExecutionId,
			"CollectionId", v.New.CollectionRef.Id,
			"ReplacementScript", v.New.ReplacementScript,
		)
		executionId = v.New.CrawlExecutionId
		jobExecutionId = v.New.GetJobExecutionId()
		collectionRef = v.New.CollectionRef
		ctx.ReplacementScript = v.New.ReplacementScript
	}

	uri := context2.GetUri(c)
	host := context2.GetHost(c)
	ps := context2.GetPort(c)

	fmt.Printf("CONTEXT: %T -- %v %v\n", req.Context(), host, ps)

	var port = 0
	if ps != "" {
		port, err = strconv.Atoi(ps)
		if err != nil {
			ctx.Close()
			panic(fmt.Sprintf("Error parsing port for %v, cause: %v", uri, err))
		}
	}
	dnsReq := &dnsresolver.ResolveRequest{
		CollectionRef: collectionRef,
		Host:          host,
		Port:          int32(port),
	}
	//c, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	//defer cancel()
	dnsResp, err := f.DnsResolverClient.Resolve(req.Context(), dnsReq)
	if err != nil {
		ctx.Close()
		panic(fmt.Sprintf("Error looking up %v, cause: %v", uri.Host, err))
	}

	req.Header.Set(ENCODING, "identity")
	req.Header.Set(CRAWL_EXECUTION_ID, executionId)
	req.Header.Set(JOB_EXECUTION_ID, jobExecutionId)

	ctx.Meta = &contentwriter.WriteRequest_Meta{
		Meta: &contentwriter.WriteRequestMeta{
			RecordMeta:     map[int32]*contentwriter.WriteRequestMeta_RecordMeta{},
			TargetUri:      req.URL.String(),
			ExecutionId:    executionId,
			IpAddress:      dnsResp.TextualIp,
			CollectionRef:  collectionRef,
			FetchTimeStamp: fetchTimeStamp,
		},
	}

	ctx.CrawlLog.JobExecutionId = jobExecutionId
	ctx.CrawlLog.ExecutionId = executionId
	ctx.CrawlLog.IpAddress = dnsResp.TextualIp
	ctx.CrawlLog.RequestedUri = req.URL.String()
	ctx.CrawlLog.FetchTimeStamp = fetchTimeStamp

	contentType := req.Header.Get("Content-Type")
	bodyWrapper, err := WrapBody(req.Body, REQUEST, ctx, 0, -1, contentType, contentwriter.RecordType_REQUEST, prolog.Bytes())
	if err != nil {
		return req, NewResponse(req, ContentTypeText, http.StatusBadGateway, "Veidemann proxy lost connection to GRPC services"+err.Error())
	}
	req.Body = bodyWrapper

	return req, nil
}

func (f *RecorderFilter) filterResponse(span opentracing.Span, respOrig *http.Response, ctx *context2.RecordContext) (resp *http.Response) {
	span.LogKV("event", "StartFilterResponse")

	resp = respOrig
	//ctx.Resp = resp
	if resp == nil {
		ctx.Close()
		panic(http.ErrAbortHandler)
	}

	if ctx.PrecludedByRobots {
		ctx.Close()
		return resp
	}

	if ctx.Error != nil && strings.HasPrefix(ctx.Error.Error(), "unknown error from browser controller") {
		ctx.Close()
		return resp
	}

	if strings.Contains(resp.Header.Get("X-Cache-Lookup"), "HIT") {
		ctx.FoundInCache = true

		span.LogKV("event", "Loaded from cache")
	}

	var prolog bytes.Buffer
	writeResponseProlog(resp, &prolog)

	contentType := resp.Header.Get("Content-Type")
	statusCode := int32(resp.StatusCode)
	var err error
	bodyWrapper, err := WrapBody(resp.Body, RESPONSE, ctx, 1, statusCode, contentType, contentwriter.RecordType_RESPONSE, prolog.Bytes())
	if err != nil {
		ctx.Close()
		return NewResponse(resp.Request, ContentTypeText, http.StatusBadGateway, "Veidemann proxy lost connection to GRPC services\n"+err.Error())
	}

	if ctx.ReplacementScript != nil {
		resp.ContentLength = int64(len(ctx.ReplacementScript.Script))
	}
	resp.Body = bodyWrapper

	return resp
}
