package dashboard

import (
	"reflect"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/session"
)

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
