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

package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/getlantern/mitm"
	"github.com/getlantern/proxy"
	"github.com/getlantern/proxy/filters"
	"github.com/nlnwa/veidemann-recorderproxy/errors"
	"github.com/nlnwa/veidemann-recorderproxy/logger"
	"github.com/nlnwa/veidemann-recorderproxy/recorderproxy"
	"github.com/nlnwa/veidemann-recorderproxy/serviceconnections"
	"github.com/nlnwa/veidemann-recorderproxy/tracing"
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	log2 "github.com/opentracing/opentracing-go/log"
	log "github.com/sirupsen/logrus"
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"
)

var (
	acceptAllCerts = &tls.Config{InsecureSkipVerify: true}
	server         *httptest.Server
)

func main() {
	flag.BoolP("help", "h", false, "Usage instructions")
	flag.String("log-level", "info", "log level, available levels are panic, fatal, error, warn, info, debug and trace")
	flag.String("log-formatter", "text", "log formatter, available values are text, logfmt and json")
	flag.Bool("log-method", false, "log method name")
	flag.Parse()
	viper.BindPFlags(flag.CommandLine)

	logger.InitLog(viper.GetString("log-level"), viper.GetString("log-formatter"), viper.GetBool("log-method"))

	tracer, closer := tracing.Init("Recorder Proxy")
	opentracing.SetGlobalTracer(tracer)
	defer closer.Close()

	if flag.NArg() != 1 || viper.GetBool("help") {
		flag.Usage()
		return
	}

	grpcServices := NewGrpcServiceMock()
	if true {
		_ = newProxy(grpcServices)
	} else {
		url := flag.Arg(0)

		client := newProxy(grpcServices)

		clientTimeout := 5000 * time.Second

		statusCode, got, err := get(url, client, clientTimeout)
		if grpcServices.doneBC != nil {
			<-grpcServices.doneBC
		}
		if grpcServices.doneCW != nil {
			<-grpcServices.doneCW
		}

		logger.LogWithComponent("CLIENT").Infof("Status: %v", statusCode)

		if len(got) > 0 {
			logger.LogWithComponent("CLIENT").Printf("Content: %s... (%d bytes)\n\n", got[0:10], len(got))
			//recorderproxy.LogWithComponent("CLIENT").Printf("Content: %s... (%d bytes)\n\n", got, len(got))
		}

		if err != nil {
			errors.LogError(errors.InvalidRequest, err.Error())
		}
	}

	// Run until interrupted
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	func() {
		for sig := range c {
			// sig is a ^C, handle it
			fmt.Printf("\nSIG: %v\n", sig)
			return
		}
	}()

	//server.Close()
}

func newProxy(mock *GrpcServiceMock) *http.Client {
	conn := serviceconnections.NewConnections()
	conn.StatsHandlerFactory = NewStatsHandler
	err := conn.Connect("", "", "", "", "", "", 1*time.Minute, mock.contextDialer)
	if err != nil {
		log.Fatalf("Could not connect to services: %v", err)
	}

	recorderproxy.SetCA("", "")
	//spAddr := spUrl.Host
	spAddr := ""
	//proxy := recorderproxy.NewRecorderProxy(0, conn, 1*time.Minute, spAddr)
	_ = recorderproxy.NewRecorderProxy(0, conn, 1*time.Minute, spAddr)

	proxyUrl, _ := url.Parse("http://localhost:9900")
	tr := &http.Transport{TLSClientConfig: acceptAllCerts, Proxy: http.ProxyURL(proxyUrl), DisableKeepAlives: true}
	client := &http.Client{Transport: tr}

	//p := httptest.NewServer(nethttp.Middleware(opentracing.GlobalTracer(), proxy))
	//proxyUrl, _ := url.Parse(p.URL)
	//tr := &http.Transport{TLSClientConfig: acceptAllCerts, Proxy: http.ProxyURL(proxyUrl), DisableKeepAlives: true}
	//client := &http.Client{Transport: tr}

	//server = p
	return client
}

func newProxyNew(mock *GrpcServiceMock) *http.Client {
	opts := &proxy.Opts{
		//OnError: onError,
		Dial: func(context context.Context, isConnect bool, network, addr string) (conn net.Conn, err error) {
			timeout := 30 * time.Second
			deadline, hasDeadline := context.Deadline()
			if hasDeadline {
				timeout = deadline.Sub(time.Now())
			}
			conn, err = net.DialTimeout(network, addr, timeout)
			if err != nil {
				log.Fatal(err)
			}
			//if !isConnect {
			conn = &testConn{conn, "early"}
			//}
			return conn, err
		},
		IdleTimeout: 3 * time.Second,
		Filter:      &testFilter{key: "foo", value: "bar"},
		ShouldMITM: func(req *http.Request, upstreamAddr string) bool {
			fmt.Printf("SHOULD MITMT FOR %v %v\n", req.URL, upstreamAddr)
			return true
		},
		MITMOpts: &mitm.Opts{
			Domains:         []string{"*"},
			ClientTLSConfig: acceptAllCerts,
			ServerTLSConfig: acceptAllCerts,
		},
	}

	p, err := proxy.New(opts)
	if err != nil {
		log.Fatal(err)
	}

	l, err := net.Listen("tcp", ":9900")
	if err != nil {
		log.Fatal(err)
	}

	proxyUrl, _ := url.Parse("http://localhost:9900")
	fmt.Println(proxyUrl, l.Addr())
	tr := &http.Transport{TLSClientConfig: acceptAllCerts, Proxy: http.ProxyURL(proxyUrl), DisableKeepAlives: true}
	client := &http.Client{Transport: tr}

	go func() {
		defer l.Close()
		err := p.Serve(l)
		if err != nil {
			log.Fatal(err)
		}
	}()
	return client
}

type testConn struct {
	net.Conn
	xx string
}

func (conn *testConn) Close() (err error) {
	fmt.Printf("Close Conn %v %v\n", conn.LocalAddr(), conn.RemoteAddr())
	return conn.Conn.Close()
}

//func (conn *testConn) Write(b []byte) (n int, err error) {
//	fmt.Printf("WRITE::: %v %v\n", conn.xx, b)
//	return conn.Conn.Write(b)
//}
//
//func (conn *testConn) OnRequest(req *http.Request) {
//	fmt.Printf("OnRequest %v\n", req.URL)
//}
//
//func (conn *testConn) OnResponse(req *http.Request, resp *http.Response, err error) {
//	fmt.Printf("OnResponse %v\n", req.URL)
//}
//
//func (conn *testConn) Wrapped() net.Conn {
//	fmt.Printf("WRAPPED\n")
//	return conn.Conn
//	//return conn
//}

type testFilter struct {
	key   string
	value string
}

func (f *testFilter) Apply(ctx filters.Context, req *http.Request, next filters.Next) (resp *http.Response, context filters.Context, err error) {
	if req.Method != http.MethodConnect {
		if f.key == "" {
			fmt.Printf("SHORT CIRCUT\n")
			// short circuit
			return filters.ShortCircuit(ctx, req, &http.Response{
				Request:    req,
				StatusCode: http.StatusAccepted,
				Body:       ioutil.NopCloser(strings.NewReader("shortcircuited")),
			})
		}
		fmt.Printf("NOT SHORT CIRCUT %v %v %v\n", req.URL, ctx.IsMITMing(), ctx.RequestNumber())

		tr := opentracing.GlobalTracer()
		spanCtx, _ := tr.Extract(opentracing.HTTPHeaders, opentracing.HTTPHeadersCarrier(req.Header))
		span := tr.StartSpan("HTTP "+req.Method, ext.RPCServerOption(spanCtx))
		ext.HTTPMethod.Set(span, req.Method)
		ext.HTTPUrl.Set(span, req.URL.String())

		componentName := "recorderProxy"
		ext.Component.Set(span, componentName)

		c := opentracing.ContextWithSpan(ctx, span)
		ctx = filters.AdaptContext(c)
		req = req.WithContext(ctx)
		//defer span.Finish()

		span.LogFields(log2.String("event", "upstream request"))
		resp, context, err = next(ctx, req)
		span.LogFields(log2.String("event", "upstream response"))

		resp.Body, err = WrapBody(context, resp.Body, int32(resp.StatusCode))
		fmt.Printf("ERR %v\n", err)
		return
	} else {
		return next(ctx, req)
	}

	//if ctx.IsMITMing() {
	//	fmt.Printf("HTTPSOnRequest %v %v\n", req.URL, req.Method)
	//} else {
	//	fmt.Printf("NormalOnRequest %v %v\n", req.URL, req.Method)
	//}

	//if ctx.IsMITMing() {
	//	fmt.Printf("HTTPSOnResponse %v %T %T\n", req.URL, req, resp)
	//} else {
	//	fmt.Printf("NormalOnResponse %v %T %T\n", req.URL, req, resp)
	//}
}

type wrappedBody struct {
	io.ReadCloser
	size              int64
	separatorAdded    bool
	statusCode        int32
	replacementReader io.Reader
	ctx               filters.Context
	span              opentracing.Span
	done              bool
}

func WrapBody(ctx filters.Context, body io.ReadCloser, statusCode int32) (*wrappedBody, error) {
	b := &wrappedBody{
		ReadCloser: body,
		statusCode: statusCode,
		ctx:        ctx,
		span:       opentracing.SpanFromContext(ctx),
	}

	return b, nil
}

func (b *wrappedBody) Read(p []byte) (n int, err error) {
	return b.innerRead(b.ReadCloser, p)
}

func (b *wrappedBody) innerRead(r io.Reader, p []byte) (n int, err error) {
	if b.done {
		return 0, io.EOF
	}

	b.span.LogFields(log2.String("event", "Start Read"))
	n, err = r.Read(p)
	b.span.LogFields(log2.String("event", "End Read"), log2.Int("size", n), log2.Error(err))
	if n > 0 {
		if !b.separatorAdded {
			b.size += 2 // Add size for header and payload separator (\r\n)
			b.separatorAdded = true
		}
		b.size += int64(n)
	}
	if err == io.EOF {
		fmt.Printf("EOF %v %v\n\n", b.statusCode, b.size)
		b.span.Finish()
		b.done = true
	}
	return
}
