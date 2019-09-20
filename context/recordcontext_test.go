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
	"testing"
	"time"
)

func Test_recordContext_cleanup(t *testing.T) {
	closeFuncCalled := false
	c1, cancel := context.WithCancel(context.Background())
	c2 := filters.AdaptContext(c1)
	r := NewRecordContext()
	SetRecordContext(c2, r)
	r.CloseFunc = func() {
		closeFuncCalled = true
	}

	if closeFuncCalled {
		t.Error("Expected CloseFunc not to be called")
	}
	if r.closed {
		t.Error("Expected context not to be closed")
	}

	cancel()
	// Give the goroutine a chance to finish
	time.Sleep(10 * time.Millisecond)

	if !closeFuncCalled {
		t.Error("Expected CloseFunc to be called")
	}
	if !r.closed {
		t.Error("Expected context to be closed")
	}
}
