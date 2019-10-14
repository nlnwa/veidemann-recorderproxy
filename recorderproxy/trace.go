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
	"crypto/tls"
	"github.com/nlnwa/veidemann-recorderproxy/logger"
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/log"
	"net/http"
	"net/http/httptrace"
)

func DecorateRequest(roundTripper http.RoundTripper, req *http.Request) (http.RoundTripper, *http.Request) {
	t := &transport{wrapped: roundTripper}
	t.log = logger.StandardLogger().WithComponent("CLIENT")

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
	log     *logger.Logger
}

// RoundTrip wraps http.DefaultTransport.RoundTrip to keep track
// of the current request.
func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.current = req
	return t.wrapped.RoundTrip(req)
}

func (t *transport) GetConn(hostPort string) {
	t.log.Tracef("get conn: %+v", hostPort)
}

// GotConn prints whether the connection has been used previously
// for the current request.
func (t *transport) GotConn(info httptrace.GotConnInfo) {
	t.log.Tracef("got connection for %v. Is resued? %v", t.current.URL, info.Reused)
}

func (t *transport) DNSStart(info httptrace.DNSStartInfo) {
	t.log.Debugf("DNS start: %+v\n", info.Host)
}

func (t *transport) DNSDone(info httptrace.DNSDoneInfo) {
	t.log.Debugf("DNS done: %+v\n", info)
}

func (t *transport) PutIdleConn(err error) {
	t.log.Debugf("** Connection PutIdleCon for %v? %v\n", t.current.URL, err)
}

func (t *transport) ConnectStart(network, addr string) {
	t.log.Debugf("Connecting to %v %v\n", network, addr)
}

func (t *transport) ConnectDone(network, addr string, err error) {
	t.log.Debugf("Connected to %v %v %v\n", network, addr, err)
}

func (t *transport) TLSHandshakeStart() {
	t.log.Debugf("TLS Handshake start")
	span := opentracing.SpanFromContext(t.current.Context())
	span.LogFields(
		log.String("event", "TLSHandshakeStart"),
	)
}

func (t *transport) TLSHandshakeDone(state tls.ConnectionState, err error) {
	t.log.Debugf("TLS Handshake done: %v %v %v\n", state.ServerName, state.HandshakeComplete, err)
	for _, c := range state.PeerCertificates {
		t.log.Debugf("TLS peer cert: %v\n", c.Issuer)
	}
	span := opentracing.SpanFromContext(t.current.Context())
	span.LogFields(
		log.String("event", "TLSHandshakeDone"),
		log.String("host", state.ServerName),
		log.Bool("complete", state.HandshakeComplete),
		log.Error(err),
	)
}
