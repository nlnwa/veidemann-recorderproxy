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
	"crypto/sha1"
	"fmt"
	"github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_ctxtags "github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/grpc-ecosystem/go-grpc-middleware/tracing/opentracing"
	browsercontrollerV1 "github.com/nlnwa/veidemann-api-go/browsercontroller/v1"
	configV1 "github.com/nlnwa/veidemann-api-go/config/v1"
	contentwriterV1 "github.com/nlnwa/veidemann-api-go/contentwriter/v1"
	dnsresolverV1 "github.com/nlnwa/veidemann-api-go/dnsresolver/v1"
	"github.com/nlnwa/veidemann-recorderproxy/recorderproxy"
	"github.com/nlnwa/veidemann-recorderproxy/tracing"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"hash"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const bufSize = 1024 * 1024

/**
 * Server mocks
 */
type GrpcServiceMock struct {
	lis *bufconn.Listener
	l   *sync.Mutex
	//requests *requests
	doneBC        chan bool
	doneCW        chan bool
	contextDialer grpc.DialOption
}

func NewGrpcServiceMock() *GrpcServiceMock {
	tracer, _ := tracing.Init("Service Mocks")
	//tracer, closer := tracing.Init("Service Mocks")
	//defer closer.Close()

	m := &GrpcServiceMock{
		lis: bufconn.Listen(bufSize),
		l:   &sync.Mutex{},
	}

	m.contextDialer = grpc.WithContextDialer(m.bufDialer)

	s := grpc.NewServer(
		grpc.StreamInterceptor(grpc_middleware.ChainStreamServer(
			grpc_ctxtags.StreamServerInterceptor(grpc_ctxtags.WithFieldExtractor(grpc_ctxtags.CodeGenRequestFieldExtractor)),
			grpc_opentracing.StreamServerInterceptor(grpc_opentracing.WithTracer(tracer)),
		)),
		grpc.UnaryInterceptor(grpc_middleware.ChainUnaryServer(
			grpc_ctxtags.UnaryServerInterceptor(grpc_ctxtags.WithFieldExtractor(grpc_ctxtags.CodeGenRequestFieldExtractor)),
			grpc_opentracing.UnaryServerInterceptor(grpc_opentracing.WithTracer(tracer)),
		)),
	)

	dnsresolverV1.RegisterDnsResolverServer(s, m)
	contentwriterV1.RegisterContentWriterServer(s, m)
	browsercontrollerV1.RegisterBrowserControllerServer(s, m)
	go func() {
		if err := s.Serve(m.lis); err != nil {
			log.Fatalf("Server exited with error: %v", err)
		}
	}()

	return m
}

func (s *GrpcServiceMock) bufDialer(context.Context, string) (net.Conn, error) {
	return s.lis.Dial()
}

func (s *GrpcServiceMock) addBcRequest(r *browsercontrollerV1.DoRequest) {
	s.l.Lock()

	recorderproxy.LogWithComponent("MOCK:BrowserController").Print(r)

	//s.requests.BrowserControllerRequests = append(s.requests.BrowserControllerRequests, r)
	s.l.Unlock()
}

func (s *GrpcServiceMock) addDnsRequest(r *dnsresolverV1.ResolveRequest) {
	s.l.Lock()

	recorderproxy.LogWithComponent("MOCK:DNSResolver").Print(r)

	//s.requests.DnsResolverRequests = append(s.requests.DnsResolverRequests, r)
	s.l.Unlock()
}

func (s *GrpcServiceMock) addCwRequest(r *contentwriterV1.WriteRequest) {
	s.l.Lock()

	switch v := r.Value.(type) {
	case *contentwriterV1.WriteRequest_Payload:
		recorderproxy.LogWithComponent("MOCK:ContentWriter").
			Printf("payload:<record_num:%d data:\"%s... (%d bytes)\" >\n",
				v.Payload.RecordNum, v.Payload.Data[0:5], len(v.Payload.Data))

	default:
		recorderproxy.LogWithComponent("MOCK:ContentWriter").Print(r)
	}

	//s.requests.ContentWriterRequests = append(s.requests.ContentWriterRequests, r)
	s.l.Unlock()
}

func (s *GrpcServiceMock) clear() {
	//s.requests = &requests{}
}

//Implements DNS service
func (s *GrpcServiceMock) Resolve(ctx context.Context, in *dnsresolverV1.ResolveRequest) (*dnsresolverV1.ResolveReply, error) {
	s.addDnsRequest(in)

	ips, err := net.LookupIP(in.Host)
	if err == nil {
		for _, ip := range ips {
			if ip.To4() != nil {
				out := &dnsresolverV1.ResolveReply{
					Host:      in.Host,
					Port:      in.Port,
					TextualIp: ip.To4().String(),
					RawIp:     ip,
				}
				return out, nil
			}
		}
	}
	fmt.Fprintf(os.Stderr, "Could not get IP: %v\n", err)
	return nil, err
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

			if strings.HasSuffix(v.New.Uri, "bccerr") {
				return fmt.Errorf("browser controller error")
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