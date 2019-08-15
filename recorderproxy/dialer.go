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
	"crypto/tls"
	"net"
)

type dnsResolverDialer struct {
	*net.Dialer
	dnsIp     string
	dnsDialer *net.Dialer
}

func NewDnsResolverDialer(dnsResolverHost string, dnsResolverPort string) (dialer *dnsResolverDialer, err error) {
	dialer = &dnsResolverDialer{
		Dialer: new(net.Dialer),
	}

	dnsIps, err := net.LookupIP(dnsResolverHost)
	if err != nil {
		return nil, err
	}

	if len(dnsIps) > 0 {
		dialer.dnsIp = dnsIps[0].String() + ":53"
	}

	dialer.dnsDialer = new(net.Dialer)

	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (conn net.Conn, e error) {
			c, e := dialer.dnsDialer.DialContext(ctx, network, dialer.dnsIp)
			//return &dnsConn{c}, e
			return c, e
		},
	}

	dialer.Resolver = resolver

	return dialer, nil
}

// DialTls connects to the given network address
// and then initiates a TLS handshake, returning the resulting
// TLS connection.
func (d *dnsResolverDialer) DialTls(addr string) (*tls.Conn, error) {
	return tls.DialWithDialer(d.Dialer, "tcp", addr, &tls.Config{InsecureSkipVerify: true})
}
