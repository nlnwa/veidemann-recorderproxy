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
	"github.com/nlnwa/veidemann-recorderproxy/errors"
	"github.com/nlnwa/veidemann-recorderproxy/logger"
	"strings"
)

func handleSquidErrorString(squidErr string) (err error) {
	l := logger.LogWithComponent("SQUID")
	l.Debugf("Handle Squid error response: %v", squidErr)
	switch {
	case strings.HasPrefix(squidErr, "ERR_CONNECT_FAIL"):
		tokens := strings.SplitN(squidErr, " ", 2)
		if len(tokens) == 2 {
			detail := errors.TcpSocketErrTxt[tokens[1]]
			err = errors.Error(errors.ConnectFailed, "CONNECT_FAILED", detail)
		} else {
			l.Warnf("Got unrecognized error from Squid: %s", squidErr)
			err = errors.Error(errors.RuntimeException, "UNKNOWN_ERROR", squidErr)
		}
	case strings.HasPrefix(squidErr, "ERR_ZERO_SIZE_OBJECT"):
		err = errors.Error(errors.EmptyResponse, "EMPTY_RESPONSE", "Empty reply from server")
	default:
		l.Warnf("Got unrecognized error from Squid: %s", squidErr)
		err = errors.Error(errors.RuntimeException, "UNKNOWN_ERROR", squidErr)
	}

	return
}
