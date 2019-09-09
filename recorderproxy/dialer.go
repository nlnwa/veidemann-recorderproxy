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
	"fmt"
	"net"
)

type dnsConn struct {
	net.Conn
}

// Read implements the Conn Read method.
func (c *dnsConn) Read(b []byte) (int, error) {
	n, e := c.Conn.Read(b)
	fmt.Printf("DNS READ %v, %s, %v\n", n, b, e)
	//if e != nil {
	//	return n, e
	//}
	//
	//p := dnsmessage.Parser{}
	//h, e2 := p.Start(b)
	//fmt.Printf("DNS READ HEAD %v, %s, %v\n", h, h.GoString(), e2)
	//
	//
	//var gotIPs []net.IP
	//for {
	//	h, err := p.AnswerHeader()
	//	if err == dnsmessage.ErrSectionDone {
	//		break
	//	}
	//	if err != nil {
	//		panic(err)
	//	}
	//
	//	if (h.Type != dnsmessage.TypeA && h.Type != dnsmessage.TypeAAAA) || h.Class != dnsmessage.ClassINET {
	//		continue
	//	}
	//
	//	//if !strings.EqualFold(h.Name.String(), wantName) {
	//	//	if err := p.SkipAnswer(); err != nil {
	//	//		panic(err)
	//	//	}
	//	//	continue
	//	//}
	//
	//	switch h.Type {
	//	case dnsmessage.TypeA:
	//		r, err := p.AResource()
	//		if err != nil {
	//			panic(err)
	//		}
	//		gotIPs = append(gotIPs, r.A[:])
	//	case dnsmessage.TypeAAAA:
	//		r, err := p.AAAAResource()
	//		if err != nil {
	//			panic(err)
	//		}
	//		gotIPs = append(gotIPs, r.AAAA[:])
	//	}
	//}
	//
	//fmt.Printf("Found A/AAAA records for name s: %v\n", gotIPs)

	return n, e
}

// Write implements the Conn Write method.
func (c *dnsConn) Write(b []byte) (int, error) {
	n, e := c.Conn.Write(b)
	fmt.Printf("DNS WRITE %v, %s, %v\n", n, b, e)
	//if e != nil {
	//	return n, e
	//}
	//
	//p := dnsmessage.Parser{}
	//h, e2 := p.Start(b)
	//fmt.Printf("DNS WRITE HEAD %v, %s, %v\n", h, h.GoString(), e2)
	//
	//for {
	//	q, err := p.Question()
	//	if err == dnsmessage.ErrSectionDone {
	//		break
	//	}
	//	if err != nil {
	//		panic(err)
	//	}
	//
	//	//if q.Name.String() != wantName {
	//	//	continue
	//	//}
	//
	//	fmt.Println("Found question for name", q.Name.String())
	//	//if err := p.SkipAllQuestions(); err != nil {
	//	//	panic(err)
	//	//}
	//	//break
	//}

	return n, e
}

//// Close closes the connection.
//func (c *dnsConn) Close() error {
//	return nil
//}
//
//// LocalAddr returns the local network address.
//// The Addr returned is shared by all invocations of LocalAddr, so
//// do not modify it.
//func (c *dnsConn) LocalAddr() net.Addr {
//	return nil
//}
//
//// RemoteAddr returns the remote network address.
//// The Addr returned is shared by all invocations of RemoteAddr, so
//// do not modify it.
//func (c *dnsConn) RemoteAddr() net.Addr {
//	return nil
//}
//
//// SetDeadline implements the Conn SetDeadline method.
//func (c *dnsConn) SetDeadline(t time.Time) error {
//	return nil
//}
//
//// SetReadDeadline implements the Conn SetReadDeadline method.
//func (c *dnsConn) SetReadDeadline(t time.Time) error {
//	return nil
//}
//
//// SetWriteDeadline implements the Conn SetWriteDeadline method.
//func (c *dnsConn) SetWriteDeadline(t time.Time) error {
//	return nil
//}

//resolver.LookupIPAddr = func(ctx context.Context, host string) (addrs []net.IPAddr, e error) {
//	if host == "" {
//		return nil, &net.DNSError{Err: errors.New("no such host").Error(), Name: host}
//	}
//
//	dnsReq := &dnsresolver.ResolveRequest{
//		//CollectionRef: collectionRef,
//		CollectionRef: &config2.ConfigRef{},
//		Host:          host,
//	}
//	dnsResp, err := proxy.conn.DnsResolverClient().Resolve(ctx, dnsReq)
//	if err != nil {
//		log.Fatalf("Error looking up %v, cause: %v", host, err)
//		return nil, err
//	}
//
//	addrs = append(addrs, net.IPAddr{IP:dnsResp.RawIp})
//
//	return addrs, nil
//}

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
			return &dnsConn{c}, e
			//return c, e
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
