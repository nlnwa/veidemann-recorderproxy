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
	"bytes"
	"fmt"
	"github.com/sirupsen/logrus"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	red    = 31
	yellow = 33
	blue   = 36
	gray   = 37
)

var baseTimestamp time.Time

func init() {
	baseTimestamp = time.Now()
}

// FieldMap allows customization of the key names for default fields.
type FieldMap map[string]string

func (f FieldMap) resolve(key string) string {
	if k, ok := f[key]; ok {
		return k
	}

	return string(key)
}

var colors = []int{31, 32, 33, 34, 35, 36}
var cIdx = 0

// CompColorMap keeps track of color for each component.
type CompColorMap map[string]int

func (f CompColorMap) resolve(key string) int {
	if col, ok := f[key]; ok {
		return col
	}

	col := colors[cIdx]
	cIdx++
	if cIdx >= len(colors) {
		cIdx = 0
	}
	f[key] = col
	return col
}

// TextFormatter formats logs into text
type TextFormatter struct {
	// Override coloring based on CLICOLOR and CLICOLOR_FORCE. - https://bixense.com/clicolors/
	EnvironmentOverrideColors bool

	// Disable timestamp logging. useful when output is redirected to logging
	// system that already adds timestamps.
	DisableTimestamp bool

	// Enable logging the full timestamp when a TTY is attached instead of just
	// the time passed since beginning of execution.
	FullTimestamp bool

	// TimestampFormat to use for display when a full timestamp is printed
	TimestampFormat string

	// The fields are sorted by default for a consistent output. For applications
	// that log extremely frequently and don't use the JSON formatter this may not
	// be desired.
	DisableSorting bool

	// The keys sorting function, when uninitialized it uses sort.Strings.
	SortingFunc func([]string)

	// Disables the truncation of the level text to 4 characters.
	DisableLevelTruncation bool

	// QuoteEmptyFields will wrap empty fields in quotes if true
	QuoteEmptyFields bool

	// FieldMap allows users to customize the names of keys for default fields.
	// As an example:
	// formatter := &TextFormatter{
	//     FieldMap: FieldMap{
	//         FieldKeyTime:  "@timestamp",
	//         FieldKeyLevel: "@level",
	//         FieldKeyMsg:   "@message"}}
	FieldMap     FieldMap
	ComponentMap FieldMap
	ColorMap     CompColorMap

	// CallerPrettyfier can be set by the user to modify the content
	// of the function and file keys in the data when ReportCaller is
	// activated. If any of the returned value is the empty string the
	// corresponding key will be removed from fields.
	CallerPrettyfier func(*runtime.Frame) (function string, file string)

	terminalInitOnce sync.Once
}

func (f *TextFormatter) init(entry *logrus.Entry) {
	f.ColorMap = CompColorMap{}
	f.ComponentMap = FieldMap{
		"ContentWriter":     "CWR",
		"BrowserController": "BCR",
		"DNSResolver":       "DNS",
	}
}

func (f *TextFormatter) isColored() bool {
	return true
}

// Format renders a single log entry
func (f *TextFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	data := make(logrus.Fields)
	for k, v := range entry.Data {
		data[k] = v
	}
	prefixFieldClashes(data, f.FieldMap, entry.HasCaller())
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}

	var funcVal, fileVal string

	fixedKeys := make([]string, 0, 4+len(data))
	if !f.DisableTimestamp {
		fixedKeys = append(fixedKeys, f.FieldMap.resolve(logrus.FieldKeyTime))
	}
	fixedKeys = append(fixedKeys, f.FieldMap.resolve(logrus.FieldKeyLevel))
	if entry.Message != "" {
		fixedKeys = append(fixedKeys, f.FieldMap.resolve(logrus.FieldKeyMsg))
	}
	//if entry.err != "" {
	//	fixedKeys = append(fixedKeys, f.FieldMap.resolve(logrus.FieldKeyLogrusError))
	//}
	if entry.HasCaller() {
		if f.CallerPrettyfier != nil {
			funcVal, fileVal = f.CallerPrettyfier(entry.Caller)
		} else {
			funcVal = entry.Caller.Function
			fileVal = fmt.Sprintf("%s:%d", entry.Caller.File, entry.Caller.Line)
		}

		if funcVal != "" {
			fixedKeys = append(fixedKeys, f.FieldMap.resolve(logrus.FieldKeyFunc))
		}
		if fileVal != "" {
			fixedKeys = append(fixedKeys, f.FieldMap.resolve(logrus.FieldKeyFile))
		}
	}

	if !f.DisableSorting {
		if f.SortingFunc == nil {
			sort.Strings(keys)
			fixedKeys = append(fixedKeys, keys...)
		} else {
			if !f.isColored() {
				fixedKeys = append(fixedKeys, keys...)
				f.SortingFunc(fixedKeys)
			} else {
				f.SortingFunc(keys)
			}
		}
	} else {
		fixedKeys = append(fixedKeys, keys...)
	}

	var b *bytes.Buffer
	if entry.Buffer != nil {
		b = entry.Buffer
	} else {
		b = &bytes.Buffer{}
	}

	f.terminalInitOnce.Do(func() { f.init(entry) })

	timestampFormat := f.TimestampFormat
	if timestampFormat == "" {
		timestampFormat = time.RFC3339
	}
	if f.isColored() {
		f.printColored(b, entry, keys, data, timestampFormat)
	} else {

		for _, key := range fixedKeys {
			var value interface{}
			switch {
			case key == f.FieldMap.resolve(logrus.FieldKeyTime):
				value = entry.Time.Format(timestampFormat)
			case key == f.FieldMap.resolve(logrus.FieldKeyLevel):
				value = entry.Level.String()
			case key == f.FieldMap.resolve(logrus.FieldKeyMsg):
				value = entry.Message
			//case key == f.FieldMap.resolve(logrus.FieldKeyLogrusError):
			//	value = entry.err
			case key == f.FieldMap.resolve(logrus.FieldKeyFunc) && entry.HasCaller():
				value = funcVal
			case key == f.FieldMap.resolve(logrus.FieldKeyFile) && entry.HasCaller():
				value = fileVal
			default:
				value = data[key]
			}
			f.appendKeyValue(b, key, value)
		}
	}

	b.WriteByte('\n')
	return b.Bytes(), nil
}

func (f *TextFormatter) printColored(b *bytes.Buffer, entry *logrus.Entry, keys []string, data logrus.Fields, timestampFormat string) {
	var levelColor int
	switch entry.Level {
	case logrus.DebugLevel, logrus.TraceLevel:
		levelColor = gray
	case logrus.WarnLevel:
		levelColor = yellow
	case logrus.ErrorLevel, logrus.FatalLevel, logrus.PanicLevel:
		levelColor = red
	default:
		levelColor = blue
	}

	levelText := strings.ToUpper(entry.Level.String())
	if !f.DisableLevelTruncation {
		levelText = levelText[0:4]
	}

	// Remove a single newline if it already exists in the message to keep
	// the behavior of logrus text_formatter the same as the stdlib log package
	entry.Message = strings.TrimSuffix(entry.Message, "\n")

	caller := ""
	if entry.HasCaller() {
		funcVal := fmt.Sprintf("%s()", entry.Caller.Function)
		fileVal := fmt.Sprintf("%s:%d", entry.Caller.File, entry.Caller.Line)

		if f.CallerPrettyfier != nil {
			funcVal, fileVal = f.CallerPrettyfier(entry.Caller)
		}

		if fileVal == "" {
			caller = funcVal
		} else if funcVal == "" {
			caller = fileVal
		} else {
			caller = fileVal + " " + funcVal
		}
	}

	compColor := 0
	compName := "        "
	comp, ok := data["component"]
	if ok {
		var proto, key string
		proto, _, key, compColor = f.parseComp(comp.(string))
		compName = proto + key
	}

	sess, ok := data["session"]
	session := ""
	if ok {
		session = strconv.FormatInt(sess.(int64), 10)
	}

	fmt.Fprintf(b, "\x1b[%dm%s\x1b[0m[%09d]%s \x1b[%dm%-8.8s\x1b[0m %2s %-44s ",
		levelColor, levelText, int(entry.Time.Sub(baseTimestamp)/time.Nanosecond), caller, compColor, compName, session, entry.Message)
	for _, k := range keys {
		if k == "component" {
			continue
		}
		v := data[k]
		fmt.Fprintf(b, " \x1b[%dm%s\x1b[0m=", levelColor, k)
		f.appendValue(b, v)
	}
}

var compNameRegex, _ = regexp.Compile("([^:]*:)?(.*)")

func (f *TextFormatter) parseComp(comp string) (proto, name, key string, color int) {
	t := compNameRegex.FindStringSubmatch(comp)
	if len(t) == 1 {
		proto = "    "
		name = t[1]
	} else {
		proto = t[1]
		name = t[2]
	}
	key = f.ComponentMap.resolve(name)
	color = f.ColorMap.resolve(key)
	return
}

func (f *TextFormatter) needsQuoting(text string) bool {
	if f.QuoteEmptyFields && len(text) == 0 {
		return true
	}
	for _, ch := range text {
		if !((ch >= 'a' && ch <= 'z') ||
			(ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') ||
			ch == '-' || ch == '.' || ch == '_' || ch == '/' || ch == '@' || ch == '^' || ch == '+') {
			return true
		}
	}
	return false
}

func (f *TextFormatter) appendKeyValue(b *bytes.Buffer, key string, value interface{}) {
	if b.Len() > 0 {
		b.WriteByte(' ')
	}
	b.WriteString(key)
	b.WriteByte('=')
	f.appendValue(b, value)
}

func (f *TextFormatter) appendValue(b *bytes.Buffer, value interface{}) {
	stringVal, ok := value.(string)
	if !ok {
		stringVal = fmt.Sprint(value)
	}

	if !f.needsQuoting(stringVal) {
		b.WriteString(stringVal)
	} else {
		b.WriteString(fmt.Sprintf("%q", stringVal))
	}
}

// This is to not silently overwrite `time`, `msg`, `func` and `level` fields when
// dumping it. If this code wasn't there doing:
//
//  logrus.WithField("level", 1).Info("hello")
//
// Would just silently drop the user provided level. Instead with this code
// it'll logged as:
//
//  {"level": "info", "fields.level": 1, "msg": "hello", "time": "..."}
//
// It's not exported because it's still using Data in an opinionated way. It's to
// avoid code duplication between the two default formatters.
func prefixFieldClashes(data logrus.Fields, fieldMap FieldMap, reportCaller bool) {
	timeKey := fieldMap.resolve(logrus.FieldKeyTime)
	if t, ok := data[timeKey]; ok {
		data["fields."+timeKey] = t
		delete(data, timeKey)
	}

	msgKey := fieldMap.resolve(logrus.FieldKeyMsg)
	if m, ok := data[msgKey]; ok {
		data["fields."+msgKey] = m
		delete(data, msgKey)
	}

	levelKey := fieldMap.resolve(logrus.FieldKeyLevel)
	if l, ok := data[levelKey]; ok {
		data["fields."+levelKey] = l
		delete(data, levelKey)
	}

	logrusErrKey := fieldMap.resolve(logrus.FieldKeyLogrusError)
	if l, ok := data[logrusErrKey]; ok {
		data["fields."+logrusErrKey] = l
		delete(data, logrusErrKey)
	}

	// If reportCaller is not set, 'func' will not conflict.
	if reportCaller {
		funcKey := fieldMap.resolve(logrus.FieldKeyFunc)
		if l, ok := data[funcKey]; ok {
			data["fields."+funcKey] = l
		}
		fileKey := fieldMap.resolve(logrus.FieldKeyFile)
		if l, ok := data[fileKey]; ok {
			data["fields."+fileKey] = l
		}
	}
}
