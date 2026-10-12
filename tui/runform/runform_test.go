package runform

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/planrun"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/tui/layout"
)

func testForm() State {
	return Open(
		plans.PlanMeta{Name: "fix-things", DirPath: "/home/me/.local/share/agent-plans/2e774ec64570-1a09b1daef1c", CWD: "/home/me/projects/api"},
		[]config.Session{{Title: "API", Profile: "codex-work"}, {Title: "Web", Profile: "claude-work"}},
		[]string{"claude-work", "codex-work"}, 1,
	)
}

func TestFormOpensOnTheFirstProjectUnderItsOwnProfile(t *testing.T) {
	form := testForm()
	if form.Profiles[form.Profile] != "codex-work" {
		t.Fatalf("profile = %q, want the first project's", form.Profiles[form.Profile])
	}
	if got := form.Parameters(); len(got) != 1 || got["profile"] != "codex-work" {
		t.Fatalf("parameters = %v, want only the profile: every other choice is ASL's default", got)
	}
	if got := planrun.Command(form.Plan.DirPath, form.Options()); got != "asl run "+form.Plan.DirPath {
		t.Fatalf("command = %q", got)
	}
}

func TestCyclingARowWrapsAndAProjectBringsItsProfile(t *testing.T) {
	form := testForm()
	form.Cycle(1)
	if project, _ := form.SelectedProject(); project.Title != "Web" || form.Profiles[form.Profile] != "claude-work" {
		t.Fatalf("project = %q profile = %q", project.Title, form.Profiles[form.Profile])
	}
	form.Cycle(1)
	if project, _ := form.SelectedProject(); project.Title != "API" {
		t.Fatalf("project did not wrap: %q", project.Title)
	}

	// A profile chosen on its own row stays until the project changes.
	form.Move(1)
	form.Cycle(-1)
	if form.Field != FieldProfile || form.Profiles[form.Profile] != "claude-work" {
		t.Fatalf("field = %v profile = %q", form.Field, form.Profiles[form.Profile])
	}

	form.Move(1)
	form.Cycle(-1) // from ASL's default back to the last harness
	form.Move(1)
	form.Cycle(1)
	form.Move(1)
	form.Cycle(1)
	opts := form.Options()
	if opts.Harness != "codex" || opts.ReviewHarness != "claude" || opts.CommitMode != planrun.CommitPerSession {
		t.Fatalf("options = %+v", opts)
	}
	got := form.Parameters()
	if got["harness"] != "codex" || got["review_harness"] != "claude" || got["commit_mode"] != "per-session" || got["profile"] != "claude-work" {
		t.Fatalf("parameters = %v", got)
	}

	form.Move(1)
	if form.Field != FieldProject {
		t.Fatalf("moving past the last row landed on %v", form.Field)
	}
}

func TestChangingAChoiceClearsWhatWasSaidAboutTheLastOne(t *testing.T) {
	form := testForm()
	form.Readiness = &planrun.Readiness{State: planrun.StateNotReady}
	form.Notice, form.Warn = "not ready", true
	form.Move(2)
	if form.Readiness == nil {
		t.Fatal("moving between rows dropped the findings")
	}
	form.Cycle(1)
	if form.Readiness != nil || form.Notice != "" || form.Warn {
		t.Fatalf("a changed choice kept the old answer: %+v", form)
	}
}

// TestRenderFitsItsWidthAndShowsEveryFinding draws the tallest form at a
// wide and a narrow terminal: no line is wider than the box, and what ASL
// said is all there.
func TestRenderFitsItsWidthAndShowsEveryFinding(t *testing.T) {
	form := testForm()
	form.Move(2)
	form.Cycle(1)
	form.Notice, form.Warn = "The plan is not ready to run: preflight reported 1 red and 1 yellow.", true
	form.Readiness = &planrun.Readiness{State: planrun.StateNotReady, Title: "Fix the things", Findings: []planrun.Finding{
		{Severity: "red", Category: "plan-structure", Message: "session-3.md is missing"},
		{Severity: "yellow", Category: "manifest-readiness", Message: "plan readiness state is 'reviewed'", Detail: "run preflight to advance to loop-ready when checks pass"},
	}}
	for _, terminal := range []int{120, 48} {
		rendered := ansi.Strip(Render(form, terminal))
		for _, line := range strings.Split(rendered, "\n") {
			if got := layout.Width(line); got > Width(terminal) {
				t.Fatalf("at %d columns a line is %d wide, want at most %d:\n%s", terminal, got, Width(terminal), rendered)
			}
		}
		flat := strings.Join(strings.Fields(strings.NewReplacer("│", " ", "\n", " ").Replace(rendered)), " ")
		for _, want := range []string{"Run Fix the things", "in /home/me/projects/api", "‹ claude ›", "plan-structure: session-3.md is missing", "loop-ready when checks pass)", "--harness claude"} {
			if !strings.Contains(flat, want) {
				t.Fatalf("at %d columns the form does not say %q:\n%s", terminal, want, rendered)
			}
		}
	}
}

func TestWrapWordsBreaksAtSpacesAndInsideOnlyALongWord(t *testing.T) {
	got := wrapWords("asl run /a/very/long/path/that/cannot/fit --harness codex", 16)
	for _, line := range got {
		if layout.Width(line) > 16 {
			t.Fatalf("line %q is wider than 16: %q", line, got)
		}
	}
	// The path is the only word cut, and the words after it share its last line.
	if got[0] != "asl run" || got[1] != "/a/very/long/pat" || got[len(got)-1] != "codex" || len(got) != 5 {
		t.Fatalf("lines = %q", got)
	}
	if got := wrapWords("", 10); len(got) != 1 || got[0] != "" {
		t.Fatalf("empty text = %q", got)
	}
}
