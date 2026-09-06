// Copyright (c) the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause licence that can be
// found in the LICENSE file.

//go:build darwin

package hotkey

import (
	"sync"
	"testing"
)

// ⛔ WHAT A GOROUTINE GETS IS THE TABLE, AND IT HAS TO BE THE SAME ANSWER.
//
// HIToolbox is asked on the main thread only: TISGetInputSourceProperty asserts
// a dispatch queue and a failed assertion traps rather than returning, so a
// caller off the main thread is served from what the main thread read. That is
// only worth anything if the two agree.
//
// ⚠ NO TEST HERE CAN SEE THE CRASH IT PREVENTS. The assertion is armed by
// whatever HIToolbox sets up for a real application; asked from a goroutine in
// a plain program -- with a run loop turning, with hot keys registered first,
// with eight callers at once -- it answers normally. It was found by
// symbolising the faulting address out of a bundled app's own log, and the
// fix's proof is that app no longer dying. What this can hold is the contract.
func TestAGoroutineIsToldWhatTheMainThreadRead(t *testing.T) {
	keys := []Key{KeyA, KeyEqual, KeyMinus, KeyN1, KeySlash, KeyISOSection}

	want := map[Key]string{}
	for _, k := range keys {
		want[k] = k.Char() // the test goroutine is on the main thread
	}
	// Something has to have been read, or this asserts that "" equals "".
	any := false
	for _, s := range want {
		if s != "" {
			any = true
		}
	}
	if !any {
		t.Skip("this keyboard prints nothing for any of these keys")
	}

	var wg sync.WaitGroup
	got := make([]map[Key]string, 4)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := map[Key]string{}
			for _, k := range keys {
				m[k] = k.Char()
			}
			got[i] = m
		}()
	}
	wg.Wait()
	for i, m := range got {
		for _, k := range keys {
			if m[k] != want[k] {
				t.Errorf("goroutine %d: key %#x is %q there and %q on the main thread",
					i, uint16(k), m[k], want[k])
			}
		}
	}
}

// The table is filled for EVERY key at once, off one input source, so that the
// caller after this one is served whatever it asks for -- and because a table
// read in pieces is a table half of which is from before the person switched
// layout.
func TestTheWholeTableIsReadAtOnce(t *testing.T) {
	if err := initLayout(); err != nil {
		t.Skip("no keyboard layout to read:", err)
	}
	layoutMu.Lock()
	clear(charCache)
	readTheWholeTable()
	n := len(charCache)
	layoutMu.Unlock()
	if n == 0 {
		t.Skip("this machine has no Unicode layout to read")
	}
	if n != keyCodes {
		t.Errorf("one read filled %d entries, want all %d", n, keyCodes)
	}
}

func TestOnMainThreadAnswersWithoutTheBinding(t *testing.T) {
	was := pthreadMainNp
	pthreadMainNp = nil
	t.Cleanup(func() { pthreadMainNp = was })
	if onMainThread() {
		t.Error("with nothing bound this claimed to be on the main thread, " +
			"which is the answer that reaches HIToolbox")
	}
}
