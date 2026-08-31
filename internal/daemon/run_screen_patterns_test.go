package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/screenstate"
)

// screenstate.Load installs a package-level registry, so a test that starts the
// daemon against a temporary HOME leaves that registry pointing at the
// temporary override until something puts the embedded defaults back. Loading
// from a directory that holds no overrides is exactly that restore, and it goes
// through the same path production uses rather than reaching into the package.
func restoreEmbeddedScreenPatterns(t *testing.T) {
	t.Helper()
	empty := filepath.Join(t.TempDir(), "no-overrides")
	t.Cleanup(func() { screenstate.Load(empty) })
}

// startDaemonWithScreenPatternOverride starts a daemon whose config directory
// is a fresh temporary HOME, optionally writing an override file first, and
// returns the health response the running daemon publishes.
func startDaemonWithScreenPatternOverride(t *testing.T, name, contents string) Health {
	t.Helper()
	restoreEmbeddedScreenPatterns(t)

	home, err := os.MkdirTemp("/tmp", "amx-screen-patterns-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)

	if name != "" {
		dir := filepath.Join(config.DefaultConfigDir(), "screenstate")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".toml"), []byte(contents), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runErr := startPublishedDaemon(t, ctx, &config.Config{}, runDependencies{})

	health := NewClientWithSocket(SocketPath()).Health()
	if !health.Alive {
		t.Fatal("daemon did not answer health after startup publication")
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), testLivenessGrace)
	defer shutdownCancel()
	if err := NewClientWithSocket(SocketPath()).ShutdownAndWait(shutdownCtx); err != nil {
		cancel()
		t.Fatalf("ShutdownAndWait: %v", err)
	}
	waitForRunReturn(t, runErr)
	return health
}

// validClaudeOverride is a well-formed replacement pattern file. It is
// deliberately tiny: this test asserts that the loader accepted it and that the
// daemon came up, not that the classifier still classifies well with it.
const validClaudeOverride = `harness = "claude"

[regex]
composer_line = '^\s*[│┃]\s*>'
`

// brokenClaudeOverride fails regex compilation, which is the most likely way a
// hand-edited override goes wrong.
const brokenClaudeOverride = `harness = "claude"

[regex]
composer_line = '^\s*(unclosed'
`

func TestRun_ScreenPatternsCleanStartWithNoOverride(t *testing.T) {
	health := startDaemonWithScreenPatternOverride(t, "", "")
	if health.ScreenPatterns == nil {
		t.Fatal("health does not carry the screen-pattern load")
	}
	if len(health.ScreenPatterns.Warnings) != 0 {
		t.Errorf("a HOME with no overrides produced warnings: %+v", health.ScreenPatterns.Warnings)
	}
	for _, name := range []string{"claude", "codex", "contextlimit"} {
		if got := health.ScreenPatterns.Sources[name]; got != screenstate.SourceEmbedded {
			t.Errorf("source[%s] = %q; want %q", name, got, screenstate.SourceEmbedded)
		}
	}
}

func TestRun_ScreenPatternsCleanStartWithValidOverride(t *testing.T) {
	health := startDaemonWithScreenPatternOverride(t, "claude", validClaudeOverride)
	if health.ScreenPatterns == nil {
		t.Fatal("health does not carry the screen-pattern load")
	}
	want := filepath.Join(config.DefaultConfigDir(), "screenstate", "claude.toml")
	if got := health.ScreenPatterns.Sources["claude"]; got != want {
		t.Errorf("source[claude] = %q; want the override path %q", got, want)
	}
	if got := health.ScreenPatterns.Sources["codex"]; got != screenstate.SourceEmbedded {
		t.Errorf("an override for claude changed codex's source to %q", got)
	}
	if len(health.ScreenPatterns.Warnings) != 1 ||
		health.ScreenPatterns.Warnings[0].Kind != string(screenstate.WarnOverrideApplied) {
		t.Fatalf("warnings = %+v; want exactly one override_applied", health.ScreenPatterns.Warnings)
	}
	// The registry the probe goroutines read must carry the override too: the
	// health block reports what Load returned, this asserts what it installed.
	if set, ok := screenstate.PatternsFor("claude"); !ok || set.Source() != want {
		t.Errorf("installed claude source = %q (found=%t); want %q", set.Source(), ok, want)
	}
}

func TestRun_ScreenPatternsCleanStartWithBrokenOverrideWarns(t *testing.T) {
	health := startDaemonWithScreenPatternOverride(t, "claude", brokenClaudeOverride)
	if health.ScreenPatterns == nil {
		t.Fatal("health does not carry the screen-pattern load")
	}
	// Degraded but working: the daemon started, and it started with the
	// embedded copy rather than with a half-loaded one.
	if got := health.ScreenPatterns.Sources["claude"]; got != screenstate.SourceEmbedded {
		t.Errorf("source[claude] = %q; a broken override must leave the embedded copy in force", got)
	}
	if len(health.ScreenPatterns.Warnings) != 1 {
		t.Fatalf("warnings = %+v; want exactly one", health.ScreenPatterns.Warnings)
	}
	warning := health.ScreenPatterns.Warnings[0]
	if warning.Kind != string(screenstate.WarnOverrideInvalid) {
		t.Errorf("kind = %q; want %q", warning.Kind, screenstate.WarnOverrideInvalid)
	}
	if warning.Harness != "claude" {
		t.Errorf("harness = %q; want claude", warning.Harness)
	}
	if !strings.HasSuffix(warning.Path, filepath.Join("screenstate", "claude.toml")) {
		t.Errorf("path = %q; want the override file", warning.Path)
	}
	if warning.Detail == "" {
		t.Error("warning carries no detail; the operator cannot tell what to fix")
	}
	if set, ok := screenstate.PatternsFor("claude"); !ok || set.Source() != screenstate.SourceEmbedded {
		t.Errorf("installed claude source = %q (found=%t); want the embedded default", set.Source(), ok)
	}
}

// A Handler that never ran the startup load must publish no block at all, so a
// client can tell "this daemon predates the field" apart from "this daemon
// loaded no patterns".
func TestHealthOmitsScreenPatternsWhenLoadNeverRan(t *testing.T) {
	h := NewHandler(NewStateGuard(0), nil)
	if resp := h.handleHealth(); resp.ScreenPatterns != nil {
		t.Fatalf("ScreenPatterns = %+v; want nil", resp.ScreenPatterns)
	}
	h.SetScreenPatterns(nil)
	resp := h.handleHealth()
	if resp.ScreenPatterns == nil {
		t.Fatal("ScreenPatterns is nil after SetScreenPatterns")
	}
	if len(resp.ScreenPatterns.Warnings) != 0 {
		t.Errorf("warnings = %+v; want none", resp.ScreenPatterns.Warnings)
	}
	if len(resp.ScreenPatterns.Sources) == 0 {
		t.Error("sources are empty; the embedded defaults are installed at package init")
	}
}
