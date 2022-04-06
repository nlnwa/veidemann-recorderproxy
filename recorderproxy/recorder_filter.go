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
	"github.com/golang/protobuf/ptypes"
	"github.com/nlnwa/veidemann-api/go/contentwriter/v1"
	dnsresolverV1 "github.com/nlnwa/veidemann-api/go/dnsresolver/v1"
	"github.com/nlnwa/veidemann-recorderproxy/constants"
	"github.com/nlnwa/veidemann-recorderproxy/errors"
	"github.com/nlnwa/veidemann-recorderproxy/filters"
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/log"
	"net/http"
	"strings"
)

// RecorderFilter is a filter which ... TODO
type RecorderFilter struct {
	proxyId           int32
	DnsResolverClient dnsresolverV1.DnsResolverClient
	hasNextProxy      bool
}

func (f *RecorderFilter) Apply(cs *filters.ConnectionState, req *http.Request, next filters.Next) (resp *http.Response, state *filters.ConnectionState, err error) {
	l := cs.LogWithContextAndRequest(req, "FLT:rec")
	if cs.ConnectErr != nil {
		return next(cs, req)
	}

	if req.Method == http.MethodConnect {
		// Handle HTTPS CONNECT
		resp, state, err = next(cs, req)
		if err != nil {
			l.WithError(err).Infof("Could not CONNECT to upstream server: %v", req.Host)
		}
		resp, state, err = filters.ShortCircuit(state, req, &http.Response{
			StatusCode: http.StatusOK,
		})
	} else {
		span := opentracing.SpanFromContext(req.Context())

		span.LogFields(log.String("event", "rec upstream request"))

		req, err = f.filterRequest(cs, span, req)
		if err != nil {
			return handleRequestError(cs, req, err)
		}

		roundTripSpan, _ := opentracing.StartSpanFromContext(req.Context(), "Roundtrip upstream")
		resp, state, err = next(cs, req)
		roundTripSpan.Finish()

		if err != nil {
			return
		}

		resp, err = f.filterResponse(state, span, resp)
		if err != nil {
			return handleRequestError(state, req, err)
		}

		span.LogFields(log.String("event", "rec upstream response"))
	}
	return
}

func (f *RecorderFilter) filterRequest(c *filters.ConnectionState, span opentracing.Span, req *http.Request) (*http.Request, error) {
	span.LogKV("event", "StartFilterRequest")

	var prolog bytes.Buffer
	err := writeRequestProlog(req, &prolog)
	if err != nil {
		e := errors.WrapInternalError(err, errors.RuntimeException, "Unable to write request headers", err.Error())
		return req, e
	}

	fetchTimeStamp, _ := ptypes.TimestampProto(c.FetchTimesTamp)
	uri := c.Uri

	req.Header.Set(constants.HeaderAcceptEncoding, "identity")
	req.Header.Set(constants.HeaderCrawlExecutionId, c.CrawlExecId)
	req.Header.Set(constants.HeaderJobExecutionId, c.JobExecId)

	c.Meta = &contentwriter.WriteRequest_Meta{
		Meta: &contentwriter.WriteRequestMeta{
			RecordMeta:     map[int32]*contentwriter.WriteRequestMeta_RecordMeta{},
			TargetUri:      uri.String(),
			ExecutionId:    c.CrawlExecId,
			IpAddress:      c.Ip,
			CollectionRef:  c.CollectionRef,
			FetchTimeStamp: fetchTimeStamp,
		},
	}

	c.CrawlLog.RequestedUri = uri.String()

	contentType := req.Header.Get("Content-Type")
	bodyWrapper, err := WrapRequestBody(c, req.Body, contentType, prolog.Bytes())
	if err != nil {
		e := errors.WrapInternalError(err, errors.RuntimeException, "Veidemann proxy lost connection to GRPC services", err.Error())
		return req, e
	}
	req.Body = bodyWrapper

	return req, nil
}

func (f *RecorderFilter) filterResponse(c *filters.ConnectionState, span opentracing.Span, respOrig *http.Response) (*http.Response, error) {
	span.LogKV("event", "StartFilterResponse")

	resp := respOrig
	if resp == nil {
		panic(http.ErrAbortHandler)
	}

	if c.Error != nil && strings.HasPrefix(c.Error.Error(), "unknown error from browser controller") {
		return resp, nil
	}

	if isFromCache(resp) {
		span.LogKV("event", "Loaded from cache")
		c.LogWithContext("FLT:rec").Info("Loaded from cache")
		c.FoundInCache = true
	}

	var prolog bytes.Buffer
	err := writeResponseProlog(resp, &prolog)
	if err != nil {
		e := errors.WrapInternalError(err, errors.RuntimeException, "Unable to write response headers", err.Error())
		return resp, e
	}

	contentType := resp.Header.Get("Content-Type")
	statusCode := int32(resp.StatusCode)
	bodyWrapper, err := WrapResponseBody(c, resp.Body, statusCode, contentType, contentwriter.RecordType_RESPONSE, prolog.Bytes())
	if err != nil {
		e := errors.WrapInternalError(err, errors.RuntimeException, "Veidemann proxy lost connection to GRPC services", err.Error())
		return nil, e
	}

	if c.ReplacementScript != nil {
		c.LogWithContext("FLT:rec").Info("Replacement script")
		resp.ContentLength = int64(len(c.ReplacementScript.Script))
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
		if strings.Contains(v, "HIT from veidemann_cache") {
			return true
		}
	}

	return false
}
