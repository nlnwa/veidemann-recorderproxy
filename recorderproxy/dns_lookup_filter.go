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
	dnsresolverV1 "github.com/nlnwa/veidemann-api/go/dnsresolver/v1"
	"github.com/nlnwa/veidemann-recorderproxy/errors"
	"github.com/nlnwa/veidemann-recorderproxy/filters"
	"github.com/opentracing/opentracing-go"
	"google.golang.org/grpc/status"
	"net/http"
	"strconv"
)

// DnsLookupFilter is a filter which returns an error if the proxy is accessed as if it where a web server and not a proxy.
type DnsLookupFilter struct {
	DnsResolverClient dnsresolverV1.DnsResolverClient
}

func (f *DnsLookupFilter) Apply(cs *filters.ConnectionState, req *http.Request, next filters.Next) (*http.Response, *filters.ConnectionState, error) {
	l := cs.LogWithContextAndRequest(req, "FLT:dns")

	host := cs.Host
	port := cs.Port
	if cs.Ip == "" && host != "" {
		if e := f.resolve(cs, req, host, port); e != nil {
			return handleRequestError(cs, req, e)
		}
		l.Debugf("resolved '%v' to '%v'", host, cs.Ip)
	}
	return next(cs, req)
}

func (f *DnsLookupFilter) resolve(cs *filters.ConnectionState, req *http.Request, host, port string) error {
	span, dnsContext := opentracing.StartSpanFromContext(req.Context(), "Resolve DNS")
	//dnsContext := context2.WrapIfNecessary(c)
	defer span.Finish()

	var p = 0
	if port != "" {
		var err error
		p, err = strconv.Atoi(port)
		if err != nil {
			return errors.Wrap(err, errors.DomainLookupFailed, "illegal port", port)
		}
	}
	dnsReq := &dnsresolverV1.ResolveRequest{
		ExecutionId:   cs.CrawlExecId,
		CollectionRef: cs.CollectionRef,
		Host:          host,
		Port:          int32(p),
	}

	dnsResp, err := f.DnsResolverClient.Resolve(dnsContext, dnsReq)
	s := status.Convert(err)
	if err != nil {
		return errors.Wrap(err, errors.DomainLookupFailed, fmt.Sprintf("Got 'no such host' from DNS for host: %s, port: %s", host, port), s.Message())
	}

	cs.Ip = dnsResp.TextualIp
	return nil
}
