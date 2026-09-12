package dashboard

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

func TestCompactSeparatesAgentCompletionFromRunPhase(t *testing.T) {
	for phase, label := range map[string]string{
		"paused": "paused", "turn": "running", "verification": "verifying",
		"between": "between turns", "waiting": "waiting",
	} {
		t.Run(phase, func(t *testing.T) {
			s := inspectorSession()
			s.Looping = true
			s.ASLPhases = []string{phase}
			s.AgentTurn = &protocol.AgentTurnSnapshot{IconState: "stop", FinishedAt: inspectorNow.Add(-42 * time.Second).Unix()}
			lines := renderCompactLines(t, CompactInput{Width: 80, Content: InspectorReady, Session: s})
			if got, want := lines[compactTitleRow], "0:42 | ASL "+label+" · Done"; !strings.HasSuffix(got, want) {
				t.Fatalf("header = %q; want suffix %q", got, want)
			}
			if s.State != "bolt" || s.AgentTurn.IconState != "stop" {
				t.Fatal("rendering mutated the session")
			}
			s.AgentTurn = &protocol.AgentTurnSnapshot{IconState: "bolt"}
			lines = renderCompactLines(t, CompactInput{Content: InspectorReady, Session: s})
			if strings.Contains(lines[compactTitleRow], "Done") || !strings.Contains(lines[compactTitleRow], "Working") {
				t.Fatalf("resumed agent still looks done: %s", lines[compactTitleRow])
			}
		})
	}
}

func TestCompactDoesNotCallASLActivityAgentWork(t *testing.T) {
	s := inspectorSession()
	s.Looping = true
	s.ASLPhases = []string{"turn"}
	s.AgentTurn = &protocol.AgentTurnSnapshot{}
	lines := renderCompactLines(t, CompactInput{Content: InspectorReady, Session: s})
	if got := lines[compactTitleRow]; !strings.HasSuffix(got, "— | ASL running · Idle") {
		t.Fatalf("status = %q", got)
	}
	// Older daemons cannot separate the facts. Preserve their aggregate status
	// and say only that ASL is attached, without inventing a phase or completion.
	s.AgentTurn, s.ASLPhases = nil, nil
	lines = renderCompactLines(t, CompactInput{Content: InspectorReady, Session: s})
	if got := lines[compactTitleRow]; !strings.HasSuffix(got, "0:42 | ASL · Working") {
		t.Fatalf("legacy status = %q", got)
	}
	for _, width := range []int{1, 5, 20, 46} {
		renderCompactLines(t, CompactInput{Width: width, Content: InspectorReady, Session: s})
	}
}

func TestCompactSummaryRightAlignsStyledStatus(t *testing.T) {
	for _, state := range []string{"bolt", "stop"} {
		for _, phases := range [][]string{nil, {"paused"}} {
			for _, width := range []int{20, 46, 80} {
				in := compactInput(CompactInput{Width: width, Height: 2, SummaryOnly: true,
					Content: InspectorReady, Session: inspectorSession()})
				in.Session.State, in.Session.ASLPhases = state, phases
				_, stateLabel := compactStatusParts(in)
				wantSuffix := ansi.Strip(stateLabel)
				line := strings.Split(RenderCompactPreview(in), "\n")[0]
				if layout.StyledWidth(line) != width || !strings.HasSuffix(ansi.Strip(line), wantSuffix) {
					t.Fatalf("state=%s phases=%v width=%d: status must end at right edge: %q", state, phases, width, line)
				}
			}
		}
	}
}

func TestCompactCardRightAlignsStateAndKeepsTheTitle(t *testing.T) {
	s := inspectorSession()
	s.Project = "a very long project name that cannot fit beside its state"
	for _, width := range []int{20, 46, 80} {
		in := compactInput(CompactInput{Width: width, Content: InspectorReady, Session: s})
		lines := renderCompactLines(t, in)
		_, stateLabel := compactStatusParts(in)
		wantSuffix := ansi.Strip(stateLabel)
		if !strings.HasPrefix(lines[compactTitleRow], "a") || !strings.HasSuffix(lines[compactTitleRow], wantSuffix) {
			t.Fatalf("width %d: card header must retain its title and end in status: %q", width, lines[compactTitleRow])
		}
		if strings.Contains(lines[compactAgentRow], "Working") {
			t.Fatalf("width %d: state is repeated on agent line: %q", width, lines[compactAgentRow])
		}
	}
	s.State = "key"
	line := renderCompactLines(t, CompactInput{Width: 20, Content: InspectorReady, Session: s})[compactTitleRow]
	if !strings.HasSuffix(line, "Needs permission") {
		t.Fatalf("narrow card truncated a state that fits: %q", line)
	}
}

func compactInput(in CompactInput) CompactInput {
	if in.Width == 0 {
		in.Width = 46
	}
	if in.Height == 0 {
		in.Height = 10
	}
	if in.Now.IsZero() {
		in.Now = inspectorNow
	}
	if in.Cfg == nil {
		in.Cfg = &config.Config{}
	}
	return in
}

// renderCompactLines renders and returns the plain-text lines, the only form an
// assertion about geometry can be made against.
func renderCompactLines(t *testing.T, in CompactInput) []string {
	t.Helper()
	in = compactInput(in)
	out := RenderCompactPreview(in)
	lines := strings.Split(out, "\n")
	if len(lines) != in.Height {
		t.Fatalf("RenderCompactPreview returned %d lines, want exactly the %d it was given:\n%s", len(lines), in.Height, out)
	}
	for i, line := range lines {
		if got := layout.Width(ansi.Strip(line)); got > in.Width {
			t.Fatalf("line %d is %d cells wide, want at most %d: %q", i, got, in.Width, ansi.Strip(line))
		}
		lines[i] = ansi.Strip(line)
	}
	return lines
}

// TestCompactHeaderGeometryIsFixed is the tile's half of the scanning contract.
// A grid of previews whose dividers sat on different rows would ripple every
// time any one session changed what it had to say about itself.
func TestCompactHeaderGeometryIsFixed(t *testing.T) {
	full := inspectorSession()
	bare := session.RunningSession{WindowID: 12, Base: "bare", Project: "bare"}

	cases := []struct {
		name string
		in   CompactInput
	}{
		{"ready", CompactInput{Content: InspectorReady, Session: full, Output: SplitOutput("a\nb\n")}},
		{"loading", CompactInput{Content: InspectorLoading, Session: full}},
		{"failed", CompactInput{Content: InspectorFailed, Session: full, Error: "kitty: no such window"}},
		{"vanished", CompactInput{Content: InspectorVanished, Session: full}},
		{"no selection", CompactInput{Content: InspectorNoSelection}},
		{"session with nothing to say", CompactInput{Content: InspectorReady, Session: bare}},
		{"qualified", CompactInput{Content: InspectorReady, Session: full, Qualifier: "feature/queue"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := renderCompactLines(t, tc.in)
			divider := lines[CompactMetaLines]
			if !strings.HasPrefix(strings.TrimSpace(divider), "─") {
				t.Errorf("the divider is not on row %d; row %d is %q", CompactMetaLines, CompactMetaLines, divider)
			}
			for i := 0; i < CompactMetaLines; i++ {
				if strings.Contains(lines[i], "─────") {
					t.Errorf("the divider moved up to row %d: %q", i, lines[i])
				}
			}
		})
	}
}

// TestCompactNamesItsThreeFacts walks the header in order. Which session, what
// is it doing and for how long, and what is running it — the three questions
// asked of a session nobody is currently looking at.
func TestCompactNamesItsThreeFacts(t *testing.T) {
	lines := renderCompactLines(t, CompactInput{
		Content: InspectorReady,
		Session: inspectorSession(),
		Output:  SplitOutput("hello\n"),
	})
	for _, want := range []string{"api", "Working", "0:42"} {
		if !strings.Contains(lines[compactTitleRow], want) {
			t.Errorf("header line = %q, want it to name %q", lines[compactTitleRow], want)
		}
	}
	if !strings.Contains(lines[compactAgentRow], "claude · work") {
		t.Errorf("agent row = %q", lines[compactAgentRow])
	}
}

// TestCompactDropsTheFactsThatBelongToTheFullInspector is the other half of the
// cut: a tile must not silently keep spending its four remaining rows on the
// branch, the directory, and the watch line.
func TestCompactDropsTheFactsThatBelongToTheFullInspector(t *testing.T) {
	s := inspectorSession()
	s.Locked = true
	s.Muted = true
	lines := renderCompactLines(t, CompactInput{Content: InspectorReady, Session: s, Output: SplitOutput("x\n")})
	header := strings.Join(lines[:CompactMetaLines], "\n")
	for _, unwanted := range []string{"feature/queue", "/home/user/projects/api", "Branch", "Dir", "Watch"} {
		if strings.Contains(header, unwanted) {
			t.Errorf("the compact header still carries %q:\n%s", unwanted, header)
		}
	}
}

// TestCompactQualifierIsDrawnBesideTheName. A grid puts colliding names side by
// side, which is exactly the case the inspector never has to render.
func TestCompactQualifierIsDrawnBesideTheName(t *testing.T) {
	lines := renderCompactLines(t, CompactInput{
		Content:   InspectorReady,
		Session:   inspectorSession(),
		Qualifier: "feature/queue",
		Output:    SplitOutput("x\n"),
	})
	if got := lines[compactTitleRow]; !strings.HasPrefix(got, "api (feature/queue)") || !strings.HasSuffix(got, "0:42 · Working") {
		t.Errorf("title row = %q, want the name and its qualifier", got)
	}

	plain := renderCompactLines(t, CompactInput{Content: InspectorReady, Session: inspectorSession(), Output: SplitOutput("x\n")})
	if got := plain[compactTitleRow]; !strings.HasPrefix(got, "api ") || !strings.HasSuffix(got, "0:42 · Working") {
		t.Errorf("unqualified title row = %q, want the bare name", got)
	}
}

// TestCompactKeepsTheFiveOutputSituationsApart. The whole reason the content
// state is an explicit input is that these five must not collapse into two.
func TestCompactKeepsTheFiveOutputSituationsApart(t *testing.T) {
	s := inspectorSession()
	cases := []struct {
		name string
		in   CompactInput
		want string
	}{
		{"loading", CompactInput{Content: InspectorLoading, Session: s}, previewLoadingText},
		{"empty buffer", CompactInput{Content: InspectorReady, Session: s}, previewEmptyText},
		{"failed capture", CompactInput{Content: InspectorFailed, Session: s, Error: "boom"}, previewErrorPrefix + "boom"},
		{"vanished target", CompactInput{Content: InspectorVanished, Session: s}, previewVanishedText},
		{"no selection", CompactInput{Content: InspectorNoSelection}, previewNoSelectionText},
	}
	seen := make(map[string]string, len(cases))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := renderCompactLines(t, tc.in)
			first := lines[CompactMetaLines+1]
			if !strings.Contains(first, tc.want) {
				t.Fatalf("first output row = %q, want it to say %q", first, tc.want)
			}
			if other, clash := seen[first]; clash {
				t.Fatalf("%q and %q render the same sentence %q", tc.name, other, first)
			}
			seen[first] = tc.name
		})
	}
}

// TestCompactAndInspectorSayTheSameSentences pins the shared vocabulary. The two
// renderers are separate functions in separate files, and a preview that told an
// operator "(loading…)" in the grid and something else in the pane they opened
// from it would read as two different facts about one session.
func TestCompactAndInspectorSayTheSameSentences(t *testing.T) {
	s := inspectorSession()
	states := []struct {
		content InspectorContent
		err     string
		output  []string
	}{
		{content: InspectorLoading},
		{content: InspectorReady},
		{content: InspectorFailed, err: "kitty: no such window"},
		{content: InspectorVanished},
		{content: InspectorNoSelection},
		{content: InspectorReady, output: SplitOutput("only line\n")},
	}
	for _, st := range states {
		t.Run(st.content.String(), func(t *testing.T) {
			full := renderInspectorLines(t, inspectorInput(InspectorInput{
				Content: st.content, Session: s, Error: st.err, Output: st.output, Width: 46, Height: 13,
			}))
			tile := renderCompactLines(t, CompactInput{
				Content: st.content, Session: s, Error: st.err, Output: st.output, Width: 46, Height: 10,
			})
			if got, want := tile[CompactMetaLines+1], full[InspectorMetaLines+1]; got != want {
				t.Errorf("tile says %q where the inspector says %q", got, want)
			}
			if st.content != InspectorNoSelection && !strings.HasSuffix(tile[compactTitleRow], "0:42 · Working") {
				t.Errorf("tile header %q does not end with the agent state", tile[compactTitleRow])
			}
		})
	}
}

// TestCompactShowsTheTailOfTheOutput. A tile has no scroll position, so the
// bottom line must be the newest one.
func TestCompactShowsTheTailOfTheOutput(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	lines := renderCompactLines(t, CompactInput{
		Content: InspectorReady,
		Session: inspectorSession(),
		Output:  SplitOutput(b.String()),
		Height:  10,
	})
	if got := lines[len(lines)-1]; !strings.HasSuffix(got, "39") {
		t.Errorf("last row = %q, want the newest output line", got)
	}
	if got := lines[CompactMetaLines+1]; !strings.HasSuffix(got, "33") {
		t.Errorf("first output row = %q, want the tail window to start at line 33", got)
	}
}

// TestCompactHonorsEveryPaneSize sweeps the sizes a grid can hand a tile,
// including the ones too small for the chrome. The height contract holds at all
// of them, and the header is what survives a pane that cannot hold everything.
func TestCompactHonorsEveryPaneSize(t *testing.T) {
	s := inspectorSession()
	for _, width := range []int{1, 2, 5, 12, 30, 44, 46, 80, 200} {
		for _, height := range []int{1, 2, 3, 4, 5, 10, 12, 40} {
			in := compactInput(CompactInput{
				Width: width, Height: height,
				Content: InspectorReady, Session: s, Output: SplitOutput("alpha\nbeta\n"),
			})
			out := RenderCompactPreview(in)
			lines := strings.Split(out, "\n")
			if len(lines) != height {
				t.Fatalf("%dx%d returned %d lines", width, height, len(lines))
			}
			for i, line := range lines {
				if got := layout.StyledWidth(line); got > width {
					t.Fatalf("%dx%d line %d is %d cells wide: %q", width, height, i, got, ansi.Strip(line))
				}
			}
			if height <= CompactMetaLines {
				// Too short for the divider: what fits is header, and no output.
				if strings.Contains(ansi.Strip(out), "alpha") {
					t.Fatalf("%dx%d drew output before it finished the header:\n%s", width, height, ansi.Strip(out))
				}
			}
		}
	}
}

// TestCompactDegenerateSizesRenderNothing. Zero and negative dimensions are
// reachable from a grid on a terminal being resized, and must not panic.
func TestCompactDegenerateSizesRenderNothing(t *testing.T) {
	for _, tc := range [][2]int{{0, 10}, {10, 0}, {-1, 10}, {10, -1}, {0, 0}} {
		in := CompactInput{Width: tc[0], Height: tc[1], Content: InspectorReady, Cfg: &config.Config{}, Now: inspectorNow}
		if got := RenderCompactPreview(in); got != "" {
			t.Errorf("%dx%d rendered %q, want nothing", tc[0], tc[1], got)
		}
	}
}

// TestCompactKeepsWideGraphemesWhole. A project icon is two cells and a CJK
// title is two cells per character; a tile that cut one in half would spill into
// the tile beside it.
func TestCompactKeepsWideGraphemesWhole(t *testing.T) {
	s := inspectorSession()
	s.Project = "🗣️ 設定管理プロジェクト"
	for width := 1; width <= 30; width++ {
		in := compactInput(CompactInput{
			Width: width, Height: 8,
			Content: InspectorReady, Session: s, Output: SplitOutput("日本語の出力行です\n"),
		})
		for i, line := range strings.Split(RenderCompactPreview(in), "\n") {
			if got := layout.StyledWidth(line); got > width {
				t.Fatalf("width %d line %d measured %d cells: %q", width, i, got, ansi.Strip(line))
			}
		}
	}
}

// TestCompactDoesNotLeakStylesPastItsWidth. A capture is arbitrary bytes from
// somebody else's terminal; a truncation that cut an escape sequence in half
// would paint the rest of the grid.
func TestCompactDoesNotLeakStylesPastItsWidth(t *testing.T) {
	poisoned := "\x1b[31mred text that runs well past the tile" + strings.Repeat(" and on", 20)
	in := compactInput(CompactInput{
		Width: 20, Height: 8,
		Content: InspectorReady, Session: inspectorSession(), Output: SplitOutput(poisoned + "\n"),
	})
	for i, line := range strings.Split(RenderCompactPreview(in), "\n") {
		if strings.Count(line, "\x1b") > 0 && !strings.Contains(line, "m") {
			t.Fatalf("line %d carries a truncated escape sequence: %q", i, line)
		}
		if got := layout.StyledWidth(line); got > 20 {
			t.Fatalf("line %d measured %d cells: %q", i, got, ansi.Strip(line))
		}
	}
}

// TestCompactIsPure renders the same input twice and compares bytes. Nothing in
// the tile may read a clock, a config file, Kitty, or the daemon.
func TestCompactIsPure(t *testing.T) {
	in := compactInput(CompactInput{
		Content: InspectorReady,
		Session: inspectorSession(),
		Output:  SplitOutput("one\ntwo\n"),
		LastActivity: map[int]time.Time{
			11: inspectorNow.Add(-time.Minute),
		},
		IdleThreshold: 10 * time.Minute,
	})
	first := RenderCompactPreview(in)
	second := RenderCompactPreview(in)
	if first != second {
		t.Fatalf("two renders of one input differ:\n%q\n%q", first, second)
	}
}

// TestAddingTheCompactPreviewLeftTheInspectorAtSixRows is the regression the
// shared-helper extraction exists to be checked against. The full pane's
// metadata block is a fixed six lines and its divider sits under them.
func TestAddingTheCompactPreviewLeftTheInspectorAtSixRows(t *testing.T) {
	if InspectorMetaLines != 6 {
		t.Fatalf("InspectorMetaLines = %d, want 6", InspectorMetaLines)
	}
	if InspectorChromeLines != 7 {
		t.Fatalf("InspectorChromeLines = %d, want 7", InspectorChromeLines)
	}
	if CompactMetaLines != 2 || CompactChromeLines != 3 {
		t.Fatalf("compact chrome = %d/%d, want 2 header rows and 3 chrome rows", CompactMetaLines, CompactChromeLines)
	}
	lines := renderInspectorLines(t, inspectorInput(InspectorInput{
		Content: InspectorReady,
		Session: session.RunningSession{WindowID: 9, Base: "x", Project: "x", Harness: harness.Claude},
		Output:  SplitOutput("body\n"),
	}))
	if !strings.HasPrefix(strings.TrimSpace(lines[6]), "─") {
		t.Fatalf("the inspector's divider is no longer on row 6: %q", lines[6])
	}
	if strings.Contains(lines[5], "─────") {
		t.Fatalf("the inspector's divider moved up to row 5: %q", lines[5])
	}
}
