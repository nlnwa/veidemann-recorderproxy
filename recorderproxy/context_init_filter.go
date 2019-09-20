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
	"fmt"
	"github.com/getlantern/proxy/filters"
	context2 "github.com/nlnwa/veidemann-recorderproxy/context"
	"github.com/nlnwa/veidemann-recorderproxy/serviceconnections"
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/log"
	"net/http"
)

// ContextInitFilter is a filter which initializes the context with sessions to external services.
type ContextInitFilter struct {
	conn *serviceconnections.Connections
}

func (f *ContextInitFilter) Apply(ctx filters.Context, req *http.Request, next filters.Next) (resp *http.Response, context filters.Context, err error) {
	if req.Method != http.MethodConnect {
		if context2.GetHost(ctx) == "" {
			ctx = context2.SetHostPort(ctx, req.URL.Hostname(), req.URL.Port())
		}
		ctx, uri := context2.ResolveAndSetUri(ctx, req.URL)
		fmt.Printf("URI: %s\n", uri)

		rc := context2.NewRecordContext()
		ctx = context2.SetRecordContext(ctx, rc)
		req = req.WithContext(ctx)
		span := opentracing.SpanFromContext(ctx)
		rc.Init(f.conn, req)

		resp, context, err = next(ctx, req)
		span.LogFields(log.String("event", "??????????"))
		return
	} else {
		// Handle HTTPS CONNECT
		ctx = context2.SetHostPort(ctx, req.URL.Hostname(), req.URL.Port())

		// Copy URI by value and add scheme
		uv := *req.URL
		uri := &uv
		uri.Scheme = "https"

		ctx, uri = context2.ResolveAndSetUri(ctx, uri)
		fmt.Printf("*************** CONNECT URI: %s %s\n", uri, req.URL)
		req = req.WithContext(ctx)

		return next(ctx, req)
	}
}
