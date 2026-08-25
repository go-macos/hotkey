//go:build !darwin

package hotkey

import (
	"errors"
	"testing"
)

// The non-darwin half. What matters here is that a consumer cross-compiles and
// gets a CLEAR error rather than a link failure or a silent no-op — and that
// the policy layer keeps working, which is what lets the ladder be tested on a
// Linux runner.

func TestRegisterIsUnsupported(t *testing.T) {
	h, err := Register(Combo{Key: KeyLeftArrow, Mods: Option | Command}, nil)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("got %v, want ErrUnsupported", err)
	}
	if h != nil {
		t.Fatal("a Hotkey was handed out on a platform that has none")
	}
}

// TestHotkeyAccessorsCompileAndAnswer: consumer code names these, so they must
// exist and must not panic on the zero value.
func TestHotkeyAccessorsCompileAndAnswer(t *testing.T) {
	want := Combo{Key: KeySpace, Mods: Option | Command}
	h := &Hotkey{combo: want, wanted: want, ch: make(chan Event)}
	if h.Combo() != want {
		t.Errorf("Combo() = %s, want %s", h.Combo(), want)
	}
	if h.Wanted() != want {
		t.Errorf("Wanted() = %s, want %s", h.Wanted(), want)
	}
	if h.Substituted() {
		t.Error("Substituted() is true although nothing was substituted")
	}
	h.combo = Combo{Key: KeySpace, Mods: Shift | Option | Command}
	if !h.Substituted() {
		t.Error("Substituted() is false although the combination differs")
	}
	if h.C() == nil {
		t.Error("C() returned a nil channel")
	}
	if err := h.Close(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Close() = %v, want ErrUnsupported", err)
	}
}

// TestLoadSystemShortcutsIsTheDefaultsList: there is no preference domain here,
// so what comes back describes macOS rather than this machine. It must still be
// usable, because that is what makes the merge testable off a Mac.
func TestLoadSystemShortcutsIsTheDefaultsList(t *testing.T) {
	s, err := LoadSystemShortcuts()
	if err != nil {
		t.Fatalf("LoadSystemShortcuts: %v", err)
	}
	if len(s.All()) != len(DefaultShortcuts()) {
		t.Fatalf("got %d shortcuts, want the %d in the defaults list",
			len(s.All()), len(DefaultShortcuts()))
	}
	for _, e := range s.All() {
		if e.Origin != FromDefaults {
			t.Errorf("%d claims origin %s on a platform with no preferences", e.ID, e.Origin)
		}
	}
	if _, taken := s.Reserved(Combo{Key: KeySpace, Mods: Option | Command}); !taken {
		t.Error("⌥⌘Space should be reserved even here")
	}
}

// TestOptionsReserved: Options must behave the same wherever it is built, so a
// caller can construct one on Linux and use it on a Mac.
func TestOptionsReserved(t *testing.T) {
	var nilOpts *Options
	if _, taken := nilOpts.reserved().Reserved(Combo{Key: KeySpace, Mods: Option | Command}); !taken {
		t.Error("nil Options should still check the system shortcuts")
	}
	if _, taken := (&Options{}).reserved().Reserved(Combo{Key: KeySpace, Mods: Option | Command}); !taken {
		t.Error("zero Options should still check the system shortcuts")
	}
	if _, taken := (&Options{Reserved: NoReserved{}}).reserved().
		Reserved(Combo{Key: KeySpace, Mods: Option | Command}); taken {
		t.Error("an explicit NoReserved was ignored")
	}
}
