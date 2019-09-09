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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"github.com/allegro/bigcache"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/singleflight"
	"math/big"
	"net"
	"time"
)

// CertCache is a wrapper around *bigcache.BigCache which takes care of marshalling and unmarshalling of dns.Msg.
type CertCache struct {
	cache *bigcache.BigCache
	now   func() time.Time
	g     singleflight.Group
}

// NewCache creates a new CertCache
func NewCache(lifeWindow time.Duration, maxSizeMb int) (*CertCache, error) {
	config := bigcache.Config{
		// number of shards (must be a power of 2)
		Shards: 1024,
		// time after which entry can be evicted
		LifeWindow: lifeWindow,
		// rps * lifeWindow, used only in initial memory allocation
		MaxEntriesInWindow: 1000 * 10 * 60,
		// max entry size in bytes, used only in initial memory allocation
		MaxEntrySize: 200,
		// prints information about additional memory allocation
		Verbose: true,
		// cache will not allocate more memory than this limit, value in MB
		// if value is reached then the oldest entries can be overridden for the new ones
		// 0 value means no size limit
		HardMaxCacheSize: maxSizeMb,
		// callback fired when the oldest entry is removed because of its expiration time or no space left
		// for the new entry, or because delete was called. A bitmask representing the reason will be returned.
		// Default value is nil which means no callback and it prevents from unwrapping the oldest entry.
		OnRemove:    nil,
		CleanWindow: 0,
	}

	c, err := bigcache.NewBigCache(config)
	if err != nil {
		return nil, err
	}

	return &CertCache{
		cache: c,
		now:   time.Now,
	}, nil
}

// Put writes a certificate to the cache
func (c *CertCache) Put(key string, derBytes []byte, ctx *recordContext) error {
	err = c.cache.Set(key, derBytes)
	ctx.Logf("Certificate for key %s written to cache", key)
	return err
}

// Get reads a certificate from the cache. It returns a new certificate when no entry exists for the given key.
func (c *CertCache) Get(host string, remoteCert *x509.Certificate, ctx *recordContext) (*tls.Certificate, error) {
	key := host
	if remoteCert != nil {
		key = remoteCert.SerialNumber.String()
		logrus.Debugf("%v\n\n%v\n\n%v\n%v", host, remoteCert.Issuer, ctx, key)
	}
	v, err, _ := c.g.Do(key, func() (interface{}, error) {
		derBytes, err := c.cache.Get(key)
		if err == bigcache.ErrEntryNotFound {
			ctx.Logf("Creating certificate for key %s", key)
			derBytes, err = signHost(key, remoteCert)
			if err != nil {
				return nil, err
			}
			err = c.Put(key, derBytes, ctx)
			if err != nil {
				return nil, err
			}
		} else {
			ctx.Logf("Using cached certificate for key %s", key)
		}
		if err != nil {
			return nil, err
		}

		cert := &tls.Certificate{
			Certificate: [][]byte{derBytes, RecorderProxyCa.Certificate[0]},
			PrivateKey:  RecorderProxyPrivateKey,
		}
		return cert, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*tls.Certificate), nil
}

func signHost(host string, remoteCert *x509.Certificate) (derBytes []byte, err error) {
	var x509ca *x509.Certificate

	// Use the provided ca and not the global GoproxyCa for certificate generation.
	if x509ca, err = x509.ParseCertificate(RecorderProxyCa.Certificate[0]); err != nil {
		return
	}

	var template x509.Certificate
	if remoteCert != nil {
		template = *remoteCert
		template.PublicKey = nil
		template.PublicKeyAlgorithm = 0
		template.Signature = nil
		template.SignatureAlgorithm = 0
	} else {
		start := time.Unix(0, 0)
		end, err := time.Parse("2006-01-02", "2049-12-31")
		if err != nil {
			panic(err)
		}
		serial := new(big.Int)
		serial.SetBytes([]byte(host))
		template = x509.Certificate{
			SerialNumber: serial,
			Issuer:       x509ca.Subject,
			Subject: pkix.Name{
				Organization: []string{"RecorderProxy untrusted MITM proxy Inc"},
			},
			NotBefore: start,
			NotAfter:  end,

			KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			BasicConstraintsValid: true,
		}
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, host)
			template.Subject.CommonName = host
		}
	}

	csprng := rand.Reader

	derBytes, err = x509.CreateCertificate(csprng, &template, x509ca, RecorderProxyPrivateKey.Public(), RecorderProxyCa.PrivateKey)
	return
}

func generatePrivateKey(pk crypto.PrivateKey) (certpriv crypto.Signer, err error) {
	csprng := rand.Reader

	switch pk.(type) {
	case *rsa.PrivateKey:
		if certpriv, err = rsa.GenerateKey(csprng, 2048); err != nil {
			return
		}
	case *ecdsa.PrivateKey:
		if certpriv, err = ecdsa.GenerateKey(elliptic.P256(), csprng); err != nil {
			return
		}
	default:
		err = fmt.Errorf("unsupported key type %T", pk)
	}
	return
}
