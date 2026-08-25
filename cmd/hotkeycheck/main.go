// Command hotkeycheck claims a system-wide shortcut and reports what it got.
//
// It is the tool to reach for when a user says "my shortcut does nothing": it
// prints the effective set of macOS system shortcuts this package knows about,
// says which of them stands in the way, shows the combination that was actually
// claimed, and then waits for you to press it.
//
//	go run ./cmd/hotkeycheck                 # ⌥⌘Space, the Finder's own
//	go run ./cmd/hotkeycheck -list           # just print what is reserved
//	go run ./cmd/hotkeycheck -key left -mods opt,cmd
//
// It exits when the shortcut fires or after -wait.
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/go-macos/hotkey"
)

// keysByName is the small set of keys the flag accepts by name. Anything else
// can be given as a hexadecimal virtual key code.
var keysByName = map[string]hotkey.Key{
	"left": hotkey.KeyLeftArrow, "right": hotkey.KeyRightArrow,
	"up": hotkey.KeyUpArrow, "down": hotkey.KeyDownArrow,
	"space": hotkey.KeySpace, "return": hotkey.KeyReturn, "tab": hotkey.KeyTab,
	"escape": hotkey.KeyEscape, "f13": hotkey.KeyF13, "f14": hotkey.KeyF14,
	"f15": hotkey.KeyF15,
}

var modsByName = map[string]hotkey.Modifier{
	"cmd": hotkey.Command, "command": hotkey.Command,
	"opt": hotkey.Option, "option": hotkey.Option, "alt": hotkey.Option,
	"shift": hotkey.Shift,
	"ctrl":  hotkey.Control, "control": hotkey.Control,
}

func main() { os.Exit(run()) }

// run is separated from main so every exit path is reachable from a test.
func run() int {
	var (
		keyFlag  = flag.String("key", "space", "key: a name (left, right, space, f13, …) or a hex virtual key code (0x7B)")
		modsFlag = flag.String("mods", "opt,cmd", "comma-separated modifiers: cmd, opt, shift, ctrl")
		list     = flag.Bool("list", false, "print the effective macOS system shortcuts and exit")
		noSys    = flag.Bool("no-system-check", false, "skip the system-shortcut check and let Carbon be the only authority")
		wait     = flag.Duration("wait", 30*time.Second, "how long to wait for a press")
	)
	flag.Parse()

	sys, err := hotkey.LoadSystemShortcuts()
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not read the system shortcuts: %v\n", err)
		return 1
	}
	if *list {
		fmt.Print(sys.Describe())
		return 0
	}

	key, err := parseKey(*keyFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	mods, err := parseMods(*modsFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	want := hotkey.Combo{Key: key, Mods: mods}

	if reason, taken := sys.Reserved(want); taken {
		fmt.Printf("%s is a macOS system shortcut: %s\n", want, reason)
	}

	opts := &hotkey.Options{}
	if *noSys {
		opts.Reserved = hotkey.NoReserved{}
	}

	runtime.LockOSThread()
	h, err := hotkey.Register(want, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not claim %s or any neighbour: %v\n", want, err)
		return 1
	}
	defer h.Close()

	if h.Substituted() {
		fmt.Printf("asked for %s, GOT %s (%s)\n", h.Wanted(), h.Combo(), h.Combo().Names())
	} else {
		fmt.Printf("claimed %s (%s)\n", h.Combo(), h.Combo().Names())
	}
	fmt.Printf("press it now — from ANOTHER application, to see that it is system-wide (%s)\n", *wait)

	done := make(chan int, 1)
	go func() {
		select {
		case ev := <-h.C():
			fmt.Printf("FIRED: %s at %s\n", ev.Combo, ev.At.Format(time.RFC3339))
			done <- 0
		case <-time.After(*wait):
			fmt.Fprintln(os.Stderr, "no press within the wait")
			done <- 1
		}
	}()
	go func() { os.Exit(<-done) }()

	runApp()
	return 0
}

// parseKey accepts a name from keysByName or a hexadecimal virtual key code.
func parseKey(s string) (hotkey.Key, error) {
	if k, ok := keysByName[strings.ToLower(s)]; ok {
		return k, nil
	}
	var n uint16
	if _, err := fmt.Sscanf(strings.ToLower(s), "0x%x", &n); err == nil {
		return hotkey.Key(n), nil
	}
	return 0, fmt.Errorf("unknown key %q: use a name (left, right, space, f13, …) or a hex code like 0x7B", s)
}

// parseMods reads the comma-separated modifier list.
func parseMods(s string) (hotkey.Modifier, error) {
	var out hotkey.Modifier
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		if part == "" {
			continue
		}
		m, ok := modsByName[part]
		if !ok {
			return 0, fmt.Errorf("unknown modifier %q: use cmd, opt, shift or ctrl", part)
		}
		out |= m
	}
	if out == 0 {
		return 0, fmt.Errorf("a system-wide hot key needs at least one modifier")
	}
	return out, nil
}
