package main

import (
	"testing"

	"github.com/go-macos/hotkey"
)

// The flag parsing is the only logic in this tool that can be wrong in a way a
// user would not immediately see, so it is what is tested. Everything else
// here is a call into the library, which has its own suite.

func TestParseKey(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want hotkey.Key
		ok   bool
	}{
		{"left", hotkey.KeyLeftArrow, true},
		{"LEFT", hotkey.KeyLeftArrow, true},
		{"space", hotkey.KeySpace, true},
		{"f13", hotkey.KeyF13, true},
		{"0x7B", hotkey.KeyLeftArrow, true},
		{"0x7b", hotkey.KeyLeftArrow, true},
		{"nonsense", 0, false},
		{"", 0, false},
	} {
		got, err := parseKey(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("parseKey(%q) error = %v, want ok=%v", tc.in, err, tc.ok)
			continue
		}
		if tc.ok && got != tc.want {
			t.Errorf("parseKey(%q) = %#x, want %#x", tc.in, uint16(got), uint16(tc.want))
		}
	}
}

func TestParseMods(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want hotkey.Modifier
		ok   bool
	}{
		{"opt,cmd", hotkey.Option | hotkey.Command, true},
		{"OPTION, COMMAND", hotkey.Option | hotkey.Command, true},
		{"alt,shift,ctrl,cmd", hotkey.Option | hotkey.Shift | hotkey.Control | hotkey.Command, true},
		{"cmd,,cmd", hotkey.Command, true},
		{"meta", 0, false},
		// A hot key with no modifier would swallow that key in every
		// application, so it is refused here as well as in the library.
		{"", 0, false},
		{",", 0, false},
	} {
		got, err := parseMods(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("parseMods(%q) error = %v, want ok=%v", tc.in, err, tc.ok)
			continue
		}
		if tc.ok && got != tc.want {
			t.Errorf("parseMods(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestKeyAndModNamesAreReal guards the two tables against naming something the
// library does not have.
func TestKeyAndModNamesAreReal(t *testing.T) {
	for name, k := range keysByName {
		if k.String() == "" {
			t.Errorf("%q maps to a key that renders as nothing", name)
		}
	}
	for name, m := range modsByName {
		if m == 0 {
			t.Errorf("%q maps to no modifier at all", name)
		}
	}
}
