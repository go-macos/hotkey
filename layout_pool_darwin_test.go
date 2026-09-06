// Copyright (c) the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause licence that can be
// found in the LICENSE file.

//go:build darwin

package hotkey

import (
	"testing"

	"github.com/go-macos/objc"
)

// ⛔ THE SHAPE OF THE BUG THAT KILLED go-xrkit/desk, AND THE ONLY SHAPE THAT
// FINDS IT.
//
// The property key this asks the system by is built with +[NSString
// stringWithUTF8String:], which hands back an AUTORELEASED object, and it is
// kept in a package variable for the life of the process. Whatever pool was in
// place the first time owns it; when that pool drains the object is freed and
// the package variable is a dangling pointer.
//
// ⚠ NOTHING GOES WRONG UNTIL THE MEMORY IS REUSED. A first attempt at this test
// drained the pool, allocated twenty thousand strings and passed -- the freed
// block simply had not been handed out again, and a use-after-free that is not
// stepped on looks exactly like correct code. Under AppKit, whose pool drains
// every pass of the event loop, it was deterministic: the desk printed its
// shortcuts at start-up and then died on the one that opened the gallery.
//
// So: ask inside a pool, drain it, churn hard, ask again. Without the retain
// this does not FAIL -- it takes the process down, which is the only signal
// available for a fault inside cgo.
func TestTheLayoutKeySurvivesThePoolItWasBornIn(t *testing.T) {
	var first string
	objc.AutoreleasePool(func() { first = KeyLeftArrow.Char() })

	// Hard enough to hand the freed block back out: measured, fifty thousand
	// is enough where twenty thousand was not.
	objc.AutoreleasePool(func() {
		for range 50000 {
			_ = objc.NSString("something long enough to reuse a freed CFString's storage")
		}
	})

	var again string
	objc.AutoreleasePool(func() { again = KeyLeftArrow.Char() })
	if again != first {
		t.Errorf("the same key printed %q before the pool drained and %q after", first, again)
	}
}
