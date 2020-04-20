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
	"google.golang.org/grpc"
	"time"
)

// connectionOptions configure a connection. connectionOptions are set by the ConnectionOption
// values passed to NewConnectionOptions.
type ConnectionOptions struct {
	serviceName    string
	host           string
	port           string
	connectTimeout time.Duration
	dialOptions    []grpc.DialOption
}

func (c *ConnectionOptions) Addr() string {
	return c.host + ":" + c.port
}

// ConnectionOption configures how to connect to a service.
type ConnectionOption interface {
	apply(*ConnectionOptions)
}

func NewConnectionOptions(serviceName string, opts ...ConnectionOption) *ConnectionOptions {
	p := defaultConnectionOptions(serviceName)
	for _, opt := range opts {
		opt.apply(&p)
	}
	return &p
}

// EmptyConnectionOption does not alter the configuration. It can be embedded in
// another structure to build custom connection options.
type EmptyConnectionOption struct{}

func (EmptyConnectionOption) apply(*ConnectionOptions) {}

// funcConnectionOption wraps a function that modifies connectionOptions into an
// implementation of the ConnectionOption interface.
type funcConnectionOption struct {
	f func(*ConnectionOptions)
}

func (fco *funcConnectionOption) apply(po *ConnectionOptions) {
	fco.f(po)
}

func newFuncConnectionOption(f func(*ConnectionOptions)) *funcConnectionOption {
	return &funcConnectionOption{
		f: f,
	}
}

func defaultConnectionOptions(serviceName string) ConnectionOptions {
	return ConnectionOptions{serviceName: serviceName}
}

func WithHost(host string) ConnectionOption {
	return newFuncConnectionOption(func(c *ConnectionOptions) {
		c.host = host
	})
}

func WithPort(port string) ConnectionOption {
	return newFuncConnectionOption(func(c *ConnectionOptions) {
		c.port = port
	})
}

func WithDialOptions(dialOption ...grpc.DialOption) ConnectionOption {
	return newFuncConnectionOption(func(c *ConnectionOptions) {
		c.dialOptions = dialOption
	})
}

func WithConnectTimeout(connectTimeout time.Duration) ConnectionOption {
	return newFuncConnectionOption(func(c *ConnectionOptions) {
		c.connectTimeout = connectTimeout
	})
}
