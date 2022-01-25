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

package testutil

import (
	"context"
	"crypto/sha1"
	"fmt"
	"github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_ctxtags "github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/grpc-ecosystem/go-grpc-middleware/tracing/opentracing"
	browsercontrollerV1 "github.com/nlnwa/veidemann-api/go/browsercontroller/v1"
	configV1 "github.com/nlnwa/veidemann-api/go/config/v1"
	contentwriterV1 "github.com/nlnwa/veidemann-api/go/contentwriter/v1"
	dnsresolverV1 "github.com/nlnwa/veidemann-api/go/dnsresolver/v1"
	"github.com/nlnwa/veidemann-recorderproxy/logger"
	"github.com/nlnwa/veidemann-recorderproxy/serviceconnections"
	"github.com/nlnwa/veidemann-recorderproxy/tracing"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"hash"
	"io"
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
	dnsresolverV1.UnimplementedDnsResolverServer
	contentwriterV1.UnimplementedContentWriterServer
	browsercontrollerV1.UnimplementedBrowserControllerServer
	dnsOpts               *serviceconnections.ConnectionOptions
	contentWriterOpts     *serviceconnections.ConnectionOptions
	browserControllerOpts *serviceconnections.ConnectionOptions
	lis                   *bufconn.Listener
	l                     *sync.Mutex
	Requests              *Requests
	DoneBC                chan bool
	DoneCW                chan bool
	contextDialer         grpc.DialOption
	Server                *grpc.Server
	ClientConn            *serviceconnections.Connections
}

// ConnectionOption configures how we parse a URL.
type MockOption interface {
	apply(*GrpcServiceMock)
}

// funcMockOption wraps a function that modifies GrpcServiceMock into an
// implementation of the MockOption interface.
type funcMockOption struct {
	f func(*GrpcServiceMock)
}

func (fmo *funcMockOption) apply(mock *GrpcServiceMock) {
	fmo.f(mock)
}

func newFuncMockOption(f func(*GrpcServiceMock)) *funcMockOption {
	return &funcMockOption{
		f: f,
	}
}

func WithExternalBrowserController(option *serviceconnections.ConnectionOptions) MockOption {
	return newFuncMockOption(func(c *GrpcServiceMock) {
		c.browserControllerOpts = option
	})
}

func WithExternalContentWriter(option *serviceconnections.ConnectionOptions) MockOption {
	return newFuncMockOption(func(c *GrpcServiceMock) {
		c.contentWriterOpts = option
	})
}

func WithExternalDns(option *serviceconnections.ConnectionOptions) MockOption {
	return newFuncMockOption(func(c *GrpcServiceMock) {
		c.dnsOpts = option
	})
}

type Requests struct {
	BrowserControllerRequests []*browsercontrollerV1.DoRequest
	DnsResolverRequests       []*dnsresolverV1.ResolveRequest
	ContentWriterRequests     []*contentwriterV1.WriteRequest
}

func NewGrpcServiceMock(opts ...MockOption) *GrpcServiceMock {
	tracer, _ := tracing.Init("Service Mocks")

	m := &GrpcServiceMock{
		lis:      bufconn.Listen(bufSize),
		l:        &sync.Mutex{},
		Requests: &Requests{},
	}

	for _, opt := range opts {
		opt.apply(m)
	}

	m.contextDialer = grpc.WithContextDialer(m.bufDialer)

	m.Server = grpc.NewServer(
		grpc.StreamInterceptor(grpc_middleware.ChainStreamServer(
			grpc_ctxtags.StreamServerInterceptor(grpc_ctxtags.WithFieldExtractor(grpc_ctxtags.CodeGenRequestFieldExtractor)),
			grpc_opentracing.StreamServerInterceptor(grpc_opentracing.WithTracer(tracer)),
		)),
		grpc.UnaryInterceptor(grpc_middleware.ChainUnaryServer(
			grpc_ctxtags.UnaryServerInterceptor(grpc_ctxtags.WithFieldExtractor(grpc_ctxtags.CodeGenRequestFieldExtractor)),
			grpc_opentracing.UnaryServerInterceptor(grpc_opentracing.WithTracer(tracer)),
		)),
	)

	if m.dnsOpts == nil {
		dnsresolverV1.RegisterDnsResolverServer(m.Server, m)
	}
	if m.contentWriterOpts == nil {
		contentwriterV1.RegisterContentWriterServer(m.Server, m)
	}
	if m.browserControllerOpts == nil {
		browsercontrollerV1.RegisterBrowserControllerServer(m.Server, m)
	}
	go func() {
		if err := m.Server.Serve(m.lis); err != nil {
			log.Fatalf("Server exited with error: %v", err)
		}
	}()

	dialOption := grpc.WithContextDialer(m.bufDialer)

	if m.contentWriterOpts == nil {
		m.contentWriterOpts = serviceconnections.NewConnectionOptions(
			"ContentWriter",
			serviceconnections.WithConnectTimeout(1*time.Minute),
			serviceconnections.WithDialOptions(dialOption, tracing.NewStatsHandler("ContentWriter", log.DebugLevel)),
		)
	}
	if m.dnsOpts == nil {
		m.dnsOpts = serviceconnections.NewConnectionOptions(
			"DnsService",
			serviceconnections.WithConnectTimeout(1*time.Minute),
			serviceconnections.WithDialOptions(dialOption, tracing.NewStatsHandler("DnsService", log.DebugLevel)),
		)
	}
	if m.browserControllerOpts == nil {
		m.browserControllerOpts = serviceconnections.NewConnectionOptions(
			"BrowserController",
			serviceconnections.WithConnectTimeout(1*time.Minute),
			serviceconnections.WithDialOptions(dialOption, tracing.NewStatsHandler("BrowserController", log.DebugLevel)),
		)
	}

	m.ClientConn = serviceconnections.NewConnections(m.contentWriterOpts, m.dnsOpts, m.browserControllerOpts)

	err := m.ClientConn.Connect()
	if err != nil {
		log.Panicf("Could not connect to services: %v", err)
	}

	return m
}

func (s *GrpcServiceMock) Close() {
	s.ClientConn.Close()
	s.Server.GracefulStop()
	s.lis.Close()
}

func (s *GrpcServiceMock) Clear() {
	s.Requests = &Requests{}
}

func (s *GrpcServiceMock) bufDialer(context.Context, string) (net.Conn, error) {
	return s.lis.Dial()
}

func (s *GrpcServiceMock) addBcRequest(r *browsercontrollerV1.DoRequest) {
	s.l.Lock()

	logger.LogWithComponent("MOCK:BrowserController").Print(r)

	s.Requests.BrowserControllerRequests = append(s.Requests.BrowserControllerRequests, r)
	s.l.Unlock()
}

func (s *GrpcServiceMock) addDnsRequest(r *dnsresolverV1.ResolveRequest) {
	s.l.Lock()

	logger.LogWithComponent("MOCK:DNSResolver").Print(r)

	s.Requests.DnsResolverRequests = append(s.Requests.DnsResolverRequests, r)
	s.l.Unlock()
}

func (s *GrpcServiceMock) addCwRequest(r *contentwriterV1.WriteRequest) {
	s.l.Lock()

	switch v := r.Value.(type) {
	case *contentwriterV1.WriteRequest_Payload:
		logger.LogWithComponent("MOCK:ContentWriter").
			Printf("payload:<record_num:%d data:\"%s... (%d bytes)\" >\n",
				v.Payload.RecordNum, v.Payload.Data[0:5], len(v.Payload.Data))

	default:
		logger.LogWithComponent("MOCK:ContentWriter").Print(r)
	}

	s.Requests.ContentWriterRequests = append(s.Requests.ContentWriterRequests, r)
	s.l.Unlock()
}

func (s *GrpcServiceMock) clear() {
	s.Requests = &Requests{}
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
	records := map[int32]*contentwriterV1.WriteResponseMeta_RecordMeta{}
	data := make(map[int32][]byte)
	size := make(map[int32]int64)
	gotMeta := false
	gotCancel := false
	blockDigest := make(map[int32]hash.Hash)

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

		if s.DoneCW == nil {
			s.DoneCW = make(chan bool, 200)
			go func() {
				<-server.Context().Done()
				s.DoneCW <- true
				s.DoneCW = nil
			}()
		}

		s.addCwRequest(request)

		switch v := request.Value.(type) {
		case *contentwriterV1.WriteRequest_ProtocolHeader:
			size[v.ProtocolHeader.RecordNum] = int64(len(v.ProtocolHeader.Data))
			blockDigest[v.ProtocolHeader.RecordNum] = sha1.New()
			blockDigest[v.ProtocolHeader.RecordNum].Write(v.ProtocolHeader.Data)
			data[v.ProtocolHeader.RecordNum] = v.ProtocolHeader.Data
		case *contentwriterV1.WriteRequest_Payload:
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

		if s.DoneBC == nil {
			s.DoneBC = make(chan bool, 200)
			go func() {
				<-server.Context().Done()
				s.DoneBC <- true
				s.DoneBC = nil
			}()
		}

		s.addBcRequest(request)

		switch v := request.Action.(type) {
		case *browsercontrollerV1.DoRequest_New:

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
