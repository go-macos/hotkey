package hotkey

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// These tests cover the whole portable layer — the fallback ladder, the
// formatting a person reads, and the parsing of the system-shortcut data. Not
// one of them touches Carbon, which is the point: they run identically on a
// Linux runner and on a Mac, and they are what the 100% coverage gate is on.

// ---------------------------------------------------------------------------
// Modifiers.
// ---------------------------------------------------------------------------

func TestModifierCarbon(t *testing.T) {
	for _, tc := range []struct {
		m    Modifier
		want uint32
	}{
		{0, 0},
		{Command, 0x0100},
		{Shift, 0x0200},
		{Option, 0x0800},
		{Control, 0x1000},
		{Option | Command, 0x0900},
		{Control | Option | Shift | Command, 0x1B00},
	} {
		if got := tc.m.carbon(); got != tc.want {
			t.Errorf("%v.carbon() = %#04x, want %#04x", tc.m, got, tc.want)
		}
	}
}

func TestModifierCocoaRoundTrip(t *testing.T) {
	for m := Modifier(0); m <= Control|Option|Shift|Command; m++ {
		if got := modifierFromCocoa(m.cocoa()); got != m {
			t.Errorf("round trip of %v gave %v", m, got)
		}
	}
	// Bits this package does not model — Fn, CapsLock, the device-dependent
	// left/right flags — must be ignored rather than confusing the result.
	const capsLock, fnKey, deviceRightCmd = 1 << 16, 1 << 23, 0x000010
	if got := modifierFromCocoa(cocoaCommand | capsLock | fnKey | deviceRightCmd); got != Command {
		t.Errorf("unmodelled bits leaked through: got %v, want %v", got, Command)
	}
}

func TestModifierString(t *testing.T) {
	for _, tc := range []struct {
		m    Modifier
		want string
	}{
		{0, ""},
		{Command, "⌘"},
		{Option | Command, "⌥⌘"},
		// Apple's order is Control, Option, Shift, Command — NOT the order
		// the bits happen to be declared in.
		{Shift | Option | Command, "⌥⇧⌘"},
		{Control | Option | Shift | Command, "⌃⌥⇧⌘"},
	} {
		if got := tc.m.String(); got != tc.want {
			t.Errorf("%d.String() = %q, want %q", tc.m, got, tc.want)
		}
	}
}

func TestModifierNames(t *testing.T) {
	if got := Modifier(0).Names(); got != nil {
		t.Errorf("empty set gave %v, want nil", got)
	}
	got := (Control | Option | Shift | Command).Names()
	want := []string{"Control", "Option", "Shift", "Command"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Names() = %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// Keys and combinations.
// ---------------------------------------------------------------------------

func TestKeyString(t *testing.T) {
	for _, tc := range []struct {
		k    Key
		want string
	}{
		{KeyLeftArrow, "←"},
		{KeyRightArrow, "→"},
		{KeyUpArrow, "↑"},
		{KeyDownArrow, "↓"},
		{KeySpace, "Space"},
		{KeyF13, "F13"},
		{KeyA, "A"},
		{KeyN0, "0"},
		{KeySlash, "/"},
		// An unnamed code is rendered honestly as a code: this package does
		// not consult the keyboard layout, so it cannot know the character.
		{Key(0x99), "key 0x99"},
	} {
		if got := tc.k.String(); got != tc.want {
			t.Errorf("Key(%#x).String() = %q, want %q", uint16(tc.k), got, tc.want)
		}
	}
}

// TestKeyNamesAreUnique guards against a copy-paste slip in the key table that
// would make two different keys print the same thing to a user.
func TestKeyNamesAreUnique(t *testing.T) {
	seen := map[string]Key{}
	for k, n := range keyNames {
		if prev, dup := seen[n]; dup {
			t.Errorf("keys %#x and %#x both render as %q", uint16(prev), uint16(k), n)
		}
		seen[n] = k
	}
}

func TestComboString(t *testing.T) {
	c := Combo{Key: KeyLeftArrow, Mods: Option | Command}
	if got, want := c.String(), "⌥⌘←"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if got, want := c.Names(), "Option-Command-←"; got != want {
		t.Errorf("Names() = %q, want %q", got, want)
	}
	bare := Combo{Key: KeySpace}
	if got, want := bare.Names(), "Space"; got != want {
		t.Errorf("bare Names() = %q, want %q", got, want)
	}
}

func TestComboValid(t *testing.T) {
	if (Combo{Key: KeySpace}).Valid() {
		t.Error("a combination with no modifier must not be valid")
	}
	if !(Combo{Key: KeySpace, Mods: Command}).Valid() {
		t.Error("⌘Space must be valid")
	}
}

// ---------------------------------------------------------------------------
// The ladder.
// ---------------------------------------------------------------------------

func TestCandidates(t *testing.T) {
	want := Combo{Key: KeyLeftArrow, Mods: Option | Command}
	got := Candidates(want, DefaultLadder)
	exp := []string{"⌥⌘←", "⌥⇧⌘←", "⌃⌥⌘←", "⌃⌥⇧⌘←"}
	if len(got) != len(exp) {
		t.Fatalf("got %d candidates (%v), want %d", len(got), got, len(exp))
	}
	for i, c := range got {
		if c.String() != exp[i] {
			t.Errorf("candidate %d = %q, want %q", i, c.String(), exp[i])
		}
	}
}

// TestCandidatesSkipsRungsThatAddNothing is the case that would otherwise ask
// the operating system for the same combination twice and report the second
// refusal as a conflict.
func TestCandidatesSkipsRungsThatAddNothing(t *testing.T) {
	want := Combo{Key: KeyLeftArrow, Mods: Shift | Option | Command}
	got := Candidates(want, DefaultLadder)
	exp := []string{"⌥⇧⌘←", "⌃⌥⇧⌘←"}
	if len(got) != len(exp) {
		t.Fatalf("got %v, want %v", got, exp)
	}
	for i, c := range got {
		if c.String() != exp[i] {
			t.Errorf("candidate %d = %q, want %q", i, c.String(), exp[i])
		}
	}
	// A ladder that repeats a rung must collapse too.
	if n := len(Candidates(Combo{Key: KeyA, Mods: Command}, []Modifier{Shift, Shift, Shift})); n != 2 {
		t.Errorf("a thrice-repeated rung produced %d candidates, want 2", n)
	}
	// An empty ladder means no fallback at all.
	if n := len(Candidates(Combo{Key: KeyA, Mods: Command}, nil)); n != 1 {
		t.Errorf("an empty ladder produced %d candidates, want 1", n)
	}
}

// fakeRegistrar is the seam that makes the whole ladder testable with no
// Carbon anywhere.
type fakeRegistrar struct {
	taken  map[Combo]bool
	fail   map[Combo]error
	claims []Combo
	closed []Combo
}

type fakeClaim struct {
	reg *fakeRegistrar
	c   Combo
}

func (f *fakeClaim) Release() error {
	f.reg.closed = append(f.reg.closed, f.c)
	delete(f.reg.taken, f.c)
	return nil
}

func (f *fakeRegistrar) Claim(c Combo) (Claim, error) {
	f.claims = append(f.claims, c)
	if err, ok := f.fail[c]; ok {
		return nil, err
	}
	if f.taken[c] {
		return nil, fmt.Errorf("%w: %s", ErrComboTaken, c)
	}
	if f.taken == nil {
		f.taken = map[Combo]bool{}
	}
	f.taken[c] = true
	return &fakeClaim{reg: f, c: c}, nil
}

func TestResolveTakesTheFirstFree(t *testing.T) {
	want := Combo{Key: KeyLeftArrow, Mods: Option | Command}
	reg := &fakeRegistrar{}
	got, claim, err := Resolve(want, DefaultLadder, reg, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	if len(reg.claims) != 1 {
		t.Errorf("asked the OS %d times, want 1", len(reg.claims))
	}
	if err := claim.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestResolveFallsBack(t *testing.T) {
	want := Combo{Key: KeyLeftArrow, Mods: Option | Command}
	reg := &fakeRegistrar{taken: map[Combo]bool{want: true}}
	got, _, err := Resolve(want, DefaultLadder, reg, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.String() != "⌥⇧⌘←" {
		t.Fatalf("fell back to %s, want ⌥⇧⌘←", got)
	}
}

// TestResolveSkipsReservedBeforeAsking is the whole reason the Reserver seam
// exists: a macOS system shortcut registers with status 0, so asking Carbon
// first would "succeed" at claiming a combination the user cannot use.
func TestResolveSkipsReservedBeforeAsking(t *testing.T) {
	want := Combo{Key: KeySpace, Mods: Option | Command}
	reg := &fakeRegistrar{}
	sys := Merge(DefaultShortcuts(), nil)
	got, _, err := Resolve(want, DefaultLadder, reg, sys)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got == want {
		t.Fatal("⌥⌘Space is the Finder's search window; it must not be handed out")
	}
	for _, asked := range reg.claims {
		if asked == want {
			t.Fatal("the OS was asked for a combination already known to be reserved")
		}
	}
	t.Logf("⌥⌘Space is reserved, so Resolve gave %s instead", got)
}

func TestResolveEveryCandidateTaken(t *testing.T) {
	want := Combo{Key: KeyLeftArrow, Mods: Option | Command}
	reg := &fakeRegistrar{taken: map[Combo]bool{}}
	for _, c := range Candidates(want, DefaultLadder) {
		reg.taken[c] = true
	}
	got, claim, err := Resolve(want, DefaultLadder, reg, nil)
	if !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("got %v, want ErrNoCandidate", err)
	}
	if claim != nil || got != (Combo{}) {
		t.Fatal("nothing must be returned when nothing was claimed")
	}
	// The error has to name every combination it tried, or a user cannot act
	// on it.
	for _, c := range Candidates(want, DefaultLadder) {
		if !strings.Contains(err.Error(), c.String()) {
			t.Errorf("the error does not mention %s: %v", c, err)
		}
	}
}

func TestResolveEveryCandidateReserved(t *testing.T) {
	want := Combo{Key: KeyLeftArrow, Mods: Command}
	reg := &fakeRegistrar{}
	all := everythingReserved{}
	_, _, err := Resolve(want, DefaultLadder, reg, all)
	if !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("got %v, want ErrNoCandidate", err)
	}
	if len(reg.claims) != 0 {
		t.Errorf("the OS was asked %d times though everything was reserved", len(reg.claims))
	}
	if !strings.Contains(err.Error(), "because I said so") {
		t.Errorf("the reserver's reason is missing from %v", err)
	}
}

type everythingReserved struct{}

func (everythingReserved) Reserved(Combo) (string, bool) { return "because I said so", true }

func TestResolveNeedsAModifier(t *testing.T) {
	_, _, err := Resolve(Combo{Key: KeySpace}, DefaultLadder, &fakeRegistrar{}, nil)
	if !errors.Is(err, ErrNoModifier) {
		t.Fatalf("got %v, want ErrNoModifier", err)
	}
}

func TestResolveNilRegistrar(t *testing.T) {
	_, _, err := Resolve(Combo{Key: KeySpace, Mods: Command}, DefaultLadder, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "nil Registrar") {
		t.Fatalf("got %v, want a nil-Registrar error", err)
	}
}

// TestResolveAbortsOnAnUnrelatedError: an error that is NOT "taken" means
// something is wrong with the process, not with this combination, so walking
// further down the ladder would just repeat the same failure three more times
// and report the wrong cause.
func TestResolveAbortsOnAnUnrelatedError(t *testing.T) {
	want := Combo{Key: KeyLeftArrow, Mods: Option | Command}
	boom := errors.New("the event target is null")
	reg := &fakeRegistrar{fail: map[Combo]error{want: boom}}
	_, _, err := Resolve(want, DefaultLadder, reg, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want the underlying error", err)
	}
	if len(reg.claims) != 1 {
		t.Errorf("kept walking the ladder after a fatal error: %d attempts", len(reg.claims))
	}
}

// ---------------------------------------------------------------------------
// Options.
// ---------------------------------------------------------------------------

func TestOptionsLadder(t *testing.T) {
	var nilOpts *Options
	if got := nilOpts.ladder(); len(got) != len(DefaultLadder) {
		t.Errorf("nil Options gave %v, want DefaultLadder", got)
	}
	if got := (&Options{}).ladder(); len(got) != len(DefaultLadder) {
		t.Errorf("zero Options gave %v, want DefaultLadder", got)
	}
	// nil means "use the default"; an explicitly empty ladder means "no
	// fallback". Conflating the two would silently substitute a combination
	// for a caller who asked not to have one substituted.
	if got := (&Options{Ladder: []Modifier{}}).ladder(); len(got) != 0 {
		t.Errorf("an explicitly empty ladder gave %v, want no fallback", got)
	}
	if got := (&Options{Ladder: []Modifier{Shift}}).ladder(); len(got) != 1 {
		t.Errorf("an explicit ladder gave %v", got)
	}
}

func TestNoReserved(t *testing.T) {
	if reason, taken := (NoReserved{}).Reserved(Combo{Key: KeySpace, Mods: Command}); taken || reason != "" {
		t.Errorf("NoReserved reserved something: %q %v", reason, taken)
	}
}

// ---------------------------------------------------------------------------
// Origin and SystemShortcut rendering.
// ---------------------------------------------------------------------------

func TestOriginString(t *testing.T) {
	if got, want := FromPreferences.String(), "preferences"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := FromDefaults.String(), "built-in defaults list"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := Origin(99).String(), "built-in defaults list"; got != want {
		t.Errorf("an unknown origin gave %q, want %q", got, want)
	}
}

func TestSystemShortcutString(t *testing.T) {
	on := SystemShortcut{ID: 65, Name: "Show Finder search window",
		Combo: Combo{Key: KeySpace, Mods: Option | Command}, Enabled: true}
	if !strings.Contains(on.String(), "enabled") || !strings.Contains(on.String(), "⌥⌘Space") {
		t.Errorf("bad rendering: %q", on.String())
	}
	off := on
	off.Enabled = false
	if !strings.Contains(off.String(), "disabled") {
		t.Errorf("bad rendering: %q", off.String())
	}
}

func TestDefaultShortcutsIsACopy(t *testing.T) {
	a := DefaultShortcuts()
	if len(a) == 0 {
		t.Fatal("the defaults list is empty")
	}
	a[0].Name = "scribbled on"
	if DefaultShortcuts()[0].Name == "scribbled on" {
		t.Fatal("DefaultShortcuts hands out the package's own slice")
	}
	for _, s := range a {
		if !s.Enabled || s.Origin != FromDefaults {
			t.Errorf("%d: Enabled=%v Origin=%v, want true/FromDefaults", s.ID, s.Enabled, s.Origin)
		}
	}
}

// TestDefaultShortcutsHasNoDuplicateIDs guards the list itself: a duplicated
// ID would make the last one silently win the merge.
func TestDefaultShortcutsHasNoDuplicateIDs(t *testing.T) {
	seen := map[int]bool{}
	for _, s := range DefaultShortcuts() {
		if seen[s.ID] {
			t.Errorf("duplicate symbolic hot key id %d", s.ID)
		}
		seen[s.ID] = true
		if s.Name == "" {
			t.Errorf("%d has no name", s.ID)
		}
		if !s.Combo.Valid() {
			t.Errorf("%d (%s) has no modifier", s.ID, s.Name)
		}
	}
}

// ---------------------------------------------------------------------------
// Parsing com.apple.symbolichotkeys.
// ---------------------------------------------------------------------------

// realDomain is the shape really found in the domain on macOS 26.6.2 (entries
// 60, 61, 79-82 and 164 verbatim), decoded the way the darwin reader decodes
// it. It is the corpus these tests are written against, so what is asserted
// here is what a real machine produces rather than what a blog post says.
const realDomain = `{
  "15":  {"enabled": false},
  "60":  {"enabled": true,  "value": {"parameters": [32, 49, 262144],  "type": "standard"}},
  "61":  {"enabled": true,  "value": {"parameters": [32, 49, 786432],  "type": "standard"}},
  "79":  {"enabled": true},
  "80":  {"enabled": true},
  "81":  {"enabled": true},
  "82":  {"enabled": true},
  "164": {"enabled": false, "value": {"parameters": [65535, 65535, 0], "type": "standard"}}
}`

func decodeDomain(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("bad test corpus: %v", err)
	}
	return m
}

func TestParseTheRealDomain(t *testing.T) {
	got := ParseSymbolicHotKeys(decodeDomain(t, realDomain))
	if len(got) != 8 {
		t.Fatalf("got %d overrides, want 8", len(got))
	}
	// Sorted by ID, so the caller sees a stable listing.
	for i := 1; i < len(got); i++ {
		if got[i-1].ID >= got[i].ID {
			t.Fatalf("not sorted by ID: %v", got)
		}
	}
	byID := indexByID(got)
	// 15: an "enabled" flag and nothing else.
	if o := byID[15]; o.Enabled || o.HasCombo {
		t.Errorf("15: got %+v, want disabled with no binding", o)
	}
	// 60: ⌃Space — 262144 is NSEventModifierFlagControl.
	if o := byID[60]; !o.Enabled || !o.HasCombo || o.Combo.String() != "⌃Space" {
		t.Errorf("60: got %+v (%s), want ⌃Space", o, o.Combo)
	}
	// 61: ⌃⌥Space — 786432 is Control|Option.
	if o := byID[61]; o.Combo.String() != "⌃⌥Space" {
		t.Errorf("61: got %s, want ⌃⌥Space", o.Combo)
	}
	// 79-82: enabled, but the domain does not say what they are bound to.
	// This is the case that makes a pure query useless.
	for _, id := range []int{79, 80, 81, 82} {
		if o := byID[id]; !o.Enabled || o.HasCombo {
			t.Errorf("%d: got %+v, want enabled with NO binding", id, o)
		}
	}
	// 164: the all-65535 "unbound" sentinel.
	if o := byID[164]; o.HasCombo {
		t.Errorf("164: the 65535 sentinel was read as a binding: %s", o.Combo)
	}
}

func TestParseIsTolerant(t *testing.T) {
	raw := decodeDomain(t, `{
	  "not a number": {"enabled": true},
	  "7":   "not a dictionary",
	  "8":   {"enabled": true, "value": "not a dictionary"},
	  "9":   {"enabled": true, "value": {"type": "modifier", "parameters": [0, 49, 262144]}},
	  "10":  {"enabled": true, "value": {"parameters": "not an array"}},
	  "11":  {"enabled": true, "value": {"parameters": [32, 49]}},
	  "12":  {"enabled": true, "value": {"parameters": [32, "x", 262144]}},
	  "13":  {"enabled": true, "value": {"parameters": [32, 49, "x"]}},
	  "14":  {"enabled": true, "value": {"parameters": [32, 49, 0]}},
	  "15":  {"enabled": 1,    "value": {"parameters": [32, 49, 262144]}},
	  "16":  {"enabled": "yes","value": {"parameters": [32, 49, 262144]}},
	  "17":  {"value": {"type": 12, "parameters": [32, 49, 262144]}}
	}`)
	got := ParseSymbolicHotKeys(raw)
	// 11 numeric keys are present; entry 7 is not a dictionary at all and is
	// dropped whole, and so is the non-numeric key. 10 survive.
	if len(got) != 10 {
		t.Fatalf("got %d overrides, want 10", len(got))
	}
	if _, ok := indexByID(got)[7]; ok {
		t.Error("7 is not a dictionary; it should have been dropped entirely")
	}
	byID := indexByID(got)
	for _, id := range []int{8, 9, 10, 11, 12, 13, 14} {
		if byID[id].HasCombo {
			t.Errorf("%d: a malformed entry produced a binding %s", id, byID[id].Combo)
		}
	}
	// enabled: 1 is the numeric form some writers use.
	if !byID[15].Enabled {
		t.Error("15: enabled:1 was not read as true")
	}
	// enabled: "yes" is not something to guess at.
	if byID[16].Enabled {
		t.Error("16: enabled:\"yes\" was read as true")
	}
	// A non-string "type" is not the "standard" encoding being rejected —
	// the type assertion simply fails and the parameters are still read.
	if !byID[17].HasCombo {
		t.Error("17: a non-string type should not discard a usable binding")
	}
	if byID[17].Enabled {
		t.Error("17: a missing enabled flag must read as false")
	}
}

func indexByID(os []Override) map[int]Override {
	m := map[int]Override{}
	for _, o := range os {
		m[o.ID] = o
	}
	return m
}

func TestParseNilAndEmpty(t *testing.T) {
	if got := ParseSymbolicHotKeys(nil); got != nil {
		t.Errorf("nil gave %v", got)
	}
	if got := ParseSymbolicHotKeys(map[string]any{}); got != nil {
		t.Errorf("empty gave %v", got)
	}
}

func TestAsNumber(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want int64
		ok   bool
	}{
		{float64(49), 49, true},
		{int(49), 49, true},
		{int64(49), 49, true},
		{json.Number("49"), 49, true},
		{json.Number("4.9"), 0, false},
		{"49", 0, false},
		{nil, 0, false},
	} {
		got, ok := asNumber(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("asNumber(%#v) = %d,%v want %d,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestAsBool(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want bool
	}{
		{true, true}, {false, false},
		{float64(1), true}, {float64(0), false},
		{int(1), true}, {int(0), false},
		{"true", false}, {nil, false},
	} {
		if got := asBool(tc.in); got != tc.want {
			t.Errorf("asBool(%#v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Merging.
// ---------------------------------------------------------------------------

func TestMergeAgainstTheRealDomain(t *testing.T) {
	s := Merge(DefaultShortcuts(), ParseSymbolicHotKeys(decodeDomain(t, realDomain)))
	byID := map[int]SystemShortcut{}
	for _, e := range s.All() {
		byID[e.ID] = e
	}
	// 60 was rebound to ⌃Space by the user: the origin becomes preferences.
	if e := byID[60]; e.Origin != FromPreferences || e.Combo.String() != "⌃Space" {
		t.Errorf("60: got %s from %s, want ⌃Space from preferences", e.Combo, e.Origin)
	}
	// 79 says only "enabled" — the binding still has to come from the list,
	// and the origin must stay honest about that.
	if e := byID[79]; e.Origin != FromDefaults || e.Combo.String() != "⌃←" {
		t.Errorf("79: got %s from %s, want ⌃← from the defaults list", e.Combo, e.Origin)
	}
	// 164 is unknown to the list and carries no usable binding: dropped.
	if _, ok := byID[164]; ok {
		t.Error("164 was kept although it names no combination")
	}
	// 65, ⌥⌘Space, is absent from the domain entirely and must survive from
	// the list. This is the conflict the whole design exists for.
	if e := byID[65]; e.Combo.String() != "⌥⌘Space" || !e.Enabled {
		t.Errorf("65: got %+v, want an enabled ⌥⌘Space", e)
	}
}

func TestMergeKeepsAnUnknownEntryThatCarriesABinding(t *testing.T) {
	overrides := []Override{
		{ID: 9001, Enabled: true, HasCombo: true, Combo: Combo{Key: KeyF13, Mods: Command}},
		{ID: 9002, Enabled: true},
	}
	s := Merge(nil, overrides)
	all := s.All()
	if len(all) != 1 {
		t.Fatalf("got %d entries, want 1: %v", len(all), all)
	}
	if all[0].ID != 9001 || all[0].Origin != FromPreferences {
		t.Errorf("got %+v", all[0])
	}
	if !strings.Contains(all[0].Name, "9001") {
		t.Errorf("an unnamed entry should still say which id it is: %q", all[0].Name)
	}
}

func TestMergeDisablingRemovesTheReservation(t *testing.T) {
	c := Combo{Key: KeySpace, Mods: Option | Command}
	s := Merge(DefaultShortcuts(), []Override{{ID: 65, Enabled: false}})
	if _, taken := s.Reserved(c); taken {
		t.Error("⌥⌘Space is switched off in preferences; it must not be reserved")
	}
	// Still listed, so a caller can show the user it exists but is off.
	var found bool
	for _, e := range s.All() {
		if e.ID == 65 {
			found = true
			if e.Enabled {
				t.Error("65 should be listed as disabled")
			}
		}
	}
	if !found {
		t.Error("a disabled shortcut disappeared from the listing")
	}
}

func TestMergeToleratesADuplicateIDInTheDefaults(t *testing.T) {
	dup := []SystemShortcut{
		{ID: 1, Name: "first", Combo: Combo{Key: KeyF13, Mods: Command}, Enabled: true},
		{ID: 1, Name: "second", Combo: Combo{Key: KeyF14, Mods: Command}, Enabled: true},
	}
	s := Merge(dup, nil)
	if got := s.All(); len(got) != 1 || got[0].Name != "second" {
		t.Fatalf("got %v, want one entry named \"second\"", got)
	}
}

func TestReserved(t *testing.T) {
	s := Merge(DefaultShortcuts(), nil)
	reason, taken := s.Reserved(Combo{Key: KeySpace, Mods: Option | Command})
	if !taken {
		t.Fatal("⌥⌘Space must be reserved")
	}
	for _, want := range []string{"Finder", "⌥⌘Space", "built-in defaults list"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the reason %q does not mention %q — a user cannot act on that", reason, want)
		}
	}
	if _, taken := s.Reserved(Combo{Key: KeyF13, Mods: Option | Command}); taken {
		t.Error("⌥⌘F13 is bound by nothing on a stock macOS")
	}
	// A nil set reserves nothing rather than panicking, so a caller that
	// failed to load preferences still works.
	var nilSet *SystemShortcuts
	if _, taken := nilSet.Reserved(Combo{Key: KeySpace, Mods: Command}); taken {
		t.Error("a nil SystemShortcuts must reserve nothing")
	}
	if got := nilSet.All(); got != nil {
		t.Errorf("a nil SystemShortcuts listed %v", got)
	}
	if got := nilSet.Describe(); got != "" {
		t.Errorf("a nil SystemShortcuts described itself as %q", got)
	}
}

// TestReservedNamesEveryClaimant: two shortcuts on one combination must both be
// named, deterministically, or the diagnostic changes between runs.
func TestReservedNamesEveryClaimant(t *testing.T) {
	c := Combo{Key: KeyF13, Mods: Command}
	s := Merge([]SystemShortcut{
		{ID: 1, Name: "Zebra", Combo: c, Enabled: true},
		{ID: 2, Name: "Aardvark", Combo: c, Enabled: true},
		{ID: 3, Name: "Switched off", Combo: c, Enabled: false},
	}, nil)
	reason, taken := s.Reserved(c)
	if !taken {
		t.Fatal("not reserved")
	}
	if strings.Index(reason, "Aardvark") > strings.Index(reason, "Zebra") {
		t.Errorf("the reasons are not sorted, so the message is unstable: %q", reason)
	}
	if strings.Contains(reason, "Switched off") {
		t.Errorf("a disabled shortcut was named as a reason: %q", reason)
	}
}

func TestDescribe(t *testing.T) {
	s := Merge(DefaultShortcuts(), ParseSymbolicHotKeys(decodeDomain(t, realDomain)))
	out := s.Describe()
	if n := strings.Count(out, "\n"); n != len(s.All()) {
		t.Errorf("Describe wrote %d lines for %d shortcuts", n, len(s.All()))
	}
	if !strings.Contains(out, "Show Finder search window") {
		t.Errorf("Describe left out the Finder search window:\n%s", out)
	}
	if !strings.Contains(out, "preferences") {
		t.Errorf("Describe does not say which entries came from preferences:\n%s", out)
	}
}

func TestAllIsACopy(t *testing.T) {
	s := Merge(DefaultShortcuts(), nil)
	got := s.All()
	got[0].Name = "scribbled on"
	if s.All()[0].Name == "scribbled on" {
		t.Fatal("All hands out the set's own slice")
	}
}

// ---------------------------------------------------------------------------
// The three combinations the XR consumer actually wants.
// ---------------------------------------------------------------------------

// TestTheConsumersThreeShortcuts is the end-to-end policy check for the case
// this package was written for, run against a stock macOS defaults set with the
// real ladder and a registrar that grants everything Carbon would grant. Two of
// the three are free; ⌥⌘Space is the Finder's and gets substituted.
func TestTheConsumersThreeShortcuts(t *testing.T) {
	sys := Merge(DefaultShortcuts(), nil)
	reg := &fakeRegistrar{}
	for _, tc := range []struct {
		want Combo
		exp  string
	}{
		{Combo{Key: KeyLeftArrow, Mods: Option | Command}, "⌥⌘←"},
		{Combo{Key: KeyRightArrow, Mods: Option | Command}, "⌥⌘→"},
		{Combo{Key: KeySpace, Mods: Option | Command}, "⌥⇧⌘Space"},
	} {
		got, _, err := Resolve(tc.want, DefaultLadder, reg, sys)
		if err != nil {
			t.Fatalf("%s: %v", tc.want, err)
		}
		if got.String() != tc.exp {
			t.Errorf("asked for %s, got %s, want %s", tc.want, got, tc.exp)
		}
		t.Logf("asked %-9s → got %-12s (substituted: %v)", tc.want, got, got != tc.want)
	}
}
