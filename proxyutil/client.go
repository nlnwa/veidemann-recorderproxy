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

package main

import (
	"context"
	"github.com/nlnwa/veidemann-recorderproxy/recorderproxy"
	"github.com/nlnwa/veidemann-recorderproxy/tracing"
	"github.com/opentracing-contrib/go-stdlib/nethttp"
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	otlog "github.com/opentracing/opentracing-go/log"
	"github.com/sirupsen/logrus"
	"io/ioutil"
	"net/http"
	"time"
)

func get(url string, client *http.Client, timeout time.Duration) (int, []byte, error) {
	log := logrus.WithField("component", "CLIENT")
	tracer, closer := tracing.Init("Internal test client")
	if tracer != nil {
		defer closer.Close()
	}
	span := tracer.StartSpan("Client Request")
	defer span.Finish()

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, nil, err
	}

	if timeout > 0 {
		ctx, cancel := context.WithTimeout(req.Context(), timeout)
		defer cancel()
		req = req.WithContext(ctx)
	}
	req = req.WithContext(opentracing.ContextWithSpan(req.Context(), span))

	options := []nethttp.ClientOption{
		nethttp.ClientTrace(true),
		nethttp.InjectSpanContext(true),
	}
	req, ht := nethttp.TraceRequest(tracer, req, options...)
	defer ht.Finish()

	t := client.Transport
	t = &nethttp.Transport{RoundTripper: t}
	client.Transport = t
	if logrus.IsLevelEnabled(logrus.DebugLevel) {
		client.Transport, req = recorderproxy.DecorateRequest(client.Transport, req)
	}

	log.Infof("submitting request: %v %v %v", req.Method, req.URL, req.Proto)
	resp, err := client.Do(req)
	if err != nil {
		onError(span, err)
		return 0, nil, err
	}
	txt, err := ioutil.ReadAll(resp.Body)
	defer resp.Body.Close()

	if err != nil {
		onError(span, err)
		return 0, nil, err
	}

	return resp.StatusCode, txt, nil
}

func onError(span opentracing.Span, err error) (int, []byte, error) {
	log := logrus.WithField("component", "CLIENT")
	// handle errors by recording them in the span
	span.SetTag(string(ext.Error), true)
	span.LogKV(otlog.Error(err))
	log.Error(err)
	return 0, nil, err
}
