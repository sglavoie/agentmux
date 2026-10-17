package dashboard

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

// cardInput builds the card input for the first family summary in a projection
// of the given rows, which is how the card is always reached in production: the
// item layer decides what a family is, and the card reads that decision.
func cardInput(t *testing.T, rows []session.RunningSession, width, height int) ProjectCardInput {
	t.Helper()
	items := BuildProjectItems(rows, nil)
	for _, it := range items.Items {
		if it.Kind == ItemFamilySummary {
			return ProjectCardInput{
				Width: width, Height: height, Items: items, Item: it,
				IdleThreshold: projectIdle, Cfg: &config.Config{},
			}
		}
	}
	t.Fatalf("no family summary in the projection of %d rows", len(rows))
	return ProjectCardInput{}
}

func renderCardLines(t *testing.T, in ProjectCardInput) []string {
	t.Helper()
	out := RenderProjectCard(in)
	lines := strings.Split(out, "\n")
	if len(lines) != in.Height {
		t.Fatalf("RenderProjectCard returned %d lines, want exactly the %d it was given:\n%s", len(lines), in.Height, out)
	}
	for i, line := range lines {
		if got := layout.Width(ansi.Strip(line)); got > in.Width {
			t.Fatalf("card line %d is %d cells wide, want at most %d: %q", i, got, in.Width, ansi.Strip(line))
		}
		lines[i] = ansi.Strip(line)
	}
	return lines
}

func cardFamily() []session.RunningSession {
	return []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "api", State: "key", Harness: harness.Claude, Profile: "work",
			Directory: "/repo/api", Branch: "main", FamilyKey: "git:/repo", FamilyLabel: "monorepo", FamilyPrimaryRoot: "/repo"},
		{TabID: 2, WindowID: 21, Base: "web", State: "bolt", Harness: harness.Codex, Profile: "personal",
			Directory: "/repo/web", Branch: "feat", FamilyKey: "git:/repo", FamilyLabel: "monorepo", FamilyPrimaryRoot: "/repo"},
	}
}

// TestProjectCardWearsTheInspectorGeometry is what keeps the pane from jumping
// when the cursor crosses from a family into one of its children.
func TestProjectCardWearsTheInspectorGeometry(t *testing.T) {
	lines := renderCardLines(t, cardInput(t, cardFamily(), 60, 14))
	if !strings.HasPrefix(strings.TrimSpace(lines[projectCardMetadataLines]), "─") {
		t.Errorf("the card's divider is not on row %d: %q", projectCardMetadataLines, lines[projectCardMetadataLines])
	}
	for i, label := range map[int]string{2: "Agent", 3: "Branch", 4: "Dir", 5: "Watch"} {
		if !strings.HasPrefix(lines[i], label) {
			t.Errorf("card line %d = %q, want the inspector's %q label on the same cell", i, lines[i], label)
		}
	}
	// The card is never paused: there was never a read to hold a snapshot of.
	if strings.Contains(strings.Join(lines, "\n"), inspectorPausedLabel) {
		t.Errorf("the card claimed to be paused:\n%s", strings.Join(lines, "\n"))
	}
}

// TestProjectCardAggregatesRatherThanBorrowing is the semantic half. Reporting
// one member's answer for the family would be the "resolve a summary to some
// arbitrary session" the item layer refuses everywhere else.
func TestProjectCardAggregatesRatherThanBorrowing(t *testing.T) {
	lines := renderCardLines(t, cardInput(t, cardFamily(), 60, 14))

	if !strings.Contains(lines[2], "2 harnesses") || !strings.Contains(lines[2], "2 profiles") {
		t.Errorf("the Agent line borrowed a member's answer: %q", lines[2])
	}
	if !strings.Contains(lines[3], "2 branches") {
		t.Errorf("the Branch line borrowed a member's checkout: %q", lines[3])
	}
	if strings.Contains(lines[3], dirtyMarker) {
		t.Errorf("a multi-checkout family attached one tree's dirty bit to the family: %q", lines[3])
	}
	if !strings.Contains(lines[4], "/repo") || strings.Contains(lines[4], "/repo/api") {
		t.Errorf("the Dir line shows a member's directory rather than the family root: %q", lines[4])
	}
	if !strings.Contains(lines[1], "1 attention") || !strings.Contains(lines[1], "1 active") {
		t.Errorf("the card disagrees with the summary row about its tally: %q", lines[1])
	}
	if strings.Contains(strings.Join(lines, "\n"), "git:/repo") {
		t.Errorf("the family key reached the card:\n%s", strings.Join(lines, "\n"))
	}
}

// TestProjectCardNamesTheValueWhenMembersAgree is the other half of the
// aggregate rule: a count is what a disagreement looks like, not the default.
func TestProjectCardNamesTheValueWhenMembersAgree(t *testing.T) {
	rows := cardFamily()
	rows[1].Harness = harness.Claude
	rows[1].Profile = "work"
	rows[1].Branch = "main"
	rows[1].Dirty = true

	lines := renderCardLines(t, cardInput(t, rows, 60, 14))
	if !strings.Contains(lines[2], "claude · work") {
		t.Errorf("a family that agrees reported a count instead of the value: %q", lines[2])
	}
	// One checkout, so the dirty bit is a fact about the family rather than
	// about the member that happened to be asked.
	if !strings.Contains(lines[3], "main"+dirtyMarker) {
		t.Errorf("a single-checkout family lost its modified marker: %q", lines[3])
	}
}

// TestProjectCardWatchCountsMembers pins the last line's aggregate form, and
// that the watchdog age has no aggregate form at all.
func TestProjectCardWatchCountsMembers(t *testing.T) {
	rows := cardFamily()
	rows[0].Locked = true
	rows[0].Looping = true
	rows[1].Muted = true

	in := cardInput(t, rows, 60, 14)
	in.LoopMarker = func(s session.RunningSession) (string, bool) {
		return "", false
	}
	lines := renderCardLines(t, in)
	if !strings.Contains(lines[5], loopMarkerGlyph+" 1") {
		t.Errorf("the Watch line lost the supervised member: %q", lines[5])
	}
	if !strings.Contains(lines[5], "1") || !strings.Contains(lines[5], "·") {
		t.Errorf("the Watch line did not count the lock and mute bits: %q", lines[5])
	}
	if strings.Contains(lines[5], "last event") {
		t.Errorf("the card reported a per-window silence as a family fact: %q", lines[5])
	}

	// A run the daemon's own loop bit has not caught up with is still a
	// supervised member, which is what the seam is asked for.
	in.LoopMarker = func(s session.RunningSession) (string, bool) {
		return loopMarkerGlyph, s.WindowID == 21
	}
	lines = renderCardLines(t, in)
	if !strings.Contains(lines[5], loopMarkerGlyph+" 2") {
		t.Errorf("the Watch line ignored the supervision seam: %q", lines[5])
	}
}

// TestProjectCardNeverDropsMembersSilently is the height bound's one rule. A
// card that named a family and then showed none of it would look exactly like a
// family with no sessions, which is a claim the projection can also make truly.
func TestProjectCardNeverDropsMembersSilently(t *testing.T) {
	rows := cardFamily()
	rows = append(rows, session.RunningSession{TabID: 3, WindowID: 31, Base: "cli", State: "bolt",
		Directory: "/repo/cli", FamilyKey: "git:/repo", FamilyLabel: "monorepo", FamilyPrimaryRoot: "/repo"})

	for height := 2; height <= projectCardChromeLines+3; height++ {
		in := cardInput(t, rows, 60, height)
		lines := renderCardLines(t, in)
		body := strings.Join(lines, "\n")
		shown := 0
		for _, name := range []string{"api", "web", "cli"} {
			// The family label is on line 0 and names no member, so members are
			// counted below the heading only.
			if strings.Contains(strings.Join(lines[1:], "\n"), name) {
				shown++
			}
		}
		if shown < 3 && !strings.Contains(body, "more") {
			t.Errorf("at height %d the card showed %d of 3 members without saying so:\n%s", height, shown, body)
		}
		if !strings.Contains(lines[0], "monorepo") {
			t.Errorf("at height %d the card lost the family it was describing: %q", height, lines[0])
		}
	}

	// A single-line card keeps the name instead: `+3 more` more than what, on a
	// card that never said which family.
	one := renderCardLines(t, cardInput(t, rows, 60, 1))
	if !strings.Contains(one[0], "monorepo") {
		t.Errorf("a one-line card dropped the family name: %q", one[0])
	}
}

func TestProjectCardRejectsADegeneratePane(t *testing.T) {
	in := cardInput(t, cardFamily(), 0, 10)
	if got := RenderProjectCard(in); got != "" {
		t.Errorf("a zero-width card rendered %q, want nothing", got)
	}
	in = cardInput(t, cardFamily(), 40, 0)
	if got := RenderProjectCard(in); got != "" {
		t.Errorf("a zero-height card rendered %q, want nothing", got)
	}
}
