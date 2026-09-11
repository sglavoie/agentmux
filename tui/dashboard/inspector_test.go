package dashboard

import (
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

var inspectorNow = time.Unix(1_700_000_000, 0)

func inspectorSession() session.RunningSession {
	return session.RunningSession{
		WindowID:  11,
		Base:      "api",
		Project:   "api",
		Profile:   "work",
		Harness:   harness.Claude,
		Directory: "/home/user/projects/api",
		Branch:    "feature/queue",
		Dirty:     true,
		State:     "bolt",
		BusySince: inspectorNow.Add(-42 * time.Second),
	}
}

func inspectorInput(in InspectorInput) InspectorInput {
	if in.Width == 0 {
		in.Width = 60
	}
	if in.Height == 0 {
		in.Height = 14
	}
	if in.Now.IsZero() {
		in.Now = inspectorNow
	}
	if in.Cfg == nil {
		in.Cfg = &config.Config{}
	}
	return in
}

// renderInspectorLines renders and returns the plain-text lines, which is the
// only form an assertion about geometry can be made against.
func renderInspectorLines(t *testing.T, in InspectorInput) []string {
	t.Helper()
	in = inspectorInput(in)
	out := RenderInspector(in)
	lines := strings.Split(out, "\n")
	if len(lines) != in.Height {
		t.Fatalf("RenderInspector returned %d lines, want exactly the %d it was given:\n%s", len(lines), in.Height, out)
	}
	for i, line := range lines {
		if got := layout.Width(ansi.Strip(line)); got > in.Width {
			t.Fatalf("line %d is %d cells wide, want at most %d: %q", i, got, in.Width, ansi.Strip(line))
		}
		lines[i] = ansi.Strip(line)
	}
	return lines
}

// TestInspectorMetadataGeometryIsFixed is the pane's scanning contract: the
// divider and the output below it must sit on the same rows no matter what the
// selected session can say about itself, so moving the cursor down a list does
// not make the text the operator is reading walk up and down the pane.
func TestInspectorMetadataGeometryIsFixed(t *testing.T) {
	full := inspectorSession()
	bare := session.RunningSession{WindowID: 12, Base: "bare", Project: "bare"}

	cases := []struct {
		name string
		in   InspectorInput
	}{
		{"ready", InspectorInput{Content: InspectorReady, Session: full, Output: SplitOutput("a\nb\n")}},
		{"loading", InspectorInput{Content: InspectorLoading, Session: full}},
		{"failed", InspectorInput{Content: InspectorFailed, Session: full, Error: "kitty: no such window"}},
		{"vanished", InspectorInput{Content: InspectorVanished, Session: full}},
		{"no selection", InspectorInput{Content: InspectorNoSelection}},
		{"session with nothing to say", InspectorInput{Content: InspectorReady, Session: bare}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := renderInspectorLines(t, tc.in)
			divider := lines[InspectorMetaLines]
			if !strings.HasPrefix(strings.TrimSpace(divider), "─") {
				t.Errorf("the divider is not on row %d; row %d is %q", InspectorMetaLines, InspectorMetaLines, divider)
			}
			for i := 0; i < InspectorMetaLines; i++ {
				if strings.Contains(lines[i], "─────") {
					t.Errorf("the divider moved up to row %d: %q", i, lines[i])
				}
			}
		})
	}
}

// TestInspectorNamesEveryMetadataFact walks the six lines in order. The order
// itself is the contract, because that is what makes the block scannable.
func TestInspectorNamesEveryMetadataFact(t *testing.T) {
	s := inspectorSession()
	s.Locked = true
	s.Muted = true
	lines := renderInspectorLines(t, InspectorInput{
		Content:  InspectorReady,
		Session:  s,
		Watchdog: protocol.WatchdogState{LastAction: "stop", LastEventAt: inspectorNow.Add(-3 * time.Minute).Unix(), ThresholdSec: 600},
		Output:   SplitOutput("hello\n"),
	})
	for i, want := range []string{"api", "Working", "claude · work", "feature/queue*", "projects/api", "last event 3m"} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("metadata line %d = %q, want it to name %q", i, lines[i], want)
		}
	}
	if !strings.Contains(lines[1], "0:42") {
		t.Errorf("the status line lost its age: %q", lines[1])
	}
	if !strings.Contains(lines[5], loopMarkerGlyph) && !strings.Contains(lines[5], "🔒") {
		t.Errorf("the watch line lost the lock bit: %q", lines[5])
	}
}

// TestInspectorUnknownFactsKeepTheirLine pins the `—` rule. A build that cannot
// supply a fact still spends the line, because a line that collapsed would move
// every line under it.
func TestInspectorUnknownFactsKeepTheirLine(t *testing.T) {
	lines := renderInspectorLines(t, InspectorInput{
		Content: InspectorReady,
		Session: session.RunningSession{WindowID: 9, Base: "plain", Project: "plain"},
	})
	for _, i := range []int{3, 4, 5} {
		if !strings.Contains(lines[i], inspectorUnknown) {
			t.Errorf("metadata line %d = %q, want the %q placeholder", i, lines[i], inspectorUnknown)
		}
	}
	for i, label := range map[int]string{2: "Agent", 3: "Branch", 4: "Dir", 5: "Watch"} {
		if !strings.HasPrefix(lines[i], label) {
			t.Errorf("metadata line %d = %q, want it to start with the %q label", i, lines[i], label)
		}
	}
}

// TestInspectorSeparatesLoadingFromEmpty is the distinction the pane it replaces
// could not draw: an empty string is the evidence that cannot tell "nothing has
// come back" from "this session has printed nothing".
func TestInspectorSeparatesLoadingFromEmpty(t *testing.T) {
	s := inspectorSession()
	cases := []struct {
		name string
		in   InspectorInput
		want string
	}{
		{"loading", InspectorInput{Content: InspectorLoading, Session: s}, "(loading…)"},
		{"legitimately empty", InspectorInput{Content: InspectorReady, Session: s}, "(no output)"},
		{"failed", InspectorInput{Content: InspectorFailed, Session: s, Error: "read timed out"}, "error: read timed out"},
		{"vanished", InspectorInput{Content: InspectorVanished, Session: s}, "(session not running)"},
		{"no selection", InspectorInput{Content: InspectorNoSelection}, "(no session selected)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Join(renderInspectorLines(t, tc.in)[InspectorChromeLines:], "\n")
			if !strings.Contains(body, tc.want) {
				t.Errorf("the %s output region did not say %q:\n%s", tc.name, tc.want, body)
			}
		})
	}
}

// TestInspectorNoSelectionDrawsNoMetadata guards against the zero session being
// rendered as if it were a conversation.
func TestInspectorNoSelectionDrawsNoMetadata(t *testing.T) {
	lines := renderInspectorLines(t, InspectorInput{Content: InspectorNoSelection})
	for i := 0; i < InspectorMetaLines; i++ {
		if strings.TrimSpace(lines[i]) != "" {
			t.Errorf("an empty selection drew metadata on line %d: %q", i, lines[i])
		}
	}
}

func TestInspectorOutputFollowsTheTail(t *testing.T) {
	lines := renderInspectorLines(t, InspectorInput{
		Height:  InspectorChromeLines + 3,
		Content: InspectorReady,
		Session: inspectorSession(),
		Output:  SplitOutput("one\ntwo\nthree\nfour\nfive\n"),
	})
	body := lines[InspectorChromeLines:]
	if strings.Join(body, "|") != "three|four|five" {
		t.Errorf("the output region showed %v, want the last three lines", body)
	}
}

// TestInspectorPausedIndicationRidesTheDivider pins where Paused is said. A row
// of its own would appear and disappear, moving the output under the reader at
// exactly the moment they asked for it to stop moving.
func TestInspectorPausedIndicationRidesTheDivider(t *testing.T) {
	latest := SplitOutput(strings.Repeat("new\n", 20))
	held := []string{"held one", "held two", "held three", "held four", "held five"}
	v := OutputViewport{}.ScrollUp(held, 2, 3)

	lines := renderInspectorLines(t, InspectorInput{
		Height:   InspectorChromeLines + 2,
		Content:  InspectorReady,
		Session:  inspectorSession(),
		Output:   latest,
		Viewport: v,
	})
	if !strings.Contains(lines[InspectorMetaLines], inspectorPausedLabel) {
		t.Errorf("the divider did not carry the Paused indication: %q", lines[InspectorMetaLines])
	}
	if got := strings.Join(lines[InspectorChromeLines:], "|"); got != "held one|held two" {
		t.Errorf("the paused pane showed %q, want the held snapshot rather than the newest one", got)
	}

	// A pane that is not holding never says Paused.
	unpaused := renderInspectorLines(t, InspectorInput{Content: InspectorReady, Session: inspectorSession(), Output: latest})
	if strings.Contains(strings.Join(unpaused, "\n"), inspectorPausedLabel) {
		t.Errorf("a following pane claimed to be paused:\n%s", strings.Join(unpaused, "\n"))
	}
}

// TestInspectorHoldSurvivesAFailedRefresh is the ordering the output region
// encodes: a pause is a promise that the text stays put, and a read that failed
// or has not landed must not blank the snapshot the operator paused to read.
// What it may not do is hide that it is stale, which the divider says.
func TestInspectorHoldSurvivesAFailedRefresh(t *testing.T) {
	held := []string{"held one", "held two", "held three", "held four"}
	v := OutputViewport{}.ScrollUp(held, 2, 2)

	for _, content := range []InspectorContent{InspectorFailed, InspectorLoading} {
		t.Run(content.String(), func(t *testing.T) {
			lines := renderInspectorLines(t, InspectorInput{
				Height:   InspectorChromeLines + 2,
				Content:  content,
				Session:  inspectorSession(),
				Error:    "read timed out",
				Viewport: v,
			})
			if got := strings.Join(lines[InspectorChromeLines:], "|"); got != "held one|held two" {
				t.Errorf("a %s refresh replaced the held text with %q", content, got)
			}
			if !strings.Contains(lines[InspectorMetaLines], inspectorPausedLabel) {
				t.Errorf("the pane kept held text without saying it was paused: %q", lines[InspectorMetaLines])
			}
		})
	}

	// A vanished window outranks the hold: its metadata is still the answer, but
	// there is no live buffer left for a snapshot of it to be a stale view OF.
	lines := renderInspectorLines(t, InspectorInput{
		Height:   InspectorChromeLines + 2,
		Content:  InspectorVanished,
		Session:  inspectorSession(),
		Viewport: v,
	})
	if !strings.Contains(strings.Join(lines[InspectorChromeLines:], "\n"), "(session not running)") {
		t.Errorf("a vanished session kept showing held output:\n%s", strings.Join(lines, "\n"))
	}
}

// TestInspectorSurvivesAPaneTooShortForItsChrome pins the priority: an
// inspector that cannot say which session it is describing is worse than one
// that cannot show its output.
func TestInspectorSurvivesAPaneTooShortForItsChrome(t *testing.T) {
	for height := 1; height <= InspectorChromeLines+1; height++ {
		lines := renderInspectorLines(t, InspectorInput{
			Height: height, Content: InspectorReady, Session: inspectorSession(),
			Output: SplitOutput("alpha\nbeta\n"),
		})
		if !strings.Contains(lines[0], "api") {
			t.Errorf("at height %d the pane lost its title: %q", height, lines[0])
		}
	}
	if got := RenderInspector(InspectorInput{Width: 0, Height: 8}); got != "" {
		t.Errorf("a zero-width pane rendered %q, want nothing", got)
	}
	if got := RenderInspector(InspectorInput{Width: 40, Height: 0}); got != "" {
		t.Errorf("a zero-height pane rendered %q, want nothing", got)
	}
}

// TestInspectorClipsStyledAndWideOutput keeps a capture from painting past the
// pane. Terminal output carries escape sequences and non-ASCII text, so it must
// be measured by the cells it paints and cut where a sequence cannot leak.
func TestInspectorClipsStyledAndWideOutput(t *testing.T) {
	output := []string{
		"\x1b[31m" + strings.Repeat("red ", 40) + "\x1b[0m",
		strings.Repeat("世界", 40),
		strings.Repeat("🗣️ok", 30),
	}
	for _, width := range []int{12, 24, 48} {
		out := RenderInspector(inspectorInput(InspectorInput{
			Width: width, Height: InspectorChromeLines + len(output),
			Content: InspectorReady, Session: inspectorSession(), Output: output,
		}))
		for i, line := range strings.Split(out, "\n") {
			if got := layout.Width(ansi.Strip(line)); got > width {
				t.Errorf("at width %d line %d painted %d cells: %q", width, i, got, ansi.Strip(line))
			}
		}
	}
}

// TestInspectorWatchdogWarningMatchesTheActivityColumn guards the one rule this
// pane shares with the queue row. Two copies of the 80% band would be two
// moments a window could start looking stale in one place and not the other.
func TestInspectorWatchdogWarningMatchesTheActivityColumn(t *testing.T) {
	s := inspectorSession()
	ws := protocol.WatchdogState{LastAction: "stop", ThresholdSec: 100}

	fresh := ws
	fresh.LastEventAt = inspectorNow.Add(-50 * time.Second).Unix()
	stale := ws
	stale.LastEventAt = inspectorNow.Add(-90 * time.Second).Unix()

	for _, tc := range []struct {
		name string
		ws   protocol.WatchdogState
		want bool
	}{{"fresh", fresh, false}, {"stale", stale, true}} {
		t.Run(tc.name, func(t *testing.T) {
			lines := renderInspectorLines(t, InspectorInput{Content: InspectorReady, Session: s, Watchdog: tc.ws})
			_, columnWarns := activityCellText(s, tc.ws, inspectorNow)
			if columnWarns != tc.want {
				t.Fatalf("the activity column warned=%v, want %v", columnWarns, tc.want)
			}
			if got := strings.Contains(lines[5], ageWarningGlyph); got != tc.want {
				t.Errorf("the watch line warned=%v while the activity column warned=%v: %q", got, tc.want, lines[5])
			}
		})
	}

	// No recorded action is an absence of evidence about silence, not evidence
	// of it, so the line says nothing rather than a number.
	lines := renderInspectorLines(t, InspectorInput{Content: InspectorReady, Session: s})
	if strings.Contains(lines[5], "last event") {
		t.Errorf("a window with no watchdog entry reported an age: %q", lines[5])
	}
}
