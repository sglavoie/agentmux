package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

// TestRenderListActivityColumnAppears verifies that RenderList reserves the
// activity column when a row carries a busy anchor, and renders the elapsed
// stretch rather than the time since the last hook event.
func TestRenderListActivityColumnAppears(t *testing.T) {
	now := time.Now()
	in := ListInput{
		Cfg:   &config.Config{},
		Width: 80,
		Now:   now,
		Rows: []session.RunningSession{
			{WindowID: 1, Base: "api", State: "bolt", Account: "work", BusySince: now.Add(-(3*time.Minute + 7500*time.Millisecond))},
		},
		Watchdog: map[int]protocol.WatchdogState{
			1: {LastAction: "active", LastEventAt: now.Unix(), ThresholdSec: 600},
		},
	}
	out := stripAnsi(RenderList(in))
	if !strings.Contains(out, "3:07") {
		t.Errorf("expected \"3:07\" run timer in render; got %q", out)
	}
}

// TestRenderListActivityColumnHiddenByDefault asserts that with no busy rows
// the column is omitted entirely so rows recover their full directory budget.
// The "—" placeholder must NOT appear because the column itself is not
// reserved.
func TestRenderListActivityColumnHiddenByDefault(t *testing.T) {
	in := ListInput{
		Cfg:   &config.Config{},
		Width: 80,
		Now:   time.Now(),
		Rows: []session.RunningSession{
			{WindowID: 1, Base: "api", State: "bolt", Account: "work"},
		},
	}
	if anyRowHasActivity(in) {
		t.Fatal("baseline input with no busy rows should not reserve the activity column")
	}
	out := stripAnsi(RenderList(in))
	if strings.Contains(out, "—") {
		t.Errorf("dash placeholder must not appear when column is hidden; got %q", out)
	}
}

// TestRenderListActivityColumnSharedWidthAcrossRows asserts that once the
// column is reserved, an idle row renders the "—" placeholder and every row
// pays the same column width, so the directory column starts at one offset
// regardless of how wide the busiest row's timer is. The third row carries the
// widest representation FormatRunElapsed can produce, pinning that even a
// multi-month stretch cannot widen the column past a warning-decorated timer.
func TestRenderListActivityColumnSharedWidthAcrossRows(t *testing.T) {
	now := time.Now()
	in := ListInput{
		Cfg:   &config.Config{},
		Width: 80,
		Now:   now,
		Rows: []session.RunningSession{
			{WindowID: 1, Base: "api", State: "bolt", Account: "work", Directory: "~/p", BusySince: now.Add(-(12*time.Hour + 7*time.Minute + 30*time.Second))},
			{WindowID: 2, Base: "web", State: "bolt", Account: "work", Directory: "~/p"},
			{WindowID: 3, Base: "etl", State: "bolt", Account: "work", Directory: "~/p", BusySince: now.Add(-(99*24*time.Hour + 23*time.Hour))},
		},
		Watchdog: map[int]protocol.WatchdogState{
			1: {LastAction: "active", LastEventAt: now.Add(-9 * time.Minute).Unix(), ThresholdSec: 600},
		},
	}

	if got, want := activityColumnWidth(in.Rows, in.Watchdog, now), layout.Width("12h07 "+ageWarningGlyph)+1; got != want {
		t.Fatalf("activityColumnWidth = %d, want %d (the widest cell of the pass plus its trailing space)", got, want)
	}

	lines := strings.Split(strings.TrimRight(stripAnsi(RenderList(in)), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 rendered rows, got %d: %q", len(lines), lines)
	}
	busy, idle, long := lines[0], lines[1], lines[2]
	if !strings.Contains(busy, "12h07") {
		t.Errorf("expected \"12h07\" on the busy row; got %q", busy)
	}
	if !strings.Contains(idle, "—") {
		t.Errorf("expected dash placeholder on the idle row; got %q", idle)
	}
	if !strings.Contains(long, "99d23") {
		t.Errorf("expected \"99d23\" on the long-running row; got %q", long)
	}
	busyDir := strings.Index(busy, "~/p")
	for _, row := range []struct {
		name string
		line string
	}{{"idle", idle}, {"long-running", long}} {
		dir := strings.Index(row.line, "~/p")
		if busyDir < 0 || dir < 0 {
			t.Fatalf("directory column missing: busy=%q %s=%q", busy, row.name, row.line)
		}
		if got, want := layout.Width(row.line[:dir]), layout.Width(busy[:busyDir]); got != want {
			t.Errorf("directory starts at cell %d on the %s row and %d on the busy row; the activity column must share one width", got, row.name, want)
		}
	}
}
