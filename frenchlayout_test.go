// Copyright (c) the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause licence that can be
// found in the LICENSE file.

package hotkey

import "testing"

// french is the layout the defect was reported on, as a table.
//
// ⛔ MEASURED, not written from memory: these are what UCKeyTranslate answered
// on a Mac set to French, read back through this package's own probe. The five
// that matter are the punctuation keys, where the ANSI legend and what is
// printed are simply different keys:
//
//	code    ANSI name              prints
//	0x18    kVK_ANSI_Equal         -
//	0x1B    kVK_ANSI_Minus         )
//	0x21    kVK_ANSI_LeftBracket   ^
//	0x1E    kVK_ANSI_RightBracket  $
//	0x2C    kVK_ANSI_Slash         =
//
// The letters are here too because AZERTY moves three of them, and "[" is
// deliberately ABSENT: French prints it nowhere unshifted, which is the case a
// caller must not have silently swapped for something else.
var french = map[Key]string{
	KeyEqual: "-", KeyMinus: ")", KeyLeftBracket: "^", KeyRightBracket: "$",
	KeySlash: "=",
	// ⭐ THE ISO KEY, and the only place a French Mac prints "@": measured on
	// this machine, position 0x0A answers "@" unshifted. No ANSI position on
	// this layout prints one at all.
	KeyISOSection: "@",
	// AZERTY: A and Q change places, so do W and Z. M moves to where ANSI keeps
	// the semicolon -- a position this package does not NAME, which is why "M"
	// is absent from the values here and the claim cannot follow it.
	KeyA: "Q", KeyQ: "A", KeyW: "Z", KeyZ: "W", KeyM: ",",
	KeyS: "S", KeyG: "G", KeyL: "L", KeyX: "X", KeyC: "C",
	// The number row prints its digit over a symbol, and the digit is the
	// legend a person names the key by.
	KeyN1: "1", KeyN2: "2", KeyN0: "0",
}

// onFrench installs the table for one test.
func onFrench(t *testing.T) {
	t.Helper()
	was := charFor
	t.Cleanup(func() { charFor = was })
	charFor = func(k Key) string { return french[k] }
}

// TestOnAFrenchKeyboardTheClaimMoves.
//
// ⛔ THE DEFECT, as a table, on every platform this package builds for.
// "ctrl+alt+cmd+Equal" claimed virtual key code 0x18, which prints "-" here;
// the shortcut was granted, it fired, and the person pressing the key printed
// "=" reached nothing at all.
func TestOnAFrenchKeyboardTheClaimMoves(t *testing.T) {
	onFrench(t)

	const mods = Control | Option | Command
	for _, c := range []struct {
		name  string
		from  Key
		to    Key
		menu  string
		moved bool
	}{
		{"Equal is over on the slash key", KeyEqual, KeySlash, "⌃⌥⌘=", true},
		{"Minus is where ANSI keeps Equal", KeyMinus, KeyEqual, "⌃⌥⌘-", true},
		{"A is where ANSI keeps Q", KeyA, KeyQ, "⌃⌥⌘A", true},
		// ⚠ The claim cannot follow M. On AZERTY it sits where ANSI keeps the
		// semicolon, and this package does not name that position -- KeyForChar
		// searches only the codes it can name, because claiming one it cannot
		// would produce a combination that cannot be written down again. So the
		// key stays put and the report says what it prints, which is "," .
		{"M cannot be followed, and says so", KeyM, KeyM, "⌃⌥⌘,", false},
		{"S has not moved", KeyS, KeyS, "⌃⌥⌘S", false},
		{"a digit is the legend, and it has not moved", KeyN1, KeyN1, "⌃⌥⌘1", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := onThisKeyboard(Combo{Key: c.from, Mods: mods})
			if got.Key != c.to {
				t.Errorf("landed on %v (0x%02X), want %v (0x%02X)",
					got.Key, uint16(got.Key), c.to, uint16(c.to))
			}
			if (got.Key != c.from) != c.moved {
				t.Errorf("moved = %v, want %v", got.Key != c.from, c.moved)
			}
			if menu := got.Glyphs(); menu != c.menu {
				t.Errorf("the menu row reads %q, want %q", menu, c.menu)
			}
		})
	}
}

// TestAKeyThisKeyboardDoesNotPrintIsLeftWhereItIs.
//
// ⛔ French prints "[" nowhere unshifted. Nothing is silently swapped for
// something else -- the claim stays where it was, and the report then says what
// that key ACTUALLY prints, which is the honest half of the answer: a person
// reading "⌃⌥⌘^" can at least find the key.
func TestAKeyThisKeyboardDoesNotPrintIsLeftWhereItIs(t *testing.T) {
	onFrench(t)
	c := Combo{Key: KeyLeftBracket, Mods: Control | Option | Command}
	got := onThisKeyboard(c)
	if got.Key != KeyLeftBracket {
		t.Errorf("moved to %v although \"[\" is printed nowhere", got.Key)
	}
	if menu := got.Glyphs(); menu != "⌃⌥⌘^" {
		t.Errorf("the menu row reads %q, want %q -- what the key prints, not "+
			"what ANSI would have printed", menu, "⌃⌥⌘^")
	}
}

// TestTheReportSaysWhatIsPrinted, which is the half of the fix a person sees.
func TestTheReportSaysWhatIsPrinted(t *testing.T) {
	onFrench(t)
	for _, c := range []struct {
		key  Key
		want string
	}{
		{KeySlash, "⌘="},     // prints "=", though ANSI calls it Slash
		{KeyEqual, "⌘-"},     // prints "-", though ANSI calls it Equal
		{KeyLeftArrow, "⌘←"}, // prints nothing: the name stands
		{KeySpace, "⌘Space"},
		{KeyF1, "⌘F1"},
	} {
		if got := (Combo{Key: c.key, Mods: Command}).Glyphs(); got != c.want {
			t.Errorf("%v reads as %q, want %q", c.key, got, c.want)
		}
	}
}

// TestLookingUpACharacterThisKeyboardDoesNotPrint.
func TestLookingUpACharacterThisKeyboardDoesNotPrint(t *testing.T) {
	onFrench(t)
	for _, ch := range []string{"[", "]", "~", ""} {
		if k, ok := KeyForChar(ch); ok {
			t.Errorf("%q was found on %v", ch, k)
		}
	}
	// And one it does, case-folded both ways.
	for _, ch := range []string{"=", "a", "A"} {
		if _, ok := KeyForChar(ch); !ok {
			t.Errorf("%q was not found", ch)
		}
	}
}

// TestTheISOKeyIsFoundByWhatItPrints.
//
// ⭐ "@" HAS NOWHERE ELSE TO BE. It is the one character a French Mac prints
// only on the extra key an ISO keyboard has and an ANSI one does not -- so
// before that key had a name here, KeyForChar("@") answered "no such key" and
// a shortcut on "@" could not be expressed at all.
func TestTheISOKeyIsFoundByWhatItPrints(t *testing.T) {
	onFrench(t)

	k, ok := KeyForChar("@")
	if !ok {
		t.Fatal(`no key prints "@", so a shortcut on it cannot be named`)
	}
	if k != KeyISOSection {
		t.Errorf(`"@" is on key %#02x, want the ISO key %#02x`, int(k), int(KeyISOSection))
	}
}

// TestTheISOKeyIsNotMovedAgain: it is named as a POSITION, and that is what
// keeps it still.
//
// ⛔ THE WALK IS THE HAZARD. onThisKeyboard moves a shortcut to the key
// PRINTING what its ANSI name says, so a key named for its own character would
// be looked up by that character and moved -- and the answer would be this key
// again only by luck. Minus and the brackets are spelled as words for the same
// reason; this one joins them.
func TestTheISOKeyIsNotMovedAgain(t *testing.T) {
	onFrench(t)

	want := Combo{Key: KeyISOSection, Mods: Control | Option | Command}
	if got := onThisKeyboard(want); got != want {
		t.Errorf("the ISO key moved to %#02x", int(got.Key))
	}
	// And it still draws as the character somebody is looking at.
	if ch := KeyISOSection.Char(); ch != "@" {
		t.Errorf("the menu would draw %q rather than the legend on the key", ch)
	}
}
