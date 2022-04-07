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
	"context"
	"crypto/tls"
	"github.com/nlnwa/veidemann-recorderproxy/errors"
	"github.com/nlnwa/veidemann-recorderproxy/filters"
	"github.com/nlnwa/veidemann-recorderproxy/logger"
	"github.com/nlnwa/veidemann-recorderproxy/mitm"
	"github.com/nlnwa/veidemann-recorderproxy/proxy"
	"github.com/nlnwa/veidemann-recorderproxy/serviceconnections"
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	"io"
	"strconv"
	"sync/atomic"

	log "github.com/sirupsen/logrus"
	"net"
	"net/http"
	"time"
)

const (
	CRLF = "\r\n"
)

var acceptAllCerts = &tls.Config{InsecureSkipVerify: true}

type RecorderProxy struct {
	proxy.Proxy
	id                int32
	Addr              string
	conn              *serviceconnections.Connections
	ConnectionTimeout time.Duration
	nextProxy         string
	listener          net.Listener
	shouldRun         bool
}

func NewRecorderProxy(id int, addr string, port int, conn *serviceconnections.Connections, connectionTimeout time.Duration, nextProxyAddr string) *RecorderProxy {
	port += id

	r := &RecorderProxy{
		id:        int32(id),
		conn:      conn,
		nextProxy: nextProxyAddr,
		shouldRun: true,
	}

	filterChain := filters.Join(
		&NonproxyFilter{},
		&TracingInitFilter{},
		&ContextInitFilter{conn, int32(id)},
		&DnsLookupFilter{conn.DnsResolverClient()},
		&RecorderFilter{int32(id), conn.DnsResolverClient(), nextProxyAddr != ""},
		&ErrorHandlerFilter{nextProxyAddr != ""},
	)

	var chainedProxyFilter *ChainedProxyFilter
	if nextProxyAddr != "" {
		chainedProxyFilter = &ChainedProxyFilter{}
		filterChain = filterChain.Append(chainedProxyFilter)
	}

	proxyOpts := &proxy.Opts{
		Dial: r.Dial,
		//IdleTimeout: 3 * time.Second,
		Filter: filterChain,
		ShouldMITM: func(req *http.Request, upstreamAddr string) bool {
			return true
		},
		MITMOpts: &mitm.Opts{
			Domains:         []string{"*"},
			ClientTLSConfig: acceptAllCerts,
			ServerTLSConfig: acceptAllCerts,
			Organization:    "Veidemann Recorder Proxy",
			CertFile:        "/tmp/rpcert.pem",
		},
		OnError: func(cs *filters.ConnectionState, req *http.Request, read bool, err error) *http.Response {
			log.WithError(err).Error("Probably bug. Error handled by OnError should have been handled elsewhere.")
			res, _, _ := filters.Fail(cs, req, 500, err)
			return res
		},
		OKWaitsForUpstream:  false,
		OKSendsServerTiming: false,
		WriteResponseInterceptor: func(cs *filters.ConnectionState, downstream io.Writer, req *http.Request, resp *http.Response, invoker proxy.WriteResponseInvoker) error {
			roundTripSpan, _ := opentracing.StartSpanFromContext(req.Context(), "Write downstream")
			ext.HTTPUrl.Set(roundTripSpan, req.URL.String())
			ext.HTTPMethod.Set(roundTripSpan, req.Method)
			ext.HTTPStatusCode.Set(roundTripSpan, uint16(resp.StatusCode))
			ext.SpanKind.Set(roundTripSpan, ext.SpanKindRPCServerEnum)
			err := invoker(cs, downstream, req, resp)
			roundTripSpan.Finish()

			//rc := context2.GetRecordContext(ctx)
			//if rc != nil {
			if cs.CrawlLog != nil {
				cs.ResponseCompleted(resp, err)
				cs.WaitForCompleted()
			}
			//}
			return err
		},
		Conn: conn,
	}

	//proxyOpts.InitMITM = func() (interceptor proxy.MITMInterceptor, e error) {
	//	i, e := mitm.Configure(proxyOpts.MITMOpts)
	//	return &errorForwardingMITMInterceptor{i}, e
	//}

	var err error
	r.Proxy, err = proxy.New(proxyOpts)
	if err != nil {
		log.Fatal(err)
	}

	r.listener, err = net.Listen("tcp", addr+":"+strconv.Itoa(port))
	if err != nil {
		if r.listener, err = net.Listen(addr+"tcp6", ":"+strconv.Itoa(port)); err != nil {
			log.Panicf("failed to listen on port %v: %v", port, err)
		}
	}
	if err != nil {
		log.Fatal(err)
	}

	r.Addr = r.listener.Addr().String()

	if chainedProxyFilter != nil {
		chainedProxyFilter.proxy = r
	}

	return r
}

//
//type errorForwardingMITMInterceptor struct {
//	*mitm.Interceptor
//}
//
//func (e *errorForwardingMITMInterceptor) MITM(cs *filters.ConnectionState, downstream net.Conn, upstream net.Conn) (newDown net.Conn, newUp net.Conn, success bool, err error) {
//	newDown, newUp, success, err = e.Interceptor.MITM(downstream, upstream)
//	if err != nil && cs.ConnectErr == nil {
//		cs.ConnectErr = err
//		err = nil
//	}
//	return
//}

func (proxy *RecorderProxy) Start() {
	l := log.WithField("component", "PROXY")
	l.Infof("Starting proxy %v ...", proxy.id)

	go func() {
		for proxy.shouldRun {
			co, err := proxy.listener.Accept()
			if err != nil {
				l.Errorf("unable to accept: %v", err)
			}

			conn := WrapConn(co, "down", false)
			c, cancel := context.WithCancel(context.Background())

			conn.CancelFunc = cancel
			go func() {
				err := proxy.Handle(c, conn, conn)
				if err != nil && errors.Code(err) == errors.RuntimeException {
					l.Errorf("Error handling request: %v", err)
				}
			}()
		}
		err := proxy.listener.Close()
		if err != nil {
			l.Fatal(err)
		}
	}()

	l.Infof("Proxy %v started, listening on %v\n", proxy.id, proxy.Addr)
}

func (proxy *RecorderProxy) Close() {
	l := log.WithField("component", "PROXY")
	l.Infof("Shutting down proxy %v ...", proxy.id)

	proxy.shouldRun = false
	var lo int64
	for {
		openSessions := filters.OpenSessions()
		if openSessions > 0 {
			if openSessions != lo {
				l.Infof("Waiting for %d sessions to complete", openSessions)
			}
			lo = openSessions
			time.Sleep(200 * time.Millisecond)
		} else {
			break
		}
	}

	l.Infof("Proxy %v shut down", proxy.id)
}

type wrappedConnection struct {
	net.Conn
	t          string
	closed     *int32
	CancelFunc func()
	dirOut     bool
}

func (conn *wrappedConnection) Close() (err error) {
	l := log.WithField("component", "CONN:"+conn.t)
	if atomic.CompareAndSwapInt32(conn.closed, 0, 1) {
		if conn.dirOut {
			l.Debugf("Close connection %v -> %v\n", conn.LocalAddr(), conn.RemoteAddr())
		} else {
			l.Debugf("Close connection %v -> %v\n", conn.RemoteAddr(), conn.LocalAddr())
		}
		if conn.CancelFunc != nil {
			conn.CancelFunc()
		}
	}
	return conn.Conn.Close()
}

func (conn *wrappedConnection) Read(b []byte) (n int, err error) {
	n, err = conn.Conn.Read(b)
	l := log.WithField("component", "CONN:"+conn.t)
	if err != nil {
		l = l.WithError(err)
	}
	if log.IsLevelEnabled(log.TraceLevel) {
		l.Tracef("read:\n%s\n", logger.FormatPayload(b, n, 10, 20))
	} else {
		l.Debugf("read: %s", logger.FormatPayload(b, n, 10, 20))
	}
	return
}

func (conn *wrappedConnection) Write(b []byte) (n int, err error) {
	n, err = conn.Conn.Write(b)
	l := log.WithField("component", "CONN:"+conn.t)
	if err != nil {
		l = l.WithError(err)
	}
	if log.IsLevelEnabled(log.TraceLevel) {
		l.Tracef("write:\n%s\n", logger.FormatPayload(b, n, 10, 20))
	} else {
		l.Debugf("write: %s", logger.FormatPayload(b, n, 10, 20))
	}
	return
}

func WrapConn(conn net.Conn, label string, dirOut bool) *wrappedConnection {
	l := log.WithField("component", "CONN:"+label)
	if dirOut {
		l.Debugf("New connection %v -> %v\n", conn.LocalAddr(), conn.RemoteAddr())
	} else {
		l.Debugf("New connection %v -> %v\n", conn.RemoteAddr(), conn.LocalAddr())
	}
	i := int32(0)
	return &wrappedConnection{Conn: conn, t: label, dirOut: dirOut, closed: &i}
}
