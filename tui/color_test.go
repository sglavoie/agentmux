package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
)

// TestPickerRowColorSplit verifies that the picker row renders the project
// title in the profile color while the [profile/harness] badge takes the
// harness color. This is the visible contract of the per-profile color
// feature: the title tracks the profile, the harness affordance tracks the
// harness.
func TestPickerRowColorSplit(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessColors: map[string]string{"claude": "blue"},
		Accounts: []config.Account{
			{Name: "work", Color: "yellow"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "/home/user/api", Account: "work"},
			{Title: "backend", Directory: "/home/user/backend", Account: "work"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m = loadSessions(m, nil)
	m = pressKey(m, "l")
	m.width, m.height = 120, 40
	// Move cursor off the first row so the assertion targets a non-highlighted
	// row. styleSelected.Render swallows inner foreground colors on the cursor
	// row, but non-selected rows preserve every inner style intact.
	m.pickerCursor = 1

	yellow, err := palette.Lookup("yellow")
	if err != nil {
		t.Fatalf("palette.Lookup(yellow): %v", err)
	}
	blue, err := palette.Lookup("blue")
	if err != nil {
		t.Fatalf("palette.Lookup(blue): %v", err)
	}
	yellowHex := yellow.ForegroundHex()
	blueHex := blue.ForegroundHex()

	out := renderPickerList(m, 0)

	titleIdx := indexColoredSegment(out, yellowHex, "api")
	if titleIdx < 0 {
		t.Errorf("expected title 'api' to be wrapped in yellow (%s); rendered:\n%s", yellowHex, out)
	}
	badgeIdx := indexColoredSegment(out, blueHex, "[work/claude]")
	if badgeIdx < 0 {
		t.Errorf("expected [work/claude] badge to be wrapped in blue (%s); rendered:\n%s", blueHex, out)
	}
}

// TestFormatRowColorSplit verifies the main list row applies the profile
// color to the title cell and the harness color to the [claude] cell.
func TestFormatRowColorSplit(t *testing.T) {
	acctColor := map[string]string{"work": "yellow"}
	harnessColor := map[string]string{"work": "blue"}
	s := session.RunningSession{
		TabID: 1, Base: "api", Account: "work",
		Harness: "claude", Directory: "/home/user/api", State: "bolt",
	}

	yellow, err := palette.Lookup("yellow")
	if err != nil {
		t.Fatalf("palette.Lookup(yellow): %v", err)
	}
	blue, err := palette.Lookup("blue")
	if err != nil {
		t.Fatalf("palette.Lookup(blue): %v", err)
	}
	yellowHex := yellow.ForegroundHex()
	blueHex := blue.ForegroundHex()

	row := formatRow(s, acctColor, harnessColor, 120, "")

	if indexColoredSegment(row, yellowHex, "api") < 0 {
		t.Errorf("expected title 'api' wrapped in yellow (%s); row:\n%q", yellowHex, row)
	}
	if indexColoredSegment(row, blueHex, "[claude]") < 0 {
		t.Errorf("expected [claude] cell wrapped in blue (%s); row:\n%q", blueHex, row)
	}
}

// indexColoredSegment reports the byte offset of the first occurrence of
// segment that appears inside an ANSI foreground-color escape carrying
// hex. Returns -1 if no such occurrence exists. The check is intentionally
// loose: it looks for the hex substring on the same line as the segment,
// since lipgloss may interleave reset/style escapes between hex and text.
func indexColoredSegment(s, hex, segment string) int {
	idx := 0
	for idx < len(s) {
		i := strings.Index(s[idx:], segment)
		if i < 0 {
			return -1
		}
		abs := idx + i
		// Walk backward to find the start of the line, search for hex in
		// the preceding window. Lipgloss emits truecolor escapes like
		// "\x1b[38;2;R;G;Bm"; we instead look for the named hex via
		// rgbFromHex.
		start := strings.LastIndexByte(s[:abs], '\n') + 1
		end := strings.IndexByte(s[abs:], '\n')
		var line string
		if end < 0 {
			line = s[start:]
		} else {
			line = s[start : abs+end]
		}
		if ansiSegmentColored(line, hex, segment) {
			return abs
		}
		idx = abs + len(segment)
	}
	return -1
}

// ansiSegmentColored returns true when line contains an ANSI escape whose
// truecolor RGB triple matches hex, preceding the segment text.
func ansiSegmentColored(line, hex, segment string) bool {
	r, g, b, ok := rgbFromHex(hex)
	if !ok {
		return false
	}
	want := "\x1b[38;2;" + itoa(r) + ";" + itoa(g) + ";" + itoa(b) + "m"
	segIdx := strings.Index(line, segment)
	if segIdx < 0 {
		return false
	}
	return strings.Contains(line[:segIdx], want)
}

func rgbFromHex(hex string) (r, g, b int, ok bool) {
	if len(hex) != 7 || hex[0] != '#' {
		return 0, 0, 0, false
	}
	rv, ok1 := parseHexByte(hex[1:3])
	gv, ok2 := parseHexByte(hex[3:5])
	bv, ok3 := parseHexByte(hex[5:7])
	if !ok1 || !ok2 || !ok3 {
		return 0, 0, 0, false
	}
	return rv, gv, bv, true
}

func parseHexByte(s string) (int, bool) {
	if len(s) != 2 {
		return 0, false
	}
	out := 0
	for i := 0; i < 2; i++ {
		c := s[i]
		var v int
		switch {
		case c >= '0' && c <= '9':
			v = int(c - '0')
		case c >= 'a' && c <= 'f':
			v = int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v = int(c-'A') + 10
		default:
			return 0, false
		}
		out = out*16 + v
	}
	return out, true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [4]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
