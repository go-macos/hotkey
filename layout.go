// Copyright (c) the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause licence that can be
// found in the LICENSE file.

package hotkey

import "sync"

// Char is what this key PRINTS on the keyboard in front of the person.
//
// A [Key] is a virtual key code, which is a POSITION, and the names in this
// package are the ANSI legends for those positions. On a layout that is not
// ANSI the two come apart -- on French the position ANSI calls Equal prints
// "-", and "=" is over on the position ANSI calls Slash -- so a name is a claim
// about a keyboard nobody is typing on.
//
// It answers "" where the system cannot say: a platform with no layout service,
// an input METHOD rather than a layout, or a key with no printed character at
// all -- an arrow, Escape, Return. A caller then has [Key.String], which is at
// least a name a person can look up.
func (k Key) Char() string { return charFor(k) }

// KeyForChar is the key that PRINTS this character on the current keyboard.
//
// The inverse of [Key.Char], and the one a settings file needs: somebody
// writing "=" means the key with "=" printed on it, not the position ANSI keeps
// "=" at. Matching is case-insensitive, so "a" and "A" find the same key.
//
// It searches the codes this package NAMES, and no others: a layout can put a
// character on a position with no name here, and claiming one of those would
// produce a combination that cannot be written down again.
func KeyForChar(ch string) (Key, bool) {
	if ch == "" {
		return 0, false
	}
	want := upperASCII(ch)
	for _, k := range namedKeys() {
		if c := k.Char(); c != "" && upperASCII(c) == want {
			return k, true
		}
	}
	return 0, false
}

// onThisKeyboard is this combination moved to the key that PRINTS what its ANSI
// name says -- the same shortcut, on the key the person is looking at.
//
// ⛔ UNEXPORTED, AND APPLIED ONCE, INSIDE Register. Reading a key's ANSI name
// and moving to the local key that prints it is a ONE-WAY interpretation: what
// comes back is a position whose own ANSI name says something else, so applying
// it again asks a different question and the key WALKS. Measured on French:
// Minus (which prints ")") moves to Equal (which prints "-"), and Equal moves
// on to Slash (which prints "="). A caller who could apply it twice would have
// a shortcut that moves by itself, so no caller can.
//
// See [Options.OnThisKeyboard], which is the door.
//
// It is IDENTITY in the two cases where moving would be wrong:
//
//   - a key with no printed character of its own -- an arrow, Return, Escape --
//     because no layout moves those and there is nothing to match on;
//   - a character this keyboard does not print anywhere, which is what "[" is
//     on French. Nothing is silently swapped for something else: the
//     combination stays where it is, and [Combo.Glyphs] then reports what that
//     key actually prints, which is the honest half of the answer.
func onThisKeyboard(c Combo) Combo {
	ch, ok := printedName(c.Key)
	if !ok {
		return c
	}
	if k, found := KeyForChar(ch); found {
		c.Key = k
	}
	return c
}

// printedName is the character this package believes is on the key, or false
// when it believes there is none.
//
// The glyph table first, because that is where the four keys whose NAME is
// spelled out keep their character: keyNames has "Equal", and what a keyboard
// prints is "=".
func printedName(k Key) (string, bool) {
	if g, ok := keyGlyphs[k]; ok {
		return g, true
	}
	if n, ok := keyNames[k]; ok && len(n) == 1 && n[0] > ' ' && n[0] < 0x7F {
		// One ASCII character: a letter, a digit or "/". Anything else in this
		// table is a word ("Space", "F1") or a glyph for a key that prints
		// nothing ("←", "⌫"), and neither is something to look for on a layout.
		return n, true
	}
	return "", false
}

// namedKeys is every key this package can name, in a stable order.
//
// Stable because KeyForChar returns the FIRST match and a map's order is not:
// two positions printing the same character would otherwise resolve to one of
// them on Tuesday and the other on Wednesday, which is a shortcut that moves by
// itself.
func namedKeys() []Key {
	namedOnce.Do(func() {
		cachedNamedKeys = make([]Key, 0, len(keyNames))
		for k := range keyNames {
			cachedNamedKeys = append(cachedNamedKeys, k)
		}
		// By code, which is an order the keyboard itself gives us. An insertion
		// sort because this is fifty entries, once.
		for i := 1; i < len(cachedNamedKeys); i++ {
			for j := i; j > 0 && cachedNamedKeys[j] < cachedNamedKeys[j-1]; j-- {
				cachedNamedKeys[j], cachedNamedKeys[j-1] = cachedNamedKeys[j-1], cachedNamedKeys[j]
			}
		}
	})
	return cachedNamedKeys
}

// upperASCII folds one character for comparison.
//
// ASCII only, deliberately: these are the characters a keyboard prints on its
// own keys, and a full Unicode fold would be a table to match a case that does
// not arise.
func upperASCII(s string) string {
	out := []byte(s)
	for i, b := range out {
		if b >= 'a' && b <= 'z' {
			out[i] = b - 32
		}
	}
	return string(out)
}

// namedOnce and cachedNamedKeys back namedKeys.
var (
	namedOnce       sync.Once
	cachedNamedKeys []Key
)
