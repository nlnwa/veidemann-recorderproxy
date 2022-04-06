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
	"github.com/nlnwa/veidemann-recorderproxy/filters"
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/log"
	"net/http"
	"net/url"
)

// ChainedProxyFilter is a filter which rewrites request to support chained proxies.
type ChainedProxyFilter struct {
	proxy *RecorderProxy
}

func (f *ChainedProxyFilter) Apply(cs *filters.ConnectionState, req *http.Request, next filters.Next) (*http.Response, *filters.ConnectionState, error) {
	l := cs.LogWithContextAndRequest(req, "FLT:chain")
	span := opentracing.SpanFromContext(req.Context())

	if req.Method == http.MethodConnect {
		return next(cs, req)
	} else {
		if cs.Host == "" || (f.proxy.nextProxy != "" && !cs.IsMITMing()) {
			span.LogFields(log.String("event", "Rewrite request"))
			uri, err := url.Parse("http:" + cs.Uri.String())
			if err != nil {
				l.WithError(err).Warnf("Error parsing uri for chained proxy: %v", "http:"+cs.Uri.String())
			}
			req.URL = uri
		}
		return next(cs, req)
	}
}
