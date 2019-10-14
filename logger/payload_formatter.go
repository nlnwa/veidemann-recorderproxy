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

package logger

import (
	"fmt"
	"github.com/sirupsen/logrus"
	"unicode"
	"unicode/utf8"
)

func FormatPayload(payload []byte, length, cutLength, runesPerLine int) string {
	if length > len(payload) {
		length = len(payload)
	}
	payload = payload[:length]

	if logrus.IsLevelEnabled(logrus.TraceLevel) {
		return formatTerminalSafe(payload, runesPerLine)
	} else {
		var result string
		var hasMore bool
		for c, i := 0, 0; i < len(payload); c++ {
			if c >= cutLength {
				hasMore = true
				break
			}
			_, val, width := replaceUnsafe(payload[i:])
			result += val
			i += width
		}
		if hasMore {
			result = fmt.Sprintf("%s ... (%d bytes)", result, length)
		} else {
			result = fmt.Sprintf("%s (%d bytes)", result, length)
		}
		return result
	}
}

func formatTerminalSafe(bytes []byte, runesPerLine int) string {
	var result, codes, values string

	for c, i := 0, 0; i < len(bytes); c++ {
		code, val, width := replaceUnsafe(bytes[i:])
		codes = codes + code
		values = values + val
		if c%runesPerLine == runesPerLine-1 {
			if result != "" {
				result += "\n"
			}
			result += fmt.Sprintf("%-[1]*.[1]*s %s", (runesPerLine*5)-1, codes, values)
			codes = ""
			values = ""
		} else {
			codes += " "
		}
		i += width
	}
	if codes != "" {
		if result != "" {
			result += "\n"
		}
		result += fmt.Sprintf("%-[1]*.[1]*s %s", (runesPerLine*5)-1, codes, values)
	}
	return result
}

func replaceUnsafe(bytes []byte) (code string, val string, width int) {
	const unprintable = "\xbf"
	runeValue, width := utf8.DecodeRune(bytes)
	if runeValue == utf8.RuneError {
		code = fmt.Sprintf("%04X", bytes[:width][0])
		val = unprintable
	} else {
		code = fmt.Sprintf("%04X", runeValue)
		if unicode.IsGraphic(runeValue) {
			val = string(runeValue)
		} else {
			val = unprintable
		}
	}
	return
}
