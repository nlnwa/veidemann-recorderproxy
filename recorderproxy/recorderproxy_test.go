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
	recorderProxy  *httptest.Server
	lis            *bufconn.Listener
	grpcServices   *GrpcServiceMock
	client         *http.Client
)

func bufDialer(context.Context, string) (net.Conn, error) {
	return lis.Dial()
}

type ConstantHandler string

func (h ConstantHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, string(h))
}

func init() {
	httpMux.Handle("/a", ConstantHandler("content from http server"))
	httpsMux.Handle("/b", ConstantHandler("content from https server"))
	httpsMux.Handle("/replace", ConstantHandler("should be replaced"))

	lis = bufconn.Listen(bufSize)
	s := grpc.NewServer()
	grpcServices = &GrpcServiceMock{l: &sync.Mutex{}}
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
	BrowserControllerRequests []interface{}
	DnsResolverRequests       []interface{}
	ContentWriterRequests     []interface{}
}
type test struct {
	name                      string
	url                       string
	wantStatus                int
	wantContent               string
	wantReplacedContent       string
	wantResponsePayloadDigest string
	wantRequestBlockSize      int64
	wantResponseBlockSize     int64
	wantGrpcRequests          *requests
	wantErr                   bool
}

func TestGoproxyThroughProxy(t *testing.T) {
	tests := []test{
		{
			name:                      "http",
			url:                       srvHttp.URL + "/a",
			wantStatus:                200,
			wantContent:               "content from http server",
			wantResponsePayloadDigest: "sha1:5b8b70028b3eeeb0b90971d0924447954ab98eff",
			wantRequestBlockSize:      97,
			wantResponseBlockSize:     141,
			wantErr:                   false,
		},
		{
			name:                      "https",
			url:                       srvHttps.URL + "/b",
			wantStatus:                200,
			wantContent:               "content from https server",
			wantResponsePayloadDigest: "sha1:b65da69891a174c26e9fc5a528ecc55fadf596d6",
			wantRequestBlockSize:      97,
			wantResponseBlockSize:     142,
			wantErr:                   false,
		},
		{
			name:                      "not found",
			url:                       srvHttps.URL + "/c",
			wantStatus:                404,
			wantContent:               "404 page not found\n",
			wantResponsePayloadDigest: "sha1:da3968197e7bf67aa45a77515b52ba2710c5fc34",
			wantRequestBlockSize:      97,
			wantResponseBlockSize:     176,
			wantErr:                   false,
		},
		{
			name:                      "replace",
			url:                       srvHttps.URL + "/replace",
			wantStatus:                200,
			wantContent:               "should be replaced",
			wantReplacedContent:       "replaced",
			wantResponsePayloadDigest: "sha1:cd5b9640d66ce21e6cb7542b4c82c1b04fe3a4c5",
			wantRequestBlockSize:      103,
			wantResponseBlockSize:     135,
			wantErr:                   false,
		},
	}

	for _, tt := range tests {
		tt.generateSuccessRequests()
		grpcServices.clear()

		t.Run(tt.name, func(t *testing.T) {
			statusCode, got, err := get(tt.url, client)
			if (err != nil) != tt.wantErr {
				t.Errorf("Client get() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if statusCode != tt.wantStatus {
				t.Errorf("Expected status code: %d, got %d", tt.wantStatus, statusCode)
			}
			if tt.wantReplacedContent == "" {
				if string(got) != tt.wantContent {
					t.Errorf("Expected '%s', got '%s'", tt.wantContent, got)
				}
			} else {
				if string(got) != tt.wantReplacedContent {
					t.Errorf("Expected '%s', got '%s'", tt.wantReplacedContent, got)
				}
			}
			compare(t, "DnsResolver", tt.wantGrpcRequests.DnsResolverRequests, grpcServices.requests.DnsResolverRequests)
			compare(t, "BrowserController", tt.wantGrpcRequests.BrowserControllerRequests, grpcServices.requests.BrowserControllerRequests)
			compare(t, "ContentWriter", tt.wantGrpcRequests.ContentWriterRequests, grpcServices.requests.ContentWriterRequests)
		})
	}
}

/**
 * Helper functions
 */

func (test *test) generateSuccessRequests() {
	u, _ := url.Parse(test.url)
	p, _ := strconv.Atoi(u.Port())

	var extraHeaders string
	if test.wantStatus == 404 {
		extraHeaders = "\r\nX-Content-Type-Options: nosniff"
	}

	r := &requests{}
	r.DnsResolverRequests = []interface{}{
		&dnsresolverV1.ResolveRequest{Host: u.Hostname(), Port: int32(p), CollectionRef: &configV1.ConfigRef{Kind: configV1.Kind_collection, Id: "col1"}},
	}

	r.BrowserControllerRequests = []interface{}{
		&browsercontrollerV1.DoRequest{Action: &browsercontrollerV1.DoRequest_New{New: &browsercontrollerV1.RegisterNew{Uri: test.url}}},
		&browsercontrollerV1.DoRequest{Action: &browsercontrollerV1.DoRequest_Notify{
			Notify: &browsercontrollerV1.NotifyActivity{Activity: browsercontrollerV1.NotifyActivity_ALL_DATA_RECEIVED}},
		},
		&browsercontrollerV1.DoRequest{Action: &browsercontrollerV1.DoRequest_Notify{
			Notify: &browsercontrollerV1.NotifyActivity{Activity: browsercontrollerV1.NotifyActivity_DATA_RECEIVED}},
		},
		&browsercontrollerV1.DoRequest{Action: &browsercontrollerV1.DoRequest_Notify{
			Notify: &browsercontrollerV1.NotifyActivity{Activity: browsercontrollerV1.NotifyActivity_ALL_DATA_RECEIVED}},
		},
		&browsercontrollerV1.DoRequest{Action: &browsercontrollerV1.DoRequest_Completed{
			Completed: &browsercontrollerV1.Completed{CrawlLog: &frontier.CrawlLog{
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
			}}},
		},
	}

	r.ContentWriterRequests = []interface{}{
		&contentwriterV1.WriteRequest{Value: &contentwriterV1.WriteRequest_ProtocolHeader{
			ProtocolHeader: &contentwriterV1.Data{
				RecordNum: 0, Data: []byte(fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s:%s\r\nAccept-Encoding: gzip\r\nUser-Agent: Go-http-client/1.1\r\n\r\n", u.Path, u.Hostname(), u.Port()))}},
		},
		&contentwriterV1.WriteRequest{Value: &contentwriterV1.WriteRequest_ProtocolHeader{
			ProtocolHeader: &contentwriterV1.Data{RecordNum: 1, Data: []byte(fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Length: %d\r\nContent-Type: text/plain; charset=utf-8\r\nDate: Wed, 15 May 2019 12:41:02 GMT%s\r\n\r\n", test.wantStatus, http.StatusText(test.wantStatus), len(test.wantContent), extraHeaders))}},
		},
		&contentwriterV1.WriteRequest{Value: &contentwriterV1.WriteRequest_Payload{
			Payload: &contentwriterV1.Data{RecordNum: 1, Data: []byte(test.wantContent)}},
		},
		&contentwriterV1.WriteRequest{Value: &contentwriterV1.WriteRequest_Meta{
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
			}},
		},
	}

	test.wantGrpcRequests = r
}

func compare(t *testing.T, serviceName string, want []interface{}, got []interface{}) {
	for i, r := range want {
		if i >= len(got) {
			t.Errorf("%s service received too few requests. Got %d, want %d. First missing request is:\n%v", serviceName,
				len(got), len(want), printRequest(want[len(got)]))
		} else {
			switch rt := r.(type) {
			case *contentwriterV1.WriteRequest:
				if !compareCwWriteRequest(t, i, rt, got[i].(*contentwriterV1.WriteRequest)) {
					t.Errorf("Got wrong %s request. %s request #%d was:\n%v\nWant:\n%v", serviceName, serviceName,
						i+1, printRequest(got[i]), printRequest(want[i]))
				}
			case *browsercontrollerV1.DoRequest:
				if !compareBcDoRequest(t, i, rt, got[i].(*browsercontrollerV1.DoRequest)) {
					t.Errorf("Got wrong %s request. %s request #%d was:\n%v\nWant:\n%v", serviceName, serviceName,
						i+1, printRequest(got[i]), printRequest(want[i]))
				}
			default:
				if !requestsEqual(got[i], r) {
					t.Errorf("Got wrong %s request. %s request #%d was:\n%v\nWant:\n%v", serviceName, serviceName,
						i+1, printRequest(got[i]), printRequest(want[i]))
				}
			}
		}
	}
	if len(got) > len(want) {
		t.Errorf("%s service received too many requests. Got %d, want %d. First unwanted request is:\n%v", serviceName,
			len(got), len(want), printRequest(got[len(want)]))
	}
}

var dateRe = regexp.MustCompile(`(?sm)^(.*Date: )([^\r\n]+)(.*)$`)

func compareCwWriteRequest(t *testing.T, i int, want *contentwriterV1.WriteRequest, got *contentwriterV1.WriteRequest) (ok bool) {
	switch wt := want.Value.(type) {
	case *contentwriterV1.WriteRequest_ProtocolHeader:
		wantBytes := wt.ProtocolHeader.Data
		gotBytes := got.Value.(*contentwriterV1.WriteRequest_ProtocolHeader).ProtocolHeader.Data
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
				if err != nil || wantT.Sub(gotT) > 2*time.Second {
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
		if err != nil || wantT.Sub(gotT) > 2*time.Second {
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

func compareBcDoRequest(t *testing.T, i int, want *browsercontrollerV1.DoRequest, got *browsercontrollerV1.DoRequest) (ok bool) {
	switch gt := got.Action.(type) {
	case *browsercontrollerV1.DoRequest_Completed:
		ok = false
		if checkTimePb(t, gt.Completed.GetCrawlLog().FetchTimeStamp) {
			gt.Completed.GetCrawlLog().FetchTimeStamp = nil

			// Remove block digest since we cannot calculate the right value without access to content
			if gt.Completed.GetCrawlLog().BlockDigest == "" {
				t.Errorf("Missing BlockDigest")
				return false
			}
			gt.Completed.GetCrawlLog().BlockDigest = ""

			if gt.Completed.CrawlLog.FetchTimeMs < 2000 {
				gt.Completed.CrawlLog.FetchTimeMs = 0
				if reflect.DeepEqual(want, got) {
					ok = true
				} else {
					ok = false
				}
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

func checkTimePb(t *testing.T, ts *timestamp.Timestamp) bool {
	tm, err := ptypes.Timestamp(ts)
	if err != nil {
		t.Errorf("Error converting timestamp from protobuf %v: %v\n", ts, err)
		return false
	}
	return checkTime(t, tm)
}

func checkTime(t *testing.T, ts time.Time) bool {
	wantT := time.Now()
	if wantT.Sub(ts) > 2*time.Second {
		t.Errorf("Date differs to much: Got '%v' which is %v ago\n", ts, wantT.Sub(ts))
		return false
	} else {
		return true
	}
}

func get(url string, client *http.Client) (int, []byte, error) {
	resp, err := client.Get(url)
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
	//m := &jsonpb.Marshaler{EmitDefaults: true, Indent: "  "}
	//js, _ := m.MarshalToString(req.(proto.Message))
	//return fmt.Sprintf("%30T: %v\n", req, js)
	return fmt.Sprintf("%30T: %v\n", req, req)
}

// localRecorderProxy creates a new recorderproxy which uses internal transport
func localRecorderProxy() (client *http.Client, proxy *httptest.Server) {
	conn := recorderproxy.NewConnections()
	err := conn.Connect("localhost:7777", "localhost:7778", "localhost:7779", grpc.WithContextDialer(bufDialer))
	if err != nil {
		log.Fatalf("Could not connect to services: %v", err)
	}

	rp := recorderproxy.NewRecorderProxy(0, conn)
	fmt.Printf("HTTP: %v, HTTPS: %v\n", srvHttp.URL, srvHttps.URL)
	//rp.Proxy.ConnectDial = rp.Proxy.NewConnectDialToProxy(srvHttp.URL)
	proxy = httptest.NewServer(rp.Proxy)

	proxyUrl, _ := url.Parse(proxy.URL)
	fmt.Printf("URL: %v\n", proxyUrl)
	tr := &http.Transport{TLSClientConfig: acceptAllCerts, Proxy: http.ProxyURL(proxyUrl)}
	client = &http.Client{Transport: tr}
	return
}

/**
 * Server mocks
 */
type GrpcServiceMock struct {
	l        *sync.Mutex
	requests *requests
}

func (s *GrpcServiceMock) addBcRequest(r interface{}) {
	s.l.Lock()
	s.requests.BrowserControllerRequests = append(s.requests.BrowserControllerRequests, r)
	s.l.Unlock()
}

func (s *GrpcServiceMock) addDnsRequest(r interface{}) {
	s.l.Lock()
	s.requests.DnsResolverRequests = append(s.requests.DnsResolverRequests, r)
	s.l.Unlock()
}

func (s *GrpcServiceMock) addCwRequest(r interface{}) {
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
			return server.SendAndClose(&contentwriterV1.WriteReply{
				Meta: &contentwriterV1.WriteResponseMeta{
					RecordMeta: records,
				},
			})
		}
		if err != nil {
			return err
		}

		s.addCwRequest(request)

		switch v := request.Value.(type) {
		case *contentwriterV1.WriteRequest_ProtocolHeader:
			//fmt.Printf("CW Protocol header: %v\n\n", v.ProtocolHeader)
		case *contentwriterV1.WriteRequest_Payload:
			//fmt.Printf("CW Payload: %v\n\n", v.Payload)
		case *contentwriterV1.WriteRequest_Meta:
			//fmt.Printf("CW Meta: %v\n\n", v.Meta)
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
			//fmt.Printf("BC Notify: %v\n\n", v.Notify)
		case *browsercontrollerV1.DoRequest_Completed:
			//fmt.Printf("BC Completed: %v\n\n", v.Completed)
		case *browsercontrollerV1.DoRequest_Error:
			//fmt.Printf("BC Error: %v\n\n", v.Error)
		default:
			fmt.Printf("UNKNOWN REQ type %T\n", v)
		}
	}
}
