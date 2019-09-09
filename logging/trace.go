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

package logging

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptrace"
)

func DecorateRequest(roundTripper http.RoundTripper, req *http.Request) (http.RoundTripper, *http.Request) {
	t := &transport{wrapped: roundTripper}

	trace := &httptrace.ClientTrace{
		DNSStart:          t.DNSStart,
		DNSDone:           t.DNSDone,
		GotConn:           t.GotConn,
		PutIdleConn:       t.PutIdleConn,
		ConnectStart:      t.ConnectStart,
		ConnectDone:       t.ConnectDone,
		TLSHandshakeStart: t.TLSHandshakeStart,
		TLSHandshakeDone:  t.TLSHandshakeDone,
		GetConn:           t.GetConn,
	}

	return t, req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
}

// transport is an http.RoundTripper that keeps track of the in-flight
// request and implements hooks to report HTTP tracing events.
type transport struct {
	wrapped http.RoundTripper
	current *http.Request
}

// RoundTrip wraps http.DefaultTransport.RoundTrip to keep track
// of the current request.
func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.current = req
	return t.wrapped.RoundTrip(req)
}

func (t *transport) GetConn(hostPort string) {
	fmt.Printf("** Get Conn: %+v\n", hostPort)
}

// GotConn prints whether the connection has been used previously
// for the current request.
func (t *transport) GotConn(info httptrace.GotConnInfo) {
	fmt.Printf("****** Connection reused for %v? %v\n", t.current.URL, info.Reused)
}

func (t *transport) DNSStart(info httptrace.DNSStartInfo) {
	fmt.Printf("** DNS Info: %+v\n", info)
}

func (t *transport) DNSDone(info httptrace.DNSDoneInfo) {
	fmt.Printf("** DNS Info: %+v\n", info)
}

func (t *transport) PutIdleConn(err error) {
	fmt.Printf("** Connection PutIdleCon for %v? %v\n", t.current.URL, err)
}

func (t *transport) ConnectStart(network, addr string) {
	fmt.Printf("** %v %v\n", network, addr)
}

func (t *transport) ConnectDone(network, addr string, err error) {
	fmt.Printf("** %v %v %v\n", network, addr, err)
}

func (t *transport) TLSHandshakeStart() {
	fmt.Println("** Handshake start")
}

func (t *transport) TLSHandshakeDone(state tls.ConnectionState, err error) {
	fmt.Printf("** Handshake done: %v %v %v\n", state.ServerName, state.HandshakeComplete, err)
	for _, c := range state.PeerCertificates {
		fmt.Printf("** Handshake done: %v\n", c.Issuer)
	}
}
