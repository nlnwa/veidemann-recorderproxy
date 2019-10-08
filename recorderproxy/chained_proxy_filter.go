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
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/log"
	"net/http"
	"net/url"
)

// ChainedProxyFilter is a filter which rewrites request to support chained proxies.
type ChainedProxyFilter struct {
	proxy *RecorderProxy
}

func (f *ChainedProxyFilter) Apply(ctx filters.Context, req *http.Request, next filters.Next) (resp *http.Response, context filters.Context, err error) {
	l := context2.LogWithContextAndRequest(ctx, req, "FLT:chain")
	span := opentracing.SpanFromContext(ctx)

	if req.Method == http.MethodConnect {
		resp, context, err = next(ctx, req)
	} else {
		if context2.GetHost(ctx) == "" || (f.proxy.nextProxy != "" && !ctx.IsMITMing()) {
			span.LogFields(log.String("event", "Rewrite request"))
			rc := context2.GetRecordContext(ctx)
			uri, err := url.Parse("http:" + rc.Uri.String())
			if err != nil {
				l.WithError(err).Warnf("Error parsing uri for chained proxy: %v", "http:"+rc.Uri.String())
			}
			req.URL = uri
		}
		resp, context, err = next(ctx, req)
	}
	return
}
