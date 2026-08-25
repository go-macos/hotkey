package hotkey

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Origin says where a [SystemShortcut]'s binding came from — which is the
// difference between a fact and a well-informed guess.
type Origin int

const (
	// FromDefaults means the binding comes from this package's built-in
	// table of macOS defaults. It is a hand-maintained LIST, not a query.
	FromDefaults Origin = iota
	// FromPreferences means the user rebound the shortcut and the key and
	// modifiers were read out of com.apple.symbolichotkeys. This is a fact
	// about this machine.
	FromPreferences
)

// String renders the origin.
func (o Origin) String() string {
	if o == FromPreferences {
		return "preferences"
	}
	return "built-in defaults list"
}

// SystemShortcut is one macOS system-wide shortcut: a Mission Control or
// Spotlight or input-source binding, the kind RegisterEventHotKey will hand you
// without complaint even though the window server will keep swallowing it.
type SystemShortcut struct {
	// ID is the com.apple.symbolichotkeys entry number.
	ID int
	// Name is what System Settings > Keyboard > Keyboard Shortcuts calls it.
	Name string
	// Combo is the key combination it occupies.
	Combo Combo
	// Enabled reports whether it is currently switched on. A disabled
	// shortcut does not reserve its combination.
	Enabled bool
	// Origin says whether Combo was read from this machine's preferences or
	// taken from the built-in defaults list.
	Origin Origin
}

// String renders the shortcut for a diagnostic listing.
func (s SystemShortcut) String() string {
	state := "enabled"
	if !s.Enabled {
		state = "disabled"
	}
	return fmt.Sprintf("%d %-38s %-10s %s (%s)", s.ID, s.Name, s.Combo, state, s.Origin)
}

// ---------------------------------------------------------------------------
// The built-in defaults list.
// ---------------------------------------------------------------------------

// defaultShortcuts is a HAND-MAINTAINED LIST of the macOS system shortcuts that
// are on by default, not the result of any query.
//
// It has to be a list, and this is the single most important finding behind
// this package. com.apple.symbolichotkeys is an OVERRIDE LAYER, not a
// catalogue: it holds only the entries the user has changed. Measured on macOS
// 26.6.2, the domain held 19 entries while macOS defines on the order of a
// hundred symbolic hot keys — and ⌥⌘Space, which is live on that machine as the
// Finder's search window, was ABSENT from it. Enumerating the domain and
// stopping there would therefore miss most of the conflicts that matter, and
// would miss them silently.
//
// Worse, entries in the domain frequently carry only an "enabled" flag with no
// "value" at all (79, 80, 81 and 82 on that machine — the space-switching
// shortcuts). For those the domain says the shortcut is on but does not say
// what it is bound to. Only a defaults list can supply the binding.
//
// So the effective set is: this list, with the preference domain layered over
// it. See [Merge].
//
// The list is deliberately limited to shortcuts that are enabled by default on
// a stock macOS and whose binding is stable. It will drift as Apple changes
// defaults; it is a best effort, clearly marked, and [SystemShortcut.Origin]
// tells a caller which entries rest on it.
var defaultShortcuts = []SystemShortcut{
	{ID: 7, Name: "Move focus to the Dock", Combo: Combo{KeyF3, Control}},
	{ID: 8, Name: "Move focus to active or next window", Combo: Combo{KeyF4, Control}},
	{ID: 9, Name: "Move focus to window toolbar", Combo: Combo{KeyF5, Control}},
	{ID: 10, Name: "Move focus to floating window", Combo: Combo{KeyF6, Control}},
	{ID: 27, Name: "Move focus to the menu bar", Combo: Combo{KeyF2, Control}},
	{ID: 28, Name: "Save picture of screen as a file", Combo: Combo{KeyN3, Shift | Command}},
	{ID: 29, Name: "Copy picture of screen to clipboard", Combo: Combo{KeyN3, Control | Shift | Command}},
	{ID: 30, Name: "Save picture of selected area as a file", Combo: Combo{KeyN4, Shift | Command}},
	{ID: 31, Name: "Copy picture of selected area to clipboard", Combo: Combo{KeyN4, Control | Shift | Command}},
	{ID: 32, Name: "Mission Control", Combo: Combo{KeyUpArrow, Control}},
	{ID: 33, Name: "Application windows", Combo: Combo{KeyDownArrow, Control}},
	{ID: 57, Name: "Move focus to status menus", Combo: Combo{KeyF8, Control}},
	{ID: 59, Name: "Turn VoiceOver on or off", Combo: Combo{KeyF5, Command}},
	{ID: 60, Name: "Select the previous input source", Combo: Combo{KeySpace, Control}},
	{ID: 61, Name: "Select the next input source", Combo: Combo{KeySpace, Control | Option}},
	{ID: 64, Name: "Show Spotlight search", Combo: Combo{KeySpace, Command}},
	{ID: 65, Name: "Show Finder search window", Combo: Combo{KeySpace, Option | Command}},
	{ID: 79, Name: "Move left a space", Combo: Combo{KeyLeftArrow, Control}},
	{ID: 80, Name: "Move right a space", Combo: Combo{KeyRightArrow, Control}},
	{ID: 81, Name: "Move up a space", Combo: Combo{KeyUpArrow, Control}},
	{ID: 82, Name: "Move down a space", Combo: Combo{KeyDownArrow, Control}},
	{ID: 98, Name: "Show help menu", Combo: Combo{KeySlash, Shift | Command}},
	{ID: 184, Name: "Screenshot and recording options", Combo: Combo{KeyN5, Shift | Command}},
}

// DefaultShortcuts returns a copy of the built-in defaults list. Every entry
// has [FromDefaults] as its origin. Callers may use it to show the user what
// this package believes is reserved, and to see plainly that it is a list.
func DefaultShortcuts() []SystemShortcut {
	out := make([]SystemShortcut, len(defaultShortcuts))
	copy(out, defaultShortcuts)
	for i := range out {
		out[i].Enabled = true
		out[i].Origin = FromDefaults
	}
	return out
}

// ---------------------------------------------------------------------------
// Parsing com.apple.symbolichotkeys.
// ---------------------------------------------------------------------------

// Override is one entry read out of the com.apple.symbolichotkeys domain. It is
// an override of a default, which is why Combo is optional: an entry may say
// only that a shortcut is switched off, without restating what it is bound to.
type Override struct {
	// ID is the entry number.
	ID int
	// Enabled is the entry's "enabled" flag.
	Enabled bool
	// Combo is the rebound combination, valid only when HasCombo is true.
	Combo Combo
	// HasCombo reports whether the entry carried a usable
	// value.parameters triple. Entries with only an "enabled" flag, and
	// entries whose parameters are the 65535 "unbound" sentinel, do not.
	HasCombo bool
}

// ParseSymbolicHotKeys reads the decoded com.apple.symbolichotkeys dictionary —
// the value of its "AppleSymbolicHotKeys" key — into a list of overrides,
// sorted by ID.
//
// The shape it expects, confirmed by dumping the real domain on macOS 26.6.2:
//
//	{"65": {"enabled": true,
//	        "value": {"type": "standard",
//	                  "parameters": [32, 49, 1572864]}}}
//
// parameters is [ASCII character, virtual key code, NSEventModifierFlags mask].
// The first element is 65535 when the shortcut has no character equivalent, and
// an all-65535 triple means the shortcut is unbound; both are handled.
//
// Parsing is deliberately tolerant. This is a preference file a user or a
// third-party tool may have written, so a malformed entry is skipped rather
// than failing the whole read: a hot key that cannot be claimed because one
// preference entry was odd would be a bad trade.
func ParseSymbolicHotKeys(raw map[string]any) []Override {
	var out []Override
	for k, v := range raw {
		id, err := strconv.Atoi(k)
		if err != nil {
			continue // not an entry number
		}
		entry, ok := v.(map[string]any)
		if !ok {
			continue
		}
		o := Override{ID: id, Enabled: asBool(entry["enabled"])}
		if c, ok := parseValue(entry["value"]); ok {
			o.Combo, o.HasCombo = c, true
		}
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// parseValue pulls a Combo out of an entry's "value" dictionary.
func parseValue(v any) (Combo, bool) {
	dict, ok := v.(map[string]any)
	if !ok {
		return Combo{}, false
	}
	if t, ok := dict["type"].(string); ok && t != "standard" {
		return Combo{}, false // only the "standard" encoding is understood
	}
	params, ok := dict["parameters"].([]any)
	if !ok || len(params) < 3 {
		return Combo{}, false
	}
	code, ok1 := asNumber(params[1])
	mask, ok2 := asNumber(params[2])
	if !ok1 || !ok2 {
		return Combo{}, false
	}
	// 65535 (0xFFFF) is the "no such parameter" sentinel; an entry bound to
	// nothing carries it in every slot.
	if code == 65535 {
		return Combo{}, false
	}
	mods := modifierFromCocoa(uint32(mask))
	if mods == 0 {
		return Combo{}, false // a system shortcut with no modifier is not one we can reason about
	}
	return Combo{Key: Key(code), Mods: mods}, true
}

// asBool reads a JSON/plist boolean, tolerating the 0/1 numeric form some
// writers use.
func asBool(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case float64:
		return b != 0
	case int:
		return b != 0
	}
	return false
}

// asNumber reads a JSON/plist number in any of the forms a decoder may produce.
func asNumber(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int:
		return int64(n), true
	case int64:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// Merging defaults with the preference overrides.
// ---------------------------------------------------------------------------

// SystemShortcuts is the effective set of macOS system shortcuts on a machine:
// the built-in defaults list with the com.apple.symbolichotkeys overrides
// layered over it. It implements [Reserver].
type SystemShortcuts struct {
	all     []SystemShortcut
	byCombo map[Combo][]SystemShortcut
}

// Merge layers preference overrides over a defaults list and returns the
// effective set.
//
// Three things happen, and each corresponds to something really seen in the
// domain on macOS 26.6.2:
//
//   - An override with a binding (entry 60: parameters [32, 49, 262144])
//     replaces the default's combination.
//   - An override with only an "enabled" flag (entries 79-82) changes only
//     whether the default is on. Its binding still comes from the list.
//   - An override for an ID the list does not know is kept if it carries a
//     binding, and dropped if it does not — an unknown ID with no binding
//     tells us nothing usable.
//
// Disabled shortcuts are retained in the set but do not reserve their
// combination; see [SystemShortcuts.Reserved].
func Merge(defaults []SystemShortcut, overrides []Override) *SystemShortcuts {
	byID := make(map[int]SystemShortcut, len(defaults))
	order := make([]int, 0, len(defaults))
	for _, d := range defaults {
		if _, seen := byID[d.ID]; !seen {
			order = append(order, d.ID)
		}
		byID[d.ID] = d
	}
	for _, o := range overrides {
		s, known := byID[o.ID]
		switch {
		case known:
			s.Enabled = o.Enabled
			if o.HasCombo {
				s.Combo, s.Origin = o.Combo, FromPreferences
			}
			byID[o.ID] = s
		case o.HasCombo:
			byID[o.ID] = SystemShortcut{
				ID:      o.ID,
				Name:    fmt.Sprintf("symbolic hot key %d", o.ID),
				Combo:   o.Combo,
				Enabled: o.Enabled,
				Origin:  FromPreferences,
			}
			order = append(order, o.ID)
		}
		// An unknown ID with no binding is dropped: nothing usable.
	}
	s := &SystemShortcuts{byCombo: map[Combo][]SystemShortcut{}}
	for _, id := range order {
		e := byID[id]
		s.all = append(s.all, e)
		s.byCombo[e.Combo] = append(s.byCombo[e.Combo], e)
	}
	return s
}

// Reserved implements [Reserver]. It reports a combination as taken when an
// ENABLED system shortcut occupies it, and says which one — so the caller can
// tell the user "⌥⌘Space is the Finder's search window" rather than just "no".
func (s *SystemShortcuts) Reserved(c Combo) (string, bool) {
	if s == nil {
		return "", false
	}
	names := map[string]bool{}
	for _, e := range s.byCombo[c] {
		if e.Enabled {
			names[fmt.Sprintf("%s (macOS: %s, per %s)", e.Name, e.Combo, e.Origin)] = true
		}
	}
	if len(names) == 0 {
		return "", false
	}
	return strings.Join(sortedReasons(names), ", "), true
}

// All returns the effective shortcuts, ordered by the defaults list and then by
// any extra entries found in preferences.
func (s *SystemShortcuts) All() []SystemShortcut {
	if s == nil {
		return nil
	}
	out := make([]SystemShortcut, len(s.all))
	copy(out, s.all)
	return out
}

// Describe renders the effective set as a diagnostic listing, one shortcut per
// line. It is what to print when a user asks why they did not get the
// combination they wanted.
func (s *SystemShortcuts) Describe() string {
	var b strings.Builder
	for _, e := range s.All() {
		b.WriteString(e.String())
		b.WriteByte('\n')
	}
	return b.String()
}
