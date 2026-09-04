// Copyright (c) the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause licence that can be
// found in the LICENSE file.

package hotkey

import "testing"

// TestTheMenuFormIsTheCharacterOnTheKey.
//
// ⛔ Four keys print one thing and are WRITTEN as another, and it is not a
// choice that can go away: a combination is written with "-" between its parts,
// so a key called "-" cannot be told from the join. A menu draws the parts with
// nothing between them, so there the character is what a person is looking for.
func TestTheMenuFormIsTheCharacterOnTheKey(t *testing.T) {
	for _, c := range []struct {
		key          Key
		glyph, spelt string
	}{
		{KeyEqual, "=", "Equal"},
		{KeyMinus, "-", "Minus"},
		{KeyLeftBracket, "[", "LeftBracket"},
		{KeyRightBracket, "]", "RightBracket"},
	} {
		if got := c.key.Glyph(); got != c.glyph {
			t.Errorf("%s prints as %q on a menu, want %q", c.spelt, got, c.glyph)
		}
		if got := c.key.String(); got != c.spelt {
			t.Errorf("%s is written as %q, want %q -- the written form must not "+
				"change, it is what a settings file round-trips", c.spelt, got, c.spelt)
		}
	}
}

// TestEveryOtherKeyPrintsAsItIsWritten, so there is one list to keep and not
// two: a key that needs no special form must not have one.
func TestEveryOtherKeyPrintsAsItIsWritten(t *testing.T) {
	for k := range keyNames {
		if _, special := keyGlyphs[k]; special {
			continue
		}
		if k.Glyph() != k.String() {
			t.Errorf("%v prints as %q and is written as %q", uint16(k), k.Glyph(), k.String())
		}
	}
}

// TestTheWholeCombinationOnAMenu.
func TestTheWholeCombinationOnAMenu(t *testing.T) {
	const mods = Control | Option | Command
	for _, c := range []struct {
		combo Combo
		menu  string
	}{
		{Combo{Key: KeyEqual, Mods: mods}, "⌃⌥⌘="},
		{Combo{Key: KeyLeftBracket, Mods: mods}, "⌃⌥⌘["},
		{Combo{Key: KeyLeftArrow, Mods: mods}, "⌃⌥⌘←"},
		{Combo{Key: KeyG, Mods: mods}, "⌃⌥⌘G"},
	} {
		if got := c.combo.Glyphs(); got != c.menu {
			t.Errorf("Glyphs() = %q, want %q", got, c.menu)
		}
	}
}

// TestAnUnnamedKeyStillSaysSomething, rather than an empty menu row.
func TestAnUnnamedKeyStillSaysSomething(t *testing.T) {
	k := Key(0xFE)
	if got := k.Glyph(); got != k.String() {
		t.Errorf("an unnamed key prints as %q", got)
	}
}
