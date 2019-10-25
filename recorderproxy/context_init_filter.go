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
	"github.com/getlantern/proxy/filters"
	context2 "github.com/nlnwa/veidemann-recorderproxy/context"
	"github.com/nlnwa/veidemann-recorderproxy/serviceconnections"
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/log"
	"net/http"
)

// ContextInitFilter is a filter which initializes the context with sessions to external services.
type ContextInitFilter struct {
	conn    *serviceconnections.Connections
	proxyId int32
}

func (f *ContextInitFilter) Apply(ctx filters.Context, req *http.Request, next filters.Next) (resp *http.Response, context filters.Context, err error) {
	l := context2.LogWithContextAndRequest(ctx, req, "FLT:ctx")

	if req.Method == http.MethodConnect {
		// Handle HTTPS CONNECT
		context2.SetHost(ctx, req.URL.Hostname())

		// Copy URI by value and add scheme
		uv := *req.URL
		uri := &uv
		uri.Scheme = "https"

		context2.SetUri(ctx, uri)
		req = req.WithContext(ctx)

		l.Debugf("Converted CONNECT request uri form %v to %v", req.URL, uri)
		resp, context, err = next(ctx, req)
	} else {
		if context2.GetHost(ctx) == "" {
			context2.SetHost(ctx, req.URL.Hostname())
		}

		uri := context2.GetUri(ctx)
		if uri != nil {
			uri = uri.ResolveReference(req.URL)
		} else {
			uri = req.URL
		}

		l.Debugf("Converted GET request uri form %v to %v", req.URL, uri)

		rc := context2.NewRecordContext()
		context2.SetRecordContext(ctx, rc)
		req = req.WithContext(ctx)
		span := opentracing.SpanFromContext(ctx)
		span.LogFields(log.String("event", "Start init record context"))
		rc.Init(f.proxyId, f.conn, req, uri)

		if e := rc.RegisterNewRequest(ctx); e != nil {
			span.LogFields(log.String("event", "Failed init record context"), log.Error(e))
			return handleRequestError(ctx, req, e)
		}
		span.LogFields(log.String("event", "Finished init record context"))

		resp, context, err = next(ctx, req)
	}
	return
}
