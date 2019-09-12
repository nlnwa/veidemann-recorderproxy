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
	"google.golang.org/grpc/stats"
)

type sh struct {
	service string
}

func NewStatsHandler(serviceName string) stats.Handler {
	return &sh{"GRPC:" + serviceName}
}

func (h *sh) TagRPC(c context.Context, i *stats.RPCTagInfo) context.Context {
	recorderproxy.LogWithComponent(h.service).Printf("TagRPC: %s %v\n", i.FullMethodName, i.FailFast)
	return c
}

func (h *sh) HandleRPC(c context.Context, s stats.RPCStats) {
	switch v := s.(type) {
	case *stats.Begin:
		recorderproxy.LogWithComponent(h.service).Printf("Begin HandleRPC: %v\n", v.BeginTime)
	case *stats.End:
		recorderproxy.LogWithComponent(h.service).Printf("End HandleRPC: %v, %v, %v\n", v.BeginTime, v.EndTime, v.Error)
	case *stats.OutPayload:
		recorderproxy.LogWithComponent(h.service).Printf("HandleRPC: %T\n", v.Payload)
	default:
		recorderproxy.LogWithComponent(h.service).Printf("HandleRPC: isclient %v %T\n", s.IsClient(), s)
	}
}

func (h *sh) TagConn(c context.Context, i *stats.ConnTagInfo) context.Context {
	recorderproxy.LogWithComponent(h.service).Printf("TagConn: %s --> %s\n", i.LocalAddr, i.RemoteAddr)
	return c
}

func (h *sh) HandleConn(c context.Context, s stats.ConnStats) {
	switch v := s.(type) {
	case *stats.ConnBegin:
		recorderproxy.LogWithComponent(h.service).Printf("Begin HandleConn: isclient %v\n", v.IsClient())
	case *stats.ConnEnd:
		recorderproxy.LogWithComponent(h.service).Printf("End HandleConn: isclient %v\n", v.IsClient())
	default:
		recorderproxy.LogWithComponent(h.service).Printf("HandleConn: isclient %v %T\n", s.IsClient(), s)
	}
}
