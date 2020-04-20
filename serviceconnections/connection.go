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
)

// Connections holds the clients for external grpc services
type Connections struct {
	contentWriterOptions        *ConnectionOptions
	dnsOptions                  *ConnectionOptions
	browserControllerOptions    *ConnectionOptions
	contentWriterClientConn     *grpc.ClientConn
	contentWriterClient         contentwriterV1.ContentWriterClient
	dnsResolverClientConn       *grpc.ClientConn
	dnsResolverClient           dnsresolverV1.DnsResolverClient
	browserControllerClientConn *grpc.ClientConn
	browserControllerClient     browsercontrollerV1.BrowserControllerClient
}

func NewConnections(contentWriterOptions, dnsOptions, browserControllerOptions *ConnectionOptions) *Connections {
	return &Connections{
		contentWriterOptions:     contentWriterOptions,
		dnsOptions:               dnsOptions,
		browserControllerOptions: browserControllerOptions,
	}
}

func (c *Connections) Connect() error {
	var err error

	// Set up ContentWriterClient
	c.contentWriterClientConn, err = c.contentWriterOptions.connectService()
	if err != nil {
		return err
	}
	c.contentWriterClient = contentwriterV1.NewContentWriterClient(c.contentWriterClientConn)
	log.WithField("component", "gRPC:CWR").Printf("Connected to contentwriter")

	// Set up DnsResolverClient
	c.dnsResolverClientConn, err = c.dnsOptions.connectService()
	if err != nil {
		return err
	}
	c.dnsResolverClient = dnsresolverV1.NewDnsResolverClient(c.dnsResolverClientConn)
	log.WithField("component", "gRPC:DNS").Printf("Connected to dns resolver")

	// Set up BrowserControllerClient
	c.browserControllerClientConn, err = c.browserControllerOptions.connectService()
	if err != nil {
		return err
	}
	c.browserControllerClient = browsercontrollerV1.NewBrowserControllerClient(c.browserControllerClientConn)
	log.WithField("component", "gRPC:CWR").Printf("Connected to browser controller")

	return nil
}

func (opts *ConnectionOptions) connectService() (*grpc.ClientConn, error) {
	log.WithField("component", "PROXY").Printf("Connecting %s at: %s", opts.serviceName, opts.Addr())

	dialOpts := append(opts.dialOptions,
		grpc.WithInsecure(),
		grpc.WithBlock(),
		//grpc.WithStreamInterceptor(grpc_opentracing.StreamClientInterceptor()),
		//grpc.WithUnaryInterceptor(grpc_opentracing.UnaryClientInterceptor()),
	)

	dialCtx, dialCancel := context.WithTimeout(context.Background(), opts.connectTimeout)
	defer dialCancel()

	clientConn, err := grpc.DialContext(dialCtx, opts.Addr(), dialOpts...)
	if err != nil {
		log.WithField("component", "PROXY").Errorf("fail to dial %s: %v", opts.serviceName, err)
	}
	return clientConn, err
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
