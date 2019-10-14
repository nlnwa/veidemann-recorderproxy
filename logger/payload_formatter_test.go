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
	"github.com/sirupsen/logrus"
	"testing"
)

func TestFormatPayload(t *testing.T) {
	type args struct {
		payload      []byte
		cutLength    int
		runesPerLine int
	}
	tests := []struct {
		name     string
		logLevel logrus.Level
		args     args
		want     string
	}{
		{"debug_all", logrus.DebugLevel, args{[]byte("abc\xbd\xb2ef\xe2\x8c\x98g"), 9, 9}, "abc\xbf\xbfef⌘g (11 bytes)"},
		{"debug_cut", logrus.DebugLevel, args{[]byte("abc\xbd\xb2ef\xe2\x8c\x98g"), 6, 9}, "abc\xbf\xbfe ... (11 bytes)"},
		{"trace_one_line", logrus.TraceLevel, args{[]byte("abc\xbd\xb2ef\xe2\x8c\x98g"), 10, 9}, "0061 0062 0063 00BD 00B2 0065 0066 2318 0067 abc\xbf\xbfef⌘g"},
		{"trace_split_line", logrus.TraceLevel, args{[]byte("abc\xbd\xb2ef\xe2\x8c\x98g"), 5, 10}, "0061 0062 0063 00BD 00B2 0065 0066 2318 0067      abc\xbf\xbfef⌘g"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logrus.SetLevel(tt.logLevel)
			if got := FormatPayload(tt.args.payload, len(tt.args.payload), tt.args.cutLength, tt.args.runesPerLine); got != tt.want {
				t.Errorf("FormatPayload() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_formatTerminalSafe(t *testing.T) {
	type args struct {
		bytes        []byte
		runesPerLine int
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{"one_line1", args{[]byte("abc\xbd\xb2ef\xe2\x8c\x98g"), 9}, "0061 0062 0063 00BD 00B2 0065 0066 2318 0067 abc\xbf\xbfef⌘g"},
		{"one_line2", args{[]byte("abc\xbd\xb2ef\xe2\x8c\x98g"), 10}, "0061 0062 0063 00BD 00B2 0065 0066 2318 0067      abc\xbf\xbfef⌘g"},
		{"split_lines1", args{[]byte("abc\xbd\xb2ef\xe2\x8c\x98g"), 4}, "0061 0062 0063 00BD abc\xbf\n00B2 0065 0066 2318 \xbfef⌘\n0067                g"},
		{"split_lines2", args{[]byte("abc\xbd\xb2ef\xe2\x8c\x98g"), 3}, "0061 0062 0063 abc\n00BD 00B2 0065 \xbf\xbfe\n0066 2318 0067 f⌘g"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatTerminalSafe(tt.args.bytes, tt.args.runesPerLine); got != tt.want {
				t.Errorf("formatTerminalSafe() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_replaceUnsafe(t *testing.T) {
	tests := []struct {
		name      string
		bytes     []byte
		wantCode  string
		wantVal   string
		wantWidth int
	}{
		{"ascii", []byte("abcd"), "0061", "a", 1},
		{"printable1", []byte("æøå"), "00E6", "æ", 2},
		{"printable2", []byte("\xe2\x8c\x98g"), "2318", "⌘", 3},
		{"non_printable", []byte("\x07\xb2ef"), "0007", "\xbf", 1},
		{"invalid", []byte("\xbd\xb2ef\xe2\x8c\x98g"), "00BD", "\xbf", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCode, gotVal, gotWidth := replaceUnsafe(tt.bytes)
			if gotCode != tt.wantCode {
				t.Errorf("replaceUnsafe() gotCode = %v, want %v", gotCode, tt.wantCode)
			}
			if gotVal != tt.wantVal {
				t.Errorf("replaceUnsafe() gotVal = %v, want %v", gotVal, tt.wantVal)
			}
			if gotWidth != tt.wantWidth {
				t.Errorf("replaceUnsafe() gotWidth = %v, want %v", gotWidth, tt.wantWidth)
			}
		})
	}
}
