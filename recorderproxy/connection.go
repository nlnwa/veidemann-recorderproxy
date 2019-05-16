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
	"context"
	browsercontrollerV1 "github.com/nlnwa/veidemann-api-go/browsercontroller/v1"
	contentwriterV1 "github.com/nlnwa/veidemann-api-go/contentwriter/v1"
	dnsresolverV1 "github.com/nlnwa/veidemann-api-go/dnsresolver/v1"
	"google.golang.org/grpc"
	"log"
	"time"
)

type Connections interface {
	Connect(contentWriterAddr string, dnsResolverAddr string, browserControllerAddr string, opts ...grpc.DialOption) error
	Close()
	ContentWriterClient() contentwriterV1.ContentWriterClient
	DnsResolverClient() dnsresolverV1.DnsResolverClient
	BrowserControllerClient() browsercontrollerV1.BrowserControllerClient
}

// Connections holds the clients for external grpc services
type connections struct {
	contentWriterClientConn     *grpc.ClientConn
	contentWriterClient         contentwriterV1.ContentWriterClient
	dnsResolverClientConn       *grpc.ClientConn
	dnsResolverClient           dnsresolverV1.DnsResolverClient
	browserControllerClientConn *grpc.ClientConn
	browserControllerClient     browsercontrollerV1.BrowserControllerClient
}

func NewConnections() *connections {
	return &connections{}
}

func (c *connections) Connect(contentWriterAddr string, dnsResolverAddr string, browserControllerAddr string, opts ...grpc.DialOption) error {
	opts = append(opts, grpc.WithInsecure(), grpc.WithBlock())

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer dialCancel()

	// Set up ContentWriterClient
	clientConn, err := grpc.DialContext(dialCtx, contentWriterAddr, opts...)
	if err != nil {
		log.Fatalf("fail to dial contentwriter at %v: %v", contentWriterAddr, err)
		return err
	}
	c.contentWriterClientConn = clientConn
	c.contentWriterClient = contentwriterV1.NewContentWriterClient(clientConn)

	log.Printf("Proxy is using contentwriter at: %s", contentWriterAddr)

	// Set up DnsResolverClient
	clientConn, err = grpc.DialContext(dialCtx, dnsResolverAddr, opts...)
	if err != nil {
		log.Fatalf("fail to dial dns resolver at %v: %v", dnsResolverAddr, err)
		return err
	}
	c.dnsResolverClientConn = clientConn
	c.dnsResolverClient = dnsresolverV1.NewDnsResolverClient(clientConn)

	log.Printf("Proxy is using dns resolver at: %s", dnsResolverAddr)

	// Set up BrowserControllerClient
	clientConn, err = grpc.DialContext(dialCtx, browserControllerAddr, opts...)
	if err != nil {
		log.Fatalf("fail to dial browser controller at %v: %v", browserControllerAddr, err)
		return err
	}
	c.browserControllerClientConn = clientConn
	c.browserControllerClient = browsercontrollerV1.NewBrowserControllerClient(clientConn)

	log.Printf("Proxy is using browser controller at: %s", browserControllerAddr)

	return nil
}

func (c *connections) Close() {
	_ = c.contentWriterClientConn.Close()
	_ = c.dnsResolverClientConn.Close()
	_ = c.browserControllerClientConn.Close()
}

func (c *connections) ContentWriterClient() contentwriterV1.ContentWriterClient {
	return c.contentWriterClient
}

func (c *connections) DnsResolverClient() dnsresolverV1.DnsResolverClient {
	return c.dnsResolverClient
}

func (c *connections) BrowserControllerClient() browsercontrollerV1.BrowserControllerClient {
	return c.browserControllerClient
}
