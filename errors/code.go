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

import "strconv"

// ErrorCode is data type of error codes for different kind of errors
type ErrorCode int32

// UnknownError is the unknown error
const RuntimeException ErrorCode = -5

const (
	ConnectFailed      ErrorCode = -2
	ConnectBroken      ErrorCode = -3
	HttpTimeout        ErrorCode = -4
	DomainLookupFailed ErrorCode = -6
	EmptyResponse      ErrorCode = -404
	CanceledByBrowser  ErrorCode = -5011
	PrecludedByRobots  ErrorCode = -9998
)

func (e ErrorCode) String() string {
	return strconv.Itoa(int(e))
}

func (e ErrorCode) Int32() int32 {
	return int32(e)
}

// TCP socket errors
const (
	Tcp110 string = "110"
	Tcp111 string = "111"
	Tcp113 string = "113"
)

var TcpSocketErrTxt = map[string]string{
	Tcp110: "Connection timed out",
	Tcp111: "connect: connection refused",
	Tcp113: "No route to host",
}
