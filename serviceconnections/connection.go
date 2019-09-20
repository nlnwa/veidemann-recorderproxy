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

package serviceconnections

import (
	"context"
	browsercontrollerV1 "github.com/nlnwa/veidemann-api-go/browsercontroller/v1"
	contentwriterV1 "github.com/nlnwa/veidemann-api-go/contentwriter/v1"
	dnsresolverV1 "github.com/nlnwa/veidemann-api-go/dnsresolver/v1"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/stats"
	"time"
)

// Connections holds the clients for external grpc services
type Connections struct {
	contentWriterHost           string
	contentWriterPort           string
	dnsResolverHost             string
	dnsResolverPort             string
	browserControllerHost       string
	browserControllerPort       string
	contentWriterClientConn     *grpc.ClientConn
	contentWriterClient         contentwriterV1.ContentWriterClient
	dnsResolverClientConn       *grpc.ClientConn
	dnsResolverClient           dnsresolverV1.DnsResolverClient
	browserControllerClientConn *grpc.ClientConn
	browserControllerClient     browsercontrollerV1.BrowserControllerClient
	StatsHandlerFactory         func(serviceName string) stats.Handler
}

func NewConnections() *Connections {
	return &Connections{}
}

func (c *Connections) Connect(contentWriterHost, contentWriterPort, dnsResolverHost, dnsResolverPort, browserControllerHost, browserControllerPort string, connectTimeout time.Duration, opts ...grpc.DialOption) error {
	c.contentWriterHost = contentWriterHost
	c.contentWriterPort = contentWriterPort
	contentWriterAddr := contentWriterHost + ":" + contentWriterPort

	c.dnsResolverHost = dnsResolverHost
	c.dnsResolverPort = dnsResolverPort
	dnsResolverAddr := dnsResolverHost + ":" + dnsResolverPort

	c.browserControllerHost = browserControllerHost
	c.browserControllerPort = browserControllerPort
	browserControllerAddr := browserControllerHost + ":" + browserControllerPort

	log.Printf("Proxy is using contentwriter at: %s, dns resolver at: %s and browser controller at: %s", contentWriterAddr, dnsResolverAddr, browserControllerAddr)

	opts = append(opts,
		grpc.WithInsecure(),
		grpc.WithBlock(),
		//grpc.WithStreamInterceptor(grpc_opentracing.StreamClientInterceptor()),
		//grpc.WithUnaryInterceptor(grpc_opentracing.UnaryClientInterceptor()),
	)

	dialCtx, dialCancel := context.WithTimeout(context.Background(), connectTimeout)
	defer dialCancel()

	// Set up ContentWriterClient
	clientConn, err := grpc.DialContext(dialCtx, contentWriterAddr, c.addStatsHandler("ContentWriter", opts)...)
	if err != nil {
		log.Errorf("fail to dial contentwriter at %v: %v", contentWriterAddr, err)
		return err
	}
	c.contentWriterClientConn = clientConn
	c.contentWriterClient = contentwriterV1.NewContentWriterClient(clientConn)

	log.Printf("Connected to contentwriter")

	// Set up DnsResolverClient
	clientConn, err = grpc.DialContext(dialCtx, dnsResolverAddr, c.addStatsHandler("DNSResolver", opts)...)
	if err != nil {
		log.Errorf("fail to dial dns resolver at %v: %v", dnsResolverAddr, err)
		return err
	}
	c.dnsResolverClientConn = clientConn
	c.dnsResolverClient = dnsresolverV1.NewDnsResolverClient(clientConn)

	log.Printf("Connected to dns resolver")

	// Set up BrowserControllerClient
	clientConn, err = grpc.DialContext(dialCtx, browserControllerAddr, c.addStatsHandler("BrowserController", opts)...)
	if err != nil {
		log.Errorf("fail to dial browser controller at %v: %v", browserControllerAddr, err)
		return err
	}
	c.browserControllerClientConn = clientConn
	c.browserControllerClient = browsercontrollerV1.NewBrowserControllerClient(clientConn)

	log.Printf("Connected to browser controller")

	return nil
}

func (c *Connections) addStatsHandler(serviceName string, opts []grpc.DialOption) []grpc.DialOption {
	if c.StatsHandlerFactory != nil {
		return append(opts, grpc.WithStatsHandler(c.StatsHandlerFactory(serviceName)))
	}
	return opts
}

func (c *Connections) Close() {
	_ = c.contentWriterClientConn.Close()
	_ = c.dnsResolverClientConn.Close()
	_ = c.browserControllerClientConn.Close()
}

func (c *Connections) ContentWriterClient() contentwriterV1.ContentWriterClient {
	return c.contentWriterClient
}

func (c *Connections) DnsResolverClient() dnsresolverV1.DnsResolverClient {
	return c.dnsResolverClient
}

func (c *Connections) BrowserControllerClient() browsercontrollerV1.BrowserControllerClient {
	return c.browserControllerClient
}
