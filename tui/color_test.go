package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
)

// TestPickerRowHarnessColor verifies that the picker row renders the project
// title in the profile color while the "<emoji> <profile>/<harness>" cell uses
// the configured harness color.
func TestPickerRowProfileColor(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		HarnessColors: map[string]string{"claude": "purple"},
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
	purple, err := palette.Lookup("purple")
	if err != nil {
		t.Fatalf("palette.Lookup(purple): %v", err)
	}
	yellowHex := yellow.ForegroundHex()
	purpleHex := purple.ForegroundHex()

	out := renderPickerList(m, 0)

	if indexColoredSegment(out, yellowHex, "api") < 0 {
		t.Errorf("expected title 'api' to be wrapped in yellow (%s); rendered:\n%s", yellowHex, out)
	}
	if indexColoredSegment(out, purpleHex, "🦊 work/claude") < 0 {
		t.Errorf("expected '🦊 work/claude' cell to be wrapped in harness purple (%s); rendered:\n%s", purpleHex, out)
	}
}

// TestPickerRowOverrideSplitsColor verifies that when a picker row's profile is
// overridden, the project title keeps the *default* profile's color while the
// harness cell uses the configured harness color rather than either profile's
// color.
func TestPickerRowOverrideSplitsColor(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		HarnessColors: map[string]string{"claude": "purple"},
		Accounts: []config.Account{
			{Name: "work", Color: "blue", Harness: "claude"},
			{Name: "personal", Color: "yellow", Harness: "claude"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "/home/user/api", Account: "personal"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m = loadSessions(m, nil)
	m = pressKey(m, "l")
	m.width, m.height = 120, 40
	// Override the only project's profile from "personal" → "work".
	if m.pickerProfileOverride == nil {
		m.pickerProfileOverride = map[int]string{}
	}
	m.pickerProfileOverride[0] = "work"
	// Move cursor off the row so styleSelected does not mask inner styles.
	m.pickerCursor = -1

	yellow, err := palette.Lookup("yellow")
	if err != nil {
		t.Fatalf("palette.Lookup(yellow): %v", err)
	}
	purple, err := palette.Lookup("purple")
	if err != nil {
		t.Fatalf("palette.Lookup(purple): %v", err)
	}
	yellowHex := yellow.ForegroundHex()
	purpleHex := purple.ForegroundHex()

	out := renderPickerList(m, 0)

	if indexColoredSegment(out, yellowHex, "api") < 0 {
		t.Errorf("expected title 'api' to keep the project default profile color (%s, yellow); rendered:\n%s", yellowHex, out)
	}
	if indexColoredSegment(out, purpleHex, "🦊 work/claude") < 0 {
		t.Errorf("expected harness cell to use harness purple (%s); rendered:\n%s", purpleHex, out)
	}
}

// TestConfigPickerHarnessEmojiAndColor verifies that the launch-profile
// overlay uses the same harness emoji/color cues as the main launch picker.
func TestConfigPickerHarnessEmojiAndColor(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{
			"claude":  "🦊",
			"codex":   "📟",
			"copilot": "🚁",
		},
		HarnessColors: map[string]string{
			"claude":  "blue",
			"codex":   "purple",
			"copilot": "green",
		},
		Accounts: []config.Account{
			{Name: "personal-codex", Color: "purple", Harness: "codex", HomeDir: "~/.codex"},
			{Name: "personal", Color: "yellow", Harness: "claude", HomeDir: "~/.claude"},
			{Name: "copilot", Color: "green", Harness: "copilot", HomeDir: "~/.copilot"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m.mode = ModeConfigPicker
	m.targetSession = session.RunningSession{Base: "dotfiles", Project: "dotfiles", Harness: "claude"}
	m.configPickerAccounts = cfg.Accounts
	m.configPickerCurrent = "personal"
	m.configPickerCursor = -1
	m.configPickerPurpose = ConfigPickerLauncherOverride
	m.configPickerReturnMode = ModePicker

	out := renderConfigPicker(m)
	plain := stripAnsi(out)
	if !strings.Contains(plain, "Launch profile: dotfiles") {
		t.Errorf("expected Launch profile header, got:\n%s", plain)
	}
	for _, segment := range []string{"📟 codex", "🦊 claude", "🚁 copilot"} {
		if !strings.Contains(plain, segment) {
			t.Errorf("expected harness segment %q in config picker, got:\n%s", segment, plain)
		}
	}

	expectedColors := map[string]string{
		"📟 codex":   "purple",
		"🦊 claude":  "blue",
		"🚁 copilot": "green",
	}
	for segment, colorName := range expectedColors {
		color, err := palette.Lookup(colorName)
		if err != nil {
			t.Fatalf("palette.Lookup(%s): %v", colorName, err)
		}
		if indexColoredSegment(out, color.ForegroundHex(), segment) < 0 {
			t.Errorf("expected %q to be wrapped in harness color %s; rendered:\n%s", segment, color.ForegroundHex(), out)
		}
	}

	expectedProfileColors := map[string]string{
		"personal-codex": "purple",
		"personal":       "yellow",
		"copilot":        "green",
	}
	for profile, colorName := range expectedProfileColors {
		color, err := palette.Lookup(colorName)
		if err != nil {
			t.Fatalf("palette.Lookup(%s): %v", colorName, err)
		}
		if indexColoredSegment(out, color.ForegroundHex(), profile) < 0 {
			t.Errorf("expected profile %q to be wrapped in profile color %s; rendered:\n%s", profile, color.ForegroundHex(), out)
		}
	}
}

// TestPickerPreviewOverrideHeaderColor verifies the picker preview header tracks
// the project's *default* profile color even when an override is active.
func TestPickerPreviewOverrideHeaderColor(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{
			Sort:            "alpha",
			RefreshInterval: 2,
			Preview:         pickerPreviewCfg(),
		},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts: []config.Account{
			{Name: "work", Color: "blue", Harness: "claude"},
			{Name: "personal", Color: "yellow", Harness: "claude"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "/home/user/api", Account: "personal"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m = loadSessions(m, nil)
	m = pressKey(m, "l")
	m.width, m.height = 120, 40
	if m.pickerProfileOverride == nil {
		m.pickerProfileOverride = map[int]string{}
	}
	m.pickerProfileOverride[0] = "work"
	m.pickerPreviewEnabled = true

	yellow, err := palette.Lookup("yellow")
	if err != nil {
		t.Fatalf("palette.Lookup(yellow): %v", err)
	}
	yellowHex := yellow.ForegroundHex()

	out := renderPickerPreview(m, 0, 40, 10)
	if indexColoredSegment(out, yellowHex, "api") < 0 {
		t.Errorf("expected preview header to keep the project default color (%s, yellow); rendered:\n%s", yellowHex, out)
	}
}

// TestFormatRowOverrideSplitsColor verifies the main list row keeps the title
// and account badge on the project's *configured default* profile color while
// the harness-name cell uses the configured harness color.
func TestFormatRowOverrideSplitsColor(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Projects: []config.Session{
			{Title: "api", Account: "personal"},
		},
	}
	acctColor := map[string]string{"work": "blue", "personal": "yellow"}
	s := session.RunningSession{
		TabID:     1,
		WindowID:  42,
		Account:   "work", // running profile (override)
		Project:   "api",  // configured default → personal
		Harness:   "claude",
		Directory: "/home/user/api",
		State:     "bolt",
	}

	yellow, err := palette.Lookup("yellow")
	if err != nil {
		t.Fatalf("palette.Lookup(yellow): %v", err)
	}
	purple, err := palette.Lookup("purple")
	if err != nil {
		t.Fatalf("palette.Lookup(purple): %v", err)
	}
	yellowHex := yellow.ForegroundHex()
	purpleHex := purple.ForegroundHex()

	row := formatRow(s, acctColor, map[string]string{"claude": "purple"}, cfg, 120, "", "")

	if indexColoredSegment(row, yellowHex, "api") < 0 {
		t.Errorf("expected title 'api' to render in the project default profile color (%s, yellow); row:\n%q", yellowHex, row)
	}
	if indexColoredSegment(row, yellowHex, "[W]") < 0 {
		t.Errorf("expected account badge to render in the project default profile color (%s, yellow); row:\n%q", yellowHex, row)
	}
	if indexColoredSegment(row, purpleHex, "🦊 claude") < 0 {
		t.Errorf("expected harness cell to render in harness purple (%s); row:\n%q", purpleHex, row)
	}
}

func TestRenderPreviewOverrideHeaderColor(t *testing.T) {
	cfg := &config.Config{
		Projects: []config.Session{
			{Title: "api", Profile: "personal"},
		},
	}
	acctColor := map[string]string{"work": "blue", "personal": "yellow"}
	s := session.RunningSession{
		WindowID:  42,
		Account:   "work",
		Profile:   "work",
		Project:   "api",
		Base:      "api",
		Directory: "/home/user/api",
	}

	yellow, err := palette.Lookup("yellow")
	if err != nil {
		t.Fatalf("palette.Lookup(yellow): %v", err)
	}
	yellowHex := yellow.ForegroundHex()

	out := renderPreview(80, 4, s, "", "", acctColor, cfg)
	if indexColoredSegment(out, yellowHex, "api") < 0 {
		t.Errorf("expected preview header to keep the project default color (%s, yellow); rendered:\n%s", yellowHex, out)
	}
}

func TestDetailHeaderOverrideBadgeColor(t *testing.T) {
	cfg := &config.Config{
		Accounts: []config.Account{
			{Name: "work", Color: "blue"},
			{Name: "personal", Color: "yellow"},
		},
		Projects: []config.Session{
			{Title: "api", Profile: "personal"},
		},
	}
	m := Model{
		cfg:            cfg,
		detailWindowID: 42,
		sessions: []session.RunningSession{
			{WindowID: 42, Account: "work", Profile: "work", Project: "api", Base: "api"},
		},
	}

	yellow, err := palette.Lookup("yellow")
	if err != nil {
		t.Fatalf("palette.Lookup(yellow): %v", err)
	}
	yellowHex := yellow.ForegroundHex()

	out := detailHeader(m)
	if indexColoredSegment(out, yellowHex, "[W]") < 0 {
		t.Errorf("expected detail badge to keep the project default color (%s, yellow); rendered:\n%s", yellowHex, out)
	}
}

// TestFormatRowProfileColor verifies the main list row applies the profile
// color to the title and the harness color to the "<emoji> <label>" cell.
func TestFormatRowProfileColor(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
	}
	acctColor := map[string]string{"work": "yellow"}
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

	row := formatRow(s, acctColor, map[string]string{"claude": "blue"}, cfg, 120, "", "")

	if indexColoredSegment(row, yellowHex, "api") < 0 {
		t.Errorf("expected title 'api' wrapped in yellow (%s); row:\n%q", yellowHex, row)
	}
	if indexColoredSegment(row, blueHex, "🦊 claude") < 0 {
		t.Errorf("expected '🦊 claude' cell wrapped in harness blue (%s); row:\n%q", blueHex, row)
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
