package tui

import (
	"strings"
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
		t.Fatalf("expected preview to enable at 141x33 with min_rows=30; toasts=%+v", got.toasts)
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
	if len(got.toasts) != 1 || got.toasts[0].id != previewNoticeToastID {
		t.Fatalf("expected one addressable preview notice toast, got %+v", got.toasts)
	}
	if got.toasts[0].Text != "preview unavailable: terminal too small" {
		t.Fatalf("preview notice toast = %q, want terminal-too-small notice", got.toasts[0].Text)
	}
	if cmd == nil {
		t.Fatal("expected preview rejection to arm toast tick")
	}
	assertStatusBarContains(t, got, "preview unavailable: terminal too small")
	if strings.Contains(stripAnsi(renderFooter(got)), "preview unavailable") {
		t.Fatal("preview notice should render through the status bar, not the footer")
	}
}

func TestTogglePreviewClearsStalePreviewNoticeOnSuccess(t *testing.T) {
	m := testModel()
	m.width = 141
	m.height = 33
	m.cfg.Defaults.Preview = dashboardPreviewCfg()
	m.toasts = []Toast{{
		Severity: toastError,
		Text:     "preview unavailable: terminal too small",
		Expires:  time.Now().Add(time.Second),
		id:       previewNoticeToastID,
	}}

	updated, _ := m.Update(keyPress("p"))
	got := updated.(Model)
	if !got.previewEnabled {
		t.Fatalf("expected stale notice not to block preview enable; toasts=%+v", got.toasts)
	}
	for _, toast := range got.toasts {
		if toast.id == previewNoticeToastID {
			t.Fatalf("expected stale preview notice toast to clear on successful enable, got %+v", got.toasts)
		}
	}
}
