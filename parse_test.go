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
