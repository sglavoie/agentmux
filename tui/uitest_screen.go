package tui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// ParseUITestSize parses a "WIDTHxHEIGHT" terminal size spec (e.g. "80x24").
// The separator is case-insensitive so "80X24" is accepted too. Both
// dimensions must be positive integers.
func ParseUITestSize(spec string) (UITestSize, error) {
	w, h, ok := strings.Cut(strings.ToLower(strings.TrimSpace(spec)), "x")
	if !ok {
		return UITestSize{}, fmt.Errorf("uitest: size %q must look like WIDTHxHEIGHT", spec)
	}
	width, err := strconv.Atoi(strings.TrimSpace(w))
	if err != nil || width <= 0 {
		return UITestSize{}, fmt.Errorf("uitest: invalid width in size %q", spec)
	}
	height, err := strconv.Atoi(strings.TrimSpace(h))
	if err != nil || height <= 0 {
		return UITestSize{}, fmt.Errorf("uitest: invalid height in size %q", spec)
	}
	return UITestSize{Width: width, Height: height}, nil
}

// NormalizeUITestScreen turns a raw rendered view into a normalized, JSON-ready
// observation. It strips ANSI escape sequences, measures each line by display
// cells (so emoji, wide glyphs, and combining marks match production rendering
// via displayWidth), flags invalid UTF-8, and records root-level width/height
// overflow for the declared terminal size.
//
// It deliberately does not inspect modal geometry: screen-wide width and height
// checks are necessary but not sufficient, and model-aware modal fit detection
// is layered on top of these observations in a later session.
func NormalizeUITestScreen(raw string, size UITestSize) UITestScreen {
	// Normalize CRLF and drop a single trailing newline so a view that ends in
	// "\n" does not report a phantom empty final line. Split before stripping
	// ANSI so invalid-UTF-8 detection sees the original bytes; ansi.Strip
	// sanitizes stray bytes, which would otherwise mask the problem.
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	normalized = strings.TrimSuffix(normalized, "\n")

	rawLines := strings.Split(normalized, "\n")
	lines := make([]string, len(rawLines))

	screen := UITestScreen{
		Size:   size,
		Lines:  lines,
		Height: len(rawLines),
	}

	for i, rawLine := range rawLines {
		if !utf8.ValidString(rawLine) {
			screen.Issues = append(screen.Issues, UITestLayoutIssue{
				Type:   UITestIssueInvalidUTF8,
				Line:   i,
				Detail: "line contains invalid UTF-8",
			})
		}
		line := ansi.Strip(rawLine)
		lines[i] = line
		w := displayWidth(line)
		if w > screen.Width {
			screen.Width = w
		}
		if size.Width > 0 && w > size.Width {
			screen.Issues = append(screen.Issues, UITestLayoutIssue{
				Type:   UITestIssueLineWidthOverflow,
				Line:   i,
				Actual: w,
				Limit:  size.Width,
			})
		}
	}

	if size.Height > 0 && len(lines) > size.Height {
		screen.Issues = append(screen.Issues, UITestLayoutIssue{
			Type:   UITestIssueScreenHeightOverflow,
			Actual: len(lines),
			Limit:  size.Height,
			Detail: "rendered line count exceeds terminal height",
		})
	}

	return screen
}
