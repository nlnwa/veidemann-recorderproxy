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

func (f *ContextInitFilter) Apply(cs *filters.ConnectionState, req *http.Request, next filters.Next) (*http.Response, *filters.ConnectionState, error) {
	l := cs.LogWithContextAndRequest(req, "FLT:ctx")

	if req.Method == http.MethodConnect {
		// Handle HTTPS CONNECT
		cs.Host = req.URL.Hostname()
		cs.Port = req.URL.Port()

		// Copy URI by value and add scheme
		uv := *req.URL
		uri := &uv
		uri.Scheme = "https"

		cs.Uri = uri
		cs.RegisterConnectRequest(f.conn, f.proxyId, req, uri)

		l.Debugf("Converted CONNECT request uri form %v to %v", req.URL, uri)
		return next(cs, req)
	} else {
		if cs.Host == "" {
			cs.Host = req.URL.Hostname()
			cs.Port = req.URL.Port()
		}

		uri := cs.Uri
		if uri != nil {
			uri = uri.ResolveReference(req.URL)
		} else {
			uri = req.URL
		}

		l.Debugf("Converted GET request uri form %v to %v", req.URL, uri)

		span := opentracing.SpanFromContext(req.Context())
		span.LogFields(log.String("event", "Start init record context"))
		cs.Init(f.proxyId, f.conn, req, uri)

		if e := cs.RegisterNewRequest(); e != nil {
			span.LogFields(log.String("event", "Failed init record context"), log.Error(e))
			return handleRequestError(cs, req, e)
		}
		span.LogFields(log.String("event", "Finished init record context"))

		return next(cs, req)
	}
}
