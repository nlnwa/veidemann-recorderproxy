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
	"io"
	"net/http"
)

func (proxy *RecorderProxy) handleHttp(w http.ResponseWriter, r *http.Request) {
	ctx := NewRecordContext(proxy)
	ctx.init(r)

	var err error
	ctx.Logf("Got request on proxy #%v, port %v for %s %s\n", proxy.id, proxy.addr, r.Method, r.URL.String())
	if !r.URL.IsAbs() {
		proxy.NonproxyHandler.ServeHTTP(w, r)
		return
	}
	r, resp := proxy.filterRequest(r, ctx)

	if resp == nil {
		removeProxyHeaders(ctx, r)
		resp, err = proxy.RoundTripper.RoundTrip(r, ctx)
		if err != nil {
			ctx.Error = err
			resp = proxy.filterResponse(nil, ctx)
			if resp == nil {
				ctx.Logf("error read response %v %v:", r.URL.Host, err.Error())
				http.Error(w, err.Error(), 500)
				return
			}
		}
		ctx.Logf("Received response %v", resp.Status)
	}
	origBody := resp.Body
	resp = proxy.filterResponse(resp, ctx)
	defer func() {
		e := resp.Body.Close()
		if e != nil {
			ctx.Warnf("Error while closing body: %v\n", e)
		}
	}()

	ctx.Logf("Copying response to client %v [%d]", resp.Status, resp.StatusCode)
	// http.ResponseWriter will take care of filling the correct response length
	// Setting it now, might impose wrong value, contradicting the actual new
	// body the user returned.
	// We keep the original body to remove the header only if things changed.
	// This will prevent problems with HEAD requests where there's no body, yet,
	// the Content-Length header should be set.
	if origBody != resp.Body {
		resp.Header.Del("Content-Length")
	}
	copyHeaders(w.Header(), resp.Header, proxy.KeepDestinationHeaders)
	w.WriteHeader(resp.StatusCode)
	nr, err := io.Copy(w, resp.Body)
	if err := resp.Body.Close(); err != nil {
		ctx.Warnf("Can't close response body %v", err)
	}
	ctx.Logf("Copied %v bytes to client error=%v", nr, err)
}
