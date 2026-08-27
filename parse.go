// Copyright (c) the go-macos authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package hotkey

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrParse says a written combination could not be read.
var ErrParse = errors.New("hotkey: unreadable combination")

// ParseCombo reads a combination a person wrote down.
//
// It exists because a configuration file is where a shortcut is CHANGED, and a
// person editing one should not have to reach for the glyph palette. All three
// of these are the same combination:
//
//	option+command+space
//	Option-Command-Space
//	⌥⌘Space
//
// Modifiers and the key may be separated by "+", "-", or spaces, in any order
// and any case; the glyph forms need no separator at all. Every name this
// package prints is accepted, so [Combo.String] and [Combo.Names] both round
// trip through here — which is the property its test asserts, over every key
// the package names.
//
// A combination with no modifier is refused. Claiming a bare key system-wide
// takes it away from every application on the machine, including whatever the
// person is typing into.
func ParseCombo(s string) (Combo, error) {
	fields := splitCombo(s)
	if len(fields) == 0 {
		return Combo{}, fmt.Errorf("%w: %q is empty", ErrParse, s)
	}
	var c Combo
	var keyed bool
	for _, f := range fields {
		if m, ok := parseModifier(f); ok {
			c.Mods |= m
			continue
		}
		k, ok := parseKey(f)
		if !ok {
			return Combo{}, fmt.Errorf("%w: %q in %q is neither a modifier nor a key "+
				"this package names (%s)", ErrParse, f, s, KeyNames())
		}
		if keyed {
			return Combo{}, fmt.Errorf("%w: %q names two keys", ErrParse, s)
		}
		c.Key, keyed = k, true
	}
	if !keyed {
		return Combo{}, fmt.Errorf("%w: %q is modifiers with no key", ErrParse, s)
	}
	if c.Mods == 0 {
		return Combo{}, fmt.Errorf("%w: %q has no modifier, and a bare key claimed "+
			"system-wide is taken from every application on the machine", ErrParse, s)
	}
	return c, nil
}

// ParseModifier reads one modifier name — "shift", "Control", "⌘" — or a sum of
// them, "control+shift". It is what a fallback ladder is written with.
func ParseModifier(s string) (Modifier, error) {
	fields := splitCombo(s)
	if len(fields) == 0 {
		return 0, fmt.Errorf("%w: %q is empty", ErrParse, s)
	}
	var m Modifier
	for _, f := range fields {
		bit, ok := parseModifier(f)
		if !ok {
			return 0, fmt.Errorf("%w: %q in %q is not a modifier", ErrParse, f, s)
		}
		m |= bit
	}
	return m, nil
}

// splitCombo cuts a written combination into its parts.
//
// The glyph forms carry no separator — "⌥⌘Space" is three parts — so a leading
// run of modifier symbols is peeled off before anything else is split.
func splitCombo(s string) []string {
	var out []string
	rest := strings.TrimSpace(s)
	for rest != "" {
		peeled := false
		for _, m := range modTable {
			if strings.HasPrefix(rest, m.symbol) {
				out = append(out, m.symbol)
				rest = strings.TrimSpace(strings.TrimPrefix(rest, m.symbol))
				peeled = true
				break
			}
		}
		if !peeled {
			break
		}
	}
	for _, f := range strings.FieldsFunc(rest, func(r rune) bool {
		return r == '+' || r == '-' || r == ' ' || r == '\t'
	}) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// parseModifier matches one modifier by symbol or by name, case-insensitively.
func parseModifier(f string) (Modifier, bool) {
	lower := strings.ToLower(f)
	for _, m := range modTable {
		if f == m.symbol || lower == strings.ToLower(m.name) {
			return m.bit, true
		}
	}
	// The names macOS also uses for two of them.
	switch lower {
	case "ctrl":
		return Control, true
	case "alt":
		return Option, true
	case "cmd":
		return Command, true
	}
	return 0, false
}

// parseKey matches one key by the name [Key.String] prints for it, or by a
// spelled-out name for the keys macOS prints as a glyph.
func parseKey(f string) (Key, bool) {
	lower := strings.ToLower(f)
	for k, name := range keyNames {
		if lower == strings.ToLower(name) {
			return k, true
		}
	}
	// A glyph is unreadable in a text editor and unwritable without one, so the
	// keys macOS prints as a glyph answer to their names as well.
	switch lower {
	case "return", "enter":
		return KeyReturn, true
	case "tab":
		return KeyTab, true
	case "delete", "backspace":
		return KeyDelete, true
	case "escape", "esc":
		return KeyEscape, true
	case "slash":
		return KeySlash, true
	case "minus", "hyphen":
		return KeyMinus, true
	// "plus" spells the same key: the plus is the shifted equals, and a person
	// writing "plus" in a settings file means the key with a plus printed on it
	// rather than a shortcut that also needs Shift.
	case "equal", "equals", "plus":
		return KeyEqual, true
	case "leftbracket", "[":
		return KeyLeftBracket, true
	case "rightbracket", "]":
		return KeyRightBracket, true
	case "left", "leftarrow":
		return KeyLeftArrow, true
	case "right", "rightarrow":
		return KeyRightArrow, true
	case "up", "uparrow":
		return KeyUpArrow, true
	case "down", "downarrow":
		return KeyDownArrow, true
	}
	return 0, false
}

// KeyNames lists every key name this package accepts, sorted, for an error
// message that tells a person what they may write instead of what they wrote.
func KeyNames() string {
	seen := map[string]bool{}
	for _, name := range keyNames {
		seen[name] = true
	}
	for _, name := range []string{
		"Return", "Enter", "Tab", "Delete", "Backspace", "Escape", "Esc",
		"Left", "Right", "Up", "Down", "Slash",
	} {
		seen[name] = true
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}
