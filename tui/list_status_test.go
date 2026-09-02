package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

// TestRenderDeprecationBannerShownWhenDeprecationsPresent verifies the
// banner renders headline + hint when the config has at least one
// deprecated key and the user has not dismissed it.
func TestRenderDeprecationBannerShownWhenDeprecationsPresent(t *testing.T) {
	m := Model{
		width: 80,
		deprecations: []config.Deprecation{
			{Key: "accounts", Replacement: "profiles", Location: "[[accounts]]"},
		},
	}
	out := stripAnsi(renderDeprecationBanner(m))
	if !strings.Contains(out, "deprecated keys") {
		t.Errorf("expected headline; got %q", out)
	}
	if !strings.Contains(out, "accounts → profiles") {
		t.Errorf("expected migration arrow; got %q", out)
	}
	if !strings.Contains(out, "press d to dismiss") {
		t.Errorf("expected dismiss hint; got %q", out)
	}
}

// TestRenderDeprecationBannerHiddenWhenDismissed verifies that setting
// deprecationDismissed suppresses the banner even when deprecations are
// recorded — the dismissal is session-scoped, not config-scoped.
func TestRenderDeprecationBannerHiddenWhenDismissed(t *testing.T) {
	m := Model{
		width: 80,
		deprecations: []config.Deprecation{
			{Key: "accounts", Replacement: "profiles"},
		},
		deprecationDismissed: true,
	}
	if got := renderDeprecationBanner(m); got != "" {
		t.Errorf("expected empty banner after dismiss; got %q", got)
	}
}

// TestRenderDeprecationBannerHiddenWhenNoDeprecations covers the common
// case where the user is on a clean canonical config — no banner ever
// appears.
func TestRenderDeprecationBannerHiddenWhenNoDeprecations(t *testing.T) {
	m := Model{width: 80}
	if got := renderDeprecationBanner(m); got != "" {
		t.Errorf("expected empty banner with no deprecations; got %q", got)
	}
}

// TestRenderHealthPipsHiddenBeforeFirstCheck verifies that the pip strip
// is suppressed until at least one health refresh has completed so the
// header doesn't briefly mis-render as "all red" at startup.
func TestRenderHealthPipsHiddenBeforeFirstCheck(t *testing.T) {
	m := Model{width: 80}
	if got := renderHealthPips(m); got != "" {
		t.Errorf("expected no pips before first check; got %q", got)
	}
}

// TestRenderHealthPipsShowsBothPips verifies that after a health refresh
// both daemon and hooks pips render with their labels.
func TestRenderHealthPipsShowsBothPips(t *testing.T) {
	m := Model{
		width:                 80,
		daemonAlive:           true,
		daemonHealthCheckedAt: time.Now(),
		hookHealth:            HookHealth{InSync: []string{"work"}},
		hookHealthCheckedAt:   time.Now(),
	}
	out := stripAnsi(renderHealthPips(m))
	if !strings.Contains(out, "daemon") {
		t.Errorf("expected daemon label; got %q", out)
	}
	if !strings.Contains(out, "hooks") {
		t.Errorf("expected hooks label; got %q", out)
	}
	if !strings.Contains(out, healthPipGlyph) {
		t.Errorf("expected pip glyph; got %q", out)
	}
}

// TestRenderHealthHintsShownWhenDegraded verifies that the under-pip hint
// surfaces both a daemon-down and a hooks-drifted message when applicable,
// while staying silent when everything is healthy.
func TestRenderHealthHintsShownWhenDegraded(t *testing.T) {
	now := time.Now()
	m := Model{
		width:                 80,
		daemonAlive:           false,
		daemonHealthCheckedAt: now,
		hookHealth:            HookHealth{Drifted: []string{"work"}},
		hookHealthCheckedAt:   now,
	}
	out := stripAnsi(renderHealthHints(m))
	if !strings.Contains(out, "daemon down") {
		t.Errorf("expected daemon-down hint; got %q", out)
	}
	if !strings.Contains(out, "hooks drifted") {
		t.Errorf("expected hooks-drifted hint; got %q", out)
	}

	healthy := Model{
		width:                 80,
		daemonAlive:           true,
		daemonHealthCheckedAt: now,
		hookHealth:            HookHealth{InSync: []string{"work"}},
		hookHealthCheckedAt:   now,
	}
	if got := renderHealthHints(healthy); got != "" {
		t.Errorf("expected empty hints when healthy; got %q", got)
	}
}
