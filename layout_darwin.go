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

	// ⛔ THE TEXT INPUT SOURCES API IS NOT SAFE TO CALL FROM TWO PLACES AT ONCE,
	// AND IT DOES NOT RETURN AN ERROR SAYING SO: IT ABORTS THE PROCESS.
	//
	// Measured. Eight goroutines asking for the same key's character, two
	// hundred times each: SIGABRT inside TISGetInputSourceProperty, "signal
	// arrived during cgo execution", no Go panic and nothing recover can catch.
	// One goroutine passes. Eight goroutines through this mutex pass. Eight
	// goroutines calling a DIFFERENT purego function pass, so it is this API and
	// not the calling machinery.
	//
	// It cost go-xrkit/desk its process: a menu asking what its rows print, on
	// the main thread, while the desk described its shortcuts on another. Two
	// callers is all it takes, and neither of them was doing anything unusual --
	// which is why the lock belongs HERE, and not in a note telling every caller
	// to hold one.
	layoutMu sync.Mutex

	// charCache is what each key printed, last time the main thread looked.
	//
	// It is the whole answer for a caller that is not on the main thread, and
	// there is no other answer available to one: see [platformChar].
	charCache = map[Key]string{}

	// pthreadMainNp is how a goroutine finds out which thread it is on.
	//
	// Go moves goroutines between threads, so "am I the main thread" is a
	// question about NOW and cannot be remembered. libSystem answers it in a
	// few instructions.
	pthreadMainNp func() int32

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
		//
		// ⛔ AND IT IS RETAINED, BECAUSE +[NSString stringWithUTF8String:] HANDS
		// BACK AN AUTORELEASED OBJECT AND THIS KEEPS IT FOREVER. Whatever pool
		// happened to be in place the first time a key was asked about owns it,
		// and when that pool drains the object is freed while this still points
		// at it. Nothing goes wrong until the memory is REUSED -- so it works,
		// and works, and then kills the process from a line that has not changed.
		//
		// Measured: keep the pointer, drain the pool, allocate 50000 strings,
		// send it -length -> SIGSEGV. That is exactly what killed go-xrkit/desk,
		// deterministically under AppKit (whose pool drains every pass of the
		// event loop) and only sometimes in a program that allocates less.
		key := objc.NSString("TISPropertyUnicodeKeyLayoutData")
		key.Send(objc.Sel("retain")) // for the life of the process: a key is a constant
		unicodeKeyLayoutDataKey = uintptr(key)

		// pthread_main_np is in libSystem, which is already open in every
		// process; CoreFoundation's handle re-exports it.
		purego.RegisterLibFunc(&pthreadMainNp, cf, "pthread_main_np")
	})
	return layoutErr
}

// platformChar is what this virtual key code prints on the layout in use now.
//
// ⛔ HITOOLBOX IS ASKED ONLY ON THE MAIN THREAD. TISGetInputSourceProperty
// asserts a dispatch queue, and an assertion that fails does not return an
// error: it traps, and the process is gone.
//
//	SIGTRAP: trace trap
//	signal arrived during cgo execution
//	hotkey.platformChar(0x7b)
//
// Symbolised, the faulting address is _dispatch_assert_queue_fail in
// libdispatch and the function being called is TISGetInputSourceProperty in
// HIToolbox. It cost go-xrkit/desk its process every time the gallery was
// opened, because that path claims its shortcuts and describes them from a
// goroutine.
//
// ⚠ AND IT DOES NOT REPRODUCE OUTSIDE AN APPLICATION. Asked from a background
// goroutine in a plain program -- with a run loop turning, with hot keys
// registered first, with eight callers at once -- it answers normally. The
// assertion is armed by whatever HIToolbox sets up for a real application, and
// a test that does not have one will pass whatever this does.
//
// So the table is read on the main thread and remembered. Every key at once,
// off one input source, because the expensive part is the round trip and
// because a table read in pieces is a table half of which is from before the
// person changed layout. A main-thread call always re-reads: switching layout
// while a program runs is what the input menu is for.
func platformChar(k Key) string {
	if err := initLayout(); err != nil {
		return ""
	}
	if !onMainThread() {
		layoutMu.Lock()
		s, ok := charCache[k]
		layoutMu.Unlock()
		if ok {
			return s
		}
		// Nothing remembered yet. Ask the main thread to fill the table so the
		// next caller is right, and answer with the ANSI name this time --
		// which is what an unprintable key falls back to anyway.
		objc.DispatchMain(func() { _ = platformChar(k) })
		return ""
	}

	layoutMu.Lock()
	defer layoutMu.Unlock()
	readTheWholeTable()
	return charCache[k]
}

// readTheWholeTable asks HIToolbox what every key prints and remembers it.
//
// ⛔ THE CALLER HOLDS layoutMu AND IS ON THE MAIN THREAD. Both matter: the
// second because a failed queue assertion inside TISGetInputSourceProperty
// traps rather than returning, and the first because the input source is
// copied, asked about and released, and it is the SEQUENCE that has to be alone
// -- eight callers through it at once abort the process even on the main
// thread, measured.
//
// Every key at once rather than the one asked for, because the expensive part
// is the round trip, because the caller after this one is as likely to be on a
// goroutine and can only have what is already here, and because a table read in
// pieces is a table half of which is from before the person switched layout.
func readTheWholeTable() {
	src := tisCopyCurrentKeyboardLayoutInputSource()
	if src == 0 {
		return
	}
	defer cfRelease(src)
	data := tisGetInputSourceProperty(src, unicodeKeyLayoutDataKey)
	if data == 0 {
		// An input source with no Unicode layout -- an input METHOD rather than
		// a layout, which is what a Chinese or Japanese source is. There is
		// nothing to ask, and the ANSI name is the best answer left.
		return
	}
	layout := cfDataGetBytePtr(data)
	if layout == 0 {
		return
	}
	for c := range Key(keyCodes) {
		charCache[c] = charOnLayout(layout, c)
	}
}

// keyCodes is how many virtual key codes there are. A code is a byte and the
// top bit is not used, so 0x00..0x7F is all of them.
const keyCodes = 0x80

// charOnLayout is one key's legend, off a layout already in hand.
func charOnLayout(layout uintptr, k Key) string {
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

// onMainThread reports whether the goroutine is running on the process's main
// thread right now.
//
// ⚠ "RIGHT NOW" IS THE WHOLE POINT. Go schedules a goroutine onto whatever
// thread is free, so this is not a property of the goroutine and cannot be
// remembered -- a goroutine that was on the main thread a moment ago may not be
// on the next line. Only a goroutine that has called runtime.LockOSThread on
// the main thread stays there, and that is exactly the arrangement a program
// with an AppKit event loop has.
func onMainThread() bool {
	if pthreadMainNp == nil {
		// Nothing was bound, so nothing can be asked. Reporting "not the main
		// thread" keeps this off HIToolbox, which is the safe answer: the cost
		// is a key named by its ANSI legend, and the alternative is a trap.
		return false
	}
	return pthreadMainNp() != 0
}
