//go:build darwin && integration

package hotkey

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/go-macos/objc"
)

// This is the LIVE suite. It talks to the real Carbon hot-key API and really
// claims system-wide combinations, so it is behind `-tags integration` AND the
// HOTKEY_INTEGRATION=1 environment guard: a CI runner must never have a hot key
// taken out from under it, and neither must the operator's machine.
//
// Everything it claims, it releases.
//
// Run it with:
//
//	HOTKEY_INTEGRATION=1 go test -tags integration -v -run TestLive .
//
// The combinations used here are built on F13/F14/F15, which no stock macOS
// binds and which the feasibility probe measured as free. Nothing here touches
// the operator's keyboard settings; the preference domain is only ever READ.

// ---------------------------------------------------------------------------
// The main thread: an NSApplication and a pump.
// ---------------------------------------------------------------------------

// pump is the channel the tests use to ask the main thread to service the
// AppKit event queue. Carbon delivers hot-key presses through that queue and
// through nothing else, so a test that waits for a press must let the main
// thread run.
var mainReady = make(chan struct{})

// TestMain pins the process main OS thread, creates the shared NSApplication on
// it, and then spends its life pumping the AppKit event queue while the tests
// run on another goroutine.
//
// This is not ceremony. -[NSApplication nextEventMatchingMask:...] is what
// drains the Carbon event queue, and the Carbon hot-key handler is called from
// inside that drain. Without a pump on the main thread a hot key registers
// perfectly and never fires — which is precisely the failure this suite exists
// to rule out.
func TestMain(m *testing.M) {
	if os.Getenv("HOTKEY_INTEGRATION") != "1" {
		os.Stderr.WriteString("live suite skipped: set HOTKEY_INTEGRATION=1 to run it\n")
		os.Exit(0)
	}
	runtime.LockOSThread()

	if err := objc.Load(objc.AppKit, objc.Foundation); err != nil {
		os.Stderr.WriteString("cannot load AppKit: " + err.Error() + "\n")
		os.Exit(1)
	}
	app := objc.App()
	if app == 0 {
		os.Stderr.WriteString("NSApplication could not be created\n")
		os.Exit(1)
	}
	// Accessory: no Dock icon, no menu bar. A hot key is global regardless.
	app.Send(objc.Sel("setActivationPolicy:"), 1)
	app.Send(objc.Sel("finishLaunching"))
	close(mainReady)

	go func() { os.Exit(m.Run()) }()

	// Some AppKit run loop must be pumping on this thread, or a hot key
	// registers perfectly and fires never. [NSApp run] is what a real
	// application does, so it is what the suite exercises. (A hand-rolled
	// -[NSApplication nextEventMatchingMask:untilDate:inMode:dequeue:] pump was
	// also put to a real keystroke and delivered presses just as well, so the
	// choice between them is not the thing that matters — having one is.)
	// NSApp run never returns; the test goroutine exits the process.
	app.Send(objc.Sel("run"))
}

// ---------------------------------------------------------------------------
// Claiming, refusing, releasing.
// ---------------------------------------------------------------------------

// TestLiveClaimIsExclusive is the proof that a claim is real, and it needs
// nobody to press a key.
//
// Registering the SAME combination twice returns eventHotKeyExistsErr (-9878),
// which can only happen if the first registration genuinely took the key from
// every other application in the session. The control is the second half: a
// combination that was never claimed registers with status 0. Without that
// control, -9878 would only prove that Carbon says no to something.
func TestLiveClaimIsExclusive(t *testing.T) {
	<-mainReady
	if err := initCarbon(); err != nil {
		t.Fatalf("initCarbon: %v", err)
	}
	reg := carbonRegistrar{}
	want := Combo{Key: KeyF13, Mods: Option | Command}

	first, err := reg.Claim(want)
	if err != nil {
		t.Fatalf("first claim of %s: %v", want, err)
	}
	defer first.Release()

	if _, err := reg.Claim(want); !errors.Is(err, ErrComboTaken) {
		t.Fatalf("second claim of %s: got %v, want ErrComboTaken", want, err)
	}
	t.Logf("second claim of %s refused with ErrComboTaken — the claim is exclusive", want)

	// The control: a combination nobody has claimed must succeed.
	control := Combo{Key: KeyF14, Mods: Control | Option | Command}
	c2, err := reg.Claim(control)
	if err != nil {
		t.Fatalf("control claim of %s: %v — -9878 above would prove nothing", control, err)
	}
	if err := c2.Release(); err != nil {
		t.Fatalf("releasing control: %v", err)
	}
	t.Logf("control %s claimed with status 0 — the refusal above means TAKEN, not ALWAYS", control)
}

// TestLiveReleaseReallyFrees claims, releases, and claims again. A Release that
// silently did nothing would be invisible until a user restarted the feature
// and found their shortcut gone, so it is asserted directly.
func TestLiveReleaseReallyFrees(t *testing.T) {
	<-mainReady
	if err := initCarbon(); err != nil {
		t.Fatalf("initCarbon: %v", err)
	}
	reg := carbonRegistrar{}
	c := Combo{Key: KeyF15, Mods: Option | Command}

	first, err := reg.Claim(c)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	// While held, it must be refused. That is what makes the re-claim below
	// mean something.
	if _, err := reg.Claim(c); !errors.Is(err, ErrComboTaken) {
		t.Fatalf("while held, second claim: got %v, want ErrComboTaken", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	second, err := reg.Claim(c)
	if err != nil {
		t.Fatalf("re-claim after release: %v — Release did NOT free the combination", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("second release: %v", err)
	}
	if err := second.Release(); !errors.Is(err, ErrClosed) {
		t.Fatalf("double release: got %v, want ErrClosed", err)
	}
	t.Logf("%s: claimed, refused while held, freed by Release, re-claimed", c)
}

// TestLiveFallbackLadder holds the wanted combination and then asks for it, so
// the ladder has to run against the real Carbon registrar rather than a fake.
// It asserts the substituted combination AND the name shown to a person.
func TestLiveFallbackLadder(t *testing.T) {
	<-mainReady
	if err := initCarbon(); err != nil {
		t.Fatalf("initCarbon: %v", err)
	}
	want := Combo{Key: KeyF13, Mods: Option | Command}

	blocker, err := carbonRegistrar{}.Claim(want)
	if err != nil {
		t.Fatalf("blocking %s: %v", want, err)
	}
	defer blocker.Release()

	// NoReserved so this exercises the Carbon rung of the ladder (conflict
	// kind 1) and not the defaults list.
	h, err := Register(want, &Options{Reserved: NoReserved{}})
	if err != nil {
		t.Fatalf("Register with %s held: %v", want, err)
	}
	defer h.Close()

	if !h.Substituted() {
		t.Fatalf("got %s, expected a substitution", h.Combo())
	}
	if got, exp := h.Combo(), (Combo{Key: KeyF13, Mods: Shift | Option | Command}); got != exp {
		t.Fatalf("fell back to %s, want %s (the first rung of DefaultLadder)", got, exp)
	}
	// Apple's canonical modifier order is Control, Option, Shift, Command —
	// so Shift+Option+Command prints ⌥⇧⌘, not ⇧⌥⌘. Asserting the exact
	// string is the point: this is what gets shown to a person.
	if got, exp := h.Combo().String(), "⌥⇧⌘F13"; got != exp {
		t.Fatalf("shown to the user as %q, want %q", got, exp)
	}
	// And the name really names what was claimed: claiming it again must be
	// refused, so the string is not describing some other combination.
	if _, err := (carbonRegistrar{}).Claim(h.Combo()); !errors.Is(err, ErrComboTaken) {
		t.Fatalf("the combination named %s is not actually held: %v", h.Combo(), err)
	}
	t.Logf("%s was held; got %s, shown as %q, and that combination is genuinely claimed",
		want, h.Combo(), h.Combo().String())
}

// TestLiveEveryCandidateTaken holds all four rungs and asserts that Register
// refuses rather than returning something unusable.
func TestLiveEveryCandidateTaken(t *testing.T) {
	<-mainReady
	if err := initCarbon(); err != nil {
		t.Fatalf("initCarbon: %v", err)
	}
	want := Combo{Key: KeyF14, Mods: Option | Command}
	for _, c := range Candidates(want, DefaultLadder) {
		claim, err := carbonRegistrar{}.Claim(c)
		if err != nil {
			t.Fatalf("blocking %s: %v", c, err)
		}
		defer claim.Release()
	}
	_, err := Register(want, &Options{Reserved: NoReserved{}})
	if !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("with every rung held: got %v, want ErrNoCandidate", err)
	}
	t.Logf("every rung held → %v", err)
}

// ---------------------------------------------------------------------------
// Firing.
// ---------------------------------------------------------------------------

// CoreGraphics entry points used to synthesise a key press. Posting an event is
// the ONLY way to prove firing without a person at the keyboard, and whether it
// is permitted is itself a finding — see the test.
var (
	cgEventCreateKeyboardEvent func(source uintptr, keycode uint16, keyDown bool) uintptr
	cgEventSetFlags            func(event uintptr, flags uint64)
	cgEventPost                func(tap uint32, event uintptr)
	cfRelease                  func(ref uintptr)
	axIsProcessTrusted         func() bool
	cgEventSourceCounter       func(sourceState, eventType uint32) uint32
)

// kCGEventKeyDown, and the source state whose counter every posted key event
// increments. Reading this counter is the witness that says whether CGEventPost
// did anything at all — without it, "the hot key did not fire" and "the event
// was never posted" are the same observation.
const (
	kCGEventKeyDown             = 10
	kCGEventSourceStateCombined = 0
)

const (
	coreGraphicsPath       = "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"
	applicationServices    = "/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices"
	coreFoundationPath     = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
	kCGHIDEventTap         = uint32(0)
	kCGSessionEventTap     = uint32(1)
	kCGAnnotatedSessionTap = uint32(2)
)

func loadEventPosting(t *testing.T) {
	t.Helper()
	if cgEventPost != nil {
		return
	}
	cg, err := purego.Dlopen(coreGraphicsPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Fatalf("dlopen CoreGraphics: %v", err)
	}
	cf, err := purego.Dlopen(coreFoundationPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Fatalf("dlopen CoreFoundation: %v", err)
	}
	as, err := purego.Dlopen(applicationServices, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Fatalf("dlopen ApplicationServices: %v", err)
	}
	purego.RegisterLibFunc(&cgEventCreateKeyboardEvent, cg, "CGEventCreateKeyboardEvent")
	purego.RegisterLibFunc(&cgEventSetFlags, cg, "CGEventSetFlags")
	purego.RegisterLibFunc(&cgEventPost, cg, "CGEventPost")
	purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
	purego.RegisterLibFunc(&axIsProcessTrusted, as, "AXIsProcessTrusted")
	purego.RegisterLibFunc(&cgEventSourceCounter, cg, "CGEventSourceCounterForEventType")
}

// postCombo synthesises a press and release of c at the given tap.
func postCombo(tap uint32, c Combo) {
	flags := uint64(c.Mods.cocoa())
	for _, down := range []bool{true, false} {
		ev := cgEventCreateKeyboardEvent(0, uint16(c.Key), down)
		if ev == 0 {
			return
		}
		cgEventSetFlags(ev, flags)
		cgEventPost(tap, ev)
		cfRelease(ev)
	}
}

// TestLiveFiringFromASyntheticKeystroke tries to make the WINDOW SERVER match a
// physical-looking ⌥⌘F13 against the registration — the one link
// TestLiveFiresFromTheEventQueue cannot reach.
//
// It is a PROBE, not an assertion: it reports and skips, it never fails. That
// is deliberate. On this machine CGEventPost is silently refused for an
// unbundled Go binary — measured separately, with the process idle: six key
// events posted across all three taps left the system's own
// CGEventSourceCounterForEventType(kCGEventKeyDown) UNCHANGED and
// CGEventSourceSecondsSinceLastEventType still climbing (96.711 s → 96.743 s)
// instead of resetting. AXIsProcessTrusted() returns true throughout, so this is
// not simply a missing Accessibility grant.
//
// The counter is logged here for context but is NOT used to decide the outcome:
// it counts the whole login session, so on a machine somebody is using it moves
// for reasons that have nothing to do with this test. Deciding a pass or a fail
// on it would be deciding on noise.
//
// What DOES assert, and would catch a regression:
//   - TestLiveFiresFromTheEventQueue — the handler, the codes, the id, the run
//     loop and the channel, end to end, from a genuine kEventHotKeyPressed.
//   - TestLiveManualFiring — the whole chain including the window server, when
//     a person is there to press the key.
func TestLiveFiringFromASyntheticKeystroke(t *testing.T) {
	<-mainReady
	loadEventPosting(t)
	trusted := axIsProcessTrusted()

	h, err := Register(Combo{Key: KeyF13, Mods: Option | Command}, &Options{Reserved: NoReserved{}})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer h.Close()

	taps := []struct {
		name string
		tap  uint32
	}{
		{"kCGHIDEventTap", kCGHIDEventTap},
		{"kCGSessionEventTap", kCGSessionEventTap},
		{"kCGAnnotatedSessionEventTap", kCGAnnotatedSessionTap},
	}
	countBefore := cgEventSourceCounter(kCGEventSourceStateCombined, kCGEventKeyDown)
	for _, tp := range taps {
		postCombo(tp.tap, h.Combo())
		select {
		case ev := <-h.C():
			if ev.Combo != h.Combo() {
				t.Fatalf("fired with %s, want %s", ev.Combo, h.Combo())
			}
			t.Logf("FIRED from a synthetic keystroke via %s: %s at %s",
				tp.name, ev.Combo, ev.At.Format(time.RFC3339Nano))
			return
		case <-time.After(2 * time.Second):
			t.Logf("no delivery within 2s via %s", tp.name)
		}
	}
	t.Skipf("no hot key fired from a synthetic keystroke. 3 key-downs were posted; "+
		"the session-wide key-down counter moved by %d (NOT attributable to this test) "+
		"and AXIsProcessTrusted=%v. CGEventPost appears to be refused for an unbundled "+
		"binary; firing itself is proved by TestLiveFiresFromTheEventQueue and, with a "+
		"person present, TestLiveManualFiring.",
		cgEventSourceCounter(kCGEventSourceStateCombined, kCGEventKeyDown)-countBefore, trusted)
}

// ---------------------------------------------------------------------------
// Firing, proved through the Carbon event queue.
// ---------------------------------------------------------------------------

// Carbon's event-construction entry points. They let a test build a REAL
// kEventHotKeyPressed event carrying our own EventHotKeyID and put it into the
// process's main event queue — exactly the event the window server posts when
// the user presses the key.
var (
	createEvent         func(alloc uintptr, class, kind uint32, when float64, attrs uint32, out *uintptr) int32
	setEventParameter   func(ev uintptr, name, typ uint32, size uint64, data unsafe.Pointer) int32
	getMainEventQueue   func() uintptr
	postEventToQueue    func(queue, ev uintptr, priority uint16) int32
	releaseEvent        func(ev uintptr)
	getCurrentEventTime func() float64
)

const (
	typeEventHotKeyIDCode  = 0x686b6964 // 'hkid'
	kEventAttributeNone    = 0
	kEventPriorityStandard = 1
)

func loadEventInjection(t *testing.T) {
	t.Helper()
	if createEvent != nil {
		return
	}
	lib, err := purego.Dlopen(carbonPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Fatalf("dlopen Carbon: %v", err)
	}
	purego.RegisterLibFunc(&createEvent, lib, "CreateEvent")
	purego.RegisterLibFunc(&setEventParameter, lib, "SetEventParameter")
	purego.RegisterLibFunc(&getMainEventQueue, lib, "GetMainEventQueue")
	purego.RegisterLibFunc(&postEventToQueue, lib, "PostEventToQueue")
	purego.RegisterLibFunc(&releaseEvent, lib, "ReleaseEvent")
	purego.RegisterLibFunc(&getCurrentEventTime, lib, "GetCurrentEventTime")
}

// TestLiveFiresFromTheEventQueue is the firing proof.
//
// CGEventPost — the obvious way to synthesise a keystroke — is silently dropped
// for this process (see TestLiveFiring), so it cannot be used to make the
// window server match a physical key against our registration. What CAN be
// proved without a person at the keyboard is everything downstream of that
// match: a genuine kEventHotKeyPressed event, carrying the very EventHotKeyID
// that RegisterEventHotKey was given, is built and posted to the process's main
// event queue. From there the ordinary AppKit run loop picks it up, Carbon
// dispatches it to the handler this package installed on the application event
// target, the handler pulls the id back out with GetEventParameter, finds the
// Hotkey, and delivers on its channel.
//
// So: the handler, its installation on the right target, the four-character
// codes, the EventHotKeyID packing and unpacking, the run-loop delivery and the
// channel are all proved end to end. The one link NOT proved here is the window
// server matching a physical ⌥⌘F13 to our registration — for that see
// TestLiveManualFiring, which needs a human finger.
func TestLiveFiresFromTheEventQueue(t *testing.T) {
	<-mainReady
	loadEventInjection(t)

	h, err := Register(Combo{Key: KeyF13, Mods: Option | Command}, &Options{Reserved: NoReserved{}})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer h.Close()

	// The id Carbon was handed for this hot key, packed exactly as
	// RegisterEventHotKey received it: signature low, id high.
	packed := uint64(hotKeySignature) | uint64(h.id)<<32

	var ev uintptr
	if st := createEvent(0, kEventClassKeyboard, kEventHotKeyPressed,
		getCurrentEventTime(), kEventAttributeNone, &ev); st != 0 || ev == 0 {
		t.Fatalf("CreateEvent: OSStatus %d", st)
	}
	defer releaseEvent(ev)
	if st := setEventParameter(ev, kEventParamDirectObj, typeEventHotKeyIDCode,
		8, unsafe.Pointer(&packed)); st != 0 {
		t.Fatalf("SetEventParameter: OSStatus %d", st)
	}
	q := getMainEventQueue()
	if q == 0 {
		t.Fatal("GetMainEventQueue returned null")
	}
	before := handlerCalls.Load()
	if st := postEventToQueue(q, ev, kEventPriorityStandard); st != 0 {
		t.Fatalf("PostEventToQueue: OSStatus %d", st)
	}

	select {
	case got := <-h.C():
		if got.Combo != h.Combo() {
			t.Fatalf("fired with %s, want %s", got.Combo, h.Combo())
		}
		if got.At.IsZero() {
			t.Fatal("Event.At is zero")
		}
		t.Logf("FIRED: handler entered %d time(s), delivered %s at %s",
			handlerCalls.Load()-before, got.Combo, got.At.Format(time.RFC3339Nano))
	case <-time.After(5 * time.Second):
		t.Fatalf("no delivery within 5s (Carbon handler entered %d times since posting)",
			handlerCalls.Load()-before)
	}
}

// TestLiveWrongIDIsIgnored posts a hot-key event carrying an id no live Hotkey
// owns. Nothing must be delivered — otherwise a second consumer's key would
// wake the wrong handler, which is a silent, maddening bug.
func TestLiveWrongIDIsIgnored(t *testing.T) {
	<-mainReady
	loadEventInjection(t)

	h, err := Register(Combo{Key: KeyF14, Mods: Option | Command}, &Options{Reserved: NoReserved{}})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer h.Close()

	packed := uint64(hotKeySignature) | uint64(0xDEADBEEF)<<32
	var ev uintptr
	if st := createEvent(0, kEventClassKeyboard, kEventHotKeyPressed,
		getCurrentEventTime(), kEventAttributeNone, &ev); st != 0 {
		t.Fatalf("CreateEvent: OSStatus %d", st)
	}
	defer releaseEvent(ev)
	if st := setEventParameter(ev, kEventParamDirectObj, typeEventHotKeyIDCode,
		8, unsafe.Pointer(&packed)); st != 0 {
		t.Fatalf("SetEventParameter: OSStatus %d", st)
	}
	before := handlerCalls.Load()
	if st := postEventToQueue(getMainEventQueue(), ev, kEventPriorityStandard); st != 0 {
		t.Fatalf("PostEventToQueue: OSStatus %d", st)
	}
	select {
	case got := <-h.C():
		t.Fatalf("an unknown hot-key id was delivered to %s: %v", h.Combo(), got)
	case <-time.After(1500 * time.Millisecond):
	}
	if n := handlerCalls.Load() - before; n == 0 {
		t.Fatal("the handler was never entered, so this proves nothing about ignoring the id")
	}
	t.Log("an unknown hot-key id reached the handler and was correctly dropped")
}

// TestLiveFiringFromARealKeystroke closes the last link: the WINDOW SERVER
// matching a physical-looking ⌥⌘F13 against this process's registration, while
// a completely different application is frontmost.
//
// It gets the keystroke from System Events, which unlike this test binary is a
// signed, bundled application the operator has already granted the right to post
// events. That is the whole trick: CGEventPost is refused to an unbundled Go
// binary (see TestLiveFiringFromASyntheticKeystroke), but nothing stops us
// asking something that IS permitted to press the key for us. The press arrives
// through the ordinary HID path, so what is exercised is the real chain, not a
// shortcut through it.
//
// Measured 2026-08-26 on macOS 26.6.2, Apple Silicon: FIRED.
//
// It skips rather than fails when System Events is not permitted, because that
// is a property of the machine and not of this package. osascript is used here
// and ONLY here — it is a test fixture standing in for a human finger, never a
// dependency of the library, which stays pure Go with no subprocesses.
func TestLiveFiringFromARealKeystroke(t *testing.T) {
	<-mainReady
	h, err := Register(Combo{Key: KeyF13, Mods: Option | Command}, &Options{Reserved: NoReserved{}})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer h.Close()

	// kVK_F13 is 0x69 = 105, which is what AppleScript's "key code" wants.
	const script = `tell application "System Events" to key code 105 using {command down, option down}`
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		t.Skipf("System Events could not press the key (%v: %s). "+
			"It needs the Accessibility grant; this is a property of the machine, not of this package.",
			err, bytes.TrimSpace(out))
	}

	select {
	case ev := <-h.C():
		if ev.Combo != h.Combo() {
			t.Fatalf("fired with %s, want %s", ev.Combo, h.Combo())
		}
		t.Logf("FIRED from a REAL keystroke while another application was frontmost: %s at %s",
			ev.Combo, ev.At.Format(time.RFC3339Nano))
	case <-time.After(10 * time.Second):
		t.Fatalf("System Events pressed %s and the hot key did not fire within 10s", h.Combo())
	}
}

// TestLiveManualFiring waits for a REAL human press, when one is available. It
// is opt-in on top of the integration guard because it needs a person.
//
//	HOTKEY_MANUAL=1 HOTKEY_INTEGRATION=1 go test -tags integration -v -run TestLiveManual .
func TestLiveManualFiring(t *testing.T) {
	<-mainReady
	if os.Getenv("HOTKEY_MANUAL") != "1" {
		t.Skip("set HOTKEY_MANUAL=1 and be ready to press the key")
	}
	h, err := Register(Combo{Key: KeyF13, Mods: Option | Command}, &Options{Reserved: NoReserved{}})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer h.Close()
	t.Logf(">>> PRESS %s now (30s). Do it from ANOTHER application to prove it is system-wide. <<<", h.Combo())
	select {
	case ev := <-h.C():
		t.Logf("FIRED: %s at %s", ev.Combo, ev.At.Format(time.RFC3339Nano))
	case <-time.After(30 * time.Second):
		t.Fatal("no press within 30s")
	}
}

// ---------------------------------------------------------------------------
// The real preference domain.
// ---------------------------------------------------------------------------

// TestLiveSystemShortcuts reads the machine's real com.apple.symbolichotkeys
// domain. It asserts nothing about its CONTENT — that is the operator's
// business and varies per machine — only that the read works and that the merge
// produces a usable set. It prints what it found, which is the point.
func TestLiveSystemShortcuts(t *testing.T) {
	<-mainReady
	s, err := LoadSystemShortcuts()
	if err != nil {
		t.Fatalf("LoadSystemShortcuts: %v", err)
	}
	all := s.All()
	if len(all) == 0 {
		t.Fatal("the effective set is empty; the defaults list alone should never be")
	}
	var fromPrefs int
	for _, e := range all {
		if e.Origin == FromPreferences {
			fromPrefs++
		}
	}
	t.Logf("%d effective shortcuts, %d of them read from this machine's preferences", len(all), fromPrefs)

	// The invariant that catches a silently-empty read. Go back to the raw
	// domain, count the overrides that actually name a combination, and insist
	// the merged set carries exactly that many entries whose origin is
	// "preferences". A reader that returns nothing — because it nested the
	// domain one level too deep, say — still produces a perfectly usable set
	// that has quietly forgotten everything the user changed, and no other
	// assertion here would notice.
	raw, err := readSymbolicHotKeysJSON()
	if err != nil {
		t.Fatalf("readSymbolicHotKeysJSON: %v", err)
	}
	var doc map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("the domain did not decode as JSON: %v", err)
		}
	}
	entries, _ := doc["AppleSymbolicHotKeys"].(map[string]any)
	t.Logf("the raw domain holds %d entries", len(entries))
	var withCombo int
	for _, o := range ParseSymbolicHotKeys(entries) {
		if o.HasCombo {
			withCombo++
		}
	}
	if fromPrefs != withCombo {
		t.Fatalf("the domain names %d rebound combinations but the effective set credits %d to preferences",
			withCombo, fromPrefs)
	}
	if len(entries) > 0 && withCombo == 0 {
		t.Logf("note: this machine's domain has %d entries but none names a combination", len(entries))
	}
	t.Log("\n" + s.Describe())

	// The three the XR consumer wants, put to the real effective set.
	for _, c := range []Combo{
		{Key: KeyLeftArrow, Mods: Option | Command},
		{Key: KeyRightArrow, Mods: Option | Command},
		{Key: KeySpace, Mods: Option | Command},
	} {
		reason, taken := s.Reserved(c)
		t.Logf("%-6s reserved=%v %s", c, taken, reason)
	}
}
