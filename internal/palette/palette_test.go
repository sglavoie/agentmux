package palette

import (
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
