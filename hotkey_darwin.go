//go:build darwin

package hotkey

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/go-macos/objc"
)

// carbonPath is the umbrella framework that exports the hot-key API. The
// symbols live in HIToolbox, but the umbrella is the supported path and is the
// one that resolves.
const carbonPath = "/System/Library/Frameworks/Carbon.framework/Carbon"

// eventHotKeyExistsErr is the OSStatus RegisterEventHotKey returns when another
// Carbon hot-key holder already has the combination. This is conflict kind 1,
// and the ONLY conflict Carbon itself reports.
const eventHotKeyExistsErr = -9878

// The Carbon event class and kind we install a handler for, and the parameter
// under which the hot key's identifier arrives. These are four-character codes.
var (
	kEventClassKeyboard  = fourCC("keyb")
	kEventParamDirectObj = fourCC("----")
	kEventParamHotKeyID  = fourCC("hkid")
	hotKeySignature      = fourCC("gohk")
)

// kEventHotKeyPressed is event kind 5 within the 'keyb' class.
const kEventHotKeyPressed = 5

// fourCC packs a four-character code the way Carbon stores one.
func fourCC(s string) uint32 {
	return uint32(s[0])<<24 | uint32(s[1])<<16 | uint32(s[2])<<8 | uint32(s[3])
}

// eventTypeSpec is Carbon's EventTypeSpec: two 32-bit codes.
type eventTypeSpec struct {
	Class uint32
	Kind  uint32
}

// The Carbon entry points, bound once by initCarbon.
//
// EventHotKeyID is a two-field struct passed by value; both fields are 32 bits,
// so it is carried here as a single little-endian uint64 (signature in the low
// half, id in the high half) rather than relying on struct-by-value marshalling.
var (
	getApplicationEventTarget func() uintptr
	registerEventHotKey       func(code, mods uint32, id uint64, target uintptr, options uint32, out *uintptr) int32
	unregisterEventHotKey     func(ref uintptr) int32
	installEventHandler       func(target, handler uintptr, n uint64, list *eventTypeSpec, userData uintptr, out *uintptr) int32
	getEventParameter         func(ev uintptr, name, typ uint32, actualType *uint32, size uint64, actualSize *uint64, data unsafe.Pointer) int32
)

var (
	initOnce sync.Once
	initErr  error
	target   uintptr
)

// initCarbon loads Carbon, makes sure an NSApplication exists, and installs the
// single process-wide hot-key handler.
//
// The NSApplication matters: GetApplicationEventTarget is only meaningful once
// the application object exists, and AppKit must be loaded for
// +[NSApplication sharedApplication] to resolve at all. Skipping that step
// yields a nil NSApp and a hot key that registers but never fires.
func initCarbon() error {
	initOnce.Do(func() {
		if err := objc.Load(objc.AppKit, objc.Foundation); err != nil {
			initErr = fmt.Errorf("hotkey: loading AppKit: %w", err)
			return
		}
		if app := objc.App(); app == 0 {
			initErr = fmt.Errorf("hotkey: NSApplication could not be created")
			return
		}
		lib, err := purego.Dlopen(carbonPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			initErr = fmt.Errorf("hotkey: loading Carbon: %w", err)
			return
		}
		purego.RegisterLibFunc(&getApplicationEventTarget, lib, "GetApplicationEventTarget")
		purego.RegisterLibFunc(&registerEventHotKey, lib, "RegisterEventHotKey")
		purego.RegisterLibFunc(&unregisterEventHotKey, lib, "UnregisterEventHotKey")
		purego.RegisterLibFunc(&installEventHandler, lib, "InstallEventHandler")
		purego.RegisterLibFunc(&getEventParameter, lib, "GetEventParameter")

		if target = getApplicationEventTarget(); target == 0 {
			initErr = fmt.Errorf("hotkey: GetApplicationEventTarget returned null")
			return
		}
		spec := eventTypeSpec{Class: kEventClassKeyboard, Kind: kEventHotKeyPressed}
		var ref uintptr
		if st := installEventHandler(target, purego.NewCallback(handleHotKey), 1, &spec, 0, &ref); st != 0 {
			initErr = fmt.Errorf("hotkey: InstallEventHandler failed with OSStatus %d", st)
		}
	})
	return initErr
}

// ---------------------------------------------------------------------------
// The live registry, keyed by the hot key id we hand Carbon.
// ---------------------------------------------------------------------------

var (
	mu     sync.Mutex
	nextID uint32
	live   = map[uint32]*Hotkey{}
)

// handleHotKey is the single Carbon event handler for the whole process. It
// pulls the hot key id back out of the event and delivers to that Hotkey's
// channel.
//
// It runs on the thread pumping the run loop and must not block, so delivery is
// a non-blocking send: a consumer that is not listening drops presses rather
// than wedging the run loop and with it the whole UI.
func handleHotKey(callRef, event, userData uintptr) uintptr {
	handlerCalls.Add(1)
	var packed uint64
	var actualType uint32
	var actualSize uint64
	if st := getEventParameter(event, kEventParamDirectObj, kEventParamHotKeyID,
		&actualType, 8, &actualSize, unsafe.Pointer(&packed)); st != 0 {
		return 0
	}
	id := uint32(packed >> 32)

	// The send stays UNDER the lock, and Close closes the channel under the
	// same one. Looking the Hotkey up, dropping the lock and only then sending
	// leaves a window in which Close runs in between — and a send on a closed
	// channel panics, on the main run-loop thread, taking the process with it.
	// Holding the lock across a send is safe here precisely because the send is
	// non-blocking: it cannot wait for a consumer while holding it.
	mu.Lock()
	defer mu.Unlock()
	h := live[id]
	if h == nil {
		return 0
	}
	select {
	case h.ch <- Event{Combo: h.combo, At: time.Now()}:
	default: // nobody listening; drop rather than block the run loop
	}
	return 0
}

// handlerCalls counts every entry into [handleHotKey], before any parameter is
// read or any delivery attempted. It is what separates "the handler was never
// called" from "the handler ran but the press went nowhere" — two failures that
// look identical from a consumer's channel. The live suite asserts on it.
var handlerCalls atomic.Uint64

// ---------------------------------------------------------------------------
// The Registrar backed by Carbon.
// ---------------------------------------------------------------------------

// carbonRegistrar is the real [Registrar]. It is the ONLY part of the fallback
// machinery that touches the operating system.
type carbonRegistrar struct{}

// carbonClaim is a live Carbon registration.
type carbonClaim struct {
	ref uintptr
	id  uint32
}

// Claim implements [Registrar] by calling RegisterEventHotKey.
func (carbonRegistrar) Claim(c Combo) (Claim, error) {
	mu.Lock()
	nextID++
	id := nextID
	mu.Unlock()

	var ref uintptr
	st := registerEventHotKey(uint32(c.Key), c.Mods.carbon(),
		uint64(hotKeySignature)|uint64(id)<<32, target, 0, &ref)
	switch {
	case st == eventHotKeyExistsErr:
		return nil, fmt.Errorf("%w: %s (OSStatus %d)", ErrComboTaken, c, st)
	case st != 0:
		return nil, fmt.Errorf("hotkey: RegisterEventHotKey(%s) failed with OSStatus %d", c, st)
	case ref == 0:
		return nil, fmt.Errorf("hotkey: RegisterEventHotKey(%s) reported success but returned no reference", c)
	}
	return &carbonClaim{ref: ref, id: id}, nil
}

// Release implements [Claim] by calling UnregisterEventHotKey, which really
// does free the combination for a subsequent claim.
func (cc *carbonClaim) Release() error {
	if cc.ref == 0 {
		return ErrClosed
	}
	st := unregisterEventHotKey(cc.ref)
	cc.ref = 0
	if st != 0 {
		return fmt.Errorf("hotkey: UnregisterEventHotKey failed with OSStatus %d", st)
	}
	return nil
}

// ---------------------------------------------------------------------------
// The public entry point.
// ---------------------------------------------------------------------------

// Register claims want, or the first free rung of the fallback ladder, and
// returns a live hot key. Call [Hotkey.Combo] to find out which combination was
// actually claimed, and show it to the user — it may not be the one you asked
// for.
//
// The caller must be running an AppKit run loop on the process's main OS
// thread, with runtime.LockOSThread held there; hot key events are delivered by
// that run loop and by nothing else. github.com/go-macos/objc's RunApp does
// this. Register itself may be called from any goroutine.
//
// Pass nil options for the default behaviour: the [DefaultLadder], and the
// system-shortcut check switched on.
func Register(want Combo, opts *Options) (*Hotkey, error) {
	if err := initCarbon(); err != nil {
		return nil, err
	}
	reserved := opts.reserved()
	resolve := Resolve
	if opts.bareKey() {
		resolve = ResolveBare
	}
	got, claim, err := resolve(want, opts.ladder(), carbonRegistrar{}, reserved)
	if err != nil {
		return nil, err
	}
	cc := claim.(*carbonClaim)
	h := &Hotkey{combo: got, wanted: want, claim: claim, ch: make(chan Event, 1)}

	mu.Lock()
	live[cc.id] = h
	mu.Unlock()
	h.id = cc.id
	return h, nil
}

// reserved picks the Reserver to use, loading the machine's effective set when
// the caller did not supply one. A failure to read preferences is NOT fatal:
// the built-in defaults list still applies, which is strictly better than
// checking nothing.
func (o *Options) reserved() Reserver {
	if o != nil && o.Reserved != nil {
		return o.Reserved
	}
	s, err := LoadSystemShortcuts()
	if err != nil {
		return Merge(DefaultShortcuts(), nil)
	}
	return s
}

// Event is one press of a hot key.
type Event struct {
	// Combo is the combination that fired — the one actually claimed.
	Combo Combo
	// At is when the press was delivered.
	At time.Time
}

// Hotkey is a live system-wide hot key. Release it with Close.
type Hotkey struct {
	combo  Combo
	wanted Combo
	claim  Claim
	ch     chan Event
	id     uint32

	closeOnce sync.Once
	closeErr  error
}

// Combo returns the combination actually claimed. SHOW THIS TO THE USER: when
// the wanted combination was taken, it is not the one that was asked for.
func (h *Hotkey) Combo() Combo { return h.combo }

// Wanted returns the combination originally asked for, so a caller can tell
// whether a fallback happened and say so.
func (h *Hotkey) Wanted() Combo { return h.wanted }

// Substituted reports whether the fallback ladder had to be used.
func (h *Hotkey) Substituted() bool { return h.combo != h.wanted }

// C returns the channel on which presses arrive. It is buffered by one and
// delivery is non-blocking: if the consumer is slow, presses are dropped rather
// than stalling the run loop that delivers them, which would freeze the UI.
func (h *Hotkey) C() <-chan Event { return h.ch }

// Close releases the hot key, giving the combination back to the system. It is
// safe to call more than once.
func (h *Hotkey) Close() error {
	h.closeOnce.Do(func() {
		// Unregistering and closing the channel happen under the same lock the
		// handler sends under, so a press being delivered at this instant
		// cannot find a closed channel. Release goes after: it talks to Carbon
		// and there is no reason to hold the lock across it.
		mu.Lock()
		delete(live, h.id)
		close(h.ch)
		mu.Unlock()
		h.closeErr = h.claim.Release()
	})
	return h.closeErr
}

// ---------------------------------------------------------------------------
// Reading com.apple.symbolichotkeys.
// ---------------------------------------------------------------------------

// LoadSystemShortcuts returns the machine's effective system-shortcut set: the
// built-in defaults list ([DefaultShortcuts]) with the
// com.apple.symbolichotkeys preference domain layered over it.
//
// The domain is read through NSUserDefaults and re-serialised to JSON by
// NSPropertyList/NSJSONSerialization, so the parsing itself
// ([ParseSymbolicHotKeys]) stays portable and testable. An absent or unreadable
// domain is not an error — it simply means the user has changed nothing, and
// the defaults list stands alone.
func LoadSystemShortcuts() (*SystemShortcuts, error) {
	if err := objc.Load(objc.Foundation); err != nil {
		return nil, fmt.Errorf("hotkey: loading Foundation: %w", err)
	}
	raw, err := readSymbolicHotKeysJSON()
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("hotkey: decoding com.apple.symbolichotkeys: %w", err)
		}
	}
	entries, _ := doc["AppleSymbolicHotKeys"].(map[string]any)
	return Merge(DefaultShortcuts(), ParseSymbolicHotKeys(entries)), nil
}

// readSymbolicHotKeysJSON pulls the preference domain out of NSUserDefaults and
// renders it as JSON bytes.
func readSymbolicHotKeysJSON() (out []byte, err error) {
	objc.AutoreleasePool(func() {
		defaults := objc.ClassID("NSUserDefaults").Send(objc.Sel("standardUserDefaults"))
		if defaults == 0 {
			err = fmt.Errorf("hotkey: NSUserDefaults unavailable")
			return
		}
		// The persistent domain ALREADY carries the AppleSymbolicHotKeys key at
		// its top level — measured: count == 1, and that one key is it. Wrapping
		// it in another dictionary under the same name (the obvious thing to
		// write) nests it twice, and the parser below then sees a single
		// non-numeric key and quietly discards every override. That failure is
		// completely silent: LoadSystemShortcuts still returns a usable set,
		// just one that has forgotten everything the user changed. It is what
		// TestLiveSystemShortcuts asserts against.
		domain := defaults.Send(objc.Sel("persistentDomainForName:"),
			objc.NSString("com.apple.symbolichotkeys"))
		if domain == 0 {
			return // no such domain: the user has changed nothing
		}
		if !objc.Send[bool](objc.ClassID("NSJSONSerialization"),
			objc.Sel("isValidJSONObject:"), domain) {
			err = fmt.Errorf("hotkey: com.apple.symbolichotkeys is not JSON-representable")
			return
		}
		data := objc.ClassID("NSJSONSerialization").Send(
			objc.Sel("dataWithJSONObject:options:error:"), domain, uint64(0), uintptr(0))
		if data == 0 {
			err = fmt.Errorf("hotkey: could not serialise com.apple.symbolichotkeys")
			return
		}
		n := int(data.Send(objc.Sel("length")))
		if n == 0 {
			return
		}
		// Copy through -[NSData getBytes:length:] into a Go-owned slice rather
		// than dereferencing the -bytes pointer. Turning that ObjC-owned
		// uintptr into an unsafe.Pointer is exactly what go vet's unsafeptr
		// check rejects, and rightly: nothing keeps the NSData alive across the
		// conversion. The buffer handed to ObjC here is a live Go pointer the
		// collector tracks.
		buf := make([]byte, n)
		data.Send(objc.Sel("getBytes:length:"), unsafe.Pointer(&buf[0]), n)
		out = buf
	})
	return out, err
}
