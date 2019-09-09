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
	"github.com/sirupsen/logrus"
)
import gerrs "github.com/pkg/errors"

// proxyError is the struct of recorder proxy error
type proxyError struct {
	code    ErrorCode
	message string
	cause   error
}

func (e *proxyError) Error() string {
	errMsg := fmt.Sprintf("%s %s", e.code, e.message)
	if nil == e.cause {
		return errMsg
	}

	return errMsg + ": " + e.cause.Error()
}

func (e *proxyError) Cause() error {
	return e.cause
}

func (e *proxyError) Code() ErrorCode {
	return e.code
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
		return UnknownError
	}
	return cd.Code()
}

// Errorf formats an error with format
func Errorf(code ErrorCode, format string, a ...interface{}) error {
	return &proxyError{
		code:    code,
		message: fmt.Sprintf(format, a...),
	}
}

// Error constructs a new error
func Error(code ErrorCode, message string) error {
	return &proxyError{
		code:    code,
		message: message,
	}
}

// Wrapf warps an error with a error code and a format message
func Wrapf(err error, code ErrorCode, format string, a ...interface{}) error {
	return Wrap(err, code, fmt.Sprintf(format, a...))
}

// Wrap waps an error with an error and a message
func Wrap(err error, code ErrorCode, message string) error {
	if err == nil {
		return nil
	}
	return &proxyError{
		code:    code,
		message: message,
		cause:   err,
	}
}

func LogErrorf(code ErrorCode, format string, a ...interface{}) {
	LogError(code, fmt.Sprintf(format, a...))
}

func LogError(code ErrorCode, message string) {
	logrus.WithFields(logrus.Fields{
		"code": code,
	}).Error(message)
}
