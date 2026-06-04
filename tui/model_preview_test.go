package tui

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

func dashboardPreviewCfg() config.PreviewConfig {
	return config.PreviewConfig{
		Direction: "vertical",
		Ratio:     0.7,
		RefreshMs: 500,
		MinCols:   100,
		MinRows:   30,
	}
}

func TestTogglePreviewVerticalUsesEnabledFooterBudget(t *testing.T) {
	m := testModel()
	m.width = 141
	m.height = 33
	m.cfg.Defaults.Preview = dashboardPreviewCfg()

	updated, cmd := m.Update(keyPress("p"))
	got := updated.(Model)
	if !got.previewEnabled {
		t.Fatalf("expected preview to enable at 141x33 with min_rows=30; notice=%q", got.previewNotice)
	}
	if cmd == nil {
		t.Fatal("expected preview enable to return fetch/tick command")
	}
}

func TestTogglePreviewVerticalRejectsGenuinelyShortTerminal(t *testing.T) {
	m := testModel()
	m.width = 141
	m.height = 29
	m.cfg.Defaults.Preview = dashboardPreviewCfg()

	updated, cmd := m.Update(keyPress("p"))
	got := updated.(Model)
	if got.previewEnabled {
		t.Fatal("expected preview to stay disabled below min_rows")
	}
	if got.previewNotice != "preview unavailable: terminal too small" {
		t.Fatalf("preview notice = %q, want terminal-too-small notice", got.previewNotice)
	}
	if cmd != nil {
		t.Fatal("expected no command when preview is rejected")
	}
}

func TestTogglePreviewClearsStalePreviewNoticeOnSuccess(t *testing.T) {
	m := testModel()
	m.width = 141
	m.height = 33
	m.cfg.Defaults.Preview = dashboardPreviewCfg()
	m.previewNotice = "preview unavailable: terminal too small"
	m.previewNoticeExpires = time.Now().Add(time.Second)

	updated, _ := m.Update(keyPress("p"))
	got := updated.(Model)
	if !got.previewEnabled {
		t.Fatalf("expected stale notice not to block preview enable; notice=%q", got.previewNotice)
	}
	if !got.previewNoticeExpires.IsZero() || got.previewNotice != "" {
		t.Fatalf("expected stale notice to clear on successful enable, got notice=%q expires=%v",
			got.previewNotice, got.previewNoticeExpires)
	}
}
