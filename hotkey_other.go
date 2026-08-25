//go:build !darwin

package hotkey

import "time"

// This file is the non-darwin half of the package. Every exported symbol the
// darwin build provides is defined here too, so a consumer cross-compiles
// without build tags of its own and finds out at run time — with a clear
// error — that there are no system-wide hot keys on this platform.
//
// Note what is NOT stubbed out: the entire policy layer. [Resolve],
// [Candidates], [Combo.String], [ParseSymbolicHotKeys] and [Merge] are in the
// portable files and work here exactly as they do on macOS. That is deliberate:
// it is what lets the fallback ladder be tested, to the last branch, on a Linux
// CI runner with no Carbon anywhere in sight.

// Event is one press of a hot key. No press is ever delivered on this platform.
type Event struct {
	// Combo is the combination that fired.
	Combo Combo
	// At is when the press was delivered.
	At time.Time
}

// Hotkey is a live system-wide hot key. On non-darwin platforms one can never
// be created, so no value of this type is ever handed out by [Register]; the
// type exists so that consumer code naming it still compiles.
type Hotkey struct {
	combo  Combo
	wanted Combo
	ch     chan Event
}

// Register reports [ErrUnsupported]: system-wide hot keys here are Carbon's,
// and Carbon is macOS-only.
func Register(want Combo, opts *Options) (*Hotkey, error) { return nil, ErrUnsupported }

// Combo returns the combination actually claimed.
func (h *Hotkey) Combo() Combo { return h.combo }

// Wanted returns the combination originally asked for.
func (h *Hotkey) Wanted() Combo { return h.wanted }

// Substituted reports whether the fallback ladder had to be used.
func (h *Hotkey) Substituted() bool { return h.combo != h.wanted }

// C returns the channel on which presses arrive. Nothing is ever sent on it
// here.
func (h *Hotkey) C() <-chan Event { return h.ch }

// Close releases the hot key. There is nothing to release here.
func (h *Hotkey) Close() error { return ErrUnsupported }

// LoadSystemShortcuts returns the built-in defaults list alone. There is no
// com.apple.symbolichotkeys domain to layer over it on this platform, so the
// result is what macOS reserves BY DEFAULT — useful for showing a user what
// their Mac would refuse, and for testing the merge, but it describes macOS
// rather than the machine this is running on.
func LoadSystemShortcuts() (*SystemShortcuts, error) {
	return Merge(DefaultShortcuts(), nil), nil
}

// reserved picks the Reserver to use. It mirrors the darwin implementation so
// that Options behaves identically wherever it is constructed.
func (o *Options) reserved() Reserver {
	if o != nil && o.Reserved != nil {
		return o.Reserved
	}
	// LoadSystemShortcuts cannot fail here — there is no preference domain to
	// fail to read — so there is no fallback branch to write, and none left
	// untestable. On darwin there is one, and it falls back to the defaults
	// list rather than checking nothing.
	s, _ := LoadSystemShortcuts()
	return s
}
