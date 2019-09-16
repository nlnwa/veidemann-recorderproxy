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
	"github.com/felixge/httpsnoop"
	"github.com/golang/protobuf/ptypes"
	"github.com/nlnwa/veidemann-api-go/browsercontroller/v1"
	"github.com/nlnwa/veidemann-api-go/commons/v1"
	"github.com/nlnwa/veidemann-api-go/config/v1"
	"github.com/nlnwa/veidemann-api-go/contentwriter/v1"
	"github.com/nlnwa/veidemann-api-go/dnsresolver/v1"
	"github.com/nlnwa/veidemann-api-go/frontier/v1"
	"github.com/opentracing/opentracing-go"
	"io"
	"io/ioutil"
	"regexp"

	"github.com/opentracing-contrib/go-stdlib/nethttp"
	log "github.com/sirupsen/logrus"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	ENCODING         = "Accept-Encoding"
	EXECUTION_ID     = "veidemann_eid"
	JOB_EXECUTION_ID = "veidemann_jeid"
	COLLECTION_ID    = "veidemann_cid"
)

const (
	ContentTypeText = "text/plain"
	ContentTypeHtml = "text/html"
)

var proxyCount int32

type RecorderProxy struct {
	id                int32
	addr              string
	conn              *Connections
	ConnectionTimeout time.Duration

	// session variable must be aligned in i386
	// see http://golang.org/src/pkg/sync/atomic/doc.go#L41
	sess int64
	// KeepDestinationHeaders indicates the proxy should retain any headers present in the http.Response before proxying
	KeepDestinationHeaders bool
	NonproxyHandler        http.Handler
	RoundTripper           *RpRoundTripper

	// ConnectDial will be used to create TCP connections for CONNECT requests
	ConnectDial       func(addr string) (*tls.Conn, error)
	dnsResolverDialer *dnsResolverDialer
}

func NewRecorderProxy(port int, conn *Connections, connectionTimeout time.Duration, cache string) *RecorderProxy {
	r := &RecorderProxy{
		id:                proxyCount,
		conn:              conn,
		addr:              ":" + strconv.Itoa(port),
		ConnectionTimeout: connectionTimeout,

		NonproxyHandler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			http.Error(w, "This is a proxy server. Does not respond to non-proxy requests.", 500)
		}),
		RoundTripper: NewRpRoundTripper(),
	}

	if cache != "" {
		cu, _ := url.Parse("http://" + cache)
		r.RoundTripper.Proxy = http.ProxyURL(cu)
	}

	proxyCount++

	if conn.dnsResolverHost != "" {
		r.dnsResolverDialer, err = NewDnsResolverDialer(conn.dnsResolverHost, conn.dnsResolverPort)
		if err != nil {
			log.Fatalf("Could not create CONNECT dialer: \"%s\"", err)
		}
		r.ConnectDial = r.dnsResolverDialer.DialTls
	} else {
		r.ConnectDial = func(addr string) (conn *tls.Conn, e error) {
			return tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
		}
	}

	return r
}

func (proxy *RecorderProxy) Start() {
	fmt.Printf("Starting proxy %v...\n", proxy.id)

	tracer := opentracing.GlobalTracer()
	go func() {
		log.Fatalf("Proxy with addr %v: %v", proxy.addr, http.ListenAndServe(proxy.addr, nethttp.Middleware(tracer, proxy)))
	}()

	fmt.Printf("Proxy %v started on port %v\n", proxy.id, proxy.addr)
}

func (proxy *RecorderProxy) filterRequest(reqOrig *http.Request, ctx *RecordContext) (*http.Request, *http.Response) {
	span, c := opentracing.StartSpanFromContext(reqOrig.Context(), "filterRequest")
	defer span.Finish()
	req := reqOrig.WithContext(c)

	var prolog bytes.Buffer
	writeRequestProlog(req, &prolog)

	var collectionRef *config.ConfigRef

	executionId := req.Header.Get(EXECUTION_ID)
	jobExecutionId := req.Header.Get(JOB_EXECUTION_ID)

	if req.Header.Get(COLLECTION_ID) != "" {
		collectionRef = &config.ConfigRef{
			Kind: config.Kind_collection,
			Id:   req.Header.Get(COLLECTION_ID),
		}
	}

	ctx.FetchTimesTamp = time.Now()
	fetchTimeStamp, _ := ptypes.TimestampProto(ctx.FetchTimesTamp)

	ctx.crawlLog = &frontier.CrawlLog{
		RequestedUri:   req.URL.String(),
		FetchTimeStamp: fetchTimeStamp,
	}

	bccRequest := &browsercontroller.DoRequest{
		Action: &browsercontroller.DoRequest_New{
			New: &browsercontroller.RegisterNew{
				ProxyId:          proxy.id,
				Uri:              req.URL.String(),
				CrawlExecutionId: executionId,
				JobExecutionId:   jobExecutionId,
				CollectionRef:    collectionRef,
			},
		},
	}

	err := ctx.bcc.Send(bccRequest)
	if err != nil {
		log.Fatalf("Error register with browser controller, cause: %v", err)
	}

	bcReply := <-ctx.bccMsgChan

	switch v := bcReply.Action.(type) {
	case *browsercontroller.DoReply_Cancel:
		if v.Cancel == "Blocked by robots.txt" {
			ctx.precludedByRobots = true
		}
		return reqOrig, NewResponse(reqOrig, ContentTypeText, 403, v.Cancel)
	}

	executionId = bcReply.GetNew().CrawlExecutionId
	jobExecutionId = bcReply.GetNew().GetJobExecutionId()
	collectionRef = bcReply.GetNew().CollectionRef
	ctx.replacementScript = bcReply.GetNew().ReplacementScript

	uri := req.URL

	host := uri.Hostname()
	ps := uri.Port()
	var port = 0
	if ps != "" {
		port, err = strconv.Atoi(ps)
		if err != nil {
			ctx.Close()
			panic(fmt.Sprintf("Error parsing port for %v, cause: %v", uri, err))
		}
	}
	dnsReq := &dnsresolver.ResolveRequest{
		CollectionRef: collectionRef,
		Host:          host,
		Port:          int32(port),
	}
	c, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()
	dnsResp, err := proxy.conn.DnsResolverClient().Resolve(c, dnsReq)
	if err != nil {
		ctx.Close()
		panic(fmt.Sprintf("Error looking up %v, cause: %v", uri.Host, err))
	}

	req.Header.Set(ENCODING, "identity")
	req.Header.Set(EXECUTION_ID, executionId)
	req.Header.Set(JOB_EXECUTION_ID, jobExecutionId)

	ctx.meta = &contentwriter.WriteRequest_Meta{
		Meta: &contentwriter.WriteRequestMeta{
			RecordMeta:     map[int32]*contentwriter.WriteRequestMeta_RecordMeta{},
			TargetUri:      req.URL.String(),
			ExecutionId:    executionId,
			IpAddress:      dnsResp.TextualIp,
			CollectionRef:  collectionRef,
			FetchTimeStamp: fetchTimeStamp,
		},
	}

	ctx.crawlLog.JobExecutionId = jobExecutionId
	ctx.crawlLog.ExecutionId = executionId
	ctx.crawlLog.IpAddress = dnsResp.TextualIp
	ctx.crawlLog.RequestedUri = req.URL.String()
	ctx.crawlLog.FetchTimeStamp = fetchTimeStamp

	contentType := req.Header.Get("Content-Type")
	bodyWrapper, err := WrapBody(req.Body, REQUEST, ctx, 0, -1, contentType, contentwriter.RecordType_REQUEST, prolog.Bytes())
	if err != nil {
		return reqOrig, NewResponse(req, ContentTypeText, http.StatusBadGateway, "Veidemann proxy lost connection to GRPC services"+err.Error())
	}
	reqOrig.Body = bodyWrapper

	return reqOrig, nil
}

func (proxy *RecorderProxy) filterResponse(respOrig *http.Response, ctx *RecordContext) (resp *http.Response) {
	span, _ := opentracing.StartSpanFromContext(respOrig.Request.Context(), "filterResponse")
	defer span.Finish()

	resp = respOrig
	ctx.Resp = resp
	if resp == nil {
		ctx.Close()
		panic(http.ErrAbortHandler)
	}

	if ctx.precludedByRobots {
		ctx.Close()
		return resp
	}

	if ctx.Error != nil && strings.HasPrefix(ctx.Error.Error(), "unknown error from browser controller") {
		ctx.Close()
		return resp
	}

	if strings.Contains(resp.Header.Get("X-Cache-Lookup"), "HIT") {
		ctx.foundInCache = true

		//span.log("Loaded from cache");
	}

	var prolog bytes.Buffer
	writeResponseProlog(resp, &prolog)

	contentType := resp.Header.Get("Content-Type")
	statusCode := int32(resp.StatusCode)
	var err error
	bodyWrapper, err := WrapBody(resp.Body, RESPONSE, ctx, 1, statusCode, contentType, contentwriter.RecordType_RESPONSE, prolog.Bytes())
	if err != nil {
		ctx.Close()
		return NewResponse(resp.Request, ContentTypeText, http.StatusBadGateway, "Veidemann proxy lost connection to GRPC services\n"+err.Error())
	}

	if ctx.replacementScript != nil {
		resp.ContentLength = int64(len(ctx.replacementScript.Script))
	}
	resp.Body = bodyWrapper

	return resp
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
	ctx.SessionLogger().Debugf("Sending request %v %v", r.Method, r.URL.String())
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

// Standard net/http function. Shouldn't be used directly, http.Serve will use it.
func (proxy *RecorderProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	hooks := httpsnoop.Hooks{
		WriteHeader: func(next httpsnoop.WriteHeaderFunc) httpsnoop.WriteHeaderFunc {
			return func(code int) {
				next(code)
				fmt.Printf("WRITE HEADER %v\n--------------------------------\n", code)
			}
		},

		Header: func(next httpsnoop.HeaderFunc) httpsnoop.HeaderFunc {
			return func() http.Header {
				h := next()
				fmt.Printf("HEADER: %v\n---------------------\n", h)
				return h
			}
		},

		Write: func(next httpsnoop.WriteFunc) httpsnoop.WriteFunc {
			return func(p []byte) (int, error) {
				n, err := next(p)
				fmt.Printf("\n%d !!! %s\n", n, p[:10])
				return n, err
			}
		},

		ReadFrom: func(next httpsnoop.ReadFromFunc) httpsnoop.ReadFromFunc {
			return func(src io.Reader) (int64, error) {
				n, err := next(src)
				fmt.Printf("\nREAD %d !!!\n", n)
				return n, err
			}
		},

		CloseNotify: func(next httpsnoop.CloseNotifyFunc) httpsnoop.CloseNotifyFunc {
			return func() <-chan bool {
				c := next()
				fmt.Printf("\n¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤\n\n")
				return c
			}
		},

		Hijack: func(next httpsnoop.HijackFunc) httpsnoop.HijackFunc {
			return func() (conn net.Conn, writer *bufio.ReadWriter, e error) {
				c, w, e := next()
				fmt.Printf("\n¤¤¤¤¤¤¤¤¤¤ HIJACK ¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤\n\n")
				return &xx{c}, w, e
			}
		},
	}

	w = httpsnoop.Wrap(w, hooks)

	//r.Header["X-Forwarded-For"] = w.RemoteAddr()
	if r.Method == "CONNECT" {
		proxy.handleHttps(w, r)
	} else {
		proxy.handleHttp(w, r)
	}
}

type xx struct {
	net.Conn
}

func (x xx) Write(b []byte) (n int, err error) {
	n, err = x.Conn.Write(b)
	//fmt.Printf("\n%d !!! WRITE HIJACK:::\n", n)
	return n, err
}
