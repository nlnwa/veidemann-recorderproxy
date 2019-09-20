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
	"fmt"
	"github.com/nlnwa/veidemann-recorderproxy/logger"
	"github.com/opentracing/opentracing-go"
	"google.golang.org/grpc/stats"
)

type sh struct {
	service string
}

func NewStatsHandler(serviceName string) stats.Handler {
	fmt.Println("¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤¤")
	return &sh{"gRPC:" + serviceName}
}

func (h *sh) TagRPC(c context.Context, i *stats.RPCTagInfo) context.Context {
	logger.LogWithComponent(h.service).Printf("TagRPC: %s %v\n", i.FullMethodName, i.FailFast)
	return c
}

func (h *sh) HandleRPC(c context.Context, s stats.RPCStats) {
	span := opentracing.SpanFromContext(c)
	switch v := s.(type) {
	case *stats.Begin:
		logger.LogWithComponent(h.service).Printf("Begin HandleRPC: %v\n", v.BeginTime)
		span.LogKV("event", fmt.Sprintf("%s Begin", h.service))
	case *stats.End:
		logger.LogWithComponent(h.service).Printf("End HandleRPC: %v, %v, %v\n", v.BeginTime, v.EndTime, v.Error)
		span.LogKV("event", fmt.Sprintf("%s End %v", h.service, v.Trailer))
	case *stats.InHeader:
		logger.LogWithComponent(h.service).Printf("InHeader HandleRPC: %v\n", v)
	case *stats.InPayload:
		logger.LogWithComponent(h.service).Printf("InPayload HandleRPC: %T\n", v.Payload)
		span.LogKV(
			"xx", fmt.Sprintf("%T", v.Payload),
			"data", fmt.Sprintf("%v", v.Payload),
			"component", h.service,
			"direction", "in",
		)
	case *stats.InTrailer:
		logger.LogWithComponent(h.service).Printf("InTrailer HandleRPC: %v\n", v)
	case *stats.OutHeader:
		logger.LogWithComponent(h.service).Printf("OutHeader HandleRPC: %v\n", v)
	case *stats.OutPayload:
		logger.LogWithComponent(h.service).Printf("OutPayload HandleRPC: %T\n", v.Payload)
		span.LogKV(
			"xx", fmt.Sprintf("%T", v.Payload),
			"data", fmt.Sprintf("%v", v.Payload),
			"component", h.service,
			"direction", "out",
		)
	case *stats.OutTrailer:
		logger.LogWithComponent(h.service).Printf("OutTrailer HandleRPC: %v\n", v)
	default:
		logger.LogWithComponent(h.service).Printf("HandleRPC: isclient %v %T\n", s.IsClient(), s)
	}
}

func (h *sh) TagConn(c context.Context, i *stats.ConnTagInfo) context.Context {
	logger.LogWithComponent(h.service).Printf("TagConn: %s --> %s\n", i.LocalAddr, i.RemoteAddr)
	return c
}

func (h *sh) HandleConn(c context.Context, s stats.ConnStats) {
	switch v := s.(type) {
	case *stats.ConnBegin:
		logger.LogWithComponent(h.service).Printf("Begin HandleConn: isclient %v\n", v.IsClient())
	case *stats.ConnEnd:
		logger.LogWithComponent(h.service).Printf("End HandleConn: isclient %v\n", v.IsClient())
	default:
		logger.LogWithComponent(h.service).Printf("HandleConn: isclient %v %T\n", s.IsClient(), s)
	}
}
