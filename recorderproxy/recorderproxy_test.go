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
	"crypto/tls"
	"fmt"
	"github.com/go-test/deep"
	"github.com/golang/protobuf/ptypes"
	"github.com/golang/protobuf/ptypes/timestamp"
	browsercontrollerV1 "github.com/nlnwa/veidemann-api-go/browsercontroller/v1"
	"github.com/nlnwa/veidemann-api-go/commons/v1"
	configV1 "github.com/nlnwa/veidemann-api-go/config/v1"
	contentwriterV1 "github.com/nlnwa/veidemann-api-go/contentwriter/v1"
	dnsresolverV1 "github.com/nlnwa/veidemann-api-go/dnsresolver/v1"
	"github.com/nlnwa/veidemann-api-go/frontier/v1"
	"github.com/nlnwa/veidemann-recorderproxy/logger"
	"github.com/nlnwa/veidemann-recorderproxy/recorderproxy"
	"github.com/nlnwa/veidemann-recorderproxy/serviceconnections"
	"github.com/nlnwa/veidemann-recorderproxy/testutil"
	"google.golang.org/protobuf/proto"
	"io/ioutil"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

var (
	acceptAllCerts = &tls.Config{InsecureSkipVerify: true}
)

type test struct {
	name                    string
	url                     string
	wantStatus              int
	wantContent             string
	wantReplacedContent     string
	wantResponseBlockDigest bool
	wantResponseBlockSize   int64
	wantGrpcRequests        *testutil.Requests
	wantErr                 bool
	clientTimeout           time.Duration
	skip                    bool
	keepAlive               bool
}

func init() {
	logger.InitLog("info", "text", false)
}

func TestRecorderProxy(t *testing.T) {
	s := testutil.NewHttpServers()
	defer s.Close()
	grpcServices := testutil.NewGrpcServiceMock()
	defer grpcServices.Close()
	client, recorderProxy := localRecorderProxy(grpcServices.ClientConn, "")
	recorderProxy.Start()
	defer client.CloseIdleConnections()
	defer recorderProxy.Close()

	tests := []test{
		{
			name:                    "http:success",
			url:                     s.SrvHttp.URL + "/a",
			wantStatus:              200,
			wantContent:             "content from http server",
			wantResponseBlockDigest: true,
			wantResponseBlockSize:   141,
			wantErr:                 false,
			skip:                    false,
		},
		{
			name:                    "https:success",
			url:                     s.SrvHttps.URL + "/b",
			wantStatus:              200,
			wantContent:             "content from https server",
			wantResponseBlockDigest: true,
			wantResponseBlockSize:   142,
			wantErr:                 false,
			skip:                    false,
		},
		{
			name:          "http:client timeout",
			url:           s.SrvHttp.URL + "/slow",
			wantStatus:    0,
			wantContent:   "",
			wantErr:       true,
			clientTimeout: 500 * time.Millisecond,
			skip:          false,
		},
		{
			name:          "https:client timeout",
			url:           s.SrvHttps.URL + "/slow",
			wantStatus:    0,
			wantContent:   "",
			wantErr:       true,
			clientTimeout: 500 * time.Millisecond,
			skip:          false,
		},
		{
			name:                    "http:not found",
			url:                     s.SrvHttp.URL + "/c",
			wantStatus:              404,
			wantContent:             "404 page not found\n",
			wantResponseBlockDigest: true,
			wantResponseBlockSize:   176,
			wantErr:                 false,
			skip:                    false,
		},
		{
			name:                    "https:not found",
			url:                     s.SrvHttps.URL + "/c",
			wantStatus:              404,
			wantContent:             "404 page not found\n",
			wantResponseBlockDigest: true,
			wantResponseBlockSize:   176,
			wantErr:                 false,
			skip:                    false,
		},
		{
			name:                    "http:replace",
			url:                     s.SrvHttp.URL + "/replace",
			wantStatus:              200,
			wantContent:             "should be replaced",
			wantReplacedContent:     "replaced",
			wantResponseBlockDigest: true,
			wantResponseBlockSize:   135,
			wantErr:                 false,
			skip:                    false,
		},
		{
			name:                    "https:replace",
			url:                     s.SrvHttps.URL + "/replace",
			wantStatus:              200,
			wantContent:             "should be replaced",
			wantReplacedContent:     "replaced",
			wantResponseBlockDigest: true,
			wantResponseBlockSize:   135,
			wantErr:                 false,
			skip:                    false,
		},
		{
			name:        "http:server timeout",
			url:         s.SrvHttp.URL + "/extraslow",
			wantStatus:  503,
			wantContent: "Code: -404, Msg: EMPTY_RESPONSE, Detail: Empty reply from server",
			wantErr:     false,
			skip:        false,
		},
		{
			name:        "https:server timeout",
			url:         s.SrvHttps.URL + "/extraslow",
			wantStatus:  503,
			wantContent: "Code: -404, Msg: EMPTY_RESPONSE, Detail: Empty reply from server",
			wantErr:     false,
			skip:        false,
		},
		{
			name: "http:browser controller cancel",
			url:  s.SrvHttp.URL + "/cancel",
			//wantStatus:  503,
			//wantContent: "Code:-5011, Msg: CANCELED_BY_BROWSER, Detail: canceled by browser controller",
			wantStatus:  200,
			wantContent: "content from http server",
			wantErr:     false,
			skip:        false,
		},
		{
			name: "https:browser controller cancel",
			url:  s.SrvHttps.URL + "/cancel",
			//wantStatus:  503,
			//wantContent: "Code:-5011, Msg: CANCELED_BY_BROWSER, Detail: canceled by browser controller",
			wantStatus:  200,
			wantContent: "content from https server",
			wantErr:     false,
			skip:        false,
		},
		{
			name:        "http:blocked by robots.txt",
			url:         s.SrvHttp.URL + "/blocked",
			wantStatus:  503,
			wantContent: "Code: -9998, Msg: PRECLUDED_BY_ROBOTS, Detail: Robots.txt rules precluded fetch",
			wantErr:     false,
			skip:        false,
		},
		{
			name:        "https:blocked by robots.txt",
			url:         s.SrvHttps.URL + "/blocked",
			wantStatus:  503,
			wantContent: "Code: -9998, Msg: PRECLUDED_BY_ROBOTS, Detail: Robots.txt rules precluded fetch",
			wantErr:     false,
			skip:        false,
		},
		{
			name:                  "http:browser controller error",
			url:                   s.SrvHttp.URL + "/bccerr",
			wantStatus:            503,
			wantContent:           "Code: -5, Msg: error notifying browser controller",
			wantResponseBlockSize: 141,
			wantErr:               false,
			skip:                  false,
		},
		{
			name:                  "https:browser controller error",
			url:                   s.SrvHttps.URL + "/bccerr",
			wantStatus:            503,
			wantContent:           "Code: -5, Msg: error notifying browser controller",
			wantResponseBlockSize: 142,
			wantErr:               false,
			skip:                  false,
		},
		{
			name:                  "http:content writer error",
			url:                   s.SrvHttp.URL + "/cwerr",
			wantStatus:            200,
			wantContent:           "content from http server",
			wantResponseBlockSize: 141,
			wantErr:               false,
			skip:                  false,
		},
		{
			name:                  "https:content writer error",
			url:                   s.SrvHttps.URL + "/cwerr",
			wantStatus:            200,
			wantContent:           "content from https server",
			wantResponseBlockSize: 142,
			wantErr:               false,
			skip:                  false,
		},
		{
			name:                  "http:cached",
			url:                   s.SrvHttp.URL + "/cached",
			wantStatus:            200,
			wantContent:           "content from http server",
			wantResponseBlockSize: 217,
			wantErr:               false,
		},
		{
			name:                  "https:cached",
			url:                   s.SrvHttps.URL + "/cached",
			wantStatus:            200,
			wantContent:           "content from https server",
			wantResponseBlockSize: 217,
			wantErr:               false,
		},
		{
			name:                  "http:no host",
			url:                   s.SrvHttp.URL[:len(s.SrvHttp.URL)-2] + "1/no_host",
			wantStatus:            503,
			wantContent:           "Code: -2, Msg: CONNECT_FAILED, Detail: connect: connection refused",
			wantResponseBlockSize: 138,
			wantErr:               false,
		},
		{
			name:                  "https:no host",
			url:                   s.SrvHttps.URL[:len(s.SrvHttps.URL)-2] + "1/no_host",
			wantStatus:            503,
			wantContent:           "Code: -2, Msg: CONNECT_FAILED, Detail: connect: connection refused",
			wantResponseBlockSize: 138,
			wantErr:               false,
		},
		{
			name:                    "https:handshake failure",
			url:                     s.SrvHttpsBadCert.URL + "/b",
			wantStatus:              503,
			wantContent:             "Code: -2, Msg: CONNECT_FAILED, Detail: tls: handshake failure",
			wantResponseBlockDigest: false,
			wantResponseBlockSize:   144,
			wantErr:                 false,
			skip:                    false,
		},
	}

	for i, tt := range tests {
		tt.keepAlive = true

		tt.generateExpectedRequests()
		grpcServices.Clear()

		t.Run(strconv.Itoa(i)+": "+tt.name, func(t *testing.T) {
			if tt.skip {
				t.Skipf(tt.name)
			}

			fmt.Printf("Request %v\n", tt.url)
			statusCode, got, err := get(tt.url, client, tt.clientTimeout)
			fmt.Println("GET", statusCode, string(got), err)
			if grpcServices.DoneBC != nil {
				<-grpcServices.DoneBC
			}
			if grpcServices.DoneCW != nil {
				<-grpcServices.DoneCW
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
				if !strings.HasPrefix(string(got), tt.wantContent) {
					t.Errorf("Expected '%s' to start with '%s'", got, tt.wantContent)
					return
				}
			} else {
				if string(got) != tt.wantReplacedContent {
					t.Errorf("Expected '%s', got '%s'", tt.wantReplacedContent, got)
					return
				}
			}
			compareDNS(t, "DnsResolver", tt, tt.wantGrpcRequests.DnsResolverRequests, grpcServices.Requests.DnsResolverRequests)
			compareBC(t, "BrowserController", tt, tt.wantGrpcRequests.BrowserControllerRequests, grpcServices.Requests.BrowserControllerRequests)
			compareCW(t, "ContentWriter", tt, tt.wantGrpcRequests.ContentWriterRequests, grpcServices.Requests.ContentWriterRequests)
		})
	}
}

func TestRecorderProxyThroughProxy(t *testing.T) {
	s := testutil.NewHttpServers()
	defer s.Close()
	grpcServices := testutil.NewGrpcServiceMock()
	defer grpcServices.Close()
	nextProxy, nextProxyAddr := testutil.NewSecondaryProxy(s)
	defer nextProxy.Close()
	client, recorderProxy := localRecorderProxy(grpcServices.ClientConn, nextProxyAddr)
	recorderProxy.Start()
	defer client.CloseIdleConnections()
	defer recorderProxy.Close()

	tests := []test{
		{
			name:                    "http:success",
			url:                     s.SrvHttp.URL + "/a",
			wantStatus:              200,
			wantContent:             "content from http server",
			wantResponseBlockDigest: true,
			wantResponseBlockSize:   141,
			wantErr:                 false,
		},
		{
			name:                    "https:success",
			url:                     s.SrvHttps.URL + "/b",
			wantStatus:              200,
			wantContent:             "content from https server",
			wantResponseBlockDigest: true,
			wantResponseBlockSize:   142,
			wantErr:                 false,
		},
		{
			name:          "http:client timeout",
			url:           s.SrvHttp.URL + "/slow",
			wantStatus:    0,
			wantContent:   "",
			wantErr:       true,
			clientTimeout: 500 * time.Millisecond,
			skip:          false,
		},
		{
			name:          "https:client timeout",
			url:           s.SrvHttps.URL + "/slow",
			wantStatus:    0,
			wantContent:   "",
			wantErr:       true,
			clientTimeout: 500 * time.Millisecond,
			skip:          false,
		},
		{
			name:                    "http:not found",
			url:                     s.SrvHttp.URL + "/c",
			wantStatus:              404,
			wantContent:             "404 page not found\n",
			wantResponseBlockDigest: true,
			wantResponseBlockSize:   176,
			wantErr:                 false,
		},
		{
			name:                    "https:not found",
			url:                     s.SrvHttps.URL + "/c",
			wantStatus:              404,
			wantContent:             "404 page not found\n",
			wantResponseBlockDigest: true,
			wantResponseBlockSize:   176,
			wantErr:                 false,
		},
		{
			name:                    "http:replace",
			url:                     s.SrvHttp.URL + "/replace",
			wantStatus:              200,
			wantContent:             "should be replaced",
			wantReplacedContent:     "replaced",
			wantResponseBlockDigest: true,
			wantResponseBlockSize:   135,
			wantErr:                 false,
		},
		{
			name:                    "https:replace",
			url:                     s.SrvHttps.URL + "/replace",
			wantStatus:              200,
			wantContent:             "should be replaced",
			wantReplacedContent:     "replaced",
			wantResponseBlockDigest: true,
			wantResponseBlockSize:   135,
			wantErr:                 false,
		},
		{
			name:        "http:server timeout",
			url:         s.SrvHttp.URL + "/extraslow",
			wantStatus:  503,
			wantContent: "Code: -404, Msg: EMPTY_RESPONSE, Detail: Empty reply from server",
			wantErr:     false,
			skip:        false,
		},
		{
			name:        "https:server timeout",
			url:         s.SrvHttps.URL + "/extraslow",
			wantStatus:  503,
			wantContent: "Code: -404, Msg: EMPTY_RESPONSE, Detail: Empty reply from server",
			wantErr:     false,
			skip:        false,
		},
		{
			name:        "http:browser controller cancel",
			url:         s.SrvHttp.URL + "/cancel",
			wantStatus:  200,
			wantContent: "content from http server",
			wantErr:     false,
			skip:        false,
		},
		{
			name:        "https:browser controller cancel",
			url:         s.SrvHttps.URL + "/cancel",
			wantStatus:  200,
			wantContent: "content from https server",
			wantErr:     false,
			skip:        false,
		},
		{
			name:        "http:blocked by robots.txt",
			url:         s.SrvHttp.URL + "/blocked",
			wantStatus:  503,
			wantContent: "Code: -9998, Msg: PRECLUDED_BY_ROBOTS, Detail: Robots.txt rules precluded fetch",
			wantErr:     false,
			skip:        false,
		},
		{
			name:        "https:blocked by robots.txt",
			url:         s.SrvHttps.URL + "/blocked",
			wantStatus:  503,
			wantContent: "Code: -9998, Msg: PRECLUDED_BY_ROBOTS, Detail: Robots.txt rules precluded fetch",
			wantErr:     false,
			skip:        false,
		},
		{
			name:                  "http:browser controller error",
			url:                   s.SrvHttp.URL + "/bccerr",
			wantStatus:            503,
			wantContent:           "Code: -5, Msg: error notifying browser controller",
			wantResponseBlockSize: 141,
			wantErr:               false,
		},
		{
			name:                  "https:browser controller error",
			url:                   s.SrvHttps.URL + "/bccerr",
			wantStatus:            503,
			wantContent:           "Code: -5, Msg: error notifying browser controller",
			wantResponseBlockSize: 142,
			wantErr:               false,
		},
		{
			name:                  "http:content writer error",
			url:                   s.SrvHttp.URL + "/cwerr",
			wantStatus:            200,
			wantContent:           "content from http server",
			wantResponseBlockSize: 141,
			wantErr:               false,
			skip:                  false,
		},
		{
			name:                  "https:content writer error",
			url:                   s.SrvHttps.URL + "/cwerr",
			wantStatus:            200,
			wantContent:           "content from https server",
			wantResponseBlockSize: 142,
			wantErr:               false,
			skip:                  false,
		},
		{
			name:                  "http:cached",
			url:                   s.SrvHttp.URL + "/cached",
			wantStatus:            200,
			wantContent:           "content from http server",
			wantResponseBlockSize: 217,
			wantErr:               false,
		},
		{
			name:                  "https:cached",
			url:                   s.SrvHttps.URL + "/cached",
			wantStatus:            200,
			wantContent:           "content from https server",
			wantResponseBlockSize: 217,
			wantErr:               false,
		},
		{
			name:                  "http:no host",
			url:                   s.SrvHttp.URL[:len(s.SrvHttp.URL)-2] + "1/no_host",
			wantStatus:            503,
			wantContent:           "Code: -2, Msg: CONNECT_FAILED, Detail: connect: connection refused",
			wantResponseBlockSize: 138,
			wantErr:               false,
		},
		{
			name:                  "https:no host",
			url:                   s.SrvHttps.URL[:len(s.SrvHttps.URL)-2] + "1/no_host",
			wantStatus:            503,
			wantContent:           "Code: -2, Msg: CONNECT_FAILED, Detail: connect: connection refused",
			wantResponseBlockSize: 138,
			wantErr:               false,
			skip:                  false,
		},
		{
			name:                    "https:handshake failure",
			url:                     s.SrvHttpsBadCert.URL + "/b",
			wantStatus:              503,
			wantContent:             "Code: -2, Msg: CONNECT_FAILED, Detail: tls: handshake failure",
			wantResponseBlockDigest: false,
			wantResponseBlockSize:   144,
			wantErr:                 false,
		},
	}

	for i, tt := range tests {
		tt.keepAlive = true

		tt.generateExpectedRequestsForRecorderProxyThroughProxy()
		grpcServices.Clear()

		t.Run(strconv.Itoa(i)+": "+tt.name, func(t *testing.T) {
			if tt.skip {
				t.Skipf(tt.name)
			}

			fmt.Printf("Request %v\n", tt.url)
			statusCode, got, err := get(tt.url, client, tt.clientTimeout)
			if grpcServices.DoneBC != nil {
				<-grpcServices.DoneBC
			}
			if grpcServices.DoneCW != nil {
				<-grpcServices.DoneCW
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
				if !strings.HasPrefix(string(got), tt.wantContent) {
					t.Errorf("Expected '%s' to start with '%s'", got, tt.wantContent)
					return
				}
			} else {
				if string(got) != tt.wantReplacedContent {
					t.Errorf("Expected '%s', got '%s'", tt.wantReplacedContent, got)
					return
				}
			}
			compareDNS(t, "DnsResolver", tt, tt.wantGrpcRequests.DnsResolverRequests, grpcServices.Requests.DnsResolverRequests)
			compareBC(t, "BrowserController", tt, tt.wantGrpcRequests.BrowserControllerRequests, grpcServices.Requests.BrowserControllerRequests)
			compareCW(t, "ContentWriter", tt, tt.wantGrpcRequests.ContentWriterRequests, grpcServices.Requests.ContentWriterRequests)
		})
	}
}

/**
 * Helper functions
 */

func (test *test) generateExpectedRequests() {
	switch n := test.name; {
	case strings.HasSuffix(n, ":client timeout"):
		test.generateClientTimeoutRequests()
	case strings.HasSuffix(n, ":replace"):
		test.generateReplaceRequests()
	case strings.HasSuffix(n, ":server timeout"):
		test.generateServerTimeoutRequests()
	case strings.HasSuffix(n, ":grpc service timeout"):
		test.generateGrpcServiceTimeoutRequests()
	case strings.HasSuffix(n, ":browser controller cancel"):
		test.generateBrowserControllerCancelRequests()
	case strings.HasSuffix(n, ":blocked by robots.txt"):
		test.generateBlockedByRobotsTxtRequests()
	case strings.HasSuffix(n, ":browser controller error"):
		test.generateBrowserControllerErrorRequests()
	case strings.HasSuffix(n, ":content writer error"):
		test.generateContentWriterErrorRequests()
	case strings.HasSuffix(n, ":cached"):
		test.generateCachedRequests()
	case strings.HasSuffix(n, ":no host"):
		test.generateConnectionRefusedRequests()
	case strings.HasSuffix(n, ":handshake failure"):
		test.generateHandshakeFailureRequests("tls: handshake failure")
	default:
		test.generateSuccessRequests()
	}
}

func (test *test) generateExpectedRequestsForRecorderProxyThroughProxy() {
	switch n := test.name; {
	case strings.HasSuffix(n, ":client timeout"):
		test.generateClientTimeoutRequests()
	case strings.HasSuffix(n, ":replace"):
		test.generateReplaceRequests()
	case strings.HasSuffix(n, ":server timeout"):
		test.generateServerTimeoutRequests()
	case strings.HasSuffix(n, ":grpc service timeout"):
		test.generateGrpcServiceTimeoutRequests()
	case strings.HasSuffix(n, ":browser controller cancel"):
		test.generateBrowserControllerCancelRequests()
	case strings.HasSuffix(n, ":blocked by robots.txt"):
		test.generateBlockedByRobotsTxtRequests()
	case strings.HasSuffix(n, ":browser controller error"):
		test.generateBrowserControllerErrorRequests()
	case strings.HasSuffix(n, ":content writer error"):
		test.generateContentWriterErrorRequests()
	case strings.HasSuffix(n, ":cached"):
		test.generateCachedRequests()
	case strings.HasSuffix(n, ":no host"):
		test.generateConnectionRefusedRequests()
	case strings.HasSuffix(n, ":handshake failure"):
		test.generateHandshakeFailureRequests("tls: handshake failure")
	default:
		test.generateSuccessRequests()
	}
}

func (test *test) parseUrlAndPort() (*url.URL, int) {
	u, _ := url.Parse(test.url)
	p, _ := strconv.Atoi(u.Port())
	return u, p
}

func isHttps(uri string) (ok bool, pathStrippedUrl string) {
	u, err := url.Parse(uri)
	if err != nil {
		panic("Failed parsing URL: " + uri)
	}
	if strings.ToLower(u.Scheme) == "https" {
		ok = true
		u.Path = ""
		pathStrippedUrl = u.String()
	}
	return
}

func generateBccNewRequests(url string, alreadyConnected bool) []*browsercontrollerV1.DoRequest {
	var r []*browsercontrollerV1.DoRequest
	https, u := isHttps(url)
	if https && !alreadyConnected {
		r = append(r,
			&browsercontrollerV1.DoRequest{
				Action: &browsercontrollerV1.DoRequest_New{New: &browsercontrollerV1.RegisterNew{
					ProxyId: 0,
					Method:  "CONNECT",
					Uri:     u,
				}},
			})
	}

	if https || alreadyConnected {
		r = append(r,
			&browsercontrollerV1.DoRequest{
				Action: &browsercontrollerV1.DoRequest_New{New: &browsercontrollerV1.RegisterNew{
					ProxyId: 0,
					Method:  "GET",
					Uri:     url,
					CollectionRef: &configV1.ConfigRef{
						Kind: configV1.Kind_collection,
						Id:   "col1",
					},
				}},
			})
	} else {
		r = append(r,
			&browsercontrollerV1.DoRequest{
				Action: &browsercontrollerV1.DoRequest_New{New: &browsercontrollerV1.RegisterNew{
					ProxyId: 0,
					Method:  "GET",
					Uri:     url,
				}},
			})
	}
	return r
}

func generateBccDataReceivedRequest() *browsercontrollerV1.DoRequest {
	return &browsercontrollerV1.DoRequest{
		Action: &browsercontrollerV1.DoRequest_Notify{
			Notify: &browsercontrollerV1.NotifyActivity{Activity: browsercontrollerV1.NotifyActivity_DATA_RECEIVED},
		},
	}
}

func generateBccAllDataReceivedRequest() *browsercontrollerV1.DoRequest {
	return &browsercontrollerV1.DoRequest{
		Action: &browsercontrollerV1.DoRequest_Notify{
			Notify: &browsercontrollerV1.NotifyActivity{Activity: browsercontrollerV1.NotifyActivity_ALL_DATA_RECEIVED},
		},
	}
}

func generateCwProtocolHeaderRequest(u *url.URL, keepAlive bool) (*contentwriterV1.WriteRequest, int64) {
	k := ""
	if !keepAlive {
		k = "Connection: close\r\n"
	}
	header := []byte(fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s:%s\r\nAccept-Encoding: gzip\r\n%sUser-Agent: Go-http-client/1.1\r\n", u.RequestURI(), u.Hostname(), u.Port(), k))
	req := &contentwriterV1.WriteRequest{
		Value: &contentwriterV1.WriteRequest_ProtocolHeader{
			ProtocolHeader: &contentwriterV1.Data{RecordNum: 0, Data: header},
		},
	}
	return req, int64(len(header))
}

func generateCwProtocolHeaderResponse(status int, contentLength int) *contentwriterV1.WriteRequest {
	nosniffHeader := ""
	if status == 404 {
		nosniffHeader = "X-Content-Type-Options: nosniff\r\n"
	}
	header := []byte(fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Length: %d\r\nContent-Type: text/plain; charset=utf-8\r\nDate: Wed, 15 May 2019 12:41:02 GMT\r\n%s", status, http.StatusText(status), contentLength, nosniffHeader))
	req := &contentwriterV1.WriteRequest{
		Value: &contentwriterV1.WriteRequest_ProtocolHeader{
			ProtocolHeader: &contentwriterV1.Data{RecordNum: 1, Data: header},
		},
	}
	return req
}

func (test *test) generateSuccessRequests() {
	u, p := test.parseUrlAndPort()

	r := &testutil.Requests{}
	r.DnsResolverRequests = []*dnsresolverV1.ResolveRequest{
		{Host: u.Hostname(), Port: int32(p), CollectionRef: &configV1.ConfigRef{Kind: configV1.Kind_collection, Id: "col1"}},
	}

	r.BrowserControllerRequests = append(
		generateBccNewRequests(test.url, false),
		generateBccDataReceivedRequest(),
		generateBccAllDataReceivedRequest(),
		&browsercontrollerV1.DoRequest{
			Action: &browsercontrollerV1.DoRequest_Completed{
				Completed: &browsercontrollerV1.Completed{
					CrawlLog: &frontier.CrawlLog{
						WarcId:              "warcid_1",
						StatusCode:          int32(test.wantStatus),
						Size:                test.wantResponseBlockSize,
						Method:              "GET",
						RequestedUri:        test.url,
						ContentType:         "text/plain; charset=utf-8",
						StorageRef:          "storageRef_1",
						RecordType:          "revisit",
						WarcRefersTo:        "revisit_0",
						IpAddress:           "127.0.0.1",
						ExecutionId:         "eid",
						JobExecutionId:      "jid",
						CollectionFinalName: "collection_0",
					},
				},
			},
		},
	)

	requestHeader, requestLength := generateCwProtocolHeaderRequest(u, test.keepAlive)
	r.ContentWriterRequests = []*contentwriterV1.WriteRequest{
		requestHeader,
		generateCwProtocolHeaderResponse(test.wantStatus, len(test.wantContent)),
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
					IpAddress:     "127.0.0.1",
					CollectionRef: &configV1.ConfigRef{Kind: configV1.Kind_collection, Id: "col1"},
					RecordMeta: map[int32]*contentwriterV1.WriteRequestMeta_RecordMeta{
						0: {
							RecordNum:         0,
							Type:              contentwriterV1.RecordType_REQUEST,
							RecordContentType: "application/http; msgtype=request",
							Size:              requestLength,
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

func (test *test) generateClientTimeoutRequests() {
	r := &testutil.Requests{}

	r.BrowserControllerRequests = append(
		generateBccNewRequests(test.url, true),
		&browsercontrollerV1.DoRequest{
			Action: &browsercontrollerV1.DoRequest_Completed{
				Completed: &browsercontrollerV1.Completed{
					CrawlLog: &frontier.CrawlLog{
						StatusCode:     -5011,
						RequestedUri:   test.url,
						Method:         "GET",
						RecordType:     "response",
						IpAddress:      "127.0.0.1",
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
	)

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

func (test *test) generateReplaceRequests() {
	u, _ := test.parseUrlAndPort()

	r := &testutil.Requests{}

	r.BrowserControllerRequests = append(
		generateBccNewRequests(test.url, true),
		generateBccDataReceivedRequest(),
		generateBccAllDataReceivedRequest(),
		&browsercontrollerV1.DoRequest{
			Action: &browsercontrollerV1.DoRequest_Completed{
				Completed: &browsercontrollerV1.Completed{
					CrawlLog: &frontier.CrawlLog{
						WarcId:              "warcid_1",
						StatusCode:          int32(test.wantStatus),
						Size:                test.wantResponseBlockSize,
						Method:              "GET",
						RequestedUri:        test.url,
						ContentType:         "text/plain; charset=utf-8",
						StorageRef:          "storageRef_1",
						RecordType:          "revisit",
						WarcRefersTo:        "revisit_0",
						IpAddress:           "127.0.0.1",
						ExecutionId:         "eid",
						JobExecutionId:      "jid",
						CollectionFinalName: "collection_0",
					},
				},
			},
		},
	)

	requestHeader, requestLength := generateCwProtocolHeaderRequest(u, test.keepAlive)
	r.ContentWriterRequests = []*contentwriterV1.WriteRequest{
		requestHeader,
		generateCwProtocolHeaderResponse(test.wantStatus, len(test.wantContent)),
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
					IpAddress:     "127.0.0.1",
					CollectionRef: &configV1.ConfigRef{Kind: configV1.Kind_collection, Id: "col1"},
					RecordMeta: map[int32]*contentwriterV1.WriteRequestMeta_RecordMeta{
						0: {
							RecordNum:         0,
							Type:              contentwriterV1.RecordType_REQUEST,
							RecordContentType: "application/http; msgtype=request",
							Size:              requestLength,
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

func (test *test) generateServerTimeoutRequests() {
	r := &testutil.Requests{}

	r.BrowserControllerRequests = append(
		generateBccNewRequests(test.url, true),
		generateBccAllDataReceivedRequest(),
		&browsercontrollerV1.DoRequest{
			Action: &browsercontrollerV1.DoRequest_Completed{
				Completed: &browsercontrollerV1.Completed{
					CrawlLog: &frontier.CrawlLog{
						StatusCode:     -404,
						RequestedUri:   test.url,
						Method:         "GET",
						RecordType:     "response",
						IpAddress:      "127.0.0.1",
						ExecutionId:    "eid",
						JobExecutionId: "jid",
						Error: &commons.Error{
							Code:   -404,
							Msg:    "EMPTY_RESPONSE",
							Detail: "Empty reply from server",
						},
					},
				},
			},
		},
	)

	r.ContentWriterRequests = []*contentwriterV1.WriteRequest{
		{
			Value: &contentwriterV1.WriteRequest_Cancel{
				Cancel: "Empty reply from server",
			},
		},
	}

	test.wantGrpcRequests = r
}

func (test *test) generateGrpcServiceTimeoutRequests() {
	u, p := test.parseUrlAndPort()
	r := &testutil.Requests{}
	r.DnsResolverRequests = []*dnsresolverV1.ResolveRequest{
		{Host: u.Hostname(), Port: int32(p), CollectionRef: &configV1.ConfigRef{Kind: configV1.Kind_collection, Id: "col1"}},
	}

	r.BrowserControllerRequests = generateBccNewRequests(test.url, false)

	requestHeader, _ := generateCwProtocolHeaderRequest(u, test.keepAlive)
	r.ContentWriterRequests = []*contentwriterV1.WriteRequest{
		requestHeader,
	}

	test.wantGrpcRequests = r
}

func (test *test) generateBrowserControllerCancelRequests() {
	u, p := test.parseUrlAndPort()
	r := &testutil.Requests{}
	r.DnsResolverRequests = []*dnsresolverV1.ResolveRequest{
		{Host: u.Hostname(), Port: int32(p), CollectionRef: &configV1.ConfigRef{Kind: configV1.Kind_collection, Id: "col1"}},
	}

	r.BrowserControllerRequests = append(
		generateBccNewRequests(test.url, false),
		&browsercontrollerV1.DoRequest{
			Action: &browsercontrollerV1.DoRequest_Completed{
				Completed: &browsercontrollerV1.Completed{
					CrawlLog: &frontier.CrawlLog{
						StatusCode:     -5011,
						Method:         "GET",
						RequestedUri:   test.url,
						RecordType:     "response",
						IpAddress:      "127.0.0.1",
						ExecutionId:    "eid",
						JobExecutionId: "jid",
						Error: &commons.Error{
							Code:   -5011,
							Msg:    "CANCELED_BY_BROWSER",
							Detail: "canceled by browser controller",
						},
					},
				},
			},
		},
	)

	r.ContentWriterRequests = []*contentwriterV1.WriteRequest{
		{
			Value: &contentwriterV1.WriteRequest_Cancel{
				Cancel: "canceled by browser controller",
			},
		},
	}

	test.wantGrpcRequests = r
}

func (test *test) generateBlockedByRobotsTxtRequests() {
	r := &testutil.Requests{}
	r.BrowserControllerRequests = append(
		generateBccNewRequests(test.url, true),
		generateBccAllDataReceivedRequest(),
		&browsercontrollerV1.DoRequest{
			Action: &browsercontrollerV1.DoRequest_Completed{
				Completed: &browsercontrollerV1.Completed{
					CrawlLog: &frontier.CrawlLog{
						StatusCode:   -9998,
						Method:       "GET",
						RequestedUri: test.url,
						RecordType:   "response",
						IpAddress:    "127.0.0.1",
						Error: &commons.Error{
							Code:   -9998,
							Msg:    "PRECLUDED_BY_ROBOTS",
							Detail: "Robots.txt rules precluded fetch",
						},
					},
				},
			},
		},
	)

	r.ContentWriterRequests = []*contentwriterV1.WriteRequest{}

	test.wantGrpcRequests = r
}

func (test *test) generateBrowserControllerErrorRequests() {
	u, p := test.parseUrlAndPort()
	r := &testutil.Requests{}

	if u.Scheme == "https" {
		r.DnsResolverRequests = []*dnsresolverV1.ResolveRequest{
			{Host: u.Hostname(), Port: int32(p), CollectionRef: &configV1.ConfigRef{Kind: configV1.Kind_collection, Id: "col1"}},
		}
	}

	r.BrowserControllerRequests = generateBccNewRequests(test.url, false)

	r.ContentWriterRequests = []*contentwriterV1.WriteRequest{}

	test.wantGrpcRequests = r
}

func (test *test) generateContentWriterErrorRequests() {
	u, p := test.parseUrlAndPort()
	r := &testutil.Requests{}
	r.DnsResolverRequests = []*dnsresolverV1.ResolveRequest{
		{Host: u.Hostname(), Port: int32(p), CollectionRef: &configV1.ConfigRef{Kind: configV1.Kind_collection, Id: "col1"}},
	}

	r.BrowserControllerRequests = append(
		generateBccNewRequests(test.url, false),
		generateBccDataReceivedRequest(),
		generateBccAllDataReceivedRequest(),
		&browsercontrollerV1.DoRequest{
			Action: &browsercontrollerV1.DoRequest_Completed{
				Completed: &browsercontrollerV1.Completed{
					CrawlLog: &frontier.CrawlLog{
						StatusCode:     -5,
						Method:         "GET",
						RequestedUri:   test.url,
						RecordType:     "response",
						IpAddress:      "127.0.0.1",
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
	)

	requestHeader, requestLength := generateCwProtocolHeaderRequest(u, test.keepAlive)
	r.ContentWriterRequests = []*contentwriterV1.WriteRequest{
		requestHeader,
		generateCwProtocolHeaderResponse(test.wantStatus, len(test.wantContent)),
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
					IpAddress:     "127.0.0.1",
					CollectionRef: &configV1.ConfigRef{Kind: configV1.Kind_collection, Id: "col1"},
					RecordMeta: map[int32]*contentwriterV1.WriteRequestMeta_RecordMeta{
						0: {
							RecordNum:         0,
							Type:              contentwriterV1.RecordType_REQUEST,
							RecordContentType: "application/http; msgtype=request",
							Size:              requestLength,
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

func (test *test) generateCachedRequests() {
	u, _ := test.parseUrlAndPort()
	r := &testutil.Requests{}

	r.BrowserControllerRequests = append(
		generateBccNewRequests(test.url, true),
		&browsercontrollerV1.DoRequest{
			Action: &browsercontrollerV1.DoRequest_Completed{
				Completed: &browsercontrollerV1.Completed{
					CrawlLog: &frontier.CrawlLog{
						StatusCode:     int32(test.wantStatus),
						Size:           test.wantResponseBlockSize,
						Method:         "GET",
						RequestedUri:   test.url,
						ContentType:    "text/plain; charset=utf-8",
						IpAddress:      "127.0.0.1",
						ExecutionId:    "eid",
						JobExecutionId: "jid",
					},
					Cached: true,
				},
			},
		},
	)

	requestHeader, _ := generateCwProtocolHeaderRequest(u, test.keepAlive)
	r.ContentWriterRequests = []*contentwriterV1.WriteRequest{
		requestHeader,
		{
			Value: &contentwriterV1.WriteRequest_Cancel{
				Cancel: "OK: Loaded from cache",
			},
		},
	}

	test.wantGrpcRequests = r
}

func (test *test) generateConnectionRefusedRequests() {
	u, p := test.parseUrlAndPort()
	r := &testutil.Requests{}
	if u.Scheme == "https" {
		r.DnsResolverRequests = []*dnsresolverV1.ResolveRequest{
			{Host: u.Hostname(), Port: int32(p), CollectionRef: &configV1.ConfigRef{Kind: configV1.Kind_collection, Id: "col1"}},
		}
	}

	https, _ := isHttps(test.url)
	r.BrowserControllerRequests = append(
		generateBccNewRequests(test.url, !https),
		generateBccAllDataReceivedRequest(),
		&browsercontrollerV1.DoRequest{
			Action: &browsercontrollerV1.DoRequest_Completed{
				Completed: &browsercontrollerV1.Completed{
					CrawlLog: &frontier.CrawlLog{
						StatusCode:     -2,
						Method:         "GET",
						RequestedUri:   test.url,
						RecordType:     "response",
						IpAddress:      "127.0.0.1",
						ExecutionId:    "eid",
						JobExecutionId: "jid",
						Error: &commons.Error{
							Code:   -2,
							Msg:    "CONNECT_FAILED",
							Detail: "connect: connection refused",
						},
					},
				},
			},
		},
	)

	requestHeader, _ := generateCwProtocolHeaderRequest(u, test.keepAlive)
	r.ContentWriterRequests = []*contentwriterV1.WriteRequest{
		requestHeader,
		{
			Value: &contentwriterV1.WriteRequest_Cancel{
				Cancel: "connect: connection refused",
			},
		},
	}

	test.wantGrpcRequests = r
}

func (test *test) generateHandshakeFailureRequests(errorMessage string) {
	u, p := test.parseUrlAndPort()
	r := &testutil.Requests{}
	r.DnsResolverRequests = []*dnsresolverV1.ResolveRequest{
		{Host: u.Hostname(), Port: int32(p), CollectionRef: &configV1.ConfigRef{Kind: configV1.Kind_collection, Id: "col1"}},
	}

	r.BrowserControllerRequests = append(
		generateBccNewRequests(test.url, false),
		generateBccAllDataReceivedRequest(),
		&browsercontrollerV1.DoRequest{
			Action: &browsercontrollerV1.DoRequest_Completed{
				Completed: &browsercontrollerV1.Completed{
					CrawlLog: &frontier.CrawlLog{
						StatusCode:     -2,
						Method:         "GET",
						RequestedUri:   test.url,
						RecordType:     "response",
						IpAddress:      "127.0.0.1",
						ExecutionId:    "eid",
						JobExecutionId: "jid",
						Error: &commons.Error{
							Code:   -2,
							Msg:    "CONNECT_FAILED",
							Detail: errorMessage,
						},
					},
				},
			},
		},
	)

	r.ContentWriterRequests = []*contentwriterV1.WriteRequest{}

	test.wantGrpcRequests = r
}

func compareCW(t *testing.T, serviceName string, tt test, want []*contentwriterV1.WriteRequest, got []*contentwriterV1.WriteRequest) {
	if len(want) == 0 && len(got) == 0 {
		return
	}

	// If last request is a cancel request, then we don't care about the others
	if len(want) > 0 {
		lastWant := want[len(want)-1].Value
		if _, ok := lastWant.(*contentwriterV1.WriteRequest_Cancel); ok {
			if len(got) == 0 {
				// No requests at all is treated similar to cancel
				return
			}
			lastGot := got[len(got)-1].Value
			if reflect.DeepEqual(lastGot, lastWant) {
				return
			} else {
				t.Errorf("%s service got wrong cwcCancelFunc request.  %s request #%d\nWas:\n%v\nWant:\n%v", serviceName, serviceName,
					len(got), printRequest(lastGot), printRequest(lastWant))
			}
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
			listGotWant(t, got, want)
		} else {
			if !compareBcDoRequest(t, tt, r, got[i]) {
				t.Errorf("Got wrong %s request. %s request #%d\nWas:\n%v\nWant:\n%v", serviceName, serviceName,
					i+1, printRequest(got[i]), printRequest(want[i]))
				t.Errorf("Got wrong %v\n",
					proto.Equal(got[i].GetCompleted().GetCrawlLog(), want[i].GetCompleted().GetCrawlLog()))
				if diff := deep.Equal(got[i], want[i]); diff != nil {
					t.Error(diff)
				}
			}
		}
	}
	if len(got) > len(want) {
		t.Errorf("%s service received too many requests. Got %d, want %d.\nFirst unwanted request is:\n%v", serviceName,
			len(got), len(want), printRequest(got[len(want)]))
		listGotWant(t, got, want)
	}
}
func listGotWant(t *testing.T, got, want interface{}) {
	g := reflect.ValueOf(got)
	for i := 0; i < g.Len(); i++ {
		t.Errorf(" GOT: %v", g.Index(i))
	}
	w := reflect.ValueOf(want)
	for i := 0; i < w.Len(); i++ {
		t.Errorf("WANT: %v", w.Index(i))
	}
}

func compareDNS(t *testing.T, serviceName string, tt test, want []*dnsresolverV1.ResolveRequest, got []*dnsresolverV1.ResolveRequest) {
	for i, r := range want {
		if i >= len(got) {
			t.Errorf("%s service received too few requests. Got %d, want %d.\nFirst missing request is:\n%v", serviceName,
				len(got), len(want), printRequest(want[len(got)]))
		} else {
			if !proto.Equal(got[i], r) {
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
					if proto.Equal(want, got) {
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

			if proto.Equal(want, got) {
				ok = true
			} else {
				ok = false
			}
		}
	default:
		ok = true
		if !proto.Equal(got, want) {
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
			if proto.Equal(want, got) {
				ok = true
			} else {
				ok = false
			}
		}
	default:
		ok = true
		if !proto.Equal(got, want) {
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

func get(uri string, client *http.Client, timeout time.Duration) (int, []byte, error) {
	req, err := http.NewRequest("GET", uri, nil)
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

func printRequest(req interface{}) string {
	return fmt.Sprintf("%30T: %v\n", req, req)
}

// localRecorderProxy creates a new recorderproxy which uses internal transport
func localRecorderProxy(conn *serviceconnections.Connections, nextProxyAddr string) (client *http.Client, proxy *recorderproxy.RecorderProxy) {
	proxy = recorderproxy.NewRecorderProxy(0, "localhost", 0, conn, 1*time.Minute, nextProxyAddr)
	proxy.Start()
	proxyUrl, _ := url.Parse("http://" + proxy.Addr)
	fmt.Printf(" FIRST PROXY URL: %v\n", proxyUrl)
	fmt.Printf("SECOND PROXY URL: %v\n", nextProxyAddr)

	tr := &http.Transport{TLSClientConfig: acceptAllCerts, Proxy: http.ProxyURL(proxyUrl), DisableKeepAlives: false}
	client = &http.Client{Transport: tr}
	return
}
