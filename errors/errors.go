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

package errors

import (
	"fmt"
	"github.com/nlnwa/veidemann-api-go/commons/v1"
	"net/http"
)
import gerrs "github.com/pkg/errors"

// ProxyError is the struct of recorder proxy error
type ProxyError struct {
	code       ErrorCode
	message    string
	detail     string
	cause      error // the root cause for this error
	statusCode int   // http status code
}

func (e *ProxyError) Error() string {
	errMsg := fmt.Sprintf("Code: %s, Msg: %s", e.code, e.message)
	if e.detail != "" {
		errMsg = errMsg + ", Detail: " + e.detail
	}
	if nil == e.cause {
		return errMsg
	}

	return errMsg + ", Cause: " + e.cause.Error()
}

func (e *ProxyError) Cause() error {
	return e.cause
}

func (e *ProxyError) Code() ErrorCode {
	return e.code
}

func (e *ProxyError) Message() string {
	return e.message
}

func (e *ProxyError) Detail() string {
	return e.detail
}

func (e *ProxyError) HttpStatusCode() string {
	return e.detail
}

// Cause returns the cause error of this error
func Cause(err error) error {
	return gerrs.Cause(err)
}

// Code returns the error code
func Code(err error) ErrorCode {
	type coder interface {
		Code() ErrorCode
	}

	cd, ok := err.(coder)
	if !ok {
		return RuntimeException
	}
	return cd.Code()
}

// Message returns the error message
func Message(err error) string {
	type msg interface {
		Message() string
	}

	m, ok := err.(msg)
	if !ok {
		fmt.Printf("ERROR FROM MSG: %v\n", err)
		return err.Error()
	}
	return m.Message()
}

// Detail returns the error detail message
func Detail(err error) string {
	type det interface {
		Detail() string
	}

	d, ok := err.(det)
	if !ok {
		return err.Error()
	}
	return d.Detail()
}

// HttpStatusCode returns the http status code which will be sent to client
func HttpStatusCode(err error) int {
	type st interface {
		HttpStatusCode() int
	}

	s, ok := err.(st)
	if !ok {
		return http.StatusServiceUnavailable
	}
	return s.HttpStatusCode()
}

// Error constructs a new error
func Error(code ErrorCode, message, detail string) error {
	return &ProxyError{
		code:       code,
		message:    message,
		detail:     detail,
		statusCode: http.StatusServiceUnavailable,
	}
}

// Wrap waps an error with an error and a message
func Wrap(err error, code ErrorCode, message, detail string) error {
	if err == nil {
		return nil
	}
	return &ProxyError{
		code:       code,
		message:    message,
		cause:      err,
		detail:     detail,
		statusCode: http.StatusServiceUnavailable,
	}
}

// Error constructs a new error
func ErrorInternal(code ErrorCode, message, detail string) error {
	return &ProxyError{
		code:       code,
		message:    message,
		detail:     detail,
		statusCode: http.StatusBadGateway,
	}
}

// Wrap waps an error with an error and a message
func WrapInternalError(err error, code ErrorCode, message, detail string) error {
	if err == nil {
		return nil
	}
	return &ProxyError{
		code:       code,
		message:    message,
		cause:      err,
		detail:     detail,
		statusCode: http.StatusBadGateway,
	}
}

func AsCommonsError(err error) *commons.Error {
	return &commons.Error{
		Code:   Code(err).Int32(),
		Msg:    Message(err),
		Detail: Detail(err),
	}
}
