package commands

import (
	"fmt"
	"strings"
	"testing"
	"unsafe"
)

// TestAClippedCaptureRetainsNothingOfTheScrollback is the memory contract, and it
// is asserted against the string headers because there is no other way to see it:
// a tail slice of strings.Split's output LOOKS like nine short lines and keeps a
// megabyte of history alive behind them.
func TestAClippedCaptureRetainsNothingOfTheScrollback(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&b, "line %04d of a long scrollback\n", i)
	}
	scrollback := b.String()

	lines := ClipCapture(scrollback, 40, 9)
	if len(lines) != 9 {
		t.Fatalf("the clip returned %d lines, want 9", len(lines))
	}
	for i, line := range lines {
		if aliases(line, scrollback) {
			t.Fatalf("line %d points into the scrollback's own memory; the whole capture is retained behind it", i)
		}
	}
}

// TestAClippedCaptureIsBoundedByTheTile. Width is counted in display cells and
// height in lines, and both are ceilings rather than targets.
func TestAClippedCaptureIsBoundedByTheTile(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString(strings.Repeat("x", 500) + "\n")
	}

	for _, size := range [][2]int{{1, 1}, {10, 3}, {44, 9}, {200, 40}} {
		lines := ClipCapture(b.String(), size[0], size[1])
		if len(lines) > size[1] {
			t.Fatalf("%dx%d returned %d lines, want at most %d", size[0], size[1], len(lines), size[1])
		}
		for _, line := range lines {
			if len([]rune(line)) > size[0] {
				t.Fatalf("%dx%d returned a %d-cell line, want at most %d", size[0], size[1], len([]rune(line)), size[0])
			}
		}
	}
}

// TestADegenerateTileCapturesNothing. A tile with no room for output is not a
// tile with one line of it.
func TestADegenerateTileCapturesNothing(t *testing.T) {
	for _, size := range [][2]int{{0, 5}, {5, 0}, {-1, 5}, {5, -1}, {0, 0}} {
		if got := ClipCapture("alpha\nbravo\n", size[0], size[1]); got != nil {
			t.Fatalf("%dx%d captured %q, want nothing", size[0], size[1], got)
		}
	}
}

// aliases reports whether s points into container's backing array, which is what
// a substring of it does and what a copy of it does not.
func aliases(s, container string) bool {
	if len(s) == 0 || len(container) == 0 {
		return false
	}
	start := uintptr(unsafe.Pointer(unsafe.StringData(container)))
	at := uintptr(unsafe.Pointer(unsafe.StringData(s)))
	return at >= start && at < start+uintptr(len(container))
}
