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
	"fmt"
	"github.com/getlantern/proxy/filters"
	"github.com/nlnwa/veidemann-api-go/dnsresolver/v1"
	dnsresolverV1 "github.com/nlnwa/veidemann-api-go/dnsresolver/v1"
	context2 "github.com/nlnwa/veidemann-recorderproxy/context"
	"github.com/nlnwa/veidemann-recorderproxy/errors"
	"github.com/opentracing/opentracing-go"
	"google.golang.org/grpc/status"
	"net/http"
	"strconv"
)

// DnsLookupFilter is a filter which returns an error if the proxy is accessed as if it where a web server and not a proxy.
type DnsLookupFilter struct {
	DnsResolverClient dnsresolverV1.DnsResolverClient
}

func (f *DnsLookupFilter) Apply(ctx filters.Context, req *http.Request, next filters.Next) (resp *http.Response, context filters.Context, err error) {
	l := context2.LogWithContextAndRequest(ctx, req, "FLT:dns")

	ip := context2.GetIp(ctx)
	host := context2.GetHost(ctx)
	port := context2.GetPort(ctx)
	if ip == "" && host != "" {
		if e := f.resolve(ctx, host, port); e != nil {
			return handleRequestError(ctx, req, e)
		}
		l.Debugf("resolved '%v' to '%v'", host, ip)
	}
	resp, context, err = next(ctx, req)
	return
}

func (f *DnsLookupFilter) resolve(ctx filters.Context, host, port string) (err error) {
	span, c := opentracing.StartSpanFromContext(ctx, "Resolve DNS")
	dnsContext := context2.WrapIfNecessary(c)
	defer span.Finish()

	var p = 0
	if port != "" {
		p, err = strconv.Atoi(port)
		if err != nil {
			err = errors.Wrap(err, errors.DomainLookupFailed, "illegal port", port)
			return
		}
	}
	dnsReq := &dnsresolver.ResolveRequest{
		CollectionRef: context2.GetCollectionRef(ctx),
		Host:          host,
		Port:          int32(p),
	}

	dnsResp, err := f.DnsResolverClient.Resolve(dnsContext, dnsReq)
	s := status.Convert(err)
	if err != nil {
		err = errors.Wrap(err, errors.DomainLookupFailed, fmt.Sprintf("Got 'no such host' from DNS for host: %s, port: %s", host, port), s.Message())
		return
	}

	context2.SetIp(ctx, dnsResp.TextualIp)
	return
}
