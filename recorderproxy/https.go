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
	"crypto/tls"
	"errors"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var (
	//TLSConfig   func(host string, ctx *recordContext) (*tls.Config, error)
	TLSConfig   = TLSConfigFromCA()
	httpsRegexp = regexp.MustCompile(`^https:\/\/`)
)

func stripPort(s string) string {
	ix := strings.IndexRune(s, ':')
	if ix == -1 {
		return s
	}
	return s[:ix]
}

func (proxy *RecorderProxy) dial(network, addr string) (c net.Conn, err error) {
	if proxy.RoundTripper.Dial != nil {
		return proxy.RoundTripper.Dial(network, addr)
	}
	return net.Dial(network, addr)
}

func (proxy *RecorderProxy) connectDial(network, addr string) (c net.Conn, err error) {
	if proxy.ConnectDial == nil {
		return proxy.dial(network, addr)
	}
	return proxy.ConnectDial(network, addr)
}

func (proxy *RecorderProxy) handleHttps(w http.ResponseWriter, r *http.Request) {
	ctx := NewRecordContext(proxy)

	hij, ok := w.(http.Hijacker)
	if !ok {
		panic("httpserver does not support hijacking")
	}

	proxyClient, _, e := hij.Hijack()
	if e != nil {
		panic("Cannot hijack connection " + e.Error())
	}

	host := r.URL.Host

	proxyClient.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))
	ctx.Logf("Assuming CONNECT is TLS, mitm proxying it")
	// this goes in a separate goroutine, so that the net/http server won't think we're
	// still handling the request even after hijacking the connection. Those HTTP CONNECT
	// request can take forever, and the server will be stuck when "closed".
	// TODO: Allow Server.Close() mechanism to shut down this connection as nicely as possible
	var tlsConfig *tls.Config
	if TLSConfig != nil {
		var err error
		tlsConfig, err = TLSConfig(host, ctx)
		if err != nil {
			httpError(proxyClient, ctx, err)
			return
		}
	} else {
		tlsConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}

	go func() {
		//TODO: cache connections to the remote website
		rawClientTls := tls.Server(proxyClient, tlsConfig)

		if err := rawClientTls.Handshake(); err != nil {
			ctx.Resp = NewResponse(r, "text/plain", 555, "CONNECT failed")
			ctx.Warnf("Cannot handshake client %v %v", r.Host, err)
			if ctx.Resp != nil {
				if err := ctx.Resp.Write(proxyClient); err != nil {
					ctx.Warnf("Cannot write response that reject http CONNECT: %v", err)
				}
			}
			defer func() {
				e := proxyClient.Close()
				if e != nil {
					ctx.Logf("Error while closing proxy client: %v\n", e)
				}
			}()

			return
		}
		defer func() {
			e := rawClientTls.Close()
			if e != nil {
				ctx.Logf("Error while closing raw client Tls: %v\n", e)
			}
		}()
		clientTlsReader := bufio.NewReader(rawClientTls)
		for !isEof(clientTlsReader) {
			if !proxy.handleTunneledRequest(proxyClient, rawClientTls, clientTlsReader, r) {
				return
			}
		}
		ctx.Logf("Exiting on EOF")
	}()
}

func (proxy *RecorderProxy) handleTunneledRequest(proxyClient net.Conn, rawClientTls *tls.Conn, clientTlsReader *bufio.Reader, connectReq *http.Request) (ok bool) {
	req, err := http.ReadRequest(clientTlsReader)
	ctx := NewRecordContext(proxy)
	ctx.init(req)
	if err != nil && err != io.EOF {
		return
	}
	if err != nil {
		ctx.Warnf("Cannot read TLS request from mitm'd client %v %v", connectReq.Host, err)
		return
	}
	req.RemoteAddr = connectReq.RemoteAddr // since we're converting the request, need to carry over the original connecting IP as well
	ctx.Logf("req %v", connectReq.Host)

	if !httpsRegexp.MatchString(req.URL.String()) {
		req.URL, err = url.Parse("https://" + connectReq.Host + req.URL.String())
	}

	ctx.Req = req

	req, resp := proxy.filterRequest(req, ctx)
	if resp == nil {
		if err != nil {
			ctx.Warnf("Illegal URL https://%s%s", connectReq.Host, req.URL.Path)
			return
		}
		removeProxyHeaders(ctx, req)
		resp, err = ctx.proxy.RoundTripper.RoundTrip(req, ctx)
		if err != nil {
			ctx.Warnf("Cannot read TLS response from mitm'd server %v, URL: %s", err, req.URL)
			if err.Error() == "Connect failed" {
				defer func() {
					e := proxyClient.Close()
					if e != nil {
						ctx.Logf("Error while closing proxy client: %v\n", e)
					}
				}()
			}
			return
		}
		ctx.Logf("resp %v", resp.Status)
	}
	resp = proxy.filterResponse(resp, ctx)
	defer func() {
		e := resp.Body.Close()
		if e != nil {
			ctx.Warnf("Error while closing body: %v\n", e)
		}
	}()

	text := resp.Status
	statusCode := strconv.Itoa(resp.StatusCode) + " "
	if strings.HasPrefix(text, statusCode) {
		text = text[len(statusCode):]
	}
	// always use 1.1 to support chunked encoding
	if _, err := io.WriteString(rawClientTls, "HTTP/1.1"+" "+statusCode+text+"\r\n"); err != nil {
		ctx.Warnf("Cannot write TLS response HTTP status from mitm'd client: %v, URL: %s", err, req.URL)
		return
	}
	// Since we don't know the length of resp, return chunked encoded response
	// TODO: use a more reasonable scheme
	resp.Header.Del("Content-Length")
	resp.Header.Set("Transfer-Encoding", "chunked")
	// Force connection close otherwise chrome will keep CONNECT tunnel open forever
	resp.Header.Set("Connection", "close")
	if err := resp.Header.Write(rawClientTls); err != nil {
		ctx.Warnf("Cannot write TLS response header from mitm'd client: %v, URL: %s", err, req.URL)
		ctx.SendErrorCode(-5011, "CANCELED_BY_BROWSER", "Veidemann recorder proxy lost connection to client")

		return
	}
	if _, err = io.WriteString(rawClientTls, "\r\n"); err != nil {
		ctx.Warnf("Cannot write TLS response header end from mitm'd client: %v, URL: %s", err, req.URL)
		return
	}
	chunked := newChunkedWriter(rawClientTls)
	if _, err := io.Copy(chunked, resp.Body); err != nil {
		ctx.Warnf("Cannot write TLS response body from mitm'd client: %v, URL: %s", err, req.URL)
		return
	}
	if err := chunked.Close(); err != nil {
		ctx.Warnf("Cannot write TLS chunked EOF from mitm'd client: %v, URL: %s", err, req.URL)
		return
	}
	if _, err = io.WriteString(rawClientTls, "\r\n"); err != nil {
		ctx.Warnf("Cannot write TLS response chunked trailer from mitm'd client: %v, URL: %s", err, req.URL)
		return
	}

	return true
}

func httpError(w io.WriteCloser, ctx *recordContext, err error) {
	ctx.Logf("Got error %s, sending 502 to client", err)
	if _, err := io.WriteString(w, "HTTP/1.1 502 Bad Gateway\r\n\r\n"); err != nil {
		ctx.Warnf("Error responding to client: %s", err)
	}
	if err := w.Close(); err != nil {
		ctx.Warnf("Error closing client connection: %s", err)
	}
}

func (proxy *RecorderProxy) NewConnectDialToProxy(httpsProxy string) func(network, addr string) (net.Conn, error) {
	return proxy.NewConnectDialToProxyWithHandler(httpsProxy, nil)
}

func (proxy *RecorderProxy) NewConnectDialToProxyWithHandler(httpsProxy string, connectReqHandler func(req *http.Request)) func(network, addr string) (net.Conn, error) {
	u, err := url.Parse(httpsProxy)
	if err != nil {
		return nil
	}
	if u.Scheme == "" || u.Scheme == "http" {
		if strings.IndexRune(u.Host, ':') == -1 {
			u.Host += ":80"
		}
		return func(network, addr string) (net.Conn, error) {
			connectReq := &http.Request{
				Method: "CONNECT",
				URL:    &url.URL{Opaque: addr},
				Host:   addr,
				Header: make(http.Header),
			}
			if connectReqHandler != nil {
				connectReqHandler(connectReq)
			}
			c, err := proxy.dial(network, u.Host)
			if err != nil {
				return nil, err
			}
			connectReq.Write(c)
			// Read response.
			// Okay to use and discard buffered reader here, because
			// TLS server will not speak until spoken to.
			br := bufio.NewReader(c)
			resp, err := http.ReadResponse(br, connectReq)
			if err != nil {
				c.Close()
				return nil, err
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				resp, err := ioutil.ReadAll(resp.Body)
				if err != nil {
					return nil, err
				}
				c.Close()
				return nil, errors.New("proxy refused connection" + string(resp))
			}
			return c, nil
		}
	}
	if u.Scheme == "https" {
		if strings.IndexRune(u.Host, ':') == -1 {
			u.Host += ":443"
		}
		return func(network, addr string) (net.Conn, error) {
			c, err := proxy.dial(network, u.Host)
			if err != nil {
				return nil, err
			}
			//c = tls.Client(c, proxy.Tr.TLSClientConfig)
			c = tls.Client(c, proxy.RoundTripper.TLSClientConfig)
			connectReq := &http.Request{
				Method: "CONNECT",
				URL:    &url.URL{Opaque: addr},
				Host:   addr,
				Header: make(http.Header),
			}
			if connectReqHandler != nil {
				connectReqHandler(connectReq)
			}
			connectReq.Write(c)
			// Read response.
			// Okay to use and discard buffered reader here, because
			// TLS server will not speak until spoken to.
			br := bufio.NewReader(c)
			resp, err := http.ReadResponse(br, connectReq)
			if err != nil {
				c.Close()
				return nil, err
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				body, err := ioutil.ReadAll(io.LimitReader(resp.Body, 500))
				if err != nil {
					return nil, err
				}
				c.Close()
				return nil, errors.New("proxy refused connection" + string(body))
			}
			return c, nil
		}
	}
	return nil
}
