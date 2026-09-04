// Copyright (c) the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause licence that can be
// found in the LICENSE file.

package hotkey

import "testing"

// TestAKeyWithNoPrintedCharacterIsNotMoved.
//
// ⛔ The arrows, Return, Escape, the function keys. No layout moves them, and
// there is nothing to match on: asking the system what they print gives a
// CONTROL CHARACTER -- the left arrow answers U+001C, the file separator the
// classic Mac put on the arrow keys -- so a combination taken at face value
// would move to whatever else happens to answer the same byte.
func TestAKeyWithNoPrintedCharacterIsNotMoved(t *testing.T) {
	for _, k := range []Key{
		KeyLeftArrow, KeyRightArrow, KeyUpArrow, KeyDownArrow,
		KeyReturn, KeyEscape, KeyTab, KeyDelete, KeySpace, KeyF1, KeyF13,
	} {
		if ch := k.Char(); ch != "" {
			t.Errorf("%v says it prints %q", k, ch)
		}
		if got := onThisKeyboard(Combo{Key: k, Mods: Command}); got.Key != k {
			t.Errorf("%v was moved to %v", k, got.Key)
		}
	}
}

// TestTheModifiersSurviveBeingMoved: only the key changes, and only ever to
// another key.
func TestTheModifiersSurviveBeingMoved(t *testing.T) {
	c := Combo{Key: KeyEqual, Mods: Control | Option | Command}
	got := onThisKeyboard(c)
	if got.Mods != c.Mods {
		t.Errorf("the modifiers became %v", got.Mods)
	}
	if got.Key == 0 && c.Key != 0 {
		t.Error("the key was cleared rather than moved")
	}
}

// TestNothingIsFoundForNothing: an empty character must not match the first key
// that prints nothing, which is how a shortcut would land on an arrow.
func TestNothingIsFoundForNothing(t *testing.T) {
	if k, ok := KeyForChar(""); ok {
		t.Errorf("the empty string was found on %v", k)
	}
	if k, ok := KeyForChar("this is not a key"); ok {
		t.Errorf("a sentence was found on %v", k)
	}
}

// TestTheSearchOrderIsStable.
//
// KeyForChar returns the FIRST match, and a map's order is not an order: two
// positions printing the same character would otherwise resolve to one of them
// on Tuesday and the other on Wednesday, which is a shortcut that moves by
// itself.
func TestTheSearchOrderIsStable(t *testing.T) {
	first := namedKeys()
	if len(first) != len(keyNames) {
		t.Fatalf("%d keys ordered out of %d named", len(first), len(keyNames))
	}
	for i := 1; i < len(first); i++ {
		if first[i] <= first[i-1] {
			t.Fatalf("out of order at %d: %v then %v", i, first[i-1], first[i])
		}
	}
	// The same slice every time, so a search cannot change its mind.
	second := namedKeys()
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("the order changed at %d", i)
		}
	}
}

// TestWhatThisPackageThinksIsPrintedOnAKey.
//
// printedName is what OnThisKeyboard looks for, so it has to tell a legend from
// a word and from a glyph: "Equal" is the NAME of a key that prints "=", "F1"
// is a word, and "←" is a picture of a key that prints nothing at all.
func TestWhatThisPackageThinksIsPrintedOnAKey(t *testing.T) {
	for _, c := range []struct {
		key  Key
		want string
	}{
		{KeyEqual, "="},
		{KeyMinus, "-"},
		{KeyLeftBracket, "["},
		{KeyRightBracket, "]"},
		{KeyA, "A"},
		{KeyN1, "1"},
		{KeySlash, "/"},
	} {
		got, ok := printedName(c.key)
		if !ok || got != c.want {
			t.Errorf("printedName(%v) = %q, %v; want %q, true", c.key, got, ok, c.want)
		}
	}
	for _, k := range []Key{KeyLeftArrow, KeyReturn, KeyEscape, KeySpace, KeyF1, Key(0xFE)} {
		if got, ok := printedName(k); ok {
			t.Errorf("printedName(%v) = %q, true; it prints nothing to look for", k, got)
		}
	}
}

// TestUpperASCIIFoldsLettersAndNothingElse.
func TestUpperASCIIFoldsLettersAndNothingElse(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"a", "A"}, {"Z", "Z"}, {"=", "="}, {"1", "1"}, {"", ""}, {"é", "é"},
	} {
		if got := upperASCII(c.in); got != c.want {
			t.Errorf("upperASCII(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
