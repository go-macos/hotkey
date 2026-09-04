// Copyright (c) the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause licence that can be
// found in the LICENSE file.

//go:build darwin

package hotkey

import "testing"

// TestTheKeyboardIsAsked, and answers about a real layout.
//
// There is no assertion here about WHICH character each key prints: that is the
// layout's business and the whole point is not to have a table of it. What is
// asserted is that the system answered at all, and that what it answered is
// something a menu could draw.
func TestTheKeyboardIsAsked(t *testing.T) {
	// The letters are the safe probe: every keyboard layout with a Latin
	// alphabet has them somewhere, even if AZERTY puts A where ANSI puts Q.
	answered := 0
	for _, k := range namedKeys() {
		ch := k.Char()
		if ch == "" {
			continue
		}
		answered++
		for _, r := range ch {
			if r <= 0x20 || r == 0x7F || (r >= 0xF700 && r <= 0xF8FF) {
				t.Errorf("%v prints %#U, which is not a character on a key", k, r)
			}
		}
	}
	if answered == 0 {
		t.Skip("no keyboard layout to ask in this session")
	}
	t.Logf("%d of %d named keys print a character on this keyboard", answered, len(namedKeys()))
}

// TestTheRoundTripThroughTheLayout.
//
// ⛔ The property that makes OnThisKeyboard safe: if a key prints something,
// looking that something up finds a key that prints the same thing. It need not
// be the SAME key -- a layout may print one character in two places -- but it
// must print what was asked for, or a shortcut has been moved somewhere
// arbitrary.
func TestTheRoundTripThroughTheLayout(t *testing.T) {
	tried := 0
	for _, k := range namedKeys() {
		ch := k.Char()
		if ch == "" {
			continue
		}
		tried++
		found, ok := KeyForChar(ch)
		if !ok {
			t.Errorf("%v prints %q and nothing was found for %q", k, ch, ch)
			continue
		}
		if got := found.Char(); upperASCII(got) != upperASCII(ch) {
			t.Errorf("%q was looked up and found %v, which prints %q", ch, found, got)
		}
	}
	if tried == 0 {
		t.Skip("no keyboard layout to ask in this session")
	}
}

// TestACombinationLandsOnTheKeyThatPrintsIt.
//
// ⛔ THE DEFECT, in the shape it was reported. "ctrl+alt+cmd+Equal" claimed
// virtual key code 0x18, which on a French Mac is the key printed "-"; the
// shortcut was granted, it fired, and the person pressing the key printed "="
// reached nothing. Every check said it had been granted, because it had been.
//
// After OnThisKeyboard the combination is on a key that PRINTS "=" -- whichever
// position that is on this machine, which on an ANSI keyboard is the one it
// started on.
func TestACombinationLandsOnTheKeyThatPrintsIt(t *testing.T) {
	for _, k := range []Key{KeyEqual, KeyMinus, KeyA, KeyN1} {
		want, ok := printedName(k)
		if !ok {
			t.Fatalf("%v has no printed name", k)
		}
		if _, here := KeyForChar(want); !here {
			t.Logf("%q is nowhere on this keyboard; %v stays where it is", want, k)
			if got := onThisKeyboard(Combo{Key: k, Mods: Command}); got.Key != k {
				t.Errorf("%v was moved even though %q is nowhere", k, want)
			}
			continue
		}
		got := onThisKeyboard(Combo{Key: k, Mods: Control | Option | Command})
		if ch := got.Key.Char(); upperASCII(ch) != upperASCII(want) {
			t.Errorf("%v moved to %v, which prints %q rather than %q",
				k, got.Key, ch, want)
		}
	}
}

// TestMovingTwiceWalks, which is WHY it is unexported and why Register is the
// only caller.
//
// ⛔ Reading a key's ANSI name and moving to the local key that prints it is a
// one-way interpretation: what comes back is a position whose own ANSI name
// says something else. On French, Minus prints ")" and moves to Equal, which
// prints "-" and moves on to Slash, which prints "=". A caller able to apply
// this twice would have a shortcut that moves by itself -- so no caller can,
// and this test is what says so out loud rather than a comment hoping to be
// read.
func TestMovingTwiceWalks(t *testing.T) {
	walked := 0
	for _, k := range namedKeys() {
		once := onThisKeyboard(Combo{Key: k, Mods: Command})
		if twice := onThisKeyboard(once); twice.Key != once.Key {
			walked++
			t.Logf("%v moved to %v and would move on to %v", k, once.Key, twice.Key)
		}
	}
	if walked > 0 {
		t.Logf("%d keys walk when this is applied twice: Register applies it once", walked)
	}
	// On an ANSI keyboard nothing moves at all and nothing walks, which is a
	// perfectly good outcome -- there is nothing to assert about the count.
}
