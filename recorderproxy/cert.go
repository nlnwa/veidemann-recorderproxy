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
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"time"
)

var tlsClientSkipVerify = &tls.Config{InsecureSkipVerify: true}

var (
	RecorderProxyCertCache  *CertCache
	RecorderProxyPrivateKey crypto.Signer
	RecorderProxyCa         tls.Certificate
	err                     error
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
	if caCertFile == "" {
		RecorderProxyCa, err = tls.X509KeyPair(caCert, caKey)
	} else {
		RecorderProxyCa, err = tls.LoadX509KeyPair(caCertFile, caKeyFile)
	}
	if err != nil {
		return err
	}
	if RecorderProxyCa.Leaf, err = x509.ParseCertificate(RecorderProxyCa.Certificate[0]); err != nil {
		return err
	}
	RecorderProxyPrivateKey, err = generatePrivateKey(RecorderProxyCa.PrivateKey)
	if err != nil {
		return err
	}
	RecorderProxyCertCache, err = NewCache(60*time.Minute, 64)
	if err != nil {
		return err
	}

	return nil
}

func TLSConfigFromCA() func(host string, remoteCert *x509.Certificate, ctx *RecordContext) (*tls.Config, error) {
	return func(host string, remoteCert *x509.Certificate, ctx *RecordContext) (*tls.Config, error) {
		var err error
		var cert *tls.Certificate

		hostname := stripPort(host)
		config := tls.Config{
			InsecureSkipVerify: true,
		}
		ctx.SessionLogger().Debugf("signing for %s", stripPort(host))

		cert, err = RecorderProxyCertCache.Get(hostname, remoteCert, ctx)

		if err != nil {
			ctx.SessionLogger().Warnf("Cannot sign host certificate with provided CA: %s", err)
			return nil, err
		}

		config.Certificates = append(config.Certificates, *cert)
		return &config, nil
	}
}
