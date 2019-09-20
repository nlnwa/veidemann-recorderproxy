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
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	"github.com/opentracing/opentracing-go/log"
	"net/http"
)

// TracingInitFilter is a filter which initializes the context with tracing.
type TracingInitFilter struct{}

func (f *TracingInitFilter) Apply(ctx filters.Context, req *http.Request, next filters.Next) (resp *http.Response, context filters.Context, err error) {
	if req.Method != http.MethodConnect {
		tr := opentracing.GlobalTracer()
		spanCtx, _ := tr.Extract(opentracing.HTTPHeaders, opentracing.HTTPHeadersCarrier(req.Header))
		span := tr.StartSpan("HTTP "+req.Method, ext.RPCServerOption(spanCtx))
		ext.HTTPMethod.Set(span, req.Method)
		ext.HTTPUrl.Set(span, req.URL.String())

		componentName := "recorderProxy"
		ext.Component.Set(span, componentName)

		c := opentracing.ContextWithSpan(ctx, span)
		ctx = filters.AdaptContext(c)
		req = req.WithContext(ctx)
		defer span.Finish()

		span.LogFields(log.String("event", "upstream request"))
		resp, context, err = next(ctx, req)
		span.LogFields(log.String("event", "upstream response"))
		return
	} else {
		return next(ctx, req)
	}
}
