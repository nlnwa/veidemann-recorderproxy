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
	"github.com/getlantern/proxy/filters"
	"github.com/nlnwa/veidemann-recorderproxy/logger"
	"net/url"
	"sync"
)

type ctxKey string

const (
	ctxKeyRecorderProxyAware = ctxKey("recorderProxyAware")
	ctxKeyRCTX               = ctxKey("recordContext")
	ctxKeyHost               = ctxKey("host")
	ctxKeyUrl                = ctxKey("url")
	ctxKeyConnectErr         = ctxKey("connectErr")
)

var dataAwareMutex sync.RWMutex

func getRecordProxyDataAware(ctx context.Context) map[ctxKey]interface{} {
	var rpData map[ctxKey]interface{}
	a := ctx.Value(ctxKeyRecorderProxyAware)
	if a == nil {
		logger.Log.Panic("BUG: Tried to get RecordProxyDataAware from uninitialized context")
	}
	rpData = a.(map[ctxKey]interface{})
	return rpData
}

func RecordProxyDataAware(ctx context.Context) context.Context {
	a := ctx.Value(ctxKeyRecorderProxyAware)
	if a == nil {
		rpData := make(map[ctxKey]interface{}, 4)
		ctx = context.WithValue(ctx, ctxKeyRecorderProxyAware, rpData)
	}
	return ctx
}

func SetHost(ctx context.Context, host string) {
	setValue(ctx, ctxKeyHost, host)
}

func SetUri(ctx context.Context, uri *url.URL) {
	setValue(ctx, ctxKeyUrl, uri)
}

func SetRecordContext(ctx context.Context, rc *RecordContext) {
	setValue(ctx, ctxKeyRCTX, rc)
}

func SetConnectError(ctx context.Context, err error) {
	setValue(ctx, ctxKeyConnectErr, err)
}

func SetConnectErrorIfNotExists(ctx context.Context, err error) {
	dataAwareMutex.Lock()
	defer dataAwareMutex.Unlock()

	a := getRecordProxyDataAware(ctx)
	if a[ctxKeyConnectErr] == nil {
		a[ctxKeyConnectErr] = err
	}
}

func GetHost(ctx context.Context) (host string) {
	host, _ = getValue(ctx, ctxKeyHost).(string)
	return
}

func GetUri(ctx context.Context) (uri *url.URL) {
	uri, _ = getValue(ctx, ctxKeyUrl).(*url.URL)
	return
}

func GetRecordContext(ctx context.Context) (recordContext *RecordContext) {
	recordContext, _ = getValue(ctx, ctxKeyRCTX).(*RecordContext)
	return
}

func GetConnectError(ctx context.Context) (err error) {
	err, _ = getValue(ctx, ctxKeyConnectErr).(error)
	return
}

func getValue(ctx context.Context, key ctxKey) interface{} {
	a := ctx.Value(ctxKeyRecorderProxyAware)
	if a == nil {
		return nil
	}

	dataAwareMutex.RLock()
	defer dataAwareMutex.RUnlock()

	rpData := a.(map[ctxKey]interface{})
	return rpData[key]
}

func setValue(ctx context.Context, key ctxKey, value interface{}) {
	dataAwareMutex.Lock()
	defer dataAwareMutex.Unlock()

	a := getRecordProxyDataAware(ctx)
	a[key] = value
}

func WrapIfNecessary(ctx context.Context) filters.Context {
	fc, ok := ctx.(filters.Context)
	if ok {
		return fc
	} else {
		return filters.AdaptContext(ctx)
	}
}
