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

package tracing

import (
	"context"
	"fmt"
	"github.com/nlnwa/veidemann-api-go/contentwriter/v1"
	context2 "github.com/nlnwa/veidemann-recorderproxy/context"
	"github.com/opentracing/opentracing-go"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/stats"
)

type sh struct {
	service string
}

// NewStatsHandler creates a stats.Handler for gRPC which logs all traffic if loglevel is equal or finer than the submitted loglevel
func NewStatsHandler(serviceName string, loglevel logrus.Level) grpc.DialOption {
	if logrus.IsLevelEnabled(loglevel) {
		return grpc.WithStatsHandler(&sh{"gRPC:" + serviceName})
	} else {
		return grpc.EmptyDialOption{}
	}
}

func (h *sh) TagRPC(c context.Context, i *stats.RPCTagInfo) context.Context {
	context2.LogWithContext(c, h.service).Debugf("TagRPC: %s %v", i.FullMethodName, i.FailFast)
	return c
}

func (h *sh) HandleRPC(c context.Context, s stats.RPCStats) {
	span := opentracing.SpanFromContext(c)
	switch v := s.(type) {
	case *stats.Begin:
		context2.LogWithContext(c, h.service).Debugf("Begin HandleRPC: %v", v.BeginTime)
		span.LogKV("event", fmt.Sprintf("%s Begin", h.service))
	case *stats.End:
		context2.LogWithContext(c, h.service).Debugf("End HandleRPC: %v, %v, %v", v.BeginTime, v.EndTime, v.Error)
		span.LogKV("event", fmt.Sprintf("%s End %v", h.service, v.Trailer))
	case *stats.InHeader:
		context2.LogWithContext(c, h.service).Debugf("InHeader HandleRPC: %v", v)
	case *stats.InPayload:
		context2.LogWithContext(c, h.service).Debugf("InPayload HandleRPC: %T", v.Payload)
		span.LogKV(
			"xx", fmt.Sprintf("%T", v.Payload),
			"data", fmt.Sprintf("%v", v.Payload),
			"component", h.service,
			"direction", "in",
		)
	case *stats.InTrailer:
		context2.LogWithContext(c, h.service).Debugf("InTrailer HandleRPC: %v", v)
	case *stats.OutHeader:
		context2.LogWithContext(c, h.service).Debugf("OutHeader HandleRPC: %v", v)
	case *stats.OutPayload:
		switch p := v.Payload.(type) {
		//case *dnsresolver.ResolveRequest:
		case *contentwriter.WriteRequest:
			switch w := p.GetValue().(type) {
			case *contentwriter.WriteRequest_Meta:
				context2.LogWithContext(c, h.service).Debug(w.Meta)
			case *contentwriter.WriteRequest_Payload:
				if logrus.IsLevelEnabled(logrus.TraceLevel) {
					context2.LogWithContext(c, h.service).Tracef("payload[%v]: %v", w.Payload.RecordNum, string(w.Payload.Data))
				} else {
					context2.LogWithContext(c, h.service).Debugf("payload[%v]: %v bytes", w.Payload.RecordNum, len(w.Payload.Data))
				}
			case *contentwriter.WriteRequest_Cancel:
				context2.LogWithContext(c, h.service).Debug(w.Cancel)
			case *contentwriter.WriteRequest_ProtocolHeader:
				context2.LogWithContext(c, h.service).Debugf("header[%v]: %v", w.ProtocolHeader.RecordNum, string(w.ProtocolHeader.Data))
			}
		default:
			context2.LogWithContext(c, h.service).Debugf("OutPayload HandleRPC: %T", v.Payload)
		}
		span.LogKV(
			"xx", fmt.Sprintf("%T", v.Payload),
			"data", fmt.Sprintf("%v", v.Payload),
			"component", h.service,
			"direction", "out",
		)
	case *stats.OutTrailer:
		context2.LogWithContext(c, h.service).Debugf("OutTrailer HandleRPC: %v", v)
	default:
		context2.LogWithContext(c, h.service).Debugf("HandleRPC: isclient %v %T", s.IsClient(), s)
	}
}

func (h *sh) TagConn(c context.Context, i *stats.ConnTagInfo) context.Context {
	context2.LogWithContext(c, h.service).Debugf("TagConn: %s --> %s\n", i.LocalAddr, i.RemoteAddr)
	return c
}

func (h *sh) HandleConn(c context.Context, s stats.ConnStats) {
	switch v := s.(type) {
	case *stats.ConnBegin:
		context2.LogWithContext(c, h.service).Debugf("Begin HandleConn: isclient %v", v.IsClient())
	case *stats.ConnEnd:
		context2.LogWithContext(c, h.service).Debugf("End HandleConn: isclient %v", v.IsClient())
	default:
		context2.LogWithContext(c, h.service).Debugf("HandleConn: isclient %v %T", s.IsClient(), s)
	}
}
