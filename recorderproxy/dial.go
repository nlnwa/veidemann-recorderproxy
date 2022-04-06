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
	"bufio"
	"context"
	"fmt"
	"github.com/nlnwa/veidemann-recorderproxy/errors"
	"github.com/nlnwa/veidemann-recorderproxy/filters"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/test/bufconn"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (proxy *RecorderProxy) Dial(context context.Context, cs *filters.ConnectionState, isConnect bool, network, addr string) (conn net.Conn, err error) {
	log := cs.LogWithContext("Dialer")
	log.Debugf("dial upstream %v, is connect request: %v\n", addr, isConnect)
	timeout := 30 * time.Second
	deadline, hasDeadline := context.Deadline()
	if hasDeadline {
		timeout = deadline.Sub(time.Now())
	}
	if proxy.nextProxy != "" {
		conn, err = net.DialTimeout(network, proxy.nextProxy, timeout)
		if err != nil {
			log.Errorf("Could not dial next proxy at %v: %v\n", proxy.nextProxy, err)
			cs.ConnectErr = err
			return conn, nil
		}
	} else {
		conn, err = net.DialTimeout(network, addr, timeout)
		if err != nil {
			log.Errorf("Could not dial %v: %v\n", addr, err)
			cs.ConnectErr = err
			if isConnect {
				l := bufconn.Listen(0)
				go func() {
					c, _ := l.Accept()
					c.Close()
				}()
				conn, _ = l.Dial()
				conn = WrapConn(conn, "fake", true)
				return conn, nil
			} else {
				return conn, err
			}
		}
	}

	if logrus.IsLevelEnabled(logrus.DebugLevel) {
		conn = WrapConn(conn, "up", true)
	}

	if isConnect && proxy.nextProxy != "" {
		req := NewConnectReq(cs.Uri.Host)
		log.Debugf("sending CONNECT for host %v to upstream proxy", req.URL)
		err = req.Write(conn)
		if err != nil {
			log.WithError(err).Warn("error while writing CONNECT request to upstream proxy")
			return
		}
		r := bufio.NewReader(conn)
		var resp *http.Response
		resp, err = http.ReadResponse(r, req)
		if err != nil {
			log.WithError(err).Warn("error while reading CONNECT response from upstream proxy")
			return
		}
		log.Debugf("response status from CONNECT request to upstream proxy was: %v", resp.Status)

		squidErr := resp.Header.Get("X-Squid-Error")
		if squidErr != "" {
			err := handleSquidErrorString(squidErr)
			if err != nil {
				cs.ConnectErr = err
				err = nil
			}
			return conn, err
		}

		if resp.StatusCode != 200 {
			cs.ConnectErr = errors.Error(errors.RuntimeException,
				fmt.Sprintf("could not connect too upstream proxy (%d)", resp.StatusCode), squidErr)
			return conn, nil
		}
	}
	return conn, err
}

func NewConnectReq(addr string) *http.Request {
	req := new(http.Request)
	req.Method = "CONNECT"
	req.RequestURI = addr
	req.Proto = "HTTP/1.1"
	rawurl := req.RequestURI
	var ok bool
	if req.ProtoMajor, req.ProtoMinor, ok = http.ParseHTTPVersion(req.Proto); !ok {
		fmt.Printf("malformed HTTP version: %v\n", req.Proto)
	}

	// CONNECT requests are used two different ways, and neither uses a full URL:
	// The standard use is to tunnel HTTPS through an HTTP proxy.
	// It looks like "CONNECT www.google.com:443 HTTP/1.1", and the parameter is
	// just the authority section of a URL. This information should go in req.URL.Host.
	//
	// The net/rpc package also uses CONNECT, but there the parameter is a path
	// that starts with a slash. It can be parsed with the regular URL parser,
	// and the path will end up in req.URL.Path, where it needs to be in order for
	// RPC to work.
	justAuthority := req.Method == "CONNECT" && !strings.HasPrefix(rawurl, "/")
	if justAuthority {
		rawurl = "http://" + rawurl
	}

	var err error
	if req.URL, err = url.ParseRequestURI(rawurl); err != nil {
		fmt.Println(err)
	}
	uri, _ := url.Parse("http:" + addr)
	req.URL = uri
	req.URL.Host = req.RequestURI

	if justAuthority {
		// Strip the bogus "http://" back off.
		req.URL.Scheme = ""
	}

	// RFC 7230, section 5.3: Must treat
	//	GET /index.html HTTP/1.1
	//	Host: www.google.com
	// and
	//	GET http://www.google.com/index.html HTTP/1.1
	//	Host: doesntmatter
	// the same. In the second case, any Host line is ignored.
	req.Host = req.URL.Host
	//if req.Host == "" {
	//	req.Host = req.Header.get("Host")
	//}
	//if deleteHostHeader {
	//	delete(req.Header, "Host")
	//}
	//
	//fixPragmaCacheControl(req.Header)
	req.URL.RequestURI()

	////req.Header.Add("Host", context2.GetHost(ctx) + ":" + context2.GetPort(ctx))
	//req.Header.Add("Proxy-Connection", "keep-alive")
	//req.RequestURI = uri.RequestURI()
	//req.Host = context2.GetUri(ctx).Host
	//
	//log.Warnf("URI: '%v' '%v' %v, %v\n", req.Method, req.RequestURI, req.Header, uri.RequestURI())

	return req
}
