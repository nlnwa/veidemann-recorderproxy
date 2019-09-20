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
	"github.com/getlantern/proxy/filters"
	"net/url"
)

type ctxKey string

const (
	ctxKeyRecorderProxyAware = ctxKey("recorderProxyAware")
	ctxKeyRCTX               = ctxKey("recordContext")
	ctxKeyHost               = ctxKey("host")
	ctxKeyPort               = ctxKey("port")
	ctxKeyUrl                = ctxKey("url")
)

func recordProxyDataAware(ctx filters.Context) (filters.Context, map[ctxKey]interface{}) {
	var rpData map[ctxKey]interface{}
	a := ctx.Value(ctxKeyRecorderProxyAware)
	if a != nil {
		rpData = a.(map[ctxKey]interface{})
	} else {
		rpData = make(map[ctxKey]interface{}, 4)
		ctx = ctx.WithValue(ctxKeyRecorderProxyAware, rpData)
	}
	return ctx, rpData
}

func SetHostPort(ctx filters.Context, host, port string) filters.Context {
	c, a := recordProxyDataAware(ctx)
	a[ctxKeyHost] = host
	a[ctxKeyPort] = port
	return c
}

func SetUri(ctx filters.Context, uri *url.URL) filters.Context {
	c, a := recordProxyDataAware(ctx)
	a[ctxKeyUrl] = uri
	return c
}

func ResolveAndSetUri(ctx filters.Context, uri *url.URL) (filters.Context, *url.URL) {
	c, a := recordProxyDataAware(ctx)
	oldUri, _ := a[ctxKeyUrl].(*url.URL)
	if oldUri == nil {
		a[ctxKeyUrl] = uri
		return c, uri
	}
	newUri := oldUri.ResolveReference(uri)
	a[ctxKeyUrl] = uri
	return c, newUri
}

func SetRecordContext(ctx filters.Context, rc *RecordContext) filters.Context {
	c, a := recordProxyDataAware(ctx)
	a[ctxKeyRCTX] = rc

	// Start listener for cancellation
	go rc.cleanup(c)

	return c
}

func GetHost(ctx filters.Context) (host string) {
	host, _ = getValue(ctx, ctxKeyHost).(string)
	return
}

func GetPort(ctx filters.Context) (port string) {
	port, _ = getValue(ctx, ctxKeyPort).(string)
	return
}

func GetUri(ctx filters.Context) (uri *url.URL) {
	uri, _ = getValue(ctx, ctxKeyUrl).(*url.URL)
	return
}

func GetRecordContext(ctx filters.Context) (recordContext *RecordContext) {
	recordContext, _ = getValue(ctx, ctxKeyRCTX).(*RecordContext)
	return
}

func getValue(ctx filters.Context, key ctxKey) interface{} {
	a := ctx.Value(ctxKeyRecorderProxyAware)
	if a == nil {
		return nil
	}
	rpData := a.(map[ctxKey]interface{})
	return rpData[key]
}
