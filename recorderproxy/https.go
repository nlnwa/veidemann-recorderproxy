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
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var (
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

func (proxy *RecorderProxy) handleHttps(w http.ResponseWriter, connectReq *http.Request) {
	ctx := NewRecordContext(proxy)

	hij, ok := w.(http.Hijacker)
	if !ok {
		panic("httpserver does not support hijacking")
	}

	proxyClient, _, e := hij.Hijack()
	if e != nil {
		panic("Cannot hijack connection " + e.Error())
	}

	host := connectReq.URL.Host

	proxyClient.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))
	ctx.Logf("Assuming CONNECT is TLS, mitm proxying it")
	// this goes in a separate goroutine, so that the net/http server won't think we're
	// still handling the request even after hijacking the connection. Those HTTP CONNECT
	// request can take forever, and the server will be stuck when "closed".
	// TODO: Allow Server.Close() mechanism to shut down this connection as nicely as possible

	go func() {
		var remoteCert *x509.Certificate
		remoteConn, remoteConnErr := tls.Dial("tcp", connectReq.Host, &tls.Config{InsecureSkipVerify: true})
		if remoteConnErr != nil {
			ctx.Warnf("Cannot handshake remote server %v %v", connectReq.Host, remoteConnErr)
		}
		if remoteConn != nil && remoteConn.ConnectionState().HandshakeComplete {
			remoteCert = remoteConn.ConnectionState().PeerCertificates[0]
		}

		var tlsConfig *tls.Config
		if TLSConfig != nil {
			var err error
			tlsConfig, err = TLSConfig(host, remoteCert, ctx)
			if err != nil {
				httpError(proxyClient, ctx, err)
				return
			}
		} else {
			tlsConfig = &tls.Config{
				InsecureSkipVerify: true,
			}
		}

		//TODO: cache connections to the remote website
		rawClientTls := tls.Server(proxyClient, tlsConfig)

		if err := rawClientTls.Handshake(); err != nil {
			ctx.Resp = NewResponse(connectReq, "text/plain", 555, "CONNECT failed")
			ctx.Warnf("Cannot handshake client %v %v", connectReq.Host, err)
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
			if !proxy.handleTunneledRequest(proxyClient, rawClientTls, clientTlsReader, connectReq, remoteConnErr) {
				return
			}
		}

		ctx.Logf("Exiting on EOF")
	}()
}

func (proxy *RecorderProxy) handleTunneledRequest(proxyClient net.Conn, rawClientTls *tls.Conn, clientTlsReader *bufio.Reader, connectReq *http.Request, remoteConnErr error) (ok bool) {
	req, err := http.ReadRequest(clientTlsReader)
	ctx := NewRecordContext(proxy)
	ctx.Error = remoteConnErr
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
	if resp == nil && ctx.Error != nil {
		handleResponseError(ctx.Error, ctx)
	} else {
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
		ctx.Logf("Copying response to client %v [%d]", resp.Status, resp.StatusCode)
		defer func() {
			e := resp.Body.Close()
			if e != nil {
				ctx.Warnf("Error while closing body: %v\n", e)
			}
		}()

		if err := resp.Write(rawClientTls); err != nil {
			ctx.Warnf("Cannot write TLS response from mitm'd client: %v, URL: %s", err, req.URL)
			ctx.SendErrorCode(-5011, "CANCELED_BY_BROWSER", "Veidemann recorder proxy lost connection to client")
			return
		}
	}
	if req.Close {
		ctx.Logf("Non-persistent connection; closing")
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
