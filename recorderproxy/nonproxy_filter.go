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
	"errors"
	"github.com/nlnwa/veidemann-recorderproxy/filters"
	"net/http"
)

// NonproxyFilter is a filter which returns an error if the proxy is accessed as if it where a web server and not a proxy.
type NonproxyFilter struct{}

func (f *NonproxyFilter) Apply(cs *filters.ConnectionState, req *http.Request, next filters.Next) (*http.Response, *filters.ConnectionState, error) {
	if req.Method == http.MethodConnect {
		return next(cs, req)
	} else if !req.URL.IsAbs() && !cs.IsMITMing() {
		return filters.Fail(cs, req, 500, errors.New("This is a proxy server. Does not respond to non-proxy requests."))
	} else {
		return next(cs, req)
	}
}
