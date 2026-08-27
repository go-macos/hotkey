// Copyright (c) the go-macos authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package hotkey

import (
	"errors"
	"strings"
	"testing"
)

// TestParseRoundTripsEveryCombinationThisPackageCanPrint is the property that
// makes the parser worth having: whatever the package shows a person is
// something they can write back.
//
// Both forms, over every named key and every one of the fifteen non-empty
// modifier sets: 780 combinations, none of them typed out by hand.
func TestParseRoundTripsEveryCombinationThisPackageCanPrint(t *testing.T) {
	n := 0
	for k := range keyNames {
		for mods := Modifier(1); mods <= Control|Option|Shift|Command; mods++ {
			want := Combo{Key: k, Mods: mods}
			for _, written := range []string{want.String(), want.Names()} {
				got, err := ParseCombo(written)
				if err != nil {
					t.Errorf("ParseCombo(%q) = %v", written, err)
					continue
				}
				if got != want {
					t.Errorf("ParseCombo(%q) = %v, want %v", written, got, want)
				}
				n++
			}
		}
	}
	if n < 700 {
		t.Errorf("only %d combinations were exercised; the tables must have shrunk", n)
	}
}

func TestParseAcceptsWhatAPersonWouldWrite(t *testing.T) {
	want := Combo{Key: KeySpace, Mods: Control | Option | Command}
	for _, written := range []string{
		"control+option+command+space",
		"Control-Option-Command-Space",
		"CTRL+ALT+CMD+SPACE",
		"⌃⌥⌘Space",
		"⌃⌥⌘ space",
		"  command + space + option+control  ", // any order, any spacing
		"cmd+ctrl+alt+space",
	} {
		got, err := ParseCombo(written)
		if err != nil {
			t.Errorf("ParseCombo(%q) = %v", written, err)
			continue
		}
		if got != want {
			t.Errorf("ParseCombo(%q) = %v, want %v", written, got, want)
		}
	}
}

func TestParseRefuses(t *testing.T) {
	for _, written := range []string{
		"",                     // nothing
		"   ",                  // still nothing
		"command",              // modifiers with no key
		"space",                // a bare key, which would be taken from every app
		"command+space+return", // two keys
		"command+banana",       // not a key this package names
		"⌘",                    // a modifier glyph alone
	} {
		if got, err := ParseCombo(written); !errors.Is(err, ErrParse) {
			t.Errorf("ParseCombo(%q) = %v, %v; want an ErrParse", written, got, err)
		}
	}
	// The refusal has to say what may be written instead.
	_, err := ParseCombo("command+banana")
	if !strings.Contains(err.Error(), "Space") {
		t.Errorf("the error does not list the names that would work: %v", err)
	}
}

func TestParseModifierIsWhatALadderIsWrittenWith(t *testing.T) {
	for written, want := range map[string]Modifier{
		"shift":         Shift,
		"Control":       Control,
		"⌘":             Command,
		"control+shift": Control | Shift,
		"⌃⇧":            Control | Shift,
		"ctrl-shift":    Control | Shift,
	} {
		got, err := ParseModifier(written)
		if err != nil {
			t.Errorf("ParseModifier(%q) = %v", written, err)
			continue
		}
		if got != want {
			t.Errorf("ParseModifier(%q) = %v, want %v", written, got, want)
		}
	}
	for _, written := range []string{"", "  ", "space", "control+space"} {
		if got, err := ParseModifier(written); !errors.Is(err, ErrParse) {
			t.Errorf("ParseModifier(%q) = %v, %v; want an ErrParse", written, got, err)
		}
	}
}

// TestKeyNamesListsWhatParseAccepts: the message an error shows must not drift
// from what the parser will actually take.
func TestKeyNamesListsWhatParseAccepts(t *testing.T) {
	for _, name := range strings.Fields(KeyNames()) {
		if _, err := ParseCombo("command+" + name); err != nil {
			t.Errorf("KeyNames offers %q, which ParseCombo refuses: %v", name, err)
		}
	}
}

// TestNamesSpellsTheKeyOut.
//
// Names is the form for a log, an accessibility tree, and a window whose font
// is not the one macOS puts on a menu. It used to spell the MODIFIERS out and
// leave the key as a glyph, so "⌥⌘←" became "Option-Command-" and stopped —
// measured in a settings window, where a line whose whole job was to say which
// combination had been granted said nothing at all.
func TestNamesSpellsTheKeyOut(t *testing.T) {
	for key, want := range map[Key]string{
		KeyLeftArrow:  "Left",
		KeyRightArrow: "Right",
		KeyUpArrow:    "Up",
		KeyDownArrow:  "Down",
		KeyReturn:     "Return",
		KeyTab:        "Tab",
		KeyDelete:     "Delete",
		KeyEscape:     "Escape",
		KeySlash:      "Slash",
		KeySpace:      "Space", // already a word; String and Name agree
		KeyA:          "A",
	} {
		if got := key.Name(); got != want {
			t.Errorf("Key(%#x).Name() = %q, want %q", uint16(key), got, want)
		}
	}
	// Every glyph String prints must have a word for it. This is what fails if
	// a key is ever added with a glyph and no name.
	for key, printed := range keyNames {
		if name := key.Name(); name == printed && !isWord(printed) {
			t.Errorf("Key(%#x) prints %q and has no word for it", uint16(key), printed)
		}
	}
	// A key the package does not name at all is still honest about it.
	if got := Key(0xFE).Name(); got != "key 0xFE" {
		t.Errorf("an unnamed key gives %q", got)
	}
}

// isWord reports whether a printed key name is already something a person could
// type — letters and digits, rather than a glyph.
func isWord(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

// TestMinusAndEqual: the keys a person reaches for to make something smaller or
// larger, and the matched pair after them.
//
// They are the reason this pair exists, and the reason they are SPELLED rather
// than printed as the characters on them: a combination is written with "-"
// between its parts, so a key whose name is "-" cannot be told from the join.
func TestMinusAndEqual(t *testing.T) {
	for _, c := range []struct {
		in   string
		want Key
	}{
		{"minus", KeyMinus}, {"Minus", KeyMinus}, {"hyphen", KeyMinus},
		{"equal", KeyEqual}, {"Equal", KeyEqual}, {"equals", KeyEqual},
		// "plus" is the same key: the plus is the shifted equals, and a person
		// writing it in a settings file means the key with a plus printed on it
		// rather than a shortcut that also needs Shift.
		{"plus", KeyEqual},
		// The brackets, by name and by the character on the key. The glyph works
		// here because neither is the "-" a combination is joined with.
		{"leftbracket", KeyLeftBracket}, {"[", KeyLeftBracket},
		{"rightbracket", KeyRightBracket}, {"]", KeyRightBracket},
	} {
		got, err := ParseCombo("Control-Option-Command-" + c.in)
		if err != nil {
			t.Errorf("ParseCombo(%q) = %v", c.in, err)
			continue
		}
		if got.Key != c.want {
			t.Errorf("%q gave key %v, want %v", c.in, got.Key, c.want)
		}
		if got.Mods != Control|Option|Command {
			t.Errorf("%q gave mods %v", c.in, got.Mods)
		}
	}

	// The codes are the US layout's positions, like every other key here, so
	// they are the two keys at the end of the number row whatever is printed on
	// them: 0x1B and 0x18 are what the window server matches.
	if KeyMinus != 0x1B || KeyEqual != 0x18 {
		t.Errorf("the codes are %#x and %#x", uint16(KeyMinus), uint16(KeyEqual))
	}
	if KeyLeftBracket != 0x21 || KeyRightBracket != 0x1E {
		t.Errorf("the bracket codes are %#x and %#x",
			uint16(KeyLeftBracket), uint16(KeyRightBracket))
	}

	// Both directions, which is what the pair is for: a settings file writes
	// what a log prints.
	for _, k := range []Key{KeyMinus, KeyEqual, KeyLeftBracket, KeyRightBracket} {
		c := Combo{Key: k, Mods: Command}
		back, err := ParseCombo(c.String())
		if err != nil {
			t.Errorf("ParseCombo(%q) = %v", c.String(), err)
			continue
		}
		if back != c {
			t.Errorf("%q round-tripped to %v", c.String(), back)
		}
		if names := c.Names(); names == "" {
			t.Errorf("%v has no spelled name", c)
		}
	}

	// And they are offered to a person who wrote something else, which is the
	// only way anybody finds out they exist.
	for _, want := range []string{"Minus", "Equal", "LeftBracket", "RightBracket"} {
		if !strings.Contains(KeyNames(), want) {
			t.Errorf("KeyNames() does not offer %q: %s", want, KeyNames())
		}
	}
}
