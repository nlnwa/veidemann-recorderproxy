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
	"google.golang.org/grpc/test/bufconn"
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
	acceptAllCerts = &tls.Config{InsecureSkipVerify: true}
	httpsMux       = http.NewServeMux()
	httpMux        = http.NewServeMux()
	srvHttps       = httptest.NewTLSServer(httpsMux)
	srvHttp        = httptest.NewServer(httpMux)
	recorderProxy  *recorderproxy.RecorderProxy
	lis            *bufconn.Listener
	grpcServices   *GrpcServiceMock
	client         *http.Client
)

func bufDialer(context.Context, string) (net.Conn, error) {
	return lis.Dial()
}

type ConstantHandler string

func (h ConstantHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	time.Sleep(20 * time.Millisecond)
	_, _ = io.WriteString(w, string(h))
}

type ConstantSlowHandler string

func (h ConstantSlowHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	time.Sleep(510 * time.Millisecond)
	_, _ = io.WriteString(w, string(h))
}

func init() {
	httpMux.Handle("/a", ConstantHandler("content from http server"))
	httpsMux.Handle("/b", ConstantHandler("content from https server"))
	httpMux.Handle("/replace", ConstantHandler("should be replaced"))
	httpsMux.Handle("/replace", ConstantHandler("should be replaced"))
	httpMux.Handle("/slow", ConstantSlowHandler("content from http server"))
	httpsMux.Handle("/slow", ConstantSlowHandler("content from https server"))

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
}

type requests struct {
	BrowserControllerRequests []*browsercontrollerV1.DoRequest
	DnsResolverRequests       []*dnsresolverV1.ResolveRequest
	ContentWriterRequests     []*contentwriterV1.WriteRequest
}
type test struct {
	name                      string
	url                       string
	wantStatus                int
	wantContent               string
	wantReplacedContent       string
	wantResponseBlockDigest   bool
	wantResponsePayloadDigest string
	wantRequestBlockSize      int64
	wantResponseBlockSize     int64
	wantGrpcRequests          *requests
	wantErr                   bool
	srvWriteTimout            time.Duration
	grpcTimeout               time.Duration
	clientTimeout             time.Duration
}

func TestGoproxyThroughProxy(t *testing.T) {
	tests := []test{
		{
			name:                      "http:success",
			url:                       srvHttp.URL + "/a",
			wantStatus:                200,
			wantContent:               "content from http server",
			wantResponsePayloadDigest: "sha1:5b8b70028b3eeeb0b90971d0924447954ab98eff",
			wantResponseBlockDigest:   true,
			wantRequestBlockSize:      116,
			wantResponseBlockSize:     141,
			wantErr:                   false,
		},
		{
			name:                      "https:success",
			url:                       srvHttps.URL + "/b",
			wantStatus:                200,
			wantContent:               "content from https server",
			wantResponsePayloadDigest: "sha1:b65da69891a174c26e9fc5a528ecc55fadf596d6",
			wantResponseBlockDigest:   true,
			wantRequestBlockSize:      116,
			wantResponseBlockSize:     142,
			wantErr:                   false,
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
			name:                      "http:not found",
			url:                       srvHttp.URL + "/c",
			wantStatus:                404,
			wantContent:               "404 page not found\n",
			wantResponsePayloadDigest: "sha1:da3968197e7bf67aa45a77515b52ba2710c5fc34",
			wantResponseBlockDigest:   true,
			wantRequestBlockSize:      116,
			wantResponseBlockSize:     176,
			wantErr:                   false,
		},
		{
			name:                      "https:not found",
			url:                       srvHttps.URL + "/c",
			wantStatus:                404,
			wantContent:               "404 page not found\n",
			wantResponsePayloadDigest: "sha1:da3968197e7bf67aa45a77515b52ba2710c5fc34",
			wantResponseBlockDigest:   true,
			wantRequestBlockSize:      116,
			wantResponseBlockSize:     176,
			wantErr:                   false,
		},
		{
			name:                      "http:replace",
			url:                       srvHttp.URL + "/replace",
			wantStatus:                200,
			wantContent:               "should be replaced",
			wantReplacedContent:       "replaced",
			wantResponsePayloadDigest: "sha1:cd5b9640d66ce21e6cb7542b4c82c1b04fe3a4c5",
			wantResponseBlockDigest:   true,
			wantRequestBlockSize:      122,
			wantResponseBlockSize:     135,
			wantErr:                   false,
		},
		{
			name:                      "https:replace",
			url:                       srvHttps.URL + "/replace",
			wantStatus:                200,
			wantContent:               "should be replaced",
			wantReplacedContent:       "replaced",
			wantResponsePayloadDigest: "sha1:cd5b9640d66ce21e6cb7542b4c82c1b04fe3a4c5",
			wantResponseBlockDigest:   true,
			wantRequestBlockSize:      122,
			wantResponseBlockSize:     135,
			wantErr:                   false,
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
	}

	for _, tt := range tests {
		srvHttp.Config.WriteTimeout = tt.srvWriteTimout
		srvHttps.Config.WriteTimeout = tt.srvWriteTimout
		//client.Timeout = tt.clientTimeout

		if tt.grpcTimeout == 0 {
			recorderProxy.ConnectionTimeout = 1 * time.Minute
		} else {
			recorderProxy.ConnectionTimeout = tt.grpcTimeout
		}

		tt.generateExpectedRequests()
		grpcServices.clear()

		t.Run(tt.name, func(t *testing.T) {
			statusCode, got, err := get(tt.url, client, tt.clientTimeout)
			<-grpcServices.doneBC
			<-grpcServices.doneCW

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
						PayloadDigest:       test.wantResponsePayloadDigest,
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
					RecordNum: 0, Data: []byte(fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s:%s\r\nAccept-Encoding: gzip\r\nConnection: close\r\nUser-Agent: Go-http-client/1.1\r\n\r\n", u.Path, u.Hostname(), u.Port()))},
			},
		},
		{
			Value: &contentwriterV1.WriteRequest_ProtocolHeader{
				ProtocolHeader: &contentwriterV1.Data{RecordNum: 1, Data: []byte(fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Length: %d\r\nContent-Type: text/plain; charset=utf-8\r\nDate: Wed, 15 May 2019 12:41:02 GMT%s\r\n\r\n", test.wantStatus, http.StatusText(test.wantStatus), len(test.wantContent), extraHeaders))},
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
							RecordNum: 0,
							Type:      contentwriterV1.RecordType_REQUEST,
							Size:      test.wantRequestBlockSize,
						},
						1: {
							RecordNum:         1,
							Type:              contentwriterV1.RecordType_RESPONSE,
							RecordContentType: "text/plain; charset=utf-8",
							PayloadDigest:     test.wantResponsePayloadDigest,
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
						StatusCode:     504,
						RequestedUri:   test.url,
						RecordType:     "response",
						IpAddress:      "1.2.1.2",
						ExecutionId:    "eid",
						JobExecutionId: "jid",
						Error: &commons.Error{
							Code:   504,
							Msg:    "GATEWAY_TIMEOUT",
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
					RecordNum: 0, Data: []byte(fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s:%s\r\nAccept-Encoding: gzip\r\nConnection: close\r\nUser-Agent: Go-http-client/1.1\r\n\r\n", u.Path, u.Hostname(), u.Port())),
				},
			},
		},
	}

	test.wantGrpcRequests = r
}

func compareCW(t *testing.T, serviceName string, tt test, want []*contentwriterV1.WriteRequest, got []*contentwriterV1.WriteRequest) {
	// If last request is a cancel request, then we don't care about the others
	lastWant := want[len(want)-1].Value
	if _, ok := lastWant.(*contentwriterV1.WriteRequest_Cancel); ok {
		lastGot := got[len(got)-1].Value
		if requestsEqual(lastGot, lastWant) {
			return
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
		allowedTimeDiff := time.Duration(gt.Completed.GetCrawlLog().FetchTimeMs+300) * time.Millisecond
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
//func localRecorderProxy(timeout time.Duration) (client *http.Client, proxy *httptest.Server) {
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
	return &GrpcServiceMock{l: &sync.Mutex{}, doneBC: make(chan bool, 200), doneCW: make(chan bool, 200)}
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
	records := map[int32]*contentwriterV1.WriteResponseMeta_RecordMeta{}

	for {
		request, err := server.Recv()
		if err == io.EOF {
			s.doneCW <- true
			return server.SendAndClose(&contentwriterV1.WriteReply{
				Meta: &contentwriterV1.WriteResponseMeta{
					RecordMeta: records,
				},
			})
		}
		if err != nil {
			s.doneCW <- true
			return err
		}

		s.addCwRequest(request)

		switch v := request.Value.(type) {
		case *contentwriterV1.WriteRequest_ProtocolHeader:
		case *contentwriterV1.WriteRequest_Payload:
		case *contentwriterV1.WriteRequest_Meta:
			for i, v := range v.Meta.RecordMeta {
				idxString := strconv.Itoa(int(i))
				records[i] = &contentwriterV1.WriteResponseMeta_RecordMeta{
					RecordNum:           i,
					CollectionFinalName: "collection_0",
					StorageRef:          "storageRef_" + idxString,
					WarcId:              "warcid_" + idxString,
					PayloadDigest:       v.PayloadDigest,
					BlockDigest:         v.BlockDigest,
				}
				switch i {
				case 0:
					records[i].Type = contentwriterV1.RecordType_REQUEST
				case 1:
					records[i].RevisitReferenceId = "revisit_0"
					records[i].Type = contentwriterV1.RecordType_REVISIT
				}
			}
		case *contentwriterV1.WriteRequest_Cancel:
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
			s.doneBC <- true
			return nil
		}
		if err != nil {
			s.doneBC <- true
			return err
		}

		s.addBcRequest(request)

		switch v := request.Action.(type) {
		case *browsercontrollerV1.DoRequest_New:
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
		case *browsercontrollerV1.DoRequest_Notify:
		case *browsercontrollerV1.DoRequest_Completed:
		default:
			fmt.Printf("UNKNOWN REQ type %T\n", v)
		}
	}
}
