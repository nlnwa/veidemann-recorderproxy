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
	"github.com/getlantern/proxy/filters"
	"github.com/golang/protobuf/ptypes"
	"github.com/nlnwa/veidemann-api-go/contentwriter/v1"
	dnsresolverV1 "github.com/nlnwa/veidemann-api-go/dnsresolver/v1"
	"github.com/nlnwa/veidemann-recorderproxy/constants"
	context2 "github.com/nlnwa/veidemann-recorderproxy/context"
	"github.com/nlnwa/veidemann-recorderproxy/errors"
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/log"
	"net/http"
	"strings"
)

// RecorderFilter is a filter which returns an error if the proxy is accessed as if it where a web server and not a proxy.
type RecorderFilter struct {
	proxyId           int32
	DnsResolverClient dnsresolverV1.DnsResolverClient
	hasNextProxy      bool
}

func (f *RecorderFilter) Apply(ctx filters.Context, req *http.Request, next filters.Next) (resp *http.Response, context filters.Context, err error) {
	l := context2.LogWithContextAndRequest(ctx, req, "FLT:rec")
	connectErr := context2.GetConnectError(ctx)
	if connectErr != nil {
		resp, context, err = next(ctx, req)
		return
	}

	if req.Method == http.MethodConnect {
		// Handle HTTPS CONNECT
		resp, context, err = next(ctx, req)
		if err != nil {
			l.WithError(err).Infof("Could not CONNECT to upstream server: %v", req.Host)
		}
		resp, ctx, err = filters.ShortCircuit(ctx, req, &http.Response{
			StatusCode: http.StatusOK,
		})
	} else {
		rc := context2.GetRecordContext(ctx)
		span := opentracing.SpanFromContext(ctx)

		span.LogFields(log.String("event", "rec upstream request"))

		req, err = f.filterRequest(ctx, span, req, rc)
		if err != nil {
			return handleRequestError(ctx, req, err)
		}

		context = ctx
		roundTripSpan, roundtripCtx := opentracing.StartSpanFromContext(ctx, "Roundtrip upstream")
		resp, roundtripCtx, err = next(context2.WrapIfNecessary(roundtripCtx), req)
		roundTripSpan.Finish()

		if err != nil {
			return
		}

		resp, err = f.filterResponse(ctx, span, resp, rc)
		if err != nil {
			return handleRequestError(ctx, req, err)
		}

		span.LogFields(log.String("event", "rec upstream response"))
	}
	return
}

func (f *RecorderFilter) filterRequest(c filters.Context, span opentracing.Span, req *http.Request, rc *context2.RecordContext) (*http.Request, error) {
	span.LogKV("event", "StartFilterRequest")

	var prolog bytes.Buffer
	err := writeRequestProlog(req, &prolog)

	fetchTimeStamp, _ := ptypes.TimestampProto(rc.FetchTimesTamp)
	uri := rc.Uri

	req.Header.Set(constants.HeaderAcceptEncoding, "identity")
	req.Header.Set(constants.HeaderCrawlExecutionId, rc.CrawlExecutionId)
	req.Header.Set(constants.HeaderJobExecutionId, rc.JobExecutionId)

	rc.Meta = &contentwriter.WriteRequest_Meta{
		Meta: &contentwriter.WriteRequestMeta{
			RecordMeta:     map[int32]*contentwriter.WriteRequestMeta_RecordMeta{},
			TargetUri:      uri.String(),
			ExecutionId:    rc.CrawlExecutionId,
			IpAddress:      rc.IP,
			CollectionRef:  rc.CollectionRef,
			FetchTimeStamp: fetchTimeStamp,
		},
	}

	rc.CrawlLog.RequestedUri = uri.String()

	contentType := req.Header.Get("Content-Type")
	bodyWrapper, err := WrapRequestBody(c, req.Body, contentType, prolog.Bytes())
	if err != nil {
		e := errors.WrapInternalError(err, errors.RuntimeException, "Veidemann proxy lost connection to GRPC services", err.Error())
		return req, e
	}
	req.Body = bodyWrapper

	return req, nil
}

func (f *RecorderFilter) filterResponse(c filters.Context, span opentracing.Span, respOrig *http.Response, rc *context2.RecordContext) (*http.Response, error) {
	span.LogKV("event", "StartFilterResponse")

	resp := respOrig
	if resp == nil {
		panic(http.ErrAbortHandler)
	}

	if rc.Error != nil && strings.HasPrefix(rc.Error.Error(), "unknown error from browser controller") {
		return resp, nil
	}

	if isFromCache(resp) {
		span.LogKV("event", "Loaded from cache")
		context2.LogWithRecordContext(rc, "FLT:rec").Info("Loaded from cache")
		rc.FoundInCache = true
	}

	var prolog bytes.Buffer
	writeResponseProlog(resp, &prolog)

	contentType := resp.Header.Get("Content-Type")
	statusCode := int32(resp.StatusCode)
	var err error
	bodyWrapper, err := WrapResponseBody(c, resp.Body, statusCode, contentType, contentwriter.RecordType_RESPONSE, prolog.Bytes())
	if err != nil {
		e := errors.WrapInternalError(err, errors.RuntimeException, "Veidemann proxy lost connection to GRPC services", err.Error())
		return nil, e
	}

	if rc.ReplacementScript != nil {
		resp.ContentLength = int64(len(rc.ReplacementScript.Script))
	}
	resp.Body = bodyWrapper

	return resp, nil
}

func isFromCache(resp *http.Response) bool {
	cacheHeaders := resp.Header["X-Cache"]
	if cacheHeaders == nil {
		return false
	}

	for _, v := range cacheHeaders {
		if strings.Contains(v, "veidemann_cache") && strings.Contains(v, "HIT") {
			return true
		}
	}

	return false
}
