// Copyright (c) the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause licence that can be
// found in the LICENSE file.

//go:build darwin

package hotkey

import (
	"sync"
	"testing"
)

// ⛔ THE TEST THAT WOULD HAVE CAUGHT IT, AND THE ONLY SHAPE IT CAN TAKE.
//
// Asking the system what a key prints goes through the Text Input Sources API,
// which is not safe to call from two places at once. It does not report that:
// it ABORTS THE PROCESS -- SIGABRT inside cgo, no Go panic, nothing recover can
// catch, and therefore no failing test either. Remove the lock in platformChar
// and this does not fail; the whole binary dies and every other test in the
// package dies with it, which is the only signal available.
//
// Two callers is all it takes. It cost go-xrkit/desk its process on a menu
// asking what its rows print while the desk described its shortcuts.
func TestAskingWhatAKeyPrintsFromEverywhereAtOnce(t *testing.T) {
	keys := []Key{KeyLeftArrow, KeyA, KeyN1, KeyEqual, KeyISOSection, KeyF1, KeyReturn}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				for _, k := range keys {
					_ = k.Char()
				}
			}
		}()
	}
	wg.Wait()
}

// The same through the type a caller actually holds, because Glyphs asks for
// every key in the combination and a menu asks for every combination it draws.
func TestDescribingEveryComboFromEverywhereAtOnce(t *testing.T) {
	combos := []Combo{
		{Mods: Control | Option | Command, Key: KeyLeftArrow},
		{Mods: Control | Option | Command, Key: KeyEqual},
		{Mods: Command, Key: KeyN1},
		{Mods: Command | Shift, Key: KeyA},
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				for _, c := range combos {
					_ = c.Glyphs()
				}
			}
		}()
	}
	wg.Wait()
}
