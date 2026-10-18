package dashboard

import (
	"reflect"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
)

func TestRunningSessionDisplayTitleFollowsConfigRename(t *testing.T) {
	dir := t.TempDir()
	const oldTitle, newTitle = "↗ZoomFlow", "⤴ZoomFlow"
	for _, kind := range []runtimesession.BackendKind{runtimesession.BackendKitty, runtimesession.BackendTmux} {
		t.Run(string(kind), func(t *testing.T) {
			cfg := &config.Config{Projects: []config.Session{{Title: oldTitle, Profile: "personal", Directory: dir}}}
			discover := func() session.RunningSession {
				var rows []session.RunningSession
				if kind == runtimesession.BackendTmux {
					rows = session.RunningSessionsFromPanes(kind, []backend.Pane{{GroupTitle: oldTitle, Vars: map[string]string{
						"amx_profile": "personal", "amx_project": oldTitle, "amx_directory": dir,
					}}}, cfg)
				} else {
					rows, _ = session.ListFromOSWindowsWithConfig([]kitty.OSWindow{{Tabs: []kitty.Tab{{ID: 1, Title: oldTitle, Windows: []kitty.Window{{
						ID: 2, UserVars: kitty.UserVars{Profile: "personal", Project: oldTitle, AMXDirectory: dir, LaunchID: "launch-1"},
					}}}}}}, cfg)
				}
				if len(rows) != 1 {
					t.Fatalf("discovered %d sessions, want 1", len(rows))
				}
				return rows[0]
			}
			before := discover()
			if got := SessionDisplayTitle(before); got != oldTitle {
				t.Fatalf("initial title = %q", got)
			}
			cfg.Projects[0].Title = newTitle
			after := discover()
			if got := SessionDisplayTitle(after); got != newTitle {
				t.Fatalf("refreshed title = %q, want %q", got, newTitle)
			}
			if after.Key != before.Key || after.Base != before.Base || after.Project != before.Project {
				t.Fatal("display rename changed launch identity")
			}
			if got := ProjectLabel(after); got != newTitle {
				t.Fatalf("project filter label = %q, want %q", got, newTitle)
			}
			cfg.Projects = nil
			if got := SessionDisplayTitle(discover()); got != oldTitle {
				t.Fatalf("removed project fallback = %q, want %q", got, oldTitle)
			}
		})
	}
}

func TestRunningSessionDisplayTitleStripsLegacyPrefix(t *testing.T) {
	sess := session.RunningSession{Base: "W:api", Account: "work"}
	if got := SessionDisplayTitle(sess); got != "api" {
		t.Fatalf("SessionDisplayTitle() = %q, want %q", got, "api")
	}
}

func TestProjectLabelPrefersFamilyAndFallsBackToEffectiveProject(t *testing.T) {
	if got := ProjectLabel(session.RunningSession{FamilyLabel: "Repository", Project: "Configured"}); got != "Repository" {
		t.Fatalf("ProjectLabel() = %q, want family label", got)
	}
	if got := ProjectLabel(session.RunningSession{Project: "Configured"}); got != "Configured" {
		t.Fatalf("ProjectLabel() fallback = %q, want configured project", got)
	}
	if got := ProjectLabel(session.RunningSession{Base: "W:legacy", Account: "work"}); got != "legacy" {
		t.Fatalf("ProjectLabel() legacy fallback = %q, want %q", got, "legacy")
	}
}

func TestCanonicalProjectLabelsDedupesAndUsesBestMemberCasing(t *testing.T) {
	now := time.Now()
	rows := []session.RunningSession{
		{FamilyLabel: "api", State: "bolt", PresentationChangedAt: now.Add(-time.Minute)},
		{FamilyLabel: "Web", State: "key", PresentationChangedAt: now.Add(-time.Minute)},
		{FamilyLabel: "API", State: "error", PresentationChangedAt: now},
		{Project: "worker", State: "warning", PresentationChangedAt: now},
		{},
	}
	got := CanonicalProjectLabels(rows, nil, time.Minute)
	want := []string{"API", "Web", "worker"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CanonicalProjectLabels() = %v, want %v", got, want)
	}
}

func TestCanonicalProjectLabelsBreaksLeadTiesByFoldedLabel(t *testing.T) {
	rows := []session.RunningSession{
		{FamilyLabel: "Zulu", State: "bolt"},
		{FamilyLabel: "alpha", State: "bolt"},
	}
	if got := CanonicalProjectLabels(rows, nil, time.Minute); !reflect.DeepEqual(got, []string{"alpha", "Zulu"}) {
		t.Fatalf("CanonicalProjectLabels() = %v", got)
	}
}
