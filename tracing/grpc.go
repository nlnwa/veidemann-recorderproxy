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
	"github.com/nlnwa/veidemann-api/go/contentwriter/v1"
	"github.com/nlnwa/veidemann-recorderproxy/logger"
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

func LogWithContext(componentName string) *logger.Logger {
	return logger.Log.WithField("component", componentName)
}

func (h *sh) TagRPC(c context.Context, i *stats.RPCTagInfo) context.Context {
	LogWithContext(h.service).Debugf("TagRPC: %s %v", i.FullMethodName, i.FailFast)
	return c
}

func (h *sh) HandleRPC(c context.Context, s stats.RPCStats) {
	span := opentracing.SpanFromContext(c)
	switch v := s.(type) {
	case *stats.Begin:
		LogWithContext(h.service).Debugf("Begin HandleRPC: %v", v.BeginTime)
		span.LogKV("event", fmt.Sprintf("%s Begin", h.service))
	case *stats.End:
		LogWithContext(h.service).Debugf("End HandleRPC: %v, %v, %v", v.BeginTime, v.EndTime, v.Error)
		span.LogKV("event", fmt.Sprintf("%s End %v", h.service, v.Trailer))
	case *stats.InHeader:
		LogWithContext(h.service).Debugf("InHeader HandleRPC: %v", v)
	case *stats.InPayload:
		LogWithContext(h.service).Debugf("InPayload HandleRPC: %T", v.Payload)
		span.LogKV(
			"xx", fmt.Sprintf("%T", v.Payload),
			"data", fmt.Sprintf("%v", v.Payload),
			"component", h.service,
			"direction", "in",
		)
	case *stats.InTrailer:
		LogWithContext(h.service).Debugf("InTrailer HandleRPC: %v", v)
	case *stats.OutHeader:
		LogWithContext(h.service).Debugf("OutHeader HandleRPC: %v", v)
	case *stats.OutPayload:
		switch p := v.Payload.(type) {
		//case *dnsresolver.ResolveRequest:
		case *contentwriter.WriteRequest:
			switch w := p.GetValue().(type) {
			case *contentwriter.WriteRequest_Meta:
				LogWithContext(h.service).Debug(w.Meta)
			case *contentwriter.WriteRequest_Payload:
				if logrus.IsLevelEnabled(logrus.TraceLevel) {
					LogWithContext(h.service).Tracef("payload[%v]: %v", w.Payload.RecordNum, string(w.Payload.Data))
				} else {
					LogWithContext(h.service).Debugf("payload[%v]: %v bytes", w.Payload.RecordNum, len(w.Payload.Data))
				}
			case *contentwriter.WriteRequest_Cancel:
				LogWithContext(h.service).Debug(w.Cancel)
			case *contentwriter.WriteRequest_ProtocolHeader:
				LogWithContext(h.service).Debugf("header[%v]: %v", w.ProtocolHeader.RecordNum, string(w.ProtocolHeader.Data))
			}
		default:
			LogWithContext(h.service).Debugf("OutPayload HandleRPC: %T", v.Payload)
		}
		span.LogKV(
			"xx", fmt.Sprintf("%T", v.Payload),
			"data", fmt.Sprintf("%v", v.Payload),
			"component", h.service,
			"direction", "out",
		)
	case *stats.OutTrailer:
		LogWithContext(h.service).Debugf("OutTrailer HandleRPC: %v", v)
	default:
		LogWithContext(h.service).Debugf("HandleRPC: isclient %v %T", s.IsClient(), s)
	}
}

func (h *sh) TagConn(c context.Context, i *stats.ConnTagInfo) context.Context {
	LogWithContext(h.service).Debugf("TagConn: %s --> %s\n", i.LocalAddr, i.RemoteAddr)
	return c
}

func (h *sh) HandleConn(c context.Context, s stats.ConnStats) {
	switch v := s.(type) {
	case *stats.ConnBegin:
		LogWithContext(h.service).Debugf("Begin HandleConn: isclient %v", v.IsClient())
	case *stats.ConnEnd:
		LogWithContext(h.service).Debugf("End HandleConn: isclient %v", v.IsClient())
	default:
		LogWithContext(h.service).Debugf("HandleConn: isclient %v %T", s.IsClient(), s)
	}
}
