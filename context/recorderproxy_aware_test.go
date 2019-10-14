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

package context

import (
	"context"
	"net/url"
	"reflect"
	"testing"
)

func Test_recordProxyDataAware(t *testing.T) {
	ctx1 := context.Background()
	ctx2 := RecordProxyDataAware(ctx1)
	ctx3 := RecordProxyDataAware(ctx2)

	if ctx1.Value(ctxKeyRecorderProxyAware) != nil {
		t.Error("Not expected to RecordProxyAware")
	}

	if ctx2.Value(ctxKeyRecorderProxyAware) == nil {
		t.Error("Expected to RecordProxyAware")
	}

	if !reflect.DeepEqual(ctx2, ctx3) {
		t.Error("Expected to get same context")
	}

	if !reflect.DeepEqual(ctx2.Value(ctxKeyRecorderProxyAware), ctx3.Value(ctxKeyRecorderProxyAware)) {
		t.Error("Expected to get same data")
	}
}

func TestGetHost(t *testing.T) {
	uri, _ := url.Parse("http://www.example.com")
	ctx1 := context.Background()
	ctx2 := RecordProxyDataAware(context.Background())
	ctx3 := RecordProxyDataAware(context.Background())
	SetUri(ctx2, uri)
	SetHost(ctx3, "foo")
	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{"No RecorderProxy aware", ctx1, ""},
		{"No value", ctx2, ""},
		{"With value", ctx3, "foo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetHost(tt.ctx); got != tt.want {
				t.Errorf("GetHost() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetUrl(t *testing.T) {
	uri, _ := url.Parse("http://www.example.com")
	ctx1 := context.Background()
	ctx2 := RecordProxyDataAware(context.Background())
	ctx3 := RecordProxyDataAware(context.Background())
	SetHost(ctx2, "foo")
	SetUri(ctx3, uri)
	tests := []struct {
		name string
		ctx  context.Context
		want *url.URL
	}{
		{"No RecorderProxy aware", ctx1, nil},
		{"No value", ctx2, nil},
		{"With value", ctx3, uri},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetUri(tt.ctx); got != tt.want {
				t.Errorf("GetUrl() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetRecordContext(t *testing.T) {
	uri, _ := url.Parse("http://www.example.com")
	ctx1 := context.Background()
	ctx2 := RecordProxyDataAware(context.Background())
	ctx3 := RecordProxyDataAware(context.Background())
	SetUri(ctx2, uri)
	SetRecordContext(ctx3, &RecordContext{})
	tests := []struct {
		name string
		ctx  context.Context
		want *RecordContext
	}{
		{"No RecorderProxy aware", ctx1, nil},
		{"No value", ctx2, nil},
		{"With value", ctx3, &RecordContext{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetRecordContext(tt.ctx); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetRecordContext() = %v, want %v", got, tt.want)
			}
		})
	}
}
