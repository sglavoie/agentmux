package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/launch"
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
	m.configPickerPurpose = launch.ConfigPickerLauncherOverride
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

// TestSelectedSessionContextUsesTheConfiguredDefaultProfileColor is where the
// row's identity color went. The queue row draws its name unstyled — a neutral
// name is what lets the amber status beside it mean something — so the project's
// color is now carried by the detail strip, which describes the selection.
//
// The claim that survives the move is the one that was load-bearing: a session
// LAUNCHED under an override profile still wears its project's CONFIGURED
// default color, so the operator's color association with a project is not
// rewritten by how a particular window happened to start.
func TestSelectedSessionContextUsesTheConfiguredDefaultProfileColor(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts: []config.Account{
			{Name: "work", Color: "blue"},
			{Name: "personal", Color: "yellow"},
		},
		Projects: []config.Session{
			{Title: "api", Account: "personal"},
		},
	}
	s := session.RunningSession{
		TabID:     1,
		WindowID:  42,
		Account:   "work", // running profile (override)
		Project:   "api",  // configured default → personal
		Base:      "api",
		Harness:   "claude",
		Directory: "/home/user/api",
		State:     "bolt",
	}

	yellow, err := palette.Lookup("yellow")
	if err != nil {
		t.Fatalf("palette.Lookup(yellow): %v", err)
	}
	yellowHex := yellow.ForegroundHex()

	items := dashboard.BuildItemsFor(dashboard.ViewUrgency, []session.RunningSession{s}, nil)
	item, _ := items.At(0)
	lines := dashboard.RenderDetailStrip(dashboard.DetailStripInput{
		Width: 100, Items: items, Item: item, Selected: true, Cfg: cfg,
	})

	if indexColoredSegment(lines[0], yellowHex, "api") < 0 {
		t.Errorf("expected the selected session's title to keep the project default profile color (%s, yellow); strip:\n%q", yellowHex, lines[0])
	}
	if indexColoredSegment(lines[0], yellowHex, "claude") < 0 {
		t.Errorf("expected the harness to sit inside the same identity span (%s, yellow); strip:\n%q", yellowHex, lines[0])
	}

	// The row itself says nothing about the profile, which is the point: the only
	// color on a queue line is the status's own emphasis.
	queue := dashboard.BuildQueue(dashboard.QueueInput{Rows: items.Rows})
	row := dashboard.FormatRow(dashboard.RowInput{Queue: queue[0], Width: 100,
		Columns: dashboard.ScanQueueColumns(queue)})
	if indexColoredSegment(row, yellowHex, "api") >= 0 {
		t.Errorf("the queue row colored its name by profile; row:\n%q", row)
	}
}

func TestRenderInspectorOverrideTitleColor(t *testing.T) {
	cfg := &config.Config{
		Projects: []config.Session{
			{Title: "api", Profile: "personal"},
		},
	}
	cfg.Accounts = []config.Account{{Name: "work", Color: "blue"}, {Name: "personal", Color: "yellow"}}
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

	out := dashboard.RenderInspector(dashboard.InspectorInput{
		Width: 80, Height: 8, Content: dashboard.InspectorReady, Session: s, Cfg: cfg,
	})
	if indexColoredSegment(out, yellowHex, "api") < 0 {
		t.Errorf("expected inspector title to keep the project default color (%s, yellow); rendered:\n%s", yellowHex, out)
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

// TestHarnessColorNoLongerReachesTheSessionRow is the deliberate loss recorded
// as a test rather than left to be rediscovered. The row used to color the
// `<emoji> <label>` harness cell with the configured harness color; the cell is
// gone with the rest of the row's metadata, and the detail strip draws the
// harness inside one identity span rather than giving it a second color that
// would break the span in two.
//
// The configured harness colors are NOT dead: dashboard.HarnessColorMap still
// drives the launcher's rows and its profile overlay, which is where an operator
// picks a harness and therefore where the color still distinguishes something.
func TestHarnessColorNoLongerReachesTheSessionRow(t *testing.T) {
	cfg := &config.Config{HarnessEmojis: map[string]string{"claude": "🦊"}}
	s := session.RunningSession{
		TabID: 1, Base: "api", Project: "api", Account: "work",
		Harness: "claude", Directory: "/home/user/api", State: "bolt",
	}

	blue, err := palette.Lookup("blue")
	if err != nil {
		t.Fatalf("palette.Lookup(blue): %v", err)
	}
	blueHex := blue.ForegroundHex()

	items := dashboard.BuildItemsFor(dashboard.ViewUrgency, []session.RunningSession{s}, nil)
	queue := dashboard.BuildQueue(dashboard.QueueInput{Rows: items.Rows})
	row := dashboard.FormatRow(dashboard.RowInput{Queue: queue[0], Width: 120,
		Columns: dashboard.ScanQueueColumns(queue)})
	if strings.Contains(stripAnsi(row), "claude") {
		t.Errorf("the queue row still carries a harness cell; row:\n%q", stripAnsi(row))
	}
	if indexColoredSegment(row, blueHex, "api") >= 0 {
		t.Errorf("the queue row carries a harness color; row:\n%q", row)
	}

	if got := dashboard.HarnessColorMap(cfg); got == nil {
		t.Error("HarnessColorMap returned nil: the launcher still reads it")
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
