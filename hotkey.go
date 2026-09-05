// Package hotkey claims system-wide keyboard shortcuts on macOS from pure Go
// (CGO_ENABLED=0), and falls back to a neighbouring combination when the one
// you asked for is already taken.
//
// A hot key registered here fires while the user is working in ANOTHER
// application. That is the whole point: the consumer is an XR virtual-desktop
// app whose ribbon of screens is turned from the keyboard while the user types
// inside the applications on those screens. A shortcut that only works when
// your own window is focused would be useless to it.
//
// # No permission is required
//
// The two obvious routes — CGEventTap and
// -[NSEvent addGlobalMonitorForEventsMatchingMask:] — both demand the
// Accessibility (TCC) grant, which means a system dialog and a trip to System
// Settings. This package instead uses Carbon's hot-key API
// (RegisterEventHotKey), which is still present on macOS 26 and needs no
// permission at all. Registering ⌥⌘← on macOS 26.6.2 produced no dialog.
//
// # Three kinds of conflict, two of them detectable
//
// RegisterEventHotKey does NOT conflict-check against system shortcuts. This is
// the single most important thing to understand about it, and the reason a
// naive implementation is useless:
//
//  1. Another Carbon hot-key holder — DETECTED. Registration returns
//     eventHotKeyExistsErr (-9878), surfaced as [ErrComboTaken].
//  2. A macOS system shortcut (⌥⌘Space is the Finder's search window) — NOT
//     detected by registration, which returns 0 for it anyway. This package
//     catches these itself, before registering, with [SystemShortcuts]. See
//     that type for exactly how dependable that is.
//  3. An ordinary application's own menu key equivalent — for example Safari's
//     ⌥⌘← for "previous tab". NOT DETECTABLE, by this package or any other.
//     Nothing on macOS enumerates other applications' menu shortcuts, and such
//     a shortcut is only live while that application is frontmost. If you claim
//     one, you will win it globally and that application will silently stop
//     seeing it. There is no coverage here and this package does not pretend
//     otherwise.
//
// # Portability
//
// Every exported symbol is defined on all platforms so consumers cross-compile.
// On non-darwin GOOS the registration entry points report [ErrUnsupported]; the
// whole policy layer — the fallback ladder, combination formatting, and the
// parsing of the system-shortcut data — is OS-independent and fully testable
// anywhere.
package hotkey

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Errors reported by the package. They are stable and may be tested with
// errors.Is.
var (
	// ErrUnsupported is returned by the registration entry points on
	// non-darwin platforms (Carbon is macOS-only).
	ErrUnsupported = errors.New("hotkey: unsupported on this platform (darwin only)")

	// ErrComboTaken reports that the combination is already held. It wraps
	// both detectable conflict kinds: another Carbon hot-key holder
	// (eventHotKeyExistsErr, -9878) and a macOS system shortcut found by
	// [SystemShortcuts].
	ErrComboTaken = errors.New("hotkey: combination already taken")

	// ErrNoCandidate reports that neither the wanted combination nor any
	// rung of the fallback ladder could be claimed. Nothing was registered.
	ErrNoCandidate = errors.New("hotkey: every candidate combination is taken")

	// ErrNoModifier reports a combination with no modifier at all. Carbon
	// accepts one, but claiming a bare key system-wide would swallow that
	// key everywhere, in every application, which is never what a caller
	// means.
	ErrNoModifier = errors.New("hotkey: a system-wide hot key needs at least one modifier")

	// ErrClosed reports use of a [Hotkey] that has already been released.
	ErrClosed = errors.New("hotkey: hot key already released")
)

// Modifier is a set of modifier keys, as a bit set. It is deliberately NOT the
// Carbon bitmask nor the Cocoa one; both of those are derived from it, so a
// caller never has to know either.
type Modifier uint8

// The modifier keys, in Apple's canonical display order.
const (
	Control Modifier = 1 << iota
	Option
	Shift
	Command
)

// Carbon modifier bits (Events.h). Exported nowhere; [Modifier.carbon] is the
// only way to reach them.
const (
	carbonCmdKey     = 0x0100
	carbonShiftKey   = 0x0200
	carbonOptionKey  = 0x0800
	carbonControlKey = 0x1000
)

// Cocoa NSEventModifierFlags bits, which is the encoding the
// com.apple.symbolichotkeys preference domain stores.
const (
	cocoaShift   = 1 << 17
	cocoaControl = 1 << 18
	cocoaOption  = 1 << 19
	cocoaCommand = 1 << 20
)

// modTable drives every conversion and the String method, so the four
// representations can never drift apart.
var modTable = []struct {
	bit    Modifier
	carbon uint32
	cocoa  uint32
	symbol string
	name   string
}{
	{Control, carbonControlKey, cocoaControl, "⌃", "Control"}, // ⌃
	{Option, carbonOptionKey, cocoaOption, "⌥", "Option"},     // ⌥
	{Shift, carbonShiftKey, cocoaShift, "⇧", "Shift"},         // ⇧
	{Command, carbonCmdKey, cocoaCommand, "⌘", "Command"},     // ⌘
}

// carbon renders the set as the Carbon modifier bitmask RegisterEventHotKey
// wants.
func (m Modifier) carbon() uint32 {
	var out uint32
	for _, e := range modTable {
		if m&e.bit != 0 {
			out |= e.carbon
		}
	}
	return out
}

// cocoa renders the set as the NSEventModifierFlags bitmask used by
// com.apple.symbolichotkeys.
func (m Modifier) cocoa() uint32 {
	var out uint32
	for _, e := range modTable {
		if m&e.bit != 0 {
			out |= e.cocoa
		}
	}
	return out
}

// modifierFromCocoa converts an NSEventModifierFlags bitmask back to a
// [Modifier], ignoring bits this package does not model (Fn, CapsLock, the
// device-dependent left/right bits).
func modifierFromCocoa(mask uint32) Modifier {
	var m Modifier
	for _, e := range modTable {
		if mask&e.cocoa != 0 {
			m |= e.bit
		}
	}
	return m
}

// String renders the modifier set with the standard glyphs, in the order macOS
// itself uses in menus: ⌃⌥⇧⌘. The empty set renders as "".
func (m Modifier) String() string {
	var b strings.Builder
	for _, e := range modTable {
		if m&e.bit != 0 {
			b.WriteString(e.symbol)
		}
	}
	return b.String()
}

// Names renders the modifier set as English words, in the same order — for
// logs and for accessibility labels, where the glyphs read badly.
func (m Modifier) Names() []string {
	var out []string
	for _, e := range modTable {
		if m&e.bit != 0 {
			out = append(out, e.name)
		}
	}
	return out
}

// Key is a macOS virtual key code (the kVK_* constants from
// HIToolbox/Events.h). It is a hardware position, not a character: Key(0) is
// the key labelled "A" on a US layout and "Q" on a French one.
type Key uint16

// The virtual key codes this package names. Any other code is usable; it
// simply renders as "key 0x…" in a [Combo] string.
const (
	KeyA     Key = 0x00
	KeyS     Key = 0x01
	KeyD     Key = 0x02
	KeyF     Key = 0x03
	KeyH     Key = 0x04
	KeyG     Key = 0x05
	KeyZ     Key = 0x06
	KeyX     Key = 0x07
	KeyC     Key = 0x08
	KeyV     Key = 0x09
	KeyB     Key = 0x0B
	KeyQ     Key = 0x0C
	KeyW     Key = 0x0D
	KeyE     Key = 0x0E
	KeyR     Key = 0x0F
	KeyY     Key = 0x10
	KeyT     Key = 0x11
	KeyO     Key = 0x1F
	KeyU     Key = 0x20
	KeyI     Key = 0x22
	KeyP     Key = 0x23
	KeyL     Key = 0x25
	KeyJ     Key = 0x26
	KeyK     Key = 0x28
	KeyN     Key = 0x2D
	KeyM     Key = 0x2E
	KeyN1    Key = 0x12
	KeyN2    Key = 0x13
	KeyN3    Key = 0x14
	KeyN4    Key = 0x15
	KeyN5    Key = 0x17
	KeyN6    Key = 0x16
	KeyN7    Key = 0x1A
	KeyN8    Key = 0x1C
	KeyN9    Key = 0x19
	KeyN0    Key = 0x1D
	KeySlash Key = 0x2C
	// KeyMinus and KeyEqual are the two keys either side of the number row's
	// end, which is where a keyboard puts "smaller" and "larger". The virtual
	// codes are the US layout's, like every other key here: a hot key is
	// registered by CODE and the code is a POSITION, so on a French keyboard
	// these are the same two keys in the same place whatever is printed on them.
	KeyMinus Key = 0x1B
	KeyEqual Key = 0x18
	// KeyLeftBracket and KeyRightBracket are the pair after P on the top row,
	// which is where a keyboard puts a matched set of opposites that nothing has
	// told anybody the meaning of. Same reasoning as Minus and Equal, and the same
	// caveat: the CODE is a position, so on a French keyboard these are the two
	// keys in that place whatever is printed on them.
	KeyLeftBracket  Key = 0x21
	KeyRightBracket Key = 0x1E
	// KeyISOSection is the EXTRA key an ISO keyboard has and an ANSI one does
	// not: the short one between the left Shift and the Z position, which Apple
	// calls kVK_ISO_Section.
	//
	// ⭐ IT IS WHERE A FRENCH MAC PRINTS "@". Measured on this machine:
	// position 0x0A prints "@" unshifted. There is nowhere else to look for it
	// -- no ANSI position on a French layout prints one -- so a shortcut on "@"
	// is this key or it is nothing.
	//
	// ⛔ NAMED AS A POSITION, deliberately, like Minus and the brackets. The
	// name is a WORD, so [onThisKeyboard] leaves it alone: a key named for what
	// it prints would be moved to the local key printing that legend, and this
	// key IS the local one. A layout that prints something else here -- "§" on
	// a Swiss keyboard, "`" on a British one -- gets the same physical key,
	// which is what a person pointing at their keyboard means.
	KeyISOSection Key = 0x0A
	KeyReturn     Key = 0x24
	KeyTab        Key = 0x30
	KeySpace      Key = 0x31
	KeyDelete     Key = 0x33
	KeyEscape     Key = 0x35
	KeyF1         Key = 0x7A
	KeyF2         Key = 0x78
	KeyF3         Key = 0x63
	KeyF4         Key = 0x76
	KeyF5         Key = 0x60
	KeyF6         Key = 0x61
	KeyF7         Key = 0x62
	KeyF8         Key = 0x64
	KeyF9         Key = 0x65
	KeyF10        Key = 0x6D
	KeyF11        Key = 0x67
	KeyF12        Key = 0x6F
	KeyF13        Key = 0x69
	KeyF14        Key = 0x6B
	KeyF15        Key = 0x71
	KeyLeftArrow  Key = 0x7B
	KeyRightArrow Key = 0x7C
	KeyDownArrow  Key = 0x7D
	KeyUpArrow    Key = 0x7E
)

// keyNames maps the named key codes to what macOS prints on a menu.
var keyNames = map[Key]string{
	KeyA: "A", KeyS: "S", KeyD: "D", KeyF: "F", KeyH: "H", KeyG: "G",
	KeyZ: "Z", KeyX: "X", KeyC: "C", KeyV: "V", KeyB: "B", KeyQ: "Q",
	KeyW: "W", KeyE: "E", KeyR: "R", KeyY: "Y", KeyT: "T", KeyO: "O",
	KeyU: "U", KeyI: "I", KeyP: "P", KeyL: "L", KeyJ: "J", KeyK: "K",
	KeyN: "N", KeyM: "M",
	KeyN1: "1", KeyN2: "2", KeyN3: "3", KeyN4: "4", KeyN5: "5",
	KeyN6: "6", KeyN7: "7", KeyN8: "8", KeyN9: "9", KeyN0: "0",
	KeySlash: "/",
	// SPELLED, not the characters on the keys.
	//
	// A combination is written with "-" between its parts, so a key whose name is
	// "-" cannot be told from the join: "Control--" is unreadable in both
	// directions, and the round trip through ParseCombo is what caught it. Equal
	// is spelled with it for the pair to read alike.
	KeyMinus: "Minus", KeyEqual: "Equal",
	// Spelled for the same reason: a bracket in a combination written with "-"
	// between its parts is readable, but the pair reads better as words and the
	// glyphs are two of the characters a settings file quotes with.
	KeyLeftBracket: "LeftBracket", KeyRightBracket: "RightBracket",
	// A word, so onThisKeyboard leaves it where it is: see [KeyISOSection].
	KeyISOSection: "ISOSection",
	KeyReturn:     "↩", KeyTab: "⇥", KeySpace: "Space",
	KeyDelete: "⌫", KeyEscape: "⎋",
	KeyF1: "F1", KeyF2: "F2", KeyF3: "F3", KeyF4: "F4", KeyF5: "F5",
	KeyF6: "F6", KeyF7: "F7", KeyF8: "F8", KeyF9: "F9", KeyF10: "F10",
	KeyF11: "F11", KeyF12: "F12", KeyF13: "F13", KeyF14: "F14", KeyF15: "F15",
	KeyLeftArrow: "←", KeyRightArrow: "→",
	KeyDownArrow: "↓", KeyUpArrow: "↑",
}

// String renders the key as macOS would print it on a menu — "←" for the
// left arrow, "Space" for the space bar. An unnamed code renders as its
// hexadecimal virtual key code, which is honest rather than wrong: this package
// does not consult the active keyboard layout, so it cannot know what character
// an arbitrary code produces.
func (k Key) String() string {
	if n, ok := keyNames[k]; ok {
		return n
	}
	return fmt.Sprintf("key 0x%02X", uint16(k))
}

// keyGlyphs are the keys macOS PRINTS as a character on a menu where
// [Key.String] spells them out.
//
// The two differ on purpose. String is what a settings file round-trips
// through [ParseCombo], and a combination is written with "-" between its
// parts -- so a key whose name is "-" cannot be told from the join, and
// "Control--" is unreadable in both directions. A menu has no such problem: it
// draws the combination as one run of glyphs with nothing between them, which
// is where "⌃⌥⌘Equal" reads as a mistake and "⌃⌥⌘=" reads as the key.
var keyGlyphs = map[Key]string{
	KeyMinus: "-", KeyEqual: "=",
	KeyLeftBracket: "[", KeyRightBracket: "]",
}

// Glyph is the key as macOS prints it on a menu: "=" rather than "Equal".
//
// It is [Key.String] for every key but the four whose printed character is one
// a written combination uses for something else. Use it for a MENU and for
// anything else drawn rather than parsed; use String where the result may be
// read back.
func (k Key) Glyph() string {
	if g, ok := keyGlyphs[k]; ok {
		return g
	}
	return k.String()
}

// Name spells the key out, where [Key.String] would print the glyph macOS puts
// on a menu: "Left" rather than "←", "Return" rather than "↩".
//
// The glyphs are right on a menu and in a terminal, and they are NOT in every
// font. Rendered in a window with a font that lacks them, "⌥⌘←" comes out as
// "Option-Command-" and stops — a line whose whole job is to say which
// combination was granted, saying nothing. So anywhere the font is not known,
// this is the one to use.
//
// A key with no name of its own still renders as its hexadecimal code, which
// is honest rather than wrong.
func (k Key) Name() string {
	if n, ok := keySpelled[k]; ok {
		return n
	}
	return k.String()
}

// keySpelled is every key [Key.String] prints as a glyph, in words. A key whose
// printed form is already a word — "Space", "F1", "A" — is not here, because
// there would be nothing to say about it.
var keySpelled = map[Key]string{
	KeyReturn:       "Return",
	KeyTab:          "Tab",
	KeyDelete:       "Delete",
	KeyEscape:       "Escape",
	KeyLeftArrow:    "Left",
	KeyRightArrow:   "Right",
	KeyDownArrow:    "Down",
	KeyUpArrow:      "Up",
	KeySlash:        "Slash",
	KeyMinus:        "Minus",
	KeyEqual:        "Equal",
	KeyLeftBracket:  "LeftBracket",
	KeyRightBracket: "RightBracket",
	KeyISOSection:   "ISOSection",
}

// Combo is a key plus its modifiers — one keyboard shortcut.
type Combo struct {
	Key  Key
	Mods Modifier
}

// String renders the combination the way macOS shows it to a person: "⌥⌘←",
// "⌃⌥⇧⌘Space". This is what you put in front of the user when the fallback
// gives them something other than what they asked for. A shortcut the user
// cannot be told about is worse than none.
func (c Combo) String() string { return c.Mods.String() + c.Key.String() }

// Names renders the combination in words — "Option-Command-←" — for logs and
// accessibility labels.
func (c Combo) Names() string {
	return strings.Join(append(c.Mods.Names(), c.Key.Name()), "-")
}

// Glyphs renders the combination as macOS prints it on a menu -- "⌃⌥⌘=" where
// [Combo.String] gives "⌃⌥⌘Equal".
//
// Three renderings rather than two, because they answer three questions. String
// is what a settings file round-trips. Names is what a font that has no ⌘ can
// still show. This is what goes on a menu row beside the thing it does, and
// there a key that says "Equal" is a key somebody looks for and does not find.
//
// ⛔ IT ASKS THE KEYBOARD FIRST. A [Key] is a POSITION, and this package's
// names are the ANSI legends for those positions -- so on a French layout the
// key this package calls Equal is printed "-", and a menu row saying "⌃⌥⌘="
// would be sending a person to a key that does nothing. [Key.Char] is what the
// system says is printed there; the ANSI name is the fallback for a key that
// prints nothing and for a platform with no layout to ask.
func (c Combo) Glyphs() string {
	if ch := c.Key.Char(); ch != "" {
		return c.Mods.String() + ch
	}
	return c.Mods.String() + c.Key.Glyph()
}

// Valid reports whether the combination can be claimed system-wide. It requires
// at least one modifier; see [ErrNoModifier].
func (c Combo) Valid() bool { return c.Mods != 0 }

// ---------------------------------------------------------------------------
// The fallback ladder.
// ---------------------------------------------------------------------------

// DefaultLadder is the order in which [Resolve] tries neighbouring
// combinations when the wanted one is taken: the same key with Shift added,
// then with Control added, then with both.
//
// The order is deliberate. Shift first because ⇧ combined with an existing
// modifier set is the least likely to collide with anything and reads most
// naturally on a menu; Control last-but-one because ⌃ is heavily used by the
// terminal and by text-editing key bindings; both together last because it is
// the most awkward to press.
var DefaultLadder = []Modifier{Shift, Control, Shift | Control}

// Candidates returns the combinations [Resolve] will try, in order: the wanted
// one first, then the wanted one with each ladder rung's modifiers added.
//
// A rung that adds nothing new — because the caller already asked for that
// modifier — is skipped rather than retried, so asking for ⇧⌥⌘← does not try
// ⇧⌥⌘← twice. Duplicate rungs are likewise collapsed.
func Candidates(want Combo, ladder []Modifier) []Combo {
	out := []Combo{want}
	seen := map[Modifier]bool{want.Mods: true}
	for _, add := range ladder {
		m := want.Mods | add
		if seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, Combo{Key: want.Key, Mods: m})
	}
	return out
}

// Registrar is the seam between the fallback policy and the operating system.
// [Resolve] speaks only to this, so the whole ladder — including the case where
// every candidate is taken — is testable on any platform with no Carbon at all.
//
// Claim must report [ErrComboTaken] (or an error wrapping it) when the
// combination is held by another Carbon hot-key holder. Any other error aborts
// the ladder, because it means something is wrong with the process rather than
// with this particular combination.
type Registrar interface {
	Claim(Combo) (Claim, error)
}

// Claim is a held hot key. Release gives the combination back to the system.
type Claim interface {
	Release() error
}

// Reserver reports combinations that are known to be taken WITHOUT asking the
// operating system to register them — the macOS system shortcuts that
// RegisterEventHotKey would happily hand out anyway. [SystemShortcuts]
// implements it. A nil Reserver means "check nothing", in which case only
// conflict kind 1 is detected.
type Reserver interface {
	Reserved(Combo) (reason string, taken bool)
}

// Resolve walks the fallback ladder and claims the first combination that is
// free, returning which one it got.
//
// Each candidate is first put to reserved (the system-shortcut check, conflict
// kind 2), and only then to reg.Claim (conflict kind 1). The order matters: a
// system shortcut registers with status 0, so asking Carbon first would
// "succeed" at claiming a combination the user cannot actually use.
//
// If every candidate is taken, Resolve returns [ErrNoCandidate] and nothing is
// registered. It never silently returns an unusable claim.
func Resolve(want Combo, ladder []Modifier, reg Registrar, reserved Reserver) (Combo, Claim, error) {
	return resolve(want, ladder, reg, reserved, false)
}

// ResolveBare is [Resolve] for a combination with no modifier. See
// [Options.BareKey] for when that is a reasonable thing to want, and for the
// obligation that comes with it.
func ResolveBare(want Combo, ladder []Modifier, reg Registrar, reserved Reserver) (Combo, Claim, error) {
	return resolve(want, ladder, reg, reserved, true)
}

func resolve(want Combo, ladder []Modifier, reg Registrar, reserved Reserver, bare bool) (Combo, Claim, error) {
	// No key check: Key(0) is the letter A on this platform (kVK_ANSI_A), so
	// "no key" is not a thing a Combo can express.
	if !bare && !want.Valid() {
		return Combo{}, nil, fmt.Errorf("%w: %s", ErrNoModifier, want)
	}
	if reg == nil {
		return Combo{}, nil, errors.New("hotkey: nil Registrar")
	}
	var why []string
	for _, c := range Candidates(want, ladder) {
		if reserved != nil {
			if reason, taken := reserved.Reserved(c); taken {
				why = append(why, fmt.Sprintf("%s: %s", c, reason))
				continue
			}
		}
		claim, err := reg.Claim(c)
		if err == nil {
			return c, claim, nil
		}
		if !errors.Is(err, ErrComboTaken) {
			return Combo{}, nil, fmt.Errorf("hotkey: claiming %s: %w", c, err)
		}
		why = append(why, fmt.Sprintf("%s: held by another application", c))
	}
	return Combo{}, nil, fmt.Errorf("%w (%s)", ErrNoCandidate, strings.Join(why, "; "))
}

// ---------------------------------------------------------------------------
// Options.
// ---------------------------------------------------------------------------

// Options tunes [Register]. The zero value is the sensible default: the
// [DefaultLadder], and the system-shortcut check switched on.
type Options struct {
	// Ladder overrides [DefaultLadder]. An explicitly empty (non-nil, len 0)
	// ladder disables the fallback entirely: the wanted combination is
	// claimed or [ErrNoCandidate] is returned.
	Ladder []Modifier

	// Reserved overrides the system-shortcut check. Leave it nil to use the
	// machine's effective set ([LoadSystemShortcuts]). Set it to
	// NoReserved{} to skip the check and let Carbon be the only authority,
	// accepting that conflict kind 2 then goes undetected.
	Reserved Reserver

	// BareKey allows a combination with NO MODIFIER — a plain arrow, Return,
	// Escape — which is otherwise refused with [ErrNoModifier].
	//
	// It is refused by default because a bare key claimed system-wide is taken
	// from every application on the machine, and a caller who did that by
	// accident would break typing everywhere. That reasoning holds for a claim
	// that lasts as long as the program.
	//
	// It does not hold for a claim that lasts as long as a MODE. go-xrkit/desk
	// puts a full-screen gallery on a pair of glasses and wants the arrows to
	// move the selection in it — plain arrows, because a person looking at a
	// grid should not have to hold three modifiers to walk it — and gives them
	// back the moment the gallery closes. The person is not typing into
	// anything while a gallery covers their view.
	//
	// So it is opt-in and named, and a caller who sets it is saying they know
	// what they are taking. RELEASE IT: a bare key left claimed is a keyboard
	// somebody else cannot use.
	BareKey bool

	// OnThisKeyboard reads each key's name as the LEGEND PRINTED ON THE KEY, and
	// claims whichever position prints it here.
	//
	// ⛔ WITHOUT IT A SETTINGS FILE MEANS SOMETHING DIFFERENT ON EVERY LAYOUT AND
	// SAYS NOTHING ABOUT IT. A [Key] is a virtual key code, which is a POSITION,
	// and this package's names are the ANSI legends for those positions. On a
	// French Mac the position called Equal prints "-", and "=" is over on the
	// position called Slash -- so "ctrl+alt+cmd+Equal" claimed the key printed
	// "-", the shortcut was granted, it fired, and the person pressing the key
	// printed "=" reached nothing at all. Every check reported it as granted,
	// because it was.
	//
	// It is an OPTION and not the default because the two readings are both
	// legitimate: a game wants the position (WASD is a shape under the hand,
	// whatever is printed there) and a shortcut wants the legend (a person
	// presses what the menu says). This package cannot tell which a caller means.
	//
	// It applies to [Register] alone, and once -- which is the point of it being
	// here rather than a method on a combination. Reading a key's ANSI name and
	// moving to the local key that prints it is a ONE-WAY interpretation: the
	// result is a position whose own ANSI name says something else, so a
	// transform a caller could apply twice would walk. Doing it at the moment a
	// combination becomes a claim is the one place it cannot happen twice.
	//
	// Keys with no printed character of their own -- the arrows, Return, Escape,
	// the function keys -- are never moved: no layout moves them, and there is
	// nothing to match on. Neither is a key whose legend this keyboard does not
	// print anywhere, which is what "[" is on French: nothing is silently
	// swapped, the claim stays where it was, and [Combo.Glyphs] then reports what
	// that key actually prints.
	//
	// [Hotkey.Wanted] is the combination as WRITTEN, so a caller can still say
	// what was asked for.
	OnThisKeyboard bool
}

// ladder returns the ladder to use, distinguishing "not set" (nil, use the
// default) from "deliberately empty" (no fallback).
func (o *Options) ladder() []Modifier {
	if o == nil || o.Ladder == nil {
		return DefaultLadder
	}
	return o.Ladder
}

// NoReserved is a [Reserver] that reserves nothing. Use it to opt out of the
// system-shortcut check.
type NoReserved struct{}

// Reserved implements [Reserver]. It never reports a combination as taken.
func (NoReserved) Reserved(Combo) (string, bool) { return "", false }

// sortedReasons is a small helper used by [SystemShortcuts.Describe] to keep
// output deterministic.
func sortedReasons(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// bareKey reports whether a modifier-less combination is allowed, tolerating a
// nil Options.
func (o *Options) bareKey() bool { return o != nil && o.BareKey }
