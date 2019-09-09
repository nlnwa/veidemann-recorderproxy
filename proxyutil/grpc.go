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
	"google.golang.org/grpc/stats"
)

type sh struct {
	service string
}

func NewStatsHandler(serviceName string) stats.Handler {
	return &sh{serviceName}
}

func (h *sh) TagRPC(c context.Context, i *stats.RPCTagInfo) context.Context {
	fmt.Printf("%s: TagRPC: %s %v\n", h.service, i.FullMethodName, i.FailFast)
	return c
}

func (h *sh) HandleRPC(c context.Context, s stats.RPCStats) {
	switch v := s.(type) {
	case *stats.Begin:
		fmt.Printf("%s: !!!! B HandleRPC: %v\n", h.service, v.BeginTime)
	case *stats.End:
		fmt.Printf("%s: !!!! E HandleRPC: %v, %v, %v\n", h.service, v.BeginTime, v.EndTime, v.Error)
	case *stats.OutPayload:
		fmt.Printf("%s: HandleRPC: %T\n", h.service, v.Payload)
	default:
		fmt.Printf("%s: HandleRPC: isclient %v %T\n", h.service, s.IsClient(), s)
	}
}

func (h *sh) TagConn(c context.Context, i *stats.ConnTagInfo) context.Context {
	fmt.Printf("%s: TagConn: %s --> %s\n", h.service, i.LocalAddr, i.RemoteAddr)
	return c
}

func (h *sh) HandleConn(c context.Context, s stats.ConnStats) {
	switch v := s.(type) {
	case *stats.ConnBegin:
		fmt.Printf("%s: HandleConn: isclient %v\n", h.service, v.IsClient())
	default:
		fmt.Printf("%s: HandleConn: isclient %v %T\n", h.service, s.IsClient(), s)
	}
}
