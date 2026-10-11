package palette

import (
	"fmt"
	"math"
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

// TestBrightBackgroundsCarryBlackText: a companion draws a session's line on
// BrightBg with black text, so every color must keep that readable (WCAG AAA,
// 7:1).
func TestBrightBackgroundsCarryBlackText(t *testing.T) {
	linear := func(c float64) float64 {
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	for _, name := range Names() {
		c, _ := Lookup(name)
		var r, g, b int
		if n, err := fmt.Sscanf(c.BrightBg, "#%02x%02x%02x", &r, &g, &b); err != nil || n != 3 || len(c.BrightBg) != 7 {
			t.Errorf("%s: bright_bg %q is not #rrggbb", name, c.BrightBg)
			continue
		}
		luminance := 0.2126*linear(float64(r)/255) + 0.7152*linear(float64(g)/255) + 0.0722*linear(float64(b)/255)
		if contrast := (luminance + 0.05) / 0.05; contrast < 7 {
			t.Errorf("%s: black on %s has contrast %.1f; want at least 7", name, c.BrightBg, contrast)
		}
	}
}
