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
	"fmt"
	"github.com/getlantern/proxy/filters"
	"net/url"
	"reflect"
	"testing"
)

func Test_recordProxyDataAware(t *testing.T) {
	ctx1 := filters.BackgroundContext()
	ctx2, data1 := recordProxyDataAware(ctx1)
	ctx3, data2 := recordProxyDataAware(ctx2)

	if ctx1.Value(ctxKeyRecorderProxyAware) != nil {
		t.Error("Not expected to RecordProxyAware")
	}

	if ctx2.Value(ctxKeyRecorderProxyAware) == nil {
		t.Error("Expected to RecordProxyAware")
	}

	if data1 == nil {
		t.Error("Expected to context to have data")
	}

	if !reflect.DeepEqual(ctx2, ctx3) {
		t.Error("Expected to get same context")
	}

	if !reflect.DeepEqual(ctx2.Value(ctxKeyRecorderProxyAware), ctx3.Value(ctxKeyRecorderProxyAware)) {
		t.Error("Expected to get same data")
	}

	if !reflect.DeepEqual(data1, data2) {
		t.Error("Expected to get same data")
	}

	if !reflect.DeepEqual(ctx2.Value(ctxKeyRecorderProxyAware), data1) {
		t.Error("Expected to get same data")
	}
}

func TestGetHost(t *testing.T) {
	uri, _ := url.Parse("http://www.example.com")
	ctx1 := filters.BackgroundContext()
	ctx2 := SetUri(filters.BackgroundContext(), uri)
	ctx3 := SetHostPort(filters.BackgroundContext(), "foo", "123")
	tests := []struct {
		name string
		ctx  filters.Context
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

func TestGetPort(t *testing.T) {
	uri, _ := url.Parse("http://www.example.com")
	ctx1 := filters.BackgroundContext()
	ctx2 := SetUri(filters.BackgroundContext(), uri)
	ctx3 := SetHostPort(filters.BackgroundContext(), "foo", "123")
	tests := []struct {
		name string
		ctx  filters.Context
		want string
	}{
		{"No RecorderProxy aware", ctx1, ""},
		{"No value", ctx2, ""},
		{"With value", ctx3, "123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetPort(tt.ctx); got != tt.want {
				t.Errorf("GetPort() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetUrl(t *testing.T) {
	uri, _ := url.Parse("http://www.example.com")
	ctx1 := filters.BackgroundContext()
	ctx2 := SetHostPort(filters.BackgroundContext(), "foo", "123")
	ctx3 := SetUri(filters.BackgroundContext(), uri)
	tests := []struct {
		name string
		ctx  filters.Context
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

func TestResolveAndSetUrl(t *testing.T) {
	uri1, _ := url.Parse("http://www.example.com")
	uri2, _ := url.Parse("/mypath")
	uri3, _ := url.Parse("http://www.example.com/mypath")
	uri4, _ := url.Parse("http://www.example.com/otherpath")
	fmt.Printf("%v\n%v\n%v\n%v\n", uri1, uri2, uri3, uri1.ResolveReference(uri2))
	ctx1 := filters.BackgroundContext()
	ctx2 := SetHostPort(filters.BackgroundContext(), "foo", "123")
	ctx3 := SetUri(filters.BackgroundContext(), uri1)
	ctx4 := SetUri(filters.BackgroundContext(), uri3)
	tests := []struct {
		name string
		ctx  filters.Context
		uri  *url.URL
		want *url.URL
	}{
		{"No existing RecorderProxy aware", ctx1, uri1, uri1},
		{"Resolve with no existing URI", ctx2, uri1, uri1},
		{"Resolve with same URI", ctx3, uri1, uri1},
		{"Resolve with relative URI", ctx3, uri2, uri3},
		{"Resolve with absolute URI", ctx4, uri4, uri4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, gotUri := ResolveAndSetUri(tt.ctx, tt.uri); !reflect.DeepEqual(gotUri, tt.want) {
				t.Errorf("ResolveAndSetUri() = %v, want %v", gotUri, tt.want)
			}
		})
	}
}

func TestGetRecordContext(t *testing.T) {
	uri, _ := url.Parse("http://www.example.com")
	ctx1 := filters.BackgroundContext()
	ctx2 := SetUri(filters.BackgroundContext(), uri)
	ctx3 := SetRecordContext(filters.BackgroundContext(), &RecordContext{})
	tests := []struct {
		name string
		ctx  filters.Context
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

//func TestMe(t *testing.T) {
//	ctx1 := context.Background()
//	ctx2, _ := context.WithTimeout(ctx1, 2*time.Second)
//	ctx3 := context.WithValue(ctx2, "foo", "bar")
//
//	go func() {
//		select {
//		case <-ctx2.Done():
//			fmt.Printf("ctx2 about to cancel\n")
//			time.Sleep(2 * time.Second)
//			fmt.Printf("ctx2 cancelled\n")
//		}
//	}()
//
//	go func() {
//		select {
//		case <-ctx3.Done():
//			fmt.Printf("ctx3 about to cancel\n")
//			time.Sleep(2 * time.Second)
//			fmt.Printf("ctx3 cancelled\n")
//		}
//	}()
//	time.Sleep(8 * time.Second)
//}
