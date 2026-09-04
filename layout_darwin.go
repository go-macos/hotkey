// Copyright (c) the go-macos authors. All rights reserved.
// Use of this source code is governed by a BSD-3-Clause licence that can be
// found in the LICENSE file.

//go:build darwin

package hotkey

import (
	"strings"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/go-macos/objc"
)

// The keyboard layout in front of the person, read from the system.
//
// ⛔ THE REASON THIS FILE EXISTS. A [Key] is a VIRTUAL KEY CODE -- a POSITION
// on the keyboard -- and the names in this package are the ANSI legends for
// those positions. On any layout that is not ANSI the two come apart, and they
// come apart silently: the registration succeeds, the key fires, and it is not
// the key the person is looking at.
//
// Measured on a Mac set to French, which is where it was reported:
//
//	code    ANSI name              prints here
//	0x18    kVK_ANSI_Equal         -
//	0x1B    kVK_ANSI_Minus         )
//	0x21    kVK_ANSI_LeftBracket   ^
//	0x1E    kVK_ANSI_RightBracket  $
//	0x2C    kVK_ANSI_Slash         =
//
// So a settings file asking for "Control-Option-Command-Equal" claimed the key
// printed "-", and the person pressing the key printed "=" reached nothing at
// all. "le raccourci du fit ne fonctionne pas" -- and every check said the
// shortcut had been granted, because it had been.
//
// UCKeyTranslate against the active layout is the only honest answer to "what
// does this key print". It is what the system itself uses to draw a menu.
var (
	layoutOnce sync.Once
	layoutErr  error

	tisCopyCurrentKeyboardLayoutInputSource func() uintptr
	tisGetInputSourceProperty               func(uintptr, uintptr) uintptr
	cfDataGetBytePtr                        func(uintptr) uintptr
	cfRelease                               func(uintptr)
	ucKeyTranslate                          func(uintptr, uint16, uint16, uint32, uint32, uint32, *uint32, uint32, *uint32, *uint16) int32
	lmGetKbdType                            func() uint32

	unicodeKeyLayoutDataKey uintptr
)

const (
	// coreFoundationPath is where CoreFoundation lives. Carbon re-exports the
	// Text Input Services this needs, so there is no third library to open.
	coreFoundationPath = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"

	// kUCKeyActionDisplay asks what the key would PRINT rather than what it
	// would insert: the difference matters for a dead key, which displays its
	// accent and inserts nothing.
	kUCKeyActionDisplay = 3
	// kUCKeyTranslateNoDeadKeysBit stops a dead key from swallowing the answer
	// and holding it for the next press -- this asks one question at a time,
	// and a translator with memory would answer the wrong one.
	kUCKeyTranslateNoDeadKeysMask = 1
)

// initLayout opens what is needed to ask the system about the keyboard.
func initLayout() error {
	layoutOnce.Do(func() {
		carbon, err := purego.Dlopen(carbonPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			layoutErr = err
			return
		}
		cf, err := purego.Dlopen(coreFoundationPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			layoutErr = err
			return
		}
		purego.RegisterLibFunc(&tisCopyCurrentKeyboardLayoutInputSource, carbon,
			"TISCopyCurrentKeyboardLayoutInputSource")
		purego.RegisterLibFunc(&tisGetInputSourceProperty, carbon, "TISGetInputSourceProperty")
		purego.RegisterLibFunc(&ucKeyTranslate, carbon, "UCKeyTranslate")
		purego.RegisterLibFunc(&lmGetKbdType, carbon, "LMGetKbdType")
		purego.RegisterLibFunc(&cfDataGetBytePtr, cf, "CFDataGetBytePtr")
		purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")

		// The property key is BUILT rather than looked up.
		//
		// kTISPropertyUnicodeKeyLayoutData is a global CFStringRef whose value is
		// its own name, and TISGetInputSourceProperty looks a key up by string
		// equality. Dlsym would hand back the address OF that pointer, and
		// dereferencing a uintptr is the conversion go vet's unsafeptr check
		// rightly flags -- the same trade go-macos/avfoundation makes for
		// AVMediaTypeVideo, and for the same reason.
		unicodeKeyLayoutDataKey = uintptr(objc.NSString("TISPropertyUnicodeKeyLayoutData"))
	})
	return layoutErr
}

// charFor is what this virtual key code prints on the layout in use now.
//
// Read every time rather than remembered: a person switches layout while a
// program is running -- that is what the input menu is for -- and a table built
// at start-up would then name keys that have moved.
func platformChar(k Key) string {
	if err := initLayout(); err != nil {
		return ""
	}
	src := tisCopyCurrentKeyboardLayoutInputSource()
	if src == 0 {
		return ""
	}
	defer cfRelease(src)
	data := tisGetInputSourceProperty(src, unicodeKeyLayoutDataKey)
	if data == 0 {
		// An input source with no Unicode layout -- an input METHOD rather than
		// a layout, which is what a Chinese or Japanese source is. There is
		// nothing to ask, and the ANSI name is the best answer left.
		return ""
	}
	layout := cfDataGetBytePtr(data)
	if layout == 0 {
		return ""
	}
	plain := translate(layout, k, 0)
	// ⛔ THE ALPHANUMERIC LEGEND WINS, because that is what a person calls the
	// key by. On French the number row prints "&" unshifted and "1" over it, and
	// a menu row saying ⌃⌥⌘& for "go to screen 1" would be technically true and
	// useless -- macOS draws ⌘1 there in its own menus. The punctuation keys are
	// unaffected: "=" over "+" keeps "=", "-" over "_" keeps "-".
	if !isAlnum(plain) {
		if shifted := translate(layout, k, shiftKeyState); isAlnum(shifted) {
			return shifted
		}
	}
	return plain
}

// shiftKeyState is what UCKeyTranslate wants for "with Shift held": the
// modifier flags shifted down by 8, which is a calling convention and not a
// mask anybody would guess.
const shiftKeyState = 0x02

// translate asks the layout one question.
func translate(layout uintptr, k Key, modifierKeyState uint32) string {
	var dead, n uint32
	buf := make([]uint16, 8)
	if st := ucKeyTranslate(layout, uint16(k), kUCKeyActionDisplay, modifierKeyState,
		lmGetKbdType(), kUCKeyTranslateNoDeadKeysMask, &dead, uint32(len(buf)), &n, &buf[0]); st != 0 || n == 0 {
		return ""
	}
	return printable(utf16Runes(buf[:n]))
}

// isAlnum reports whether this is a single ASCII letter or digit -- the shape
// of a legend a person names a key by.
func isAlnum(s string) bool {
	if len(s) != 1 {
		return false
	}
	c := s[0]
	return c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// printable is what UCKeyTranslate said, or "" when it said nothing a person
// could see printed on a key.
//
// ⚠ IT ANSWERS FOR EVERY KEY, INCLUDING THE ONES WITH NO CHARACTER. The left
// arrow comes back as U+001C, which is the file separator -- the code the
// classic Mac put on the arrow keys and the layout still carries. Escape comes
// back as U+001B, Return as U+000D. Taken at face value those become a menu row
// whose shortcut is an unprintable byte, which is worse than the ANSI name it
// replaced: measured, a left arrow rendered as "⌃⌥⌘\x1c".
//
// So a control character is NOT a printed character, and neither is a function
// key's private-use code. Upper case because that is how a menu prints a
// letter.
func printable(rs []rune) string {
	if len(rs) == 0 {
		return ""
	}
	for _, r := range rs {
		// A SPACE is in here too: it is printable in the Unicode sense and
		// invisible on a menu, where "Space" is the word every system draws.
		if r <= 0x20 || r == 0x7F || (r >= 0xF700 && r <= 0xF8FF) {
			return ""
		}
	}
	return strings.ToUpper(string(rs))
}

// utf16Runes decodes what UCKeyTranslate wrote. It is one character in every
// case this asks about, and a loop rather than an index because a layout is
// free to answer with more.
func utf16Runes(u []uint16) []rune {
	out := make([]rune, 0, len(u))
	for _, c := range u {
		out = append(out, rune(c))
	}
	return out
}
