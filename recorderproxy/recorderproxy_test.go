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

package recorderproxy_test

import (
	"context"
	"crypto/sha1"
	"crypto/tls"
	"fmt"
	"github.com/golang/protobuf/ptypes"
	"github.com/golang/protobuf/ptypes/timestamp"
	browsercontrollerV1 "github.com/nlnwa/veidemann-api-go/browsercontroller/v1"
	"github.com/nlnwa/veidemann-api-go/commons/v1"
	configV1 "github.com/nlnwa/veidemann-api-go/config/v1"
	contentwriterV1 "github.com/nlnwa/veidemann-api-go/contentwriter/v1"
	dnsresolverV1 "github.com/nlnwa/veidemann-api-go/dnsresolver/v1"
	"github.com/nlnwa/veidemann-api-go/frontier/v1"
	"github.com/nlnwa/veidemann-recorderproxy/recorderproxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"hash"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const bufSize = 1024 * 1024

var (
	acceptAllCerts  = &tls.Config{InsecureSkipVerify: true}
	httpsMux        = http.NewServeMux()
	httpMux         = http.NewServeMux()
	srvHttps        = httptest.NewTLSServer(httpsMux)
	srvHttpsBadCert = httptest.NewTLSServer(httpsMux)
	srvHttp         = httptest.NewServer(httpMux)
	recorderProxy   *recorderproxy.RecorderProxy
	lis             *bufconn.Listener
	grpcServices    *GrpcServiceMock
	client          *http.Client
)

func bufDialer(context.Context, string) (net.Conn, error) {
	return lis.Dial()
}

type ConstantHandler string

func (h ConstantHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = io.WriteString(w, string(h))
}

type ConstantSlowHandler string

func (h ConstantSlowHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	time.Sleep(600 * time.Millisecond)
	_, _ = io.WriteString(w, string(h))
}

type ConstantCacheHandler string

func (h ConstantCacheHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Cache-Lookup", "HIT")
	_, _ = io.WriteString(w, string(h))
}

func init() {
	httpMux.Handle("/a", ConstantHandler("content from http server"))
	httpsMux.Handle("/b", ConstantHandler("content from https server"))
	httpMux.Handle("/replace", ConstantHandler("should be replaced"))
	httpsMux.Handle("/replace", ConstantHandler("should be replaced"))
	httpMux.Handle("/slow", ConstantSlowHandler("content from http server"))
	httpsMux.Handle("/slow", ConstantSlowHandler("content from https server"))
	httpMux.Handle("/cancel", ConstantSlowHandler("content from http server"))
	httpsMux.Handle("/cancel", ConstantSlowHandler("content from https server"))
	httpMux.Handle("/blocked", ConstantSlowHandler("content from http server"))
	httpsMux.Handle("/blocked", ConstantSlowHandler("content from https server"))
	httpMux.Handle("/cwerr", ConstantHandler("content from http server"))
	httpsMux.Handle("/cwerr", ConstantHandler("content from https server"))
	httpMux.Handle("/cached", ConstantCacheHandler("content from http server"))
	httpsMux.Handle("/cached", ConstantCacheHandler("content from https server"))

	lis = bufconn.Listen(bufSize)
	s := grpc.NewServer()
	grpcServices = NewGrpcServiceMock()
	dnsresolverV1.RegisterDnsResolverServer(s, grpcServices)
	contentwriterV1.RegisterContentWriterServer(s, grpcServices)
	browsercontrollerV1.RegisterBrowserControllerServer(s, grpcServices)
	go func() {
		if err := s.Serve(lis); err != nil {
			log.Fatalf("Server exited with error: %v", err)
		}
	}()

	client, recorderProxy = localRecorderProxy()
	srvHttpsBadCert.TLS.Certificates = []tls.Certificate{tls.Certificate{}}
}

type requests struct {
	BrowserControllerRequests []*browsercontrollerV1.DoRequest
	DnsResolverRequests       []*dnsresolverV1.ResolveRequest
	ContentWriterRequests     []*contentwriterV1.WriteRequest
}
type test struct {
	name                    string
	url                     string
	wantStatus              int
	wantContent             string
	wantReplacedContent     string
	wantResponseBlockDigest bool
	wantRequestBlockSize    int64
	wantResponseBlockSize   int64
	wantGrpcRequests        *requests
	wantErr                 bool
	srvWriteTimout          time.Duration
	grpcTimeout             time.Duration
	clientTimeout           time.Duration
}

func TestGoproxyThroughProxy(t *testing.T) {
	tests := []test{
		{
			name:                    "http:success",
			url:                     srvHttp.URL + "/a",
			wantStatus:              200,
			wantContent:             "content from http server",
			wantResponseBlockDigest: true,
			wantRequestBlockSize:    114,
			wantResponseBlockSize:   141,
			wantErr:                 false,
		},
		{
			name:                    "https:success",
			url:                     srvHttps.URL + "/b",
			wantStatus:              200,
			wantContent:             "content from https server",
			wantResponseBlockDigest: true,
			wantRequestBlockSize:    114,
			wantResponseBlockSize:   142,
			wantErr:                 false,
		},
		{
			name:          "http:client timeout",
			url:           srvHttp.URL + "/slow",
			wantStatus:    0,
			wantContent:   "",
			wantErr:       true,
			clientTimeout: 500 * time.Millisecond,
		},
		{
			name:          "https:client timeout",
			url:           srvHttps.URL + "/slow",
			wantStatus:    0,
			wantContent:   "",
			wantErr:       true,
			clientTimeout: 500 * time.Millisecond,
		},
		{
			name:                    "http:not found",
			url:                     srvHttp.URL + "/c",
			wantStatus:              404,
			wantContent:             "404 page not found\n",
			wantResponseBlockDigest: true,
			wantRequestBlockSize:    114,
			wantResponseBlockSize:   176,
			wantErr:                 false,
		},
		{
			name:                    "https:not found",
			url:                     srvHttps.URL + "/c",
			wantStatus:              404,
			wantContent:             "404 page not found\n",
			wantResponseBlockDigest: true,
			wantRequestBlockSize:    114,
			wantResponseBlockSize:   176,
			wantErr:                 false,
		},
		{
			name:                    "http:replace",
			url:                     srvHttp.URL + "/replace",
			wantStatus:              200,
			wantContent:             "should be replaced",
			wantReplacedContent:     "replaced",
			wantResponseBlockDigest: true,
			wantRequestBlockSize:    120,
			wantResponseBlockSize:   135,
			wantErr:                 false,
		},
		{
			name:                    "https:replace",
			url:                     srvHttps.URL + "/replace",
			wantStatus:              200,
			wantContent:             "should be replaced",
			wantReplacedContent:     "replaced",
			wantResponseBlockDigest: true,
			wantRequestBlockSize:    120,
			wantResponseBlockSize:   135,
			wantErr:                 false,
		},
		{
			name:           "http:server timeout",
			url:            srvHttp.URL + "/slow",
			wantStatus:     0,
			wantContent:    "",
			wantErr:        true,
			srvWriteTimout: 10 * time.Millisecond,
		},
		{
			name:           "https:server timeout",
			url:            srvHttps.URL + "/slow",
			wantStatus:     0,
			wantContent:    "",
			wantErr:        true,
			srvWriteTimout: 10 * time.Millisecond,
		},
		{
			name:        "http:grpc service timeout",
			url:         srvHttp.URL + "/slow",
			wantStatus:  502,
			wantContent: "Veidemann proxy lost connection to GRPC services\nerror writing payload to content writer: EOF",
			wantErr:     false,
			grpcTimeout: 10 * time.Millisecond,
		},
		{
			name:        "https:grpc service timeout",
			url:         srvHttps.URL + "/slow",
			wantStatus:  502,
			wantContent: "Veidemann proxy lost connection to GRPC services\nerror writing payload to content writer: EOF",
			wantErr:     false,
			grpcTimeout: 10 * time.Millisecond,
		},
		{
			name:        "http:browser controller cancel",
			url:         srvHttp.URL + "/cancel",
			wantStatus:  200,
			wantContent: "content from http server",
			wantErr:     false,
		},
		{
			name:        "https:browser controller cancel",
			url:         srvHttps.URL + "/cancel",
			wantStatus:  200,
			wantContent: "content from https server",
			wantErr:     false,
		},
		{
			name:        "http:blocked by robots.txt",
			url:         srvHttp.URL + "/blocked",
			wantStatus:  403,
			wantContent: "Blocked by robots.txt",
			wantErr:     false,
		},
		{
			name:        "https:blocked by robots.txt",
			url:         srvHttps.URL + "/blocked",
			wantStatus:  403,
			wantContent: "Blocked by robots.txt",
			wantErr:     false,
		},
		{
			name:                  "http:content writer error",
			url:                   srvHttp.URL + "/cwerr",
			wantStatus:            200,
			wantContent:           "content from http server",
			wantRequestBlockSize:  118,
			wantResponseBlockSize: 141,
			wantErr:               false,
		},
		{
			name:                  "https:content writer error",
			url:                   srvHttps.URL + "/cwerr",
			wantStatus:            200,
			wantContent:           "content from https server",
			wantRequestBlockSize:  118,
			wantResponseBlockSize: 142,
			wantErr:               false,
		},
		{
			name:                  "http:cached",
			url:                   srvHttp.URL + "/cached",
			wantStatus:            200,
			wantContent:           "content from http server",
			wantRequestBlockSize:  116,
			wantResponseBlockSize: 136,
			wantErr:               false,
		},
		{
			name:                  "https:cached",
			url:                   srvHttps.URL + "/cached",
			wantStatus:            200,
			wantContent:           "content from https server",
			wantRequestBlockSize:  116,
			wantResponseBlockSize: 136,
			wantErr:               false,
		},
		{
			name:                  "http:no host",
			url:                   srvHttp.URL[:len(srvHttp.URL)-2] + "1/no_host",
			wantStatus:            0,
			wantContent:           "",
			wantRequestBlockSize:  116,
			wantResponseBlockSize: 138,
			wantErr:               true,
		},
		{
			name:                  "https:no host",
			url:                   srvHttps.URL[:len(srvHttps.URL)-2] + "1/no_host",
			wantStatus:            0,
			wantContent:           "",
			wantRequestBlockSize:  116,
			wantResponseBlockSize: 138,
			wantErr:               true,
		},
		{
			name:                    "https:handshake failure",
			url:                     srvHttpsBadCert.URL + "/b",
			wantStatus:              0,
			wantContent:             "",
			wantResponseBlockDigest: false,
			wantRequestBlockSize:    116,
			wantResponseBlockSize:   144,
			wantErr:                 true,
		},
	}

	for _, tt := range tests {
		srvHttp.Config.WriteTimeout = tt.srvWriteTimout
		srvHttps.Config.WriteTimeout = tt.srvWriteTimout

		if tt.grpcTimeout == 0 {
			recorderProxy.ConnectionTimeout = 1 * time.Minute
		} else {
			recorderProxy.ConnectionTimeout = tt.grpcTimeout
		}

		tt.generateExpectedRequests()
		grpcServices.clear()

		t.Run(tt.name, func(t *testing.T) {
			statusCode, got, err := get(tt.url, client, tt.clientTimeout)
			if grpcServices.doneBC != nil {
				<-grpcServices.doneBC
			}
			if grpcServices.doneCW != nil {
				<-grpcServices.doneCW
			}

			if (err != nil) != tt.wantErr {
				t.Errorf("Client get() error = %v, wantErr %v (%v, %s)", err, tt.wantErr, statusCode, got)
				return
			}
			if statusCode != tt.wantStatus {
				t.Errorf("Expected status code: %d, got %d", tt.wantStatus, statusCode)
				return
			}
			if tt.wantReplacedContent == "" {
				if string(got) != tt.wantContent {
					t.Errorf("Expected '%s', got '%s'", tt.wantContent, got)
					return
				}
			} else {
				if string(got) != tt.wantReplacedContent {
					t.Errorf("Expected '%s', got '%s'", tt.wantReplacedContent, got)
					return
				}
			}
			compareDNS(t, "DnsResolver", tt, tt.wantGrpcRequests.DnsResolverRequests, grpcServices.requests.DnsResolverRequests)
			compareBC(t, "BrowserController", tt, tt.wantGrpcRequests.BrowserControllerRequests, grpcServices.requests.BrowserControllerRequests)
			compareCW(t, "ContentWriter", tt, tt.wantGrpcRequests.ContentWriterRequests, grpcServices.requests.ContentWriterRequests)
		})
	}

	srvHttp.Close()
	srvHttps.Close()
}

/**
 * Helper functions
 */

func (test *test) generateExpectedRequests() {
	u, _ := url.Parse(test.url)
	p, _ := strconv.Atoi(u.Port())

	switch n := test.name; {
	case strings.HasSuffix(n, ":client timeout"):
		test.generateClientTimeoutRequests(u, p)
	case strings.HasSuffix(n, ":server timeout"):
		test.generateServerTimeoutRequests(u, p)
	case strings.HasSuffix(n, ":grpc service timeout"):
		test.generateGrpcServiceTimeoutRequests(u, p)
	case strings.HasSuffix(n, ":browser controller cancel"):
		test.generateBrowserControllerCancelRequests(u, p)
	case strings.HasSuffix(n, ":blocked by robots.txt"):
		test.generateBlockedByRobotsTxtRequests(u, p)
	case strings.HasSuffix(n, ":content writer error"):
		test.generateContentWriterErrorRequests(u, p)
	case strings.HasSuffix(n, ":cached"):
		test.generateCachedRequests(u, p)
	case strings.HasSuffix(n, ":no host"):
		test.generateConnectionRefusedRequests(u, p)
	case strings.HasSuffix(n, ":handshake failure"):
		test.generateHandshakeFailureRequests(u, p)
	default:
		test.generateSuccessRequests(u, p)
	}
}

func (test *test) generateSuccessRequests(u *url.URL, p int) {
	var extraHeaders string
	if test.wantStatus == 404 {
		extraHeaders = "\r\nX-Content-Type-Options: nosniff"
	}

	r := &requests{}
	r.DnsResolverRequests = []*dnsresolverV1.ResolveRequest{
		{Host: u.Hostname(), Port: int32(p), CollectionRef: &configV1.ConfigRef{Kind: configV1.Kind_collection, Id: "col1"}},
	}

	r.BrowserControllerRequests = []*browsercontrollerV1.DoRequest{
		{
			Action: &browsercontrollerV1.DoRequest_New{New: &browsercontrollerV1.RegisterNew{Uri: test.url}},
		},
		{
			Action: &browsercontrollerV1.DoRequest_Notify{
				Notify: &browsercontrollerV1.NotifyActivity{Activity: browsercontrollerV1.NotifyActivity_DATA_RECEIVED},
			},
		},
		{
			Action: &browsercontrollerV1.DoRequest_Notify{
				Notify: &browsercontrollerV1.NotifyActivity{Activity: browsercontrollerV1.NotifyActivity_ALL_DATA_RECEIVED},
			},
		},
		{
			Action: &browsercontrollerV1.DoRequest_Completed{
				Completed: &browsercontrollerV1.Completed{
					CrawlLog: &frontier.CrawlLog{
						WarcId:              "warcid_1",
						StatusCode:          int32(test.wantStatus),
						Size:                test.wantResponseBlockSize,
						RequestedUri:        test.url,
						ContentType:         "text/plain; charset=utf-8",
						StorageRef:          "storageRef_1",
						RecordType:          "revisit",
						WarcRefersTo:        "revisit_0",
						IpAddress:           "1.2.1.2",
						ExecutionId:         "eid",
						JobExecutionId:      "jid",
						CollectionFinalName: "collection_0",
					},
				},
			},
		},
	}

	r.ContentWriterRequests = []*contentwriterV1.WriteRequest{
		{
			Value: &contentwriterV1.WriteRequest_ProtocolHeader{
				ProtocolHeader: &contentwriterV1.Data{
					RecordNum: 0, Data: []byte(fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s:%s\r\nAccept-Encoding: gzip\r\nConnection: close\r\nUser-Agent: Go-http-client/1.1\r\n", u.Path, u.Hostname(), u.Port()))},
			},
		},
		{
			Value: &contentwriterV1.WriteRequest_ProtocolHeader{
				ProtocolHeader: &contentwriterV1.Data{RecordNum: 1, Data: []byte(fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Length: %d\r\nContent-Type: text/plain; charset=utf-8\r\nDate: Wed, 15 May 2019 12:41:02 GMT%s\r\n", test.wantStatus, http.StatusText(test.wantStatus), len(test.wantContent), extraHeaders))},
			},
		},
		{
			Value: &contentwriterV1.WriteRequest_Payload{
				Payload: &contentwriterV1.Data{RecordNum: 1, Data: []byte(test.wantContent)},
			},
		},
		{
			Value: &contentwriterV1.WriteRequest_Meta{
				Meta: &contentwriterV1.WriteRequestMeta{
					ExecutionId:   "eid",
					TargetUri:     test.url,
					IpAddress:     "1.2.1.2",
					CollectionRef: &configV1.ConfigRef{Kind: configV1.Kind_collection, Id: "col1"},
					RecordMeta: map[int32]*contentwriterV1.WriteRequestMeta_RecordMeta{
						0: {
							RecordNum:         0,
							Type:              contentwriterV1.RecordType_REQUEST,
							RecordContentType: "application/http; msgtype=request",
							Size:              test.wantRequestBlockSize,
						},
						1: {
							RecordNum:         1,
							Type:              contentwriterV1.RecordType_RESPONSE,
							RecordContentType: "application/http; msgtype=response",
							Size:              test.wantResponseBlockSize,
						},
					},
				},
			},
		},
	}

	test.wantGrpcRequests = r
}

func (test *test) generateClientTimeoutRequests(u *url.URL, p int) {
	r := &requests{}
	r.DnsResolverRequests = []*dnsresolverV1.ResolveRequest{
		{Host: u.Hostname(), Port: int32(p), CollectionRef: &configV1.ConfigRef{Kind: configV1.Kind_collection, Id: "col1"}},
	}

	r.BrowserControllerRequests = []*browsercontrollerV1.DoRequest{
		{
			Action: &browsercontrollerV1.DoRequest_New{New: &browsercontrollerV1.RegisterNew{Uri: test.url}},
		},
		{
			Action: &browsercontrollerV1.DoRequest_Completed{
				Completed: &browsercontrollerV1.Completed{
					CrawlLog: &frontier.CrawlLog{
						StatusCode:     -5011,
						RequestedUri:   test.url,
						RecordType:     "response",
						IpAddress:      "1.2.1.2",
						ExecutionId:    "eid",
						JobExecutionId: "jid",
						Error: &commons.Error{
							Code:   -5011,
							Msg:    "CANCELED_BY_BROWSER",
							Detail: "Veidemann recorder proxy lost connection to client",
						},
					},
				},
			},
		},
	}

	r.ContentWriterRequests = []*contentwriterV1.WriteRequest{
		{
			Value: &contentwriterV1.WriteRequest_ProtocolHeader{
				ProtocolHeader: &contentwriterV1.Data{RecordNum: 1, Data: []byte(fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Length: %d\r\nContent-Type: text/plain; charset=utf-8\r\nDate: Wed, 15 May 2019 12:41:02 GMT\r\n\r\n", test.wantStatus, http.StatusText(test.wantStatus), len(test.wantContent)))},
			},
		},
		{
			Value: &contentwriterV1.WriteRequest_Cancel{
				Cancel: "Veidemann recorder proxy lost connection to client",
			},
		},
	}

	test.wantGrpcRequests = r
}

func (test *test) generateServerTimeoutRequests(u *url.URL, p int) {
	r := &requests{}
	r.DnsResolverRequests = []*dnsresolverV1.ResolveRequest{
		{Host: u.Hostname(), Port: int32(p), CollectionRef: &configV1.ConfigRef{Kind: configV1.Kind_collection, Id: "col1"}},
	}

	r.BrowserControllerRequests = []*browsercontrollerV1.DoRequest{
		{
			Action: &browsercontrollerV1.DoRequest_New{New: &browsercontrollerV1.RegisterNew{Uri: test.url}},
		},
		{
			Action: &browsercontrollerV1.DoRequest_Completed{
				Completed: &browsercontrollerV1.Completed{
					CrawlLog: &frontier.CrawlLog{
						StatusCode:     -4,
						RequestedUri:   test.url,
						RecordType:     "response",
						IpAddress:      "1.2.1.2",
						ExecutionId:    "eid",
						JobExecutionId: "jid",
						Error: &commons.Error{
							Code:   -4,
							Msg:    "HTTP_TIMEOUT",
							Detail: "Veidemann recorder proxy lost connection to upstream server",
						},
					},
				},
			},
		},
	}

	r.ContentWriterRequests = []*contentwriterV1.WriteRequest{
		{
			Value: &contentwriterV1.WriteRequest_ProtocolHeader{
				ProtocolHeader: &contentwriterV1.Data{
					RecordNum: 0, Data: []byte(fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s:%s\r\nAccept-Encoding: gzip\r\nConnection: close\r\nUser-Agent: Go-http-client/1.1\r\n\r\n", u.Path, u.Hostname(), u.Port())),
				},
			},
		},
		{
			Value: &contentwriterV1.WriteRequest_Cancel{
				Cancel: "Veidemann recorder proxy lost connection to upstream server",
			},
		},
	}

	test.wantGrpcRequests = r
}

func (test *test) generateGrpcServiceTimeoutRequests(u *url.URL, p int) {
	r := &requests{}
	r.DnsResolverRequests = []*dnsresolverV1.ResolveRequest{
		{Host: u.Hostname(), Port: int32(p), CollectionRef: &configV1.ConfigRef{Kind: configV1.Kind_collection, Id: "col1"}},
	}

	r.BrowserControllerRequests = []*browsercontrollerV1.DoRequest{
		{
			Action: &browsercontrollerV1.DoRequest_New{New: &browsercontrollerV1.RegisterNew{Uri: test.url}},
		},
	}

	r.ContentWriterRequests = []*contentwriterV1.WriteRequest{
		{
			Value: &contentwriterV1.WriteRequest_ProtocolHeader{
				ProtocolHeader: &contentwriterV1.Data{
					RecordNum: 0, Data: []byte(fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s:%s\r\nAccept-Encoding: gzip\r\nConnection: close\r\nUser-Agent: Go-http-client/1.1\r\n", u.Path, u.Hostname(), u.Port())),
				},
			},
		},
	}

	test.wantGrpcRequests = r
}

func (test *test) generateBrowserControllerCancelRequests(u *url.URL, p int) {
	r := &requests{}
	r.DnsResolverRequests = []*dnsresolverV1.ResolveRequest{
		{Host: u.Hostname(), Port: int32(p), CollectionRef: &configV1.ConfigRef{Kind: configV1.Kind_collection, Id: "col1"}},
	}

	r.BrowserControllerRequests = []*browsercontrollerV1.DoRequest{
		{
			Action: &browsercontrollerV1.DoRequest_New{New: &browsercontrollerV1.RegisterNew{Uri: test.url}},
		},
		{
			Action: &browsercontrollerV1.DoRequest_Completed{
				Completed: &browsercontrollerV1.Completed{
					CrawlLog: &frontier.CrawlLog{
						StatusCode:     -5011,
						RequestedUri:   test.url,
						RecordType:     "response",
						IpAddress:      "1.2.1.2",
						ExecutionId:    "eid",
						JobExecutionId: "jid",
						Error: &commons.Error{
							Code:   -5011,
							Msg:    "CANCELED_BY_BROWSER",
							Detail: "cancelled by browser controller",
						},
					},
				},
			},
		},
	}

	r.ContentWriterRequests = []*contentwriterV1.WriteRequest{
		{
			Value: &contentwriterV1.WriteRequest_Cancel{
				Cancel: "cancelled by browser controller",
			},
		},
	}

	test.wantGrpcRequests = r
}

func (test *test) generateBlockedByRobotsTxtRequests(u *url.URL, p int) {
	r := &requests{}
	r.DnsResolverRequests = []*dnsresolverV1.ResolveRequest{}

	r.BrowserControllerRequests = []*browsercontrollerV1.DoRequest{
		{
			Action: &browsercontrollerV1.DoRequest_New{New: &browsercontrollerV1.RegisterNew{Uri: test.url}},
		},
		{
			Action: &browsercontrollerV1.DoRequest_Completed{
				Completed: &browsercontrollerV1.Completed{
					CrawlLog: &frontier.CrawlLog{
						StatusCode:   -9998,
						RequestedUri: test.url,
						RecordType:   "response",
						Error: &commons.Error{
							Code:   -9998,
							Msg:    "PRECLUDED_BY_ROBOTS",
							Detail: "Robots.txt rules precluded fetch",
						},
					},
				},
			},
		},
	}

	r.ContentWriterRequests = []*contentwriterV1.WriteRequest{
		{
			Value: &contentwriterV1.WriteRequest_Cancel{
				Cancel: "Robots.txt rules precluded fetch",
			},
		},
	}

	test.wantGrpcRequests = r
}

func (test *test) generateContentWriterErrorRequests(u *url.URL, p int) {
	r := &requests{}
	r.DnsResolverRequests = []*dnsresolverV1.ResolveRequest{
		{Host: u.Hostname(), Port: int32(p), CollectionRef: &configV1.ConfigRef{Kind: configV1.Kind_collection, Id: "col1"}},
	}

	r.BrowserControllerRequests = []*browsercontrollerV1.DoRequest{
		{
			Action: &browsercontrollerV1.DoRequest_New{New: &browsercontrollerV1.RegisterNew{Uri: test.url}},
		},
		{
			Action: &browsercontrollerV1.DoRequest_Notify{
				Notify: &browsercontrollerV1.NotifyActivity{Activity: browsercontrollerV1.NotifyActivity_DATA_RECEIVED},
			},
		},
		{
			Action: &browsercontrollerV1.DoRequest_Notify{
				Notify: &browsercontrollerV1.NotifyActivity{Activity: browsercontrollerV1.NotifyActivity_ALL_DATA_RECEIVED},
			},
		},
		{
			Action: &browsercontrollerV1.DoRequest_Completed{
				Completed: &browsercontrollerV1.Completed{
					CrawlLog: &frontier.CrawlLog{
						StatusCode:     -5,
						RequestedUri:   test.url,
						RecordType:     "response",
						IpAddress:      "1.2.1.2",
						ExecutionId:    "eid",
						JobExecutionId: "jid",
						Error: &commons.Error{
							Code:   -5,
							Msg:    "Error writing to content writer",
							Detail: "rpc error: code = InvalidArgument desc = Fake error",
						},
					},
				},
			},
		},
	}

	r.ContentWriterRequests = []*contentwriterV1.WriteRequest{
		{
			Value: &contentwriterV1.WriteRequest_ProtocolHeader{
				ProtocolHeader: &contentwriterV1.Data{
					RecordNum: 0, Data: []byte(fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s:%s\r\nAccept-Encoding: gzip\r\nConnection: close\r\nUser-Agent: Go-http-client/1.1\r\n", u.Path, u.Hostname(), u.Port()))},
			},
		},
		{
			Value: &contentwriterV1.WriteRequest_ProtocolHeader{
				ProtocolHeader: &contentwriterV1.Data{RecordNum: 1, Data: []byte(fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Length: %d\r\nContent-Type: text/plain; charset=utf-8\r\nDate: Wed, 15 May 2019 12:41:02 GMT\r\n", test.wantStatus, http.StatusText(test.wantStatus), len(test.wantContent)))},
			},
		},
		{
			Value: &contentwriterV1.WriteRequest_Payload{
				Payload: &contentwriterV1.Data{RecordNum: 1, Data: []byte(test.wantContent)},
			},
		},
		{
			Value: &contentwriterV1.WriteRequest_Meta{
				Meta: &contentwriterV1.WriteRequestMeta{
					ExecutionId:   "eid",
					TargetUri:     test.url,
					IpAddress:     "1.2.1.2",
					CollectionRef: &configV1.ConfigRef{Kind: configV1.Kind_collection, Id: "col1"},
					RecordMeta: map[int32]*contentwriterV1.WriteRequestMeta_RecordMeta{
						0: {
							RecordNum:         0,
							Type:              contentwriterV1.RecordType_REQUEST,
							RecordContentType: "application/http; msgtype=request",
							Size:              test.wantRequestBlockSize,
						},
						1: {
							RecordNum:         1,
							Type:              contentwriterV1.RecordType_RESPONSE,
							RecordContentType: "application/http; msgtype=response",
							Size:              test.wantResponseBlockSize,
						},
					},
				},
			},
		},
	}

	test.wantGrpcRequests = r
}

func (test *test) generateCachedRequests(u *url.URL, p int) {
	r := &requests{}
	r.DnsResolverRequests = []*dnsresolverV1.ResolveRequest{
		{Host: u.Hostname(), Port: int32(p), CollectionRef: &configV1.ConfigRef{Kind: configV1.Kind_collection, Id: "col1"}},
	}

	r.BrowserControllerRequests = []*browsercontrollerV1.DoRequest{
		{
			Action: &browsercontrollerV1.DoRequest_New{New: &browsercontrollerV1.RegisterNew{Uri: test.url}},
		},
		{
			Action: &browsercontrollerV1.DoRequest_Completed{
				Completed: &browsercontrollerV1.Completed{
					CrawlLog: &frontier.CrawlLog{
						StatusCode:     int32(test.wantStatus),
						Size:           test.wantResponseBlockSize,
						RequestedUri:   test.url,
						ContentType:    "text/plain; charset=utf-8",
						IpAddress:      "1.2.1.2",
						ExecutionId:    "eid",
						JobExecutionId: "jid",
					},
					Cached: true,
				},
			},
		},
	}

	r.ContentWriterRequests = []*contentwriterV1.WriteRequest{
		{
			Value: &contentwriterV1.WriteRequest_ProtocolHeader{
				ProtocolHeader: &contentwriterV1.Data{
					RecordNum: 0, Data: []byte(fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s:%s\r\nAccept-Encoding: gzip\r\nConnection: close\r\nUser-Agent: Go-http-client/1.1\r\n", u.Path, u.Hostname(), u.Port()))},
			},
		},
		{
			Value: &contentwriterV1.WriteRequest_Cancel{
				Cancel: "OK: Loaded from cache",
			},
		},
	}

	test.wantGrpcRequests = r
}

func (test *test) generateConnectionRefusedRequests(u *url.URL, p int) {
	r := &requests{}
	r.DnsResolverRequests = []*dnsresolverV1.ResolveRequest{
		{Host: u.Hostname(), Port: int32(p), CollectionRef: &configV1.ConfigRef{Kind: configV1.Kind_collection, Id: "col1"}},
	}

	r.BrowserControllerRequests = []*browsercontrollerV1.DoRequest{
		{
			Action: &browsercontrollerV1.DoRequest_New{New: &browsercontrollerV1.RegisterNew{Uri: test.url}},
		},
		{
			Action: &browsercontrollerV1.DoRequest_Completed{
				Completed: &browsercontrollerV1.Completed{
					CrawlLog: &frontier.CrawlLog{
						StatusCode:     -2,
						RequestedUri:   test.url,
						RecordType:     "response",
						IpAddress:      "1.2.1.2",
						ExecutionId:    "eid",
						JobExecutionId: "jid",
						Error: &commons.Error{
							Code:   -2,
							Msg:    "CONNECT_FAILED",
							Detail: "dial tcp 127.0.0.1:" + u.Port() + ": connect: connection refused",
						},
					},
				},
			},
		},
	}

	r.ContentWriterRequests = []*contentwriterV1.WriteRequest{
		{
			Value: &contentwriterV1.WriteRequest_ProtocolHeader{
				ProtocolHeader: &contentwriterV1.Data{
					RecordNum: 0, Data: []byte(fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s:%s\r\nAccept-Encoding: gzip\r\nConnection: close\r\nUser-Agent: Go-http-client/1.1\r\n", u.Path, u.Hostname(), u.Port()))},
			},
		},
		{
			Value: &contentwriterV1.WriteRequest_Cancel{
				Cancel: "dial tcp 127.0.0.1:" + u.Port() + ": connect: connection refused",
			},
		},
	}

	test.wantGrpcRequests = r
}

func (test *test) generateHandshakeFailureRequests(u *url.URL, p int) {
	r := &requests{}
	r.DnsResolverRequests = []*dnsresolverV1.ResolveRequest{
		{Host: u.Hostname(), Port: int32(p), CollectionRef: &configV1.ConfigRef{Kind: configV1.Kind_collection, Id: "col1"}},
	}

	r.BrowserControllerRequests = []*browsercontrollerV1.DoRequest{
		{
			Action: &browsercontrollerV1.DoRequest_New{New: &browsercontrollerV1.RegisterNew{Uri: test.url}},
		},
		{
			Action: &browsercontrollerV1.DoRequest_Completed{
				Completed: &browsercontrollerV1.Completed{
					CrawlLog: &frontier.CrawlLog{
						StatusCode:     -2,
						RequestedUri:   test.url,
						RecordType:     "response",
						IpAddress:      "1.2.1.2",
						ExecutionId:    "eid",
						JobExecutionId: "jid",
						Error: &commons.Error{
							Code:   -2,
							Msg:    "CONNECT_FAILED",
							Detail: "tls: handshake failure",
						},
					},
				},
			},
		},
	}

	r.ContentWriterRequests = []*contentwriterV1.WriteRequest{
		{
			Value: &contentwriterV1.WriteRequest_ProtocolHeader{
				ProtocolHeader: &contentwriterV1.Data{
					RecordNum: 0, Data: []byte(fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s:%s\r\nAccept-Encoding: gzip\r\nConnection: close\r\nUser-Agent: Go-http-client/1.1\r\n", u.Path, u.Hostname(), u.Port()))},
			},
		},
		{
			Value: &contentwriterV1.WriteRequest_Cancel{
				Cancel: "tls: handshake failure",
			},
		},
	}

	test.wantGrpcRequests = r
}

func compareCW(t *testing.T, serviceName string, tt test, want []*contentwriterV1.WriteRequest, got []*contentwriterV1.WriteRequest) {
	// If last request is a cancel request, then we don't care about the others
	lastWant := want[len(want)-1].Value
	if _, ok := lastWant.(*contentwriterV1.WriteRequest_Cancel); ok {
		if len(got) == 0 {
			t.Errorf("%s service got wrong cwcCancelFunc request.  %s request #%d\nWas:\n%v\nWant:\n%v", serviceName, serviceName,
				len(got), "Nothing", printRequest(lastWant))
			return
		}
		lastGot := got[len(got)-1].Value
		if requestsEqual(lastGot, lastWant) {
			return
		} else {
			t.Errorf("%s service got wrong cwcCancelFunc request.  %s request #%d\nWas:\n%v\nWant:\n%v", serviceName, serviceName,
				len(got), printRequest(lastGot), printRequest(lastWant))
		}
	}

	for i, r := range want {
		if i >= len(got) {
			t.Errorf("%s service received too few requests. Got %d, want %d.\nFirst missing request is:\n%v", serviceName,
				len(got), len(want), printRequest(want[len(got)]))
		} else {
			if !compareCwWriteRequest(t, r, got[i]) {
				t.Errorf("Got wrong %s request. %s request #%d\nWas:\n%v\nWant:\n%v", serviceName, serviceName,
					i+1, printRequest(got[i]), printRequest(want[i]))
			}
		}
	}
	if len(got) > len(want) {
		t.Errorf("%s service received too many requests. Got %d, want %d.\nFirst unwanted request is:\n%v", serviceName,
			len(got), len(want), printRequest(got[len(want)]))
	}
}
func compareBC(t *testing.T, serviceName string, tt test, want []*browsercontrollerV1.DoRequest, got []*browsercontrollerV1.DoRequest) {
	for i, r := range want {
		if i >= len(got) {
			t.Errorf("%s service received too few requests. Got %d, want %d.\nFirst missing request is:\n%v", serviceName,
				len(got), len(want), printRequest(want[len(got)]))
		} else {
			if !compareBcDoRequest(t, tt, r, got[i]) {
				t.Errorf("Got wrong %s request. %s request #%d\nWas:\n%v\nWant:\n%v", serviceName, serviceName,
					i+1, printRequest(got[i]), printRequest(want[i]))
			}
		}
	}
	if len(got) > len(want) {
		t.Errorf("%s service received too many requests. Got %d, want %d.\nFirst unwanted request is:\n%v", serviceName,
			len(got), len(want), printRequest(got[len(want)]))
	}
}
func compareDNS(t *testing.T, serviceName string, tt test, want []*dnsresolverV1.ResolveRequest, got []*dnsresolverV1.ResolveRequest) {
	for i, r := range want {
		if i >= len(got) {
			t.Errorf("%s service received too few requests. Got %d, want %d.\nFirst missing request is:\n%v", serviceName,
				len(got), len(want), printRequest(want[len(got)]))
		} else {
			if !requestsEqual(got[i], r) {
				t.Errorf("Got wrong %s request. %s request #%d\nWas:\n%v\nWant:\n%v", serviceName, serviceName,
					i+1, printRequest(got[i]), printRequest(want[i]))
			}
		}
	}
	if len(got) > len(want) {
		t.Errorf("%s service received too many requests. Got %d, want %d.\nFirst unwanted request is:\n%v", serviceName,
			len(got), len(want), printRequest(got[len(want)]))
	}
}

var dateRe = regexp.MustCompile(`(?sm)^(.*Date: )([^\r\n]+)(.*)$`)

func compareCwWriteRequest(t *testing.T, want *contentwriterV1.WriteRequest, got *contentwriterV1.WriteRequest) (ok bool) {
	switch wt := want.Value.(type) {
	case *contentwriterV1.WriteRequest_ProtocolHeader:
		wantBytes := wt.ProtocolHeader.Data
		g, o := got.Value.(*contentwriterV1.WriteRequest_ProtocolHeader)
		if !o {
			ok = false
			return
		}
		gotBytes := g.ProtocolHeader.Data
		if reflect.DeepEqual(wantBytes, gotBytes) {
			ok = true
		} else {
			// Compare date in string to time.Now()
			gotDateM := dateRe.FindSubmatch(gotBytes)
			if gotDateM == nil {
				ok = false
			} else {
				wantT := time.Now()
				gotT, err := time.Parse(time.RFC1123, string(gotDateM[2]))
				if err != nil || wantT.Sub(gotT) > 10*time.Second {
					t.Errorf("Date differs to much: Got '%v' which is %v ago\n", gotT, wantT.Sub(gotT))
					ok = false
				} else {
					wantDateM := dateRe.FindSubmatch(wantBytes)
					gotBytes = []byte(fmt.Sprintf("%s%s%s", gotDateM[1], wantDateM[2], gotDateM[3]))
					got.Value.(*contentwriterV1.WriteRequest_ProtocolHeader).ProtocolHeader.Data = gotBytes
					if reflect.DeepEqual(want, got) {
						ok = true
					} else {
						ok = false
					}
				}
			}
		}
	case *contentwriterV1.WriteRequest_Meta:
		gotT, err := ptypes.Timestamp(got.GetMeta().FetchTimeStamp)
		wantT := time.Now()
		if err != nil || wantT.Sub(gotT) > 10*time.Second {
			t.Errorf("Date differs to much: Got '%v' which is %v ago\n", gotT, wantT.Sub(gotT))
			ok = false
		} else {
			got.GetMeta().FetchTimeStamp = nil

			// Remove block digest since we cannot calculate the right value without access to content
			for _, r := range got.GetMeta().RecordMeta {
				if r.BlockDigest == "" {
					t.Errorf("Missing BlockDigest")
					return false
				}
				r.BlockDigest = ""
			}

			if reflect.DeepEqual(want, got) {
				ok = true
			} else {
				ok = false
			}
		}
	default:
		ok = true
		if !requestsEqual(got, want) {
			ok = false
		}
	}
	return
}

func compareBcDoRequest(t *testing.T, tt test, want *browsercontrollerV1.DoRequest, got *browsercontrollerV1.DoRequest) (ok bool) {
	switch gt := got.Action.(type) {
	case *browsercontrollerV1.DoRequest_Completed:
		ok = false
		allowedTimeDiff := time.Duration(gt.Completed.GetCrawlLog().FetchTimeMs+1500) * time.Millisecond
		if checkTimePb(t, gt.Completed.GetCrawlLog().FetchTimeStamp, allowedTimeDiff) {
			gt.Completed.GetCrawlLog().FetchTimeStamp = nil

			if !tt.wantResponseBlockDigest && gt.Completed.GetCrawlLog().BlockDigest != "" {
				t.Errorf("BlockDigest was not expected")
			}
			// Remove block digest since we cannot calculate the right value without access to content
			if tt.wantResponseBlockDigest && gt.Completed.GetCrawlLog().BlockDigest == "" {
				t.Errorf("Missing BlockDigest")
				return false
			}
			gt.Completed.GetCrawlLog().BlockDigest = ""

			gt.Completed.CrawlLog.FetchTimeMs = 0
			if reflect.DeepEqual(want, got) {
				ok = true
			} else {
				ok = false
			}
		}
	default:
		ok = true
		if !requestsEqual(got, want) {
			ok = false
		}
	}
	return
}

func checkTimePb(t *testing.T, ts *timestamp.Timestamp, allowedDiff time.Duration) bool {
	tm, err := ptypes.Timestamp(ts)
	if err != nil {
		t.Errorf("Error converting timestamp from protobuf %v: %v\n", ts, err)
		return false
	}
	return checkTime(t, tm, allowedDiff)
}

func checkTime(t *testing.T, ts time.Time, allowedDiff time.Duration) bool {
	wantT := time.Now()
	if wantT.Sub(ts) > allowedDiff {
		t.Errorf("Date differs to much: Got '%v' which is %v ago\n", ts, wantT.Sub(ts))
		return false
	} else {
		return true
	}
}

func get(url string, client *http.Client, timeout time.Duration) (int, []byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, nil, err
	}

	if timeout > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		req = req.WithContext(ctx)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	txt, err := ioutil.ReadAll(resp.Body)
	defer resp.Body.Close()
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, txt, nil
}

func requestsEqual(got, want interface{}) bool {
	if !reflect.DeepEqual(got, want) {
		return false
	}
	return true
}

func printRequest(req interface{}) string {
	return fmt.Sprintf("%30T: %v\n", req, req)
}

// localRecorderProxy creates a new recorderproxy which uses internal transport
func localRecorderProxy() (client *http.Client, proxy *recorderproxy.RecorderProxy) {
	recorderproxy.SetCA("", "")
	conn := recorderproxy.NewConnections()
	err := conn.Connect("", "", "", 1*time.Minute, grpc.WithContextDialer(bufDialer))
	if err != nil {
		log.Fatalf("Could not connect to services: %v", err)
	}

	proxy = recorderproxy.NewRecorderProxy(0, conn, 1*time.Minute)
	p := httptest.NewServer(proxy)
	proxyUrl, _ := url.Parse(p.URL)
	tr := &http.Transport{TLSClientConfig: acceptAllCerts, Proxy: http.ProxyURL(proxyUrl), DisableKeepAlives: true}
	client = &http.Client{Transport: tr}
	return
}

/**
 * Server mocks
 */
type GrpcServiceMock struct {
	l        *sync.Mutex
	requests *requests
	doneBC   chan bool
	doneCW   chan bool
}

func NewGrpcServiceMock() *GrpcServiceMock {
	return &GrpcServiceMock{l: &sync.Mutex{}}
}

func (s *GrpcServiceMock) addBcRequest(r *browsercontrollerV1.DoRequest) {
	s.l.Lock()
	s.requests.BrowserControllerRequests = append(s.requests.BrowserControllerRequests, r)
	s.l.Unlock()
}

func (s *GrpcServiceMock) addDnsRequest(r *dnsresolverV1.ResolveRequest) {
	s.l.Lock()
	s.requests.DnsResolverRequests = append(s.requests.DnsResolverRequests, r)
	s.l.Unlock()
}

func (s *GrpcServiceMock) addCwRequest(r *contentwriterV1.WriteRequest) {
	s.l.Lock()
	s.requests.ContentWriterRequests = append(s.requests.ContentWriterRequests, r)
	s.l.Unlock()
}

func (s *GrpcServiceMock) clear() {
	s.requests = &requests{}
}

//Implements DNS service
func (s *GrpcServiceMock) Resolve(ctx context.Context, in *dnsresolverV1.ResolveRequest) (*dnsresolverV1.ResolveReply, error) {
	s.addDnsRequest(in)

	out := &dnsresolverV1.ResolveReply{
		Host:      in.Host,
		Port:      in.Port,
		TextualIp: "1.2.1.2",
	}
	return out, nil
}

// Implements ContentWriterService
func (s *GrpcServiceMock) Write(server contentwriterV1.ContentWriter_WriteServer) error {
	s.doneCW = make(chan bool, 200)
	records := map[int32]*contentwriterV1.WriteResponseMeta_RecordMeta{}
	data := make(map[int32][]byte)
	size := make(map[int32]int64)
	separatorAdded := make(map[int32]bool)
	gotMeta := false
	gotCancel := false
	blockDigest := make(map[int32]hash.Hash)

	go func() {
		<-server.Context().Done()
		s.doneCW <- true
	}()

	for {
		request, err := server.Recv()
		if err == io.EOF {
			if !gotMeta && !gotCancel {
				return fmt.Errorf("missing metadata")
			}
			return server.SendAndClose(&contentwriterV1.WriteReply{
				Meta: &contentwriterV1.WriteResponseMeta{
					RecordMeta: records,
				},
			})
		}
		if err != nil {
			fmt.Printf("Unknown Error in ContentWriter communication %v\n", err)
			return err
		}

		s.addCwRequest(request)

		switch v := request.Value.(type) {
		case *contentwriterV1.WriteRequest_ProtocolHeader:
			size[v.ProtocolHeader.RecordNum] = int64(len(v.ProtocolHeader.Data))
			separatorAdded[v.ProtocolHeader.RecordNum] = false
			blockDigest[v.ProtocolHeader.RecordNum] = sha1.New()
			blockDigest[v.ProtocolHeader.RecordNum].Write(v.ProtocolHeader.Data)
			data[v.ProtocolHeader.RecordNum] = v.ProtocolHeader.Data
		case *contentwriterV1.WriteRequest_Payload:
			if !separatorAdded[v.Payload.RecordNum] {
				separatorAdded[v.Payload.RecordNum] = true
				size[v.Payload.RecordNum] += 2
				blockDigest[v.Payload.RecordNum].Write([]byte("\r\n"))
				data[v.Payload.RecordNum] = append(data[v.Payload.RecordNum], '\r', '\n')
			}
			size[v.Payload.RecordNum] += int64(len(v.Payload.Data))
			blockDigest[v.Payload.RecordNum].Write(v.Payload.Data)
			data[v.Payload.RecordNum] = append(data[v.Payload.RecordNum], v.Payload.Data...)
		case *contentwriterV1.WriteRequest_Meta:
			gotMeta = true
			for i, v2 := range v.Meta.RecordMeta {
				if size[v2.RecordNum] != v2.Size {
					return status.Error(codes.InvalidArgument, "Size mismatch")
				}

				pld := ""
				if blockDigest[v2.RecordNum] != nil {
					pld = fmt.Sprintf("sha1:%x", blockDigest[v2.RecordNum].Sum(nil))
				}
				if pld != v2.BlockDigest {
					return status.Error(codes.InvalidArgument, "Block digest mismatch")
				}

				// Fake error
				if strings.Contains(v.Meta.TargetUri, "cwerr") {
					return status.Error(codes.InvalidArgument, "Fake error")
				}

				idxString := strconv.Itoa(int(i))
				records[i] = &contentwriterV1.WriteResponseMeta_RecordMeta{
					RecordNum:           i,
					CollectionFinalName: "collection_0",
					StorageRef:          "storageRef_" + idxString,
					WarcId:              "warcid_" + idxString,
					PayloadDigest:       v2.PayloadDigest,
					BlockDigest:         v2.BlockDigest,
				}

				if v2.Type == contentwriterV1.RecordType_RESPONSE {
					records[i].RevisitReferenceId = "revisit_0"
					records[i].Type = contentwriterV1.RecordType_REVISIT
				} else {
					records[i].Type = contentwriterV1.RecordType_REQUEST
				}
			}
		case *contentwriterV1.WriteRequest_Cancel:
			gotCancel = true
		default:
			fmt.Printf("UNKNOWN REQ type %T\n", v)
		}
	}
}

// Implements BrowserController
func (s *GrpcServiceMock) Do(server browsercontrollerV1.BrowserController_DoServer) error {
	for {
		request, err := server.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		s.addBcRequest(request)

		switch v := request.Action.(type) {
		case *browsercontrollerV1.DoRequest_New:
			s.doneBC = make(chan bool, 200)
			go func() {
				<-server.Context().Done()
				s.doneBC <- true
			}()

			if strings.HasSuffix(v.New.Uri, "blocked") {
				_ = server.Send(&browsercontrollerV1.DoReply{
					Action: &browsercontrollerV1.DoReply_Cancel{
						Cancel: "Blocked by robots.txt",
					},
				})
				break
			}

			reply := &browsercontrollerV1.DoReply{
				Action: &browsercontrollerV1.DoReply_New{
					New: &browsercontrollerV1.NewReply{
						CrawlExecutionId: "eid",
						JobExecutionId:   "jid",
						CollectionRef: &configV1.ConfigRef{
							Kind: configV1.Kind_collection,
							Id:   "col1",
						},
					},
				},
			}
			if strings.HasSuffix(v.New.Uri, "replace") {
				reply.GetNew().ReplacementScript = &configV1.BrowserScript{
					Script: "replaced",
				}
			}
			_ = server.Send(reply)

			if strings.HasSuffix(v.New.Uri, "cancel") {
				go func() {
					time.Sleep(100 * time.Millisecond)
					_ = server.Send(&browsercontrollerV1.DoReply{
						Action: &browsercontrollerV1.DoReply_Cancel{
							Cancel: "Cancelled by browser controller",
						},
					})
				}()
			}
		case *browsercontrollerV1.DoRequest_Notify:
		case *browsercontrollerV1.DoRequest_Completed:
		default:
			fmt.Printf("UNKNOWN REQ type %T\n", v)
		}
	}
}
