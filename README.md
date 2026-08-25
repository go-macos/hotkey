# go-macos/hotkey

[![ci](https://github.com/go-macos/hotkey/actions/workflows/ci.yml/badge.svg)](https://github.com/go-macos/hotkey/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-macos/hotkey.svg)](https://pkg.go.dev/github.com/go-macos/hotkey)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue.svg)](LICENSE)

**System-wide keyboard shortcuts on macOS from pure Go — `CGO_ENABLED=0`, and no
permission dialog at all.** When the shortcut you want is already taken, it
falls back to a neighbour and tells you, in glyphs, which one it got.

```go
h, err := hotkey.Register(hotkey.Combo{Key: hotkey.KeySpace, Mods: hotkey.Option | hotkey.Command}, nil)
if err != nil {
        return err
}
defer h.Close()

if h.Substituted() {
        // ⌥⌘Space is the Finder's search window, so this really happens.
        fmt.Printf("%s was taken; using %s instead\n", h.Wanted(), h.Combo()) // ⌥⌘Space → ⌥⇧⌘Space
}

for ev := range h.C() {
        turnTheRibbon(ev.Combo)
}
```

The shortcut fires **while the user is working in another application**. That is
the whole point: the consumer is an XR virtual-desktop app whose ribbon of
screens is turned from the keyboard while the user types inside the applications
on those screens. A shortcut that only works when your own window is focused
would be useless to it.

## No permission is required

The two obvious routes both demand the **Accessibility** (TCC) grant, which
means a system dialog and a trip to System Settings:

| route | permission |
|---|---|
| `CGEventTap` | Accessibility |
| `-[NSEvent addGlobalMonitorForEventsMatchingMask:]` | Accessibility |
| **Carbon `RegisterEventHotKey`** | **none** |

This package uses the third. It is old, it is still present on macOS 26, and it
is what every shortcut manager on the platform is built on. Registering ⌥⌘←,
⌥⌘→ and ⌥⌘Space on macOS 26.6.2 produced no dialog and needed no grant.

## Three kinds of conflict — and only two of them are detectable

This is the single most important thing about `RegisterEventHotKey`, and the
reason a naive implementation is worse than useless: **it does not
conflict-check against system shortcuts.** ⌥⌘Space is the Finder's search
window and ⌥⌘←/→ are Safari's tab navigation, and all three register with
**status 0** regardless. A fallback driven by the return status alone would
never fire for the conflicts a user actually cares about.

**1. Another Carbon hot-key holder — DETECTED.**
Registration returns `eventHotKeyExistsErr` (−9878), surfaced as
`ErrComboTaken`. This is also how the package's own tests prove a claim is real
without anyone pressing a key (see *Verification*).

**2. A macOS system shortcut — DETECTED, by this package, before registering.**
Carbon will not tell you, so `Resolve` asks a `Reserver` first. The default one
is a **hand-maintained list of macOS defaults with the user's
`com.apple.symbolichotkeys` overrides layered over it**. See the next section
for exactly how dependable that is; `SystemShortcut.Origin` tells you, per
entry, whether a binding is a fact about this machine or a well-informed guess.

**3. An ordinary application's own menu key equivalent — NOT DETECTABLE.**
Safari's ⌥⌘← for "previous tab" is live only while Safari is frontmost, and
nothing on macOS enumerates other applications' menu shortcuts. If you claim
one, you win it globally and that application silently stops seeing it. There is
no coverage here and this package does not pretend otherwise. **Make your
shortcuts configurable.**

## What `com.apple.symbolichotkeys` really is

It was dumped rather than trusted, and the shape is not what most write-ups
imply.

**It is an override layer, not a catalogue.** On macOS 26.6.2 the domain held
**19 entries in 486 bytes**, while macOS defines on the order of a hundred
symbolic hot keys. ⌥⌘Space — live on that machine as the Finder's search window
— was **absent from it entirely**, because the user had never changed it.
Enumerating the domain and stopping there would miss most of the conflicts that
matter, and would miss them *silently*. `defaults -currentHost read
com.apple.symbolichotkeys` reports the domain does not exist at all; there is no
`/Library/Preferences` copy either, and no readable file of the defaults
anywhere on the system.

**Entries frequently carry no binding.** Four of the nineteen (79, 80, 81, 82 —
the space-switching shortcuts) were `{"enabled": true}` and nothing else. The
domain says the shortcut is *on* but not what it is *bound to*. Only a defaults
list can supply that.

**The entries that do carry a binding have this shape**, confirmed against the
real domain:

```json
"61": {"enabled": true, "value": {"type": "standard", "parameters": [32, 49, 786432]}}
```

`parameters` is `[ASCII character, virtual key code, modifier mask]`. The mask
is **NSEventModifierFlags** (Shift `1<<17`, Control `1<<18`, Option `1<<19`,
Command `1<<20`) — *not* the Carbon mask `RegisterEventHotKey` takes, so the two
are converted through a single table. `65535` is the "no such parameter"
sentinel and an all-`65535` triple means unbound; both were present in the real
data and both are handled.

**So the answer to "is reading it dependable?" is: dependable for what it
contains, and it does not contain what you most need.** This package therefore
ships `DefaultShortcuts()` — an **honest, clearly-marked static list** of the
macOS shortcuts that are on by default — and layers the domain over it with
`Merge`. `Origin` reports which of the two a given binding came from. Parsing is
deliberately tolerant: a malformed entry is skipped rather than failing the
whole read, because a hot key that could not be claimed on account of one odd
preference entry would be a bad trade.

## The fallback ladder

`DefaultLadder` is: the same key **with Shift added**, then **with Control
added**, then **with both**.

```
⌥⌘←   →   ⌥⇧⌘←   →   ⌃⌥⌘←   →   ⌃⌥⇧⌘←
```

Shift first because it collides with least and reads most naturally on a menu;
Control after it because ⌃ is heavily used by the terminal and by text-editing
key bindings; both together last because it is the most awkward to press. A rung
that adds nothing new — because you already asked for that modifier — is
skipped, not retried. Pass `Options.Ladder` to change the order, or an
**explicitly empty** ladder to disable the fallback entirely and get
`ErrNoCandidate` instead of a substitution.

**It reports what it got, in a form you can show a person.** `h.Combo().String()`
is `⌥⇧⌘Space`, not a bitmask; `h.Combo().Names()` is `Option-Shift-Command-Space`
for logs and accessibility labels. Modifiers render in Apple's canonical order,
**⌃⌥⇧⌘** — so Shift+Option+Command prints `⌥⇧⌘`, not `⇧⌥⌘`. A shortcut the user
cannot be told about is worse than none.

**Nothing is ever substituted silently and nothing unusable is ever returned.**
If every rung is taken, `Resolve` returns `ErrNoCandidate` naming every
combination it tried and why, and registers nothing.

**The policy is separate from the operating system.** `Resolve` speaks only to a
`Registrar` interface, so the entire ladder — including "every candidate is
taken" — is tested on Linux with no Carbon in sight.

## Verification

Registration, exclusivity, release and delivery are all proved by the live suite
(`-tags integration`, plus `HOTKEY_INTEGRATION=1`), which claims only
F13/F14/F15 combinations and releases every one of them.

**A claim is provable with no keypress.** Registering the *same* combination
twice returns −9878. The control is what makes it a proof rather than a
coincidence: a combination that was never claimed registers with status 0.

```
second claim of ⌥⌘F13 refused with ErrComboTaken — the claim is exclusive
control ⌃⌥⌘F14 claimed with status 0 — the refusal above means TAKEN, not ALWAYS
⌥⌘F15: claimed, refused while held, freed by Release, re-claimed
⌥⌘F13 was held; got ⌥⇧⌘F13, shown as "⌥⇧⌘F13", and that combination is genuinely claimed
every rung held → hotkey: every candidate combination is taken (⌥⌘F14: …; ⌥⇧⌘F14: …; ⌃⌥⌘F14: …; ⌃⌥⇧⌘F14: …)
```

**Firing is proved — including the window server matching a real keystroke.**

The consumer's whole reason for existing is that the shortcut fires *while
someone is working in another application*, so that is what was measured:

```
FIRED from a REAL keystroke while another application was frontmost: ⌥⌘F13 at 2026-08-26T01:32:33.42+02:00
```

Getting there took two steps, and the first one is a finding in its own right.

**`CGEventPost` — the obvious way to synthesise a press — is silently refused
for an unbundled Go binary.** Two independent witnesses agree: after posting six
key events across all three taps, the system's own
`CGEventSourceCounterForEventType(…, kCGEventKeyDown)` was **unchanged**, and
`CGEventSourceSecondsSinceLastEventType` kept climbing (96.711 s → 96.743 s)
instead of resetting to zero. `AXIsProcessTrusted()` returned **true**
throughout, so this is *not* simply a missing Accessibility grant. A global
`NSEvent` monitor in the same process saw nothing either. The probe test reports
this and skips; it never fails, because it is measuring the platform, not the
package.

**So the keystroke was borrowed from something that IS permitted to press keys.**
`TestLiveFiringFromARealKeystroke` asks **System Events** — signed, bundled, and
already trusted on the operator's machine — to press ⌥⌘F13. The press travels
the ordinary HID path, the window server matches it against this process's
registration, and it is delivered *while the terminal, not this process, is
frontmost*. That is the complete chain, with nothing stubbed. `osascript` appears
in that one test and nowhere else: it is a fixture standing in for a human
finger, never a dependency of the library, which stays pure Go with no
subprocesses.

**And the chain below the window server is asserted separately**, so a
regression is caught even on a machine where System Events is not permitted.
`TestLiveFiresFromTheEventQueue` builds a **genuine `kEventHotKeyPressed` event
carrying the very `EventHotKeyID` that `RegisterEventHotKey` was handed**, posts
it with `PostEventToQueue`, and the ordinary `[NSApp run]` loop delivers it:

```
FIRED: handler entered 1 time(s), delivered ⌥⌘F13 at 2026-08-26T01:28:23.02+02:00
an unknown hot-key id reached the handler and was correctly dropped
```

That covers the handler, its installation on the application event target, the
four-character codes, the `EventHotKeyID` packing and unpacking, the run-loop
delivery, the id→`Hotkey` lookup and the channel. There is also
`TestLiveManualFiring`, which waits for an actual human:

```
HOTKEY_MANUAL=1 HOTKEY_INTEGRATION=1 go test -tags integration -v -run TestLiveManual .
```

**A trap found along the way that will bite anyone who repeats this.**

`-[NSUserDefaults persistentDomainForName:]` returns a dictionary that
**already** has `AppleSymbolicHotKeys` as its single top-level key. Wrapping it
in another dictionary under that name — the obvious thing to write — nests it
twice, and the parser then sees one non-numeric key and discards every override
**in silence**: you still get a perfectly usable shortcut set that has quietly
forgotten everything the user changed. This package had exactly that bug, and it
was invisible until the live suite was made to go back to the raw domain, count
the overrides that name a combination, and insist the merged set credits that
many entries to preferences. On the machine above that is 2 of 19.

**And a conclusion that looked obvious and was wrong**, recorded because the
next person will reach it too. When the first firing attempt failed, the natural
explanation was that the hand-rolled
`-[NSApplication nextEventMatchingMask:untilDate:inMode:dequeue:]` pump in use at
the time drains the AppKit queue without dispatching Carbon events. It does not:
put to a *real* keystroke, that pump delivers hot-key presses perfectly well, and
so does `[NSApp run]`. The failure was entirely the refused `CGEventPost` — one
cause, wearing the costume of another. What actually matters is only that
**some** AppKit run loop is being pumped on the process's main OS thread.

## Running the tests

```bash
# The portable suite: no Carbon, no display, no permission. Runs anywhere.
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 GOOS=linux go test ./...

# The live suite. It really claims system-wide keys (F13/F14/F15 only) and
# releases every one of them.
HOTKEY_INTEGRATION=1 go test -tags integration -v -run TestLive .
```

The portable layer — the ladder, the formatting, the preference parsing, the
error mapping — is at **100% statement coverage**, gated in CI on both the
darwin and the linux lane, and *run* (not merely compiled) on all six of Go's
64-bit architectures.

## Using it

An `NSApplication` must exist and its run loop must be running on the process's
main OS thread, with `runtime.LockOSThread` held there. Hot-key events are
delivered by that run loop and by nothing else.

```go
func main() {
        runtime.LockOSThread()

        h, err := hotkey.Register(hotkey.Combo{Key: hotkey.KeyLeftArrow,
                Mods: hotkey.Option | hotkey.Command}, nil)
        if err != nil {
                log.Fatal(err)
        }
        defer h.Close()
        log.Printf("listening on %s", h.Combo())

        go func() {
                for ev := range h.C() {
                        turnTheRibbon(ev.Combo)
                }
        }()

        objc.RunApp(1) // NSApplicationActivationPolicyAccessory
}
```

`Register` itself may be called from any goroutine. Delivery is buffered by one
and **non-blocking**: a consumer that is not listening drops presses rather than
wedging the run loop, which would freeze the whole UI.

## Platforms

macOS only. Every other platform **compiles** and reports `ErrUnsupported`, so
consumers cross-compile without a build tag of their own — verified on
linux/{amd64,arm64,riscv64,loong64,ppc64le,s390x}, windows/{amd64,arm64},
darwin/amd64 and freebsd/amd64. The whole policy layer works on all of them,
which is what makes it testable off a Mac.

## Licence

BSD-3-Clause.
