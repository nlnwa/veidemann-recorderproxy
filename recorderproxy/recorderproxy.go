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
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/getlantern/mitm"
	"github.com/getlantern/proxy"
	"github.com/getlantern/proxy/filters"
	"github.com/nlnwa/veidemann-api-go/commons/v1"
	"github.com/nlnwa/veidemann-recorderproxy/serviceconnections"
	"io"
	"io/ioutil"
	"regexp"
	"strconv"
	"sync/atomic"

	log "github.com/sirupsen/logrus"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	ENCODING           = "Accept-Encoding"
	CRAWL_EXECUTION_ID = "veidemann_eid"
	JOB_EXECUTION_ID   = "veidemann_jeid"
	COLLECTION_ID      = "veidemann_cid"
)

const (
	ContentTypeText = "text/plain"
	ContentTypeHtml = "text/html"
)

var proxyCount int32
var acceptAllCerts = &tls.Config{InsecureSkipVerify: true}

type RecorderProxy struct {
	id                int32
	Addr              string
	conn              *serviceconnections.Connections
	ConnectionTimeout time.Duration
	RoundTripper      *RpRoundTripper

	// ConnectDial will be used to create TCP connections for CONNECT requests
	ConnectDial       func(addr string) (*tls.Conn, error)
	dnsResolverDialer *dnsResolverDialer
}

func NewRecorderProxy(addr string, port int, conn *serviceconnections.Connections, connectionTimeout time.Duration, cache string) *RecorderProxy {
	filterChain := filters.Join(
		&NonproxyFilter{},
		&TracingInitFilter{},
		&ContextInitFilter{conn},
		&RecorderFilter{proxyCount, conn.DnsResolverClient()},
	)

	opts := &proxy.Opts{
		Dial: func(context context.Context, isConnect bool, network, addr string) (conn net.Conn, err error) {
			timeout := 30 * time.Second
			deadline, hasDeadline := context.Deadline()
			if hasDeadline {
				timeout = deadline.Sub(time.Now())
			}
			conn, err = net.DialTimeout(network, addr, timeout)
			if err != nil {
				log.Errorf("??????????????? %v\n", err)
			}
			//if !isConnect {
			conn = WrapConn(conn, "upstream")
			//}
			return conn, err
		},
		//IdleTimeout: 3 * time.Second,
		Filter: filterChain,
		ShouldMITM: func(req *http.Request, upstreamAddr string) bool {
			fmt.Printf("SHOULD MITMT FOR %v %v\n", req.URL, upstreamAddr)
			return true
		},
		MITMOpts: &mitm.Opts{
			Domains:         []string{"*"},
			ClientTLSConfig: acceptAllCerts,
			ServerTLSConfig: acceptAllCerts,
			Organization:    "Veidemann Recorder Proxy",
			CertFile:        "/tmp/rpcert.pem",
		},
		OnError: func(ctx filters.Context, req *http.Request, read bool, err error) *http.Response {
			fmt.Printf("ON ERROR: %v\n", err)
			//return nil
			res, _, _ := filters.Fail(ctx, req, 555, err)
			return res
		},
		OKWaitsForUpstream:  true,
		OKSendsServerTiming: true,
	}

	p, err := proxy.New(opts)
	if err != nil {
		log.Fatal(err)
	}

	l, err := net.Listen("tcp", addr+":"+strconv.Itoa(port))
	if err != nil {
		if l, err = net.Listen(addr+"tcp6", ":"+strconv.Itoa(port)); err != nil {
			panic(fmt.Sprintf("httptest: failed to listen on port %v: %v", port, err))
		}
	}
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		defer l.Close()
		//err := p.Serve(l)
		err := Serve(p, l)
		if err != nil {
			log.Fatal(err)
		}
	}()

	fmt.Printf("#####################################\n")

	r := &RecorderProxy{
		id:   proxyCount,
		conn: conn,
		Addr: l.Addr().String(),
		//addr: ":" + strconv.Itoa(port),
	}
	proxyCount++

	return r

	//r := &RecorderProxy{
	//	id:                proxyCount,
	//	conn:              conn,
	//	addr:              ":" + strconv.Itoa(port),
	//	ConnectionTimeout: connectionTimeout,
	//
	//	RoundTripper: NewRpRoundTripper(),
	//}
	//
	//if cache != "" {
	//	cu, _ := url.Parse("http://" + cache)
	//	r.RoundTripper.Proxy = http.ProxyURL(cu)
	//}
	//
	//proxyCount++
	//
	//if conn.dnsResolverHost != "" {
	//	r.dnsResolverDialer, err = NewDnsResolverDialer(conn.dnsResolverHost, conn.dnsResolverPort)
	//	if err != nil {
	//		log.Fatalf("Could not create CONNECT dialer: \"%s\"", err)
	//	}
	//	r.ConnectDial = r.dnsResolverDialer.DialTls
	//} else {
	//	r.ConnectDial = func(addr string) (conn *tls.Conn, e error) {
	//		return tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
	//	}
	//}
	//
	//return r
}

func Serve(proxy proxy.Proxy, l net.Listener) error {
	for {
		co, err := l.Accept()
		conn := WrapConn(co, "downstream")
		if err != nil {
			log.Errorf("unable to accept: %v", err)
			return err
			//return errors.New("Unable to accept: %v", err)
		}
		//go proxy.Handle(context.Background(), conn, conn)
		//c, _ := context.WithTimeout(context.Background(), 7*time.Second)
		c, cancel := context.WithCancel(context.Background())
		conn.CancelFunc = cancel
		go proxy.Handle(c, conn, conn)
	}
}

func (proxy *RecorderProxy) Start() {
	fmt.Printf("Starting proxy %v...\n", proxy.id)

	//tracer := opentracing.GlobalTracer()
	//go func() {
	//	log.Fatalf("Proxy with addr %v: %v", proxy.addr, http.ListenAndServe(proxy.addr, nethttp.Middleware(tracer, proxy)))
	//}()

	fmt.Printf("Proxy %v started, listening on %v\n", proxy.id, proxy.Addr)
}

type testConn struct {
	net.Conn
	t          string
	closed     *int32
	CancelFunc func()
}

func (conn *testConn) Close() (err error) {
	if atomic.CompareAndSwapInt32(conn.closed, 0, 1) {
		log.Infof("Close %s Conn %v %v %T\n", conn.t, conn.LocalAddr(), conn.RemoteAddr(), conn.Conn)
		if conn.CancelFunc != nil {
			conn.CancelFunc()
		}
	}
	return conn.Conn.Close()
}

func WrapConn(conn net.Conn, label string) *testConn {
	i := int32(0)
	return &testConn{Conn: conn, t: label, closed: &i}
}

func NewResponse(r *http.Request, contentType string, status int, body string) *http.Response {
	resp := &http.Response{}
	resp.ProtoMajor = r.ProtoMajor
	resp.ProtoMinor = r.ProtoMinor
	resp.Request = r
	resp.TransferEncoding = r.TransferEncoding
	resp.Header = make(http.Header)
	if contentType != "" {
		resp.Header.Add("Content-Type", contentType)
	}
	resp.StatusCode = status
	resp.Status = http.StatusText(status)
	buf := bytes.NewBufferString(body)
	resp.ContentLength = int64(buf.Len())
	resp.Body = ioutil.NopCloser(buf)
	return resp
}

type RpRoundTripper struct {
	*http.Transport
}

func NewRpRoundTripper() *RpRoundTripper {
	rt := &RpRoundTripper{
		Transport: http.DefaultTransport.(*http.Transport),
	}
	rt.TLSClientConfig = tlsClientSkipVerify
	return rt
}

func (r *RpRoundTripper) RoundTrip(req *http.Request, ctx *RecordContext) (response *http.Response, e error) {
	var transport http.RoundTripper
	if log.GetLevel() >= log.DebugLevel {
		transport, req = DecorateRequest(r.Transport, req, ctx)
	} else {
		transport = r.Transport
	}
	response, e = transport.RoundTrip(req)

	if e != nil {
		handleResponseError(e, ctx)
	} else {
		if strings.Contains(response.Header.Get("X-Squid-Error"), "ERR_CONNECT_FAIL") {
			err := &commons.Error{}
			err.Code = -2
			err.Msg = "CONNECT_FAILED"
			err.Detail = "Failed to establish tls connection"
			ctx.SendError(err)

			e = errors.New("Connect failed")
		}
	}

	return
}

func handleResponseError(e error, ctx *RecordContext) {
	if e != nil {
		err := &commons.Error{}
		if ctx.Error != nil {
			err.Detail = ctx.Error.Error()
		} else {
			err.Detail = e.Error()
		}
		switch e {
		case io.EOF:
			err.Code = -4
			err.Msg = "HTTP_TIMEOUT"
			err.Detail = "Veidemann recorder proxy lost connection to upstream server"
		case context.Canceled:
			err.Code = -5011
			err.Msg = "CANCELED_BY_BROWSER"
			err.Detail = "Veidemann recorder proxy lost connection to client"
		default:
			switch et := e.(type) {
			case *net.OpError:
				switch {
				case et.Err.Error() == "tls: handshake failure":
					err.Code = -2
					err.Msg = "CONNECT_FAILED"
					err.Detail = et.Err.Error()
				case strings.HasSuffix(et.Err.Error(), "connect: connection refused"):
					err.Code = -2
					err.Msg = "CONNECT_FAILED"
				default:
					err.Code = -5
					err.Msg = "RUNTIME_EXCEPTION"
				}
			default:
				switch {
				case e.Error() == "Bad Gateway":
					err.Code = -2
					err.Msg = "CONNECT_FAILED"
					err.Detail = e.Error()
				default:
					err.Code = -5
					err.Msg = "RUNTIME_EXCEPTION"
				}
			}
		}
		ctx.SendError(err)
	}
}

var hasPort = regexp.MustCompile(`:\d+$`)

func copyHeaders(dst, src http.Header, keepDestHeaders bool) {
	if !keepDestHeaders {
		for k := range dst {
			dst.Del(k)
		}
	}
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func isEof(r *bufio.Reader) bool {
	_, err := r.Peek(1)
	if err == io.EOF {
		return true
	}
	return false
}

func removeProxyHeaders(ctx *RecordContext, r *http.Request) {
	r.RequestURI = "" // this must be reset when serving a request with the client
	//ctx.SessionLogger().Debugf("Sending request %v %v", r.Method, r.URL.String())
	// If no Accept-Encoding header exists, Transport will add the headers it can accept
	// and would wrap the response body with the relevant reader.
	r.Header.Del("Accept-Encoding")
	// curl can add that, see
	// https://jdebp.eu./FGA/web-proxy-connection-header.html
	r.Header.Del("Proxy-Connection")
	r.Header.Del("Proxy-Authenticate")
	r.Header.Del("Proxy-Authorization")
	// Connection, Authenticate and Authorization are single hop Header:
	// http://www.w3.org/Protocols/rfc2616/rfc2616.txt
	// 14.10 Connection
	//   The Connection general-header field allows the sender to specify
	//   options that are desired for that particular connection and MUST NOT
	//   be communicated by proxies over further connections.
	r.Header.Del("Connection")
}
