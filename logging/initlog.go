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

package logging

import (
	"flag"
	"fmt"
	"github.com/nlnwa/veidemann-recorderproxy/errors"
	stdLog "log"
	"strings"
)
import log "github.com/sirupsen/logrus"

const (
	FORMATTER_TEXT   = "text"
	FORMATTER_JSON   = "json"
	FORMATTER_LOGFMT = "logfmt"
)

func InitLog(level, formatter string, logMethod bool) {
	stdLog.SetOutput(log.StandardLogger().Writer())

	// Configure the log level, defaults to "INFO"
	logLevel, err := log.ParseLevel(level)
	if err != nil {
		fmt.Println(errors.Wrapf(err, errors.LoggingError, "failed to parse log level: %q", level))
		flag.Usage()
		return
	}
	log.SetLevel(logLevel)

	// Configure the log formatter, defaults to ASCII formatter
	switch strings.ToLower(formatter) {
	case FORMATTER_TEXT:
		log.SetFormatter(&log.TextFormatter{})
	case FORMATTER_LOGFMT:
		log.SetFormatter(&log.TextFormatter{
			DisableColors: true,
			FullTimestamp: true,
		})
	case FORMATTER_JSON:
		log.SetFormatter(&log.JSONFormatter{})
	default:
		fmt.Println(errors.Errorf(errors.LoggingError, "unknown formatter type: %q", formatter))
		flag.Usage()
		return
	}

	if logMethod {
		log.SetReportCaller(true)
	}
}
