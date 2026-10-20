package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/tui/commands"
)

func noopPlanCommands(m *Model) {
	m.planRefreshCommand = func(*plans.Index, *config.Config, int) tea.Cmd { return nil }
	m.planSearchCommand = func(int, *plans.Index, plans.Query) tea.Cmd { return nil }
}

// The scan notice is settled by its own outcome: it neither lingers once the
// refresh and its search report nor expires while the scan is still running.
func TestPlanScanToastSettlesOnCompletion(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	m := testModel()
	m.now = func() time.Time { return now }
	m.plansIndex = &plans.Index{}
	noopPlanCommands(&m)
	m.mode = ModePlanSearch
	updated, _ := m.startPlanSearchRefresh()
	m = updated.(Model)
	if toastByID(m, planScanToastID) == "" {
		t.Fatal("scan did not announce itself")
	}
	seq := m.planSearchSeq

	now = now.Add(time.Hour)
	m.sweepToasts()
	if toastByID(m, planScanToastID) == "" {
		t.Fatal("a scan still running lost its notice")
	}

	updated, _ = m.Update(plansRefreshDoneMsg{seq: seq})
	m = updated.(Model)
	if toastByID(m, planScanToastID) == "" {
		t.Fatal("notice cleared before the follow-up search reported")
	}
	updated, _ = m.Update(planSearchDoneMsg{seq: seq})
	m = updated.(Model)
	if got := toastByID(m, planScanToastID); got != "" {
		t.Fatalf("scan notice outlived the scan: %q", got)
	}
}

// A query typed during the scan supersedes the refresh's search; the scan's
// notice still ends when the refresh reports.
func TestPlanScanToastSettlesWhenSuperseded(t *testing.T) {
	m := testModel()
	m.plansIndex = &plans.Index{}
	noopPlanCommands(&m)
	m.mode = ModePlanSearch
	updated, _ := m.startPlanSearchRefresh()
	m = updated.(Model)
	seq := m.planSearchSeq
	m.planSearchSeq++
	updated, _ = m.Update(planSearchDoneMsg{seq: m.planSearchSeq})
	m = updated.(Model)
	if toastByID(m, planScanToastID) == "" {
		t.Fatal("a query's search cleared a scan still running")
	}
	updated, _ = m.Update(plansRefreshDoneMsg{seq: seq})
	m = updated.(Model)
	if got := toastByID(m, planScanToastID); got != "" {
		t.Fatalf("superseded scan kept its notice: %q", got)
	}
}

func TestResortOutcomeReplacesProgress(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	m := testModel()
	m.now = func() time.Time { return now }
	m.replaceToastByID(resortToastID, Toast{Severity: toastInfo, Text: "Resorting tabs…", Pending: true, Expires: now.Add(progressToastTTL)})
	updated, _ := m.Update(commands.ResortDoneMsg{})
	m = updated.(Model)
	if got := toastByID(m, resortToastID); got != "Tabs resorted" {
		t.Fatalf("resort channel = %q", got)
	}
	now = now.Add(time.Hour)
	m.sweepToasts()
	if len(m.toasts) != 0 {
		t.Fatalf("progress reappeared after the outcome expired: %+v", m.toasts)
	}
}

func TestExplicitExpiryIsHonouredForWarnings(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	m := testModel()
	m.now = func() time.Time { return now }
	m.pushPreviewNotice("preview unavailable: terminal too small")
	if len(m.toasts) != 1 || m.toasts[0].Persistent {
		t.Fatalf("explicit expiry overruled: %+v", m.toasts)
	}
	now = now.Add(previewNoticeTTL + time.Second)
	m.sweepToasts()
	if len(m.toasts) != 0 {
		t.Fatalf("preview notice never cleared: %+v", m.toasts)
	}
	m.pushToast(Toast{Severity: toastError, Text: "Delete failed"})
	if !m.toasts[0].Persistent {
		t.Fatal("an error with no deadline must stay retained")
	}
}
