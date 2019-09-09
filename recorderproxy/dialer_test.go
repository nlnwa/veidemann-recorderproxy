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

//var (
//	acceptAllCerts  = &tls.Config{InsecureSkipVerify: true}
//	httpsMux        = http.NewServeMux()
//	httpMux         = http.NewServeMux()
//	srvHttps        = httptest.NewTLSServer(httpsMux)
//	srvHttpsBadCert = httptest.NewTLSServer(httpsMux)
//	srvHttp         = httptest.NewServer(httpMux)
//	recorderProxy   *recorderproxy.RecorderProxy
//	lis             *bufconn.Listener
//	grpcServices    *GrpcServiceMock
//	client          *http.Client
//	secondProxy     *httptest.Server
//)
//
//func init() {
//	httpMux.Handle("/a", ConstantHandler("content from http server"))
//	httpsMux.Handle("/b", ConstantHandler("content from https server"))
//	httpMux.Handle("/replace", ConstantHandler("should be replaced"))
//	httpsMux.Handle("/replace", ConstantHandler("should be replaced"))
//	httpMux.Handle("/slow", ConstantSlowHandler("content from http server"))
//	httpsMux.Handle("/slow", ConstantSlowHandler("content from https server"))
//	httpMux.Handle("/cancel", ConstantSlowHandler("content from http server"))
//	httpsMux.Handle("/cancel", ConstantSlowHandler("content from https server"))
//	httpMux.Handle("/blocked", ConstantSlowHandler("content from http server"))
//	httpsMux.Handle("/blocked", ConstantSlowHandler("content from https server"))
//	httpMux.Handle("/bccerr", ConstantHandler("content from http server"))
//	httpsMux.Handle("/bccerr", ConstantHandler("content from https server"))
//	httpMux.Handle("/cwerr", ConstantHandler("content from http server"))
//	httpsMux.Handle("/cwerr", ConstantHandler("content from https server"))
//	httpMux.Handle("/cached", ConstantCacheHandler("content from http server"))
//	httpsMux.Handle("/cached", ConstantCacheHandler("content from https server"))
//
//	client, recorderProxy = localRecorderProxy()
//}
//
//type dns_test struct {
//	name                    string
//	url                     string
//	wantErr                 bool
//}
//
//func TestDnsDialer(t *testing.T) {
//	tests := []dns_test{
//		{
//			name:                    "http:success",
//			url:                     srvHttp.URL + "/a",
//			wantErr:                 false,
//		},
//	}
//
//	for _, tt := range tests {
//		t.Run(tt.name, func(t *testing.T) {
//			statusCode, got, err := get(tt.url, client, tt.clientTimeout)
//			if grpcServices.doneBC != nil {
//				<-grpcServices.doneBC
//			}
//			if grpcServices.doneCW != nil {
//				<-grpcServices.doneCW
//			}
//
//			if (err != nil) != tt.wantErr {
//				t.Errorf("Client get() error = %v, wantErr %v (%v, %s)", err, tt.wantErr, statusCode, got)
//				return
//			}
//			if statusCode != tt.wantStatus {
//				t.Errorf("Expected status code: %d, got %d", tt.wantStatus, statusCode)
//				return
//			}
//			if tt.wantReplacedContent == "" {
//				if string(got) != tt.wantContent {
//					t.Errorf("Expected '%s', got '%s'", tt.wantContent, got)
//					return
//				}
//			} else {
//				if string(got) != tt.wantReplacedContent {
//					t.Errorf("Expected '%s', got '%s'", tt.wantReplacedContent, got)
//					return
//				}
//			}
//			compareDNS(t, "DnsResolver", tt, tt.wantGrpcRequests.DnsResolverRequests, grpcServices.requests.DnsResolverRequests)
//			compareBC(t, "BrowserController", tt, tt.wantGrpcRequests.BrowserControllerRequests, grpcServices.requests.BrowserControllerRequests)
//			compareCW(t, "ContentWriter", tt, tt.wantGrpcRequests.ContentWriterRequests, grpcServices.requests.ContentWriterRequests)
//		})
//	}
//
//	srvHttp.Close()
//	srvHttps.Close()
//}
