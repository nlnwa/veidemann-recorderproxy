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

package testutil

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"
)

// HttpServers contains test servers for HTTP and HTTPS
type HttpServers struct {
	httpsMux        *http.ServeMux
	httpMux         *http.ServeMux
	SrvHttp         *httptest.Server
	SrvHttps        *httptest.Server
	SrvHttpsBadCert *httptest.Server
}

func replaceIpWithHostname(server *httptest.Server) *httptest.Server {
	u, _ := url.Parse(server.URL)
	server.URL = strings.Replace(server.URL, u.Hostname(), "localhost", 1)
	return server
}

func NewHttpServer(mux *http.ServeMux) (server *httptest.Server) {
	server = httptest.NewUnstartedServer(mux)
	var err error
	server.Listener, err = net.Listen("tcp4", ":0")
	if err != nil {
		panic(err)
	}
	server.Start()
	server = replaceIpWithHostname(server)
	return
}

func NewHttpsServer(mux *http.ServeMux) (server *httptest.Server) {
	server = httptest.NewUnstartedServer(mux)
	var err error
	server.Listener, err = net.Listen("tcp4", ":0")
	if err != nil {
		panic(err)
	}
	server.StartTLS()
	server = replaceIpWithHostname(server)
	return
}

type ConstantHandler string

func (h ConstantHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = io.WriteString(w, string(h))
}

type ConstantSlowHandler struct {
	string
	waitTime time.Duration
}

func NewConstantSlowHandler(response string, waitMillis int) *ConstantSlowHandler {
	return &ConstantSlowHandler{string: response, waitTime: time.Millisecond * time.Duration(waitMillis)}
}

func (h ConstantSlowHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	time.Sleep(h.waitTime)
	_, _ = io.WriteString(w, string(h.string))
}

type ConstantCacheHandler string

func (h ConstantCacheHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Cache", "HIT")
	w.Header().Set("X-Cache-Lookup", "HIT")
	_, _ = io.WriteString(w, string(h))
}

func NewHttpServers() *HttpServers {
	httpMux := http.NewServeMux()
	httpsMux := http.NewServeMux()

	httpMux.Handle("/a", ConstantHandler("content from http server"))
	httpsMux.Handle("/b", ConstantHandler("content from https server"))
	httpMux.Handle("/replace", ConstantHandler("should be replaced"))
	httpsMux.Handle("/replace", ConstantHandler("should be replaced"))
	httpMux.Handle("/slow", NewConstantSlowHandler("content from http server", 600))
	httpsMux.Handle("/slow", NewConstantSlowHandler("content from https server", 600))
	httpMux.Handle("/extraslow", NewConstantSlowHandler("content from http server", 1000))
	httpsMux.Handle("/extraslow", NewConstantSlowHandler("content from https server", 1000))
	httpMux.Handle("/cancel", NewConstantSlowHandler("content from http server", 600))
	httpsMux.Handle("/cancel", NewConstantSlowHandler("content from https server", 600))
	httpMux.Handle("/blocked", NewConstantSlowHandler("content from http server", 600))
	httpsMux.Handle("/blocked", NewConstantSlowHandler("content from https server", 600))
	httpMux.Handle("/bccerr", ConstantHandler("content from http server"))
	httpsMux.Handle("/bccerr", ConstantHandler("content from https server"))
	httpMux.Handle("/cwerr", ConstantHandler("content from http server"))
	httpsMux.Handle("/cwerr", ConstantHandler("content from https server"))
	httpMux.Handle("/cached", ConstantCacheHandler("content from http server"))
	httpsMux.Handle("/cached", ConstantCacheHandler("content from https server"))

	s := &HttpServers{
		SrvHttp:         NewHttpServer(httpMux),
		SrvHttps:        NewHttpsServer(httpsMux),
		SrvHttpsBadCert: NewHttpsServer(httpsMux),
	}

	s.SrvHttp.Config.WriteTimeout = 800 * time.Millisecond
	s.SrvHttps.Config.WriteTimeout = 800 * time.Millisecond
	s.SrvHttpsBadCert.TLS.Certificates = []tls.Certificate{{}}

	fmt.Printf("HTTP server url:           %v\n", s.SrvHttp.URL)
	fmt.Printf("HTTPS server url:          %v\n", s.SrvHttps.URL)
	fmt.Printf("HTTPS bad cert server url: %v\n", s.SrvHttpsBadCert.URL)

	return s
}

func (s *HttpServers) Close() {
	s.SrvHttp.Close()
	s.SrvHttps.Close()
	s.SrvHttpsBadCert.Close()
}
