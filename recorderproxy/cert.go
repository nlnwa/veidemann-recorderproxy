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
	"crypto/tls"
	"crypto/x509"
	"github.com/elazarl/goproxy"
)

var caCert = []byte(`-----BEGIN CERTIFICATE-----
MIIB8jCCAZmgAwIBAgIUQVlggOJ1FJzPWdBCQARPSc/3H78wCgYIKoZIzj0EAwIw
VjEcMBoGA1UECgwTVmVpZGVtYW5uIGhhcnZlc3RlcjEYMBYGA1UECwwPVmVpZGVt
YW5uIGNhY2hlMRwwGgYDVQQDDBN2ZWlkZW1hbm4taGFydmVzdGVyMB4XDTE5MDUw
MzA5MTMxMFoXDTI0MDUwMTA5MTMxMFowVjEcMBoGA1UECgwTVmVpZGVtYW5uIGhh
cnZlc3RlcjEYMBYGA1UECwwPVmVpZGVtYW5uIGNhY2hlMRwwGgYDVQQDDBN2ZWlk
ZW1hbm4taGFydmVzdGVyMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEHNxDIN56
wFYqiJN2tH+aZRM6l72enLEHaWrLSExA8oBRoK5SIzzwOXVDA9K5SlUsgKQ5cQ0b
0dzWek+w35KwxqNFMEMwDwYDVR0TAQH/BAUwAwEB/zALBgNVHQ8EBAMCAbYwIwYD
VR0lBBwwGgYIKwYBBQUHAwEGCCsGAQUFBwMCBgRVHSUAMAoGCCqGSM49BAMCA0cA
MEQCIEza7JHF7/tpLPrlGscg6zzx7l15VJnu8R5+Q808gi0rAiBhfS71cE/fnz8p
8tzSzXGAdFpvzZdDb2M/SSlKmnGtoQ==
-----END CERTIFICATE-----`)

var caKey = []byte(`-----BEGIN PRIVATE KEY-----
MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQgSZfSRtGI9jO3hzEr
mp7599ZmAEHmXunmpcLk6QQNA9KhRANCAAQc3EMg3nrAViqIk3a0f5plEzqXvZ6c
sQdpastITEDygFGgrlIjPPA5dUMD0rlKVSyApDlxDRvR3NZ6T7DfkrDG
-----END PRIVATE KEY-----`)

func SetCA(caCertFile, caKeyFile string) error {
	var (
		goproxyCa tls.Certificate
		err       error
	)

	if caCertFile == "" {
		goproxyCa, err = tls.X509KeyPair(caCert, caKey)
	} else {
		goproxyCa, err = tls.LoadX509KeyPair(caCertFile, caKeyFile)
	}
	if err != nil {
		return err
	}
	if goproxyCa.Leaf, err = x509.ParseCertificate(goproxyCa.Certificate[0]); err != nil {
		return err
	}
	goproxy.GoproxyCa = goproxyCa
	goproxy.OkConnect = &goproxy.ConnectAction{Action: goproxy.ConnectAccept, TLSConfig: goproxy.TLSConfigFromCA(&goproxyCa)}
	goproxy.MitmConnect = &goproxy.ConnectAction{Action: goproxy.ConnectMitm, TLSConfig: goproxy.TLSConfigFromCA(&goproxyCa)}
	goproxy.HTTPMitmConnect = &goproxy.ConnectAction{Action: goproxy.ConnectHTTPMitm, TLSConfig: goproxy.TLSConfigFromCA(&goproxyCa)}
	goproxy.RejectConnect = &goproxy.ConnectAction{Action: goproxy.ConnectReject, TLSConfig: goproxy.TLSConfigFromCA(&goproxyCa)}
	return nil
}
