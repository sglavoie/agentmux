package palette

import (
	"slices"
	"strings"
	"testing"
)

func TestLookupAllColors(t *testing.T) {
	names := []string{"green", "blue", "red", "orange", "purple", "teal", "pink", "yellow", "gray", "cyan"}
	for _, name := range names {
		c, err := Lookup(name)
		if err != nil {
			t.Errorf("Lookup(%q) error: %v", name, err)
			continue
		}
		if c.ActiveBg == "" {
			t.Errorf("Lookup(%q): empty ActiveBg", name)
		}
		if c.InactiveBg == "" {
			t.Errorf("Lookup(%q): empty InactiveBg", name)
		}
		if c.Emoji == "" {
			t.Errorf("Lookup(%q): empty Emoji", name)
		}
	}
}

func TestLookupUnknown(t *testing.T) {
	_, err := Lookup("magenta")
	if err == nil {
		t.Error("Lookup(\"magenta\") expected error, got nil")
	}
}

func TestForegroundHex(t *testing.T) {
	c, err := Lookup("green")
	if err != nil {
		t.Fatal(err)
	}
	// green text_fg is [72, 200, 112] → #48c870
	got := c.ForegroundHex()
	want := "#48c870"
	if got != want {
		t.Errorf("ForegroundHex() = %q, want %q", got, want)
	}
}

func TestForegroundHexClampsOutOfRangeChannels(t *testing.T) {
	c := Color{TextFg: [3]int{-1, 300, 127}}
	if got := c.ForegroundHex(); got != "#00ff7f" {
		t.Errorf("ForegroundHex() = %q, want %q", got, "#00ff7f")
	}
}

func TestActiveFgInactiveFg(t *testing.T) {
	if ActiveFg == "" {
		t.Error("ActiveFg is empty")
	}
	if InactiveFg == "" {
		t.Error("InactiveFg is empty")
	}
	if !strings.HasPrefix(ActiveFg, "#") {
		t.Errorf("ActiveFg %q does not start with #", ActiveFg)
	}
	if !strings.HasPrefix(InactiveFg, "#") {
		t.Errorf("InactiveFg %q does not start with #", InactiveFg)
	}
}

func TestNamesCount(t *testing.T) {
	if got := len(Names()); got != 10 {
		t.Errorf("Names() returned %d colors, want 10", got)
	}
}

func TestNamesSorted(t *testing.T) {
	names := Names()
	if !slices.IsSorted(names) {
		t.Fatalf("Names() = %v, want sorted order", names)
	}
}

// TestLookupEmptyStringErrors guards against silently returning a zero-value
// Color for an empty config value: callers that ignore the error would then
// apply a black-on-black tab color, which is impossible to read.
func TestLookupEmptyStringErrors(t *testing.T) {
	c, err := Lookup("")
	if err == nil {
		t.Fatalf("Lookup(\"\") should error, got %+v", c)
	}
}

// TestNamesReturnsFreshSlice ensures callers can sort/mutate the slice
// returned by Names() without affecting subsequent calls.
func TestNamesReturnsFreshSlice(t *testing.T) {
	first := Names()
	if len(first) == 0 {
		t.Fatal("Names() returned empty slice")
	}
	for i := range first {
		first[i] = "MUTATED"
	}
	second := Names()
	for _, name := range second {
		if name == "MUTATED" {
			t.Fatalf("Names() returned mutated value %q; slice must not alias prior return", name)
		}
		if _, err := Lookup(name); err != nil {
			t.Errorf("Lookup(%q) from Names() failed: %v", name, err)
		}
	}
}

// TestLookupCaseSensitive locks in the current (case-sensitive) contract so a
// regression toward case-insensitive lookups cannot land silently — there are
// downstream config validators (internal/config) that enforce the lowercase
// names and would need to be updated in lockstep.
func TestLookupCaseSensitive(t *testing.T) {
	if _, err := Lookup("Blue"); err == nil {
		t.Error("Lookup(\"Blue\") expected error (lookup is case-sensitive), got nil")
	}
}
