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

package recorderproxy

import (
	"crypto/tls"
	errors3 "errors"
	"github.com/getlantern/errors"
	"github.com/getlantern/proxy/filters"
	context2 "github.com/nlnwa/veidemann-recorderproxy/context"
	errors2 "github.com/nlnwa/veidemann-recorderproxy/errors"
	"github.com/nlnwa/veidemann-recorderproxy/logger"
	"net"
	"net/http"
)

// ErrorHandlerFilter is a filter which initializes the context with sessions to external services.
type ErrorHandlerFilter struct {
	hasNextProxy bool
}

func (f *ErrorHandlerFilter) Apply(ctx filters.Context, req *http.Request, next filters.Next) (resp *http.Response, context filters.Context, err error) {
	l := context2.LogWithContextAndRequest(ctx, req, "FLT:err")

	connectErr := context2.GetConnectError(ctx)
	if connectErr != nil {
		l.WithError(connectErr).WithField("method", req.Method).Debug("Handle connect error")
		e := f.normalizeError(connectErr, l)
		return handleRequestError(ctx, req, e)
	}

	resp, context, err = next(ctx, req)

	if err != nil {
		l.WithError(err).Debug("Handle roundtrip error")

		e := f.normalizeError(err, l)
		return handleRequestError(ctx, req, e)
	}

	squidErr := resp.Header.Get("X-Squid-Error")
	if squidErr != "" {
		e := handleSquidErrorString(squidErr)
		if e != nil {
			return handleRequestError(ctx, req, e)
		}
	}

	return
}

func (f *ErrorHandlerFilter) normalizeError(err error, l *logger.Logger) error {
	l = l.WithError(err)
	l.Tracef("Normalize error (type: %T): %v", err, err)
	var e1 *errors2.ProxyError
	var e2 *net.OpError
	var e3 tls.RecordHeaderError
	var e4 errors.Error
	switch {
	case errors3.As(err, &e1):
		return err
	case errors3.As(err, &e2):
		return f.normalizeNetOpError(e2, l)
	case errors3.As(err, &e3):
		return errors2.Wrap(&e3, errors2.ConnectFailed, "CONNECT_FAILED", "tls: handshake failure")
	case errors3.As(err, &e4):
		return f.normalizeGetlanternProxyError(e4, l)
	default:
		switch s := err.Error(); {
		case s == "EOF":
			return errors2.Wrap(err, errors2.EmptyResponse, "EMPTY_RESPONSE", "Empty reply from server")
		default:
			l.Debugf("Unknown error (type: %T): %v. Returning -5 UNKNOWN_ERROR", err, err)
			return errors2.Wrap(err, errors2.RuntimeException, "UNKNOWN_ERROR", s)
		}
	}
}

func (f *ErrorHandlerFilter) normalizeNetOpError(err *net.OpError, l *logger.Logger) error {
	l.Tracef("Normalize error (type: %T) (op: %s): %v", err, err.Op, err)
	var e error
	switch err.Op {
	case "dial":
		e = errors2.Wrap(err.Err, errors2.ConnectFailed, "CONNECT_FAILED", err.Err.Error())
	case "remote error":
		e = errors2.Wrap(err.Err, errors2.ConnectFailed, "CONNECT_FAILED", err.Err.Error())
	case "local error":
		e = errors2.Wrap(err.Err, errors2.ConnectFailed, "CONNECT_FAILED", err.Err.Error())
	default:
		l.Debugf("Unknown error operation (type: %T): %v. Returning -2 CONNECT_FAILED", err, err)
		e = errors2.Wrap(err, errors2.ConnectFailed, "CONNECT_FAILED", err.Error())
	}
	return e
}

func (f *ErrorHandlerFilter) normalizeGetlanternProxyError(err errors.Error, l *logger.Logger) error {
	l.Tracef("Normalize getlantern error (type: %T) (root cause type: %T): %v", err, err.RootCause(), err.ErrorClean())
	switch e := err.RootCause().(type) {
	case *net.OpError:
		return f.normalizeNetOpError(e, l)
	default:
		var e1 tls.RecordHeaderError
		var e2 *net.OpError
		if errors3.As(err.RootCause(), &e1) {
			return errors2.Wrap(e, errors2.EmptyResponse, "EMPTY_RESPONSE", "Empty reply from server")
		}
		if errors3.As(err.RootCause(), &e2) {
			return f.normalizeNetOpError(e2, l)
		}
		switch s := e.Error(); {
		case s == "EOF":
			return errors2.Wrap(e, errors2.EmptyResponse, "EMPTY_RESPONSE", "Empty reply from server")
		default:
			l.Debugf("Unknown root cause (type: %T) for proxy err '%s': %v. Returning -5 UNKNOWN_ERROR", err.RootCause(), err.ErrorClean(), err.RootCause())
			return errors2.Wrap(e, errors2.RuntimeException, "UNKNOWN_ERROR", s)
		}
	}
}
