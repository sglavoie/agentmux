package session

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// shortColorRetries shrinks the retry tuning vars so failure-path tests
// finish in milliseconds rather than ~1.3 seconds of real time. Returns a
// cleanup func the caller must defer.
func shortColorRetries(t *testing.T, attempts int) func() {
	t.Helper()
	origDelay, origMul, origRetries := colorInitialDelay, colorBackoffMul, colorMaxRetries
	colorInitialDelay = 1 * time.Millisecond
	colorBackoffMul = 1.0
	colorMaxRetries = attempts
	return func() {
		colorInitialDelay = origDelay
		colorBackoffMul = origMul
		colorMaxRetries = origRetries
	}
}

// colorFailRunner serves a fixed ls response and fails set-tab-color on
// every call. All other kitten commands succeed with empty output.
type colorFailRunner struct {
	lsResponse  []byte
	setColorErr error
	setCalls    int
}

func (r *colorFailRunner) Run(_ string, args ...string) ([]byte, []byte, error) {
	for _, a := range args {
		switch a {
		case "ls":
			return r.lsResponse, nil, nil
		case "set-tab-color":
			r.setCalls++
			return nil, []byte("simulated kitten set-tab-color failure"), r.setColorErr
		}
	}
	return []byte{}, nil, nil
}

// TestApplyColorWithRetry_PropagatesSetTabColorFailure pins the new
// contract: when SetTabColor fails for every retry, the function returns
// a non-nil error wrapping the underlying failure. Previously the failure
// was swallowed and the caller had no signal that the color never landed.
func TestApplyColorWithRetry_PropagatesSetTabColorFailure(t *testing.T) {
	defer shortColorRetries(t, 3)()

	lsResp := []byte(`[{"id":1,"is_active":true,"tabs":[` +
		`{"id":100,"is_active":true,"title":"W:newproject","windows":[` +
		`{"id":101,"is_active":true,"title":"W:newproject"}` +
		`]}]}]`)
	runner := &colorFailRunner{
		lsResponse:  lsResp,
		setColorErr: errors.New("boom"),
	}
	client := kitty.NewClientWithSocket(runner, "")

	err := applyColorWithRetry(client, map[int]bool{}, "W:newproject", palette.Color{
		ActiveBg: "#000000", InactiveBg: "#ffffff",
	})
	if err == nil {
		t.Fatal("expected non-nil error after every set-tab-color attempt failed")
	}
	if !strings.Contains(err.Error(), "setting tab color") {
		t.Errorf("expected error to wrap SetTabColor failure, got %v", err)
	}
	if runner.setCalls != colorMaxRetries {
		t.Errorf("expected %d set-tab-color attempts (one per retry), got %d", colorMaxRetries, runner.setCalls)
	}
}

// TestApplyColorWithRetry_ReportsMissingTab pins the second failure mode:
// ListWindows succeeds but the freshly launched tab never shows up. We
// want the returned error to make that distinction clear so the daemon
// log explains *why* the color stuck (or didn't).
func TestApplyColorWithRetry_ReportsMissingTab(t *testing.T) {
	defer shortColorRetries(t, 2)()

	runner := &colorFailRunner{
		lsResponse: []byte(`[]`),
	}
	client := kitty.NewClientWithSocket(runner, "")

	err := applyColorWithRetry(client, map[int]bool{}, "W:absent", palette.Color{})
	if err == nil {
		t.Fatal("expected non-nil error when the tab never appears")
	}
	if !strings.Contains(err.Error(), "W:absent") || !strings.Contains(err.Error(), "not visible") {
		t.Errorf("expected error to name the missing tab, got %v", err)
	}
	if runner.setCalls != 0 {
		t.Errorf("expected zero set-tab-color attempts when tab is missing, got %d", runner.setCalls)
	}
}

// sequentialColorFailRunner serves a list of ls responses in order and
// fails set-tab-color on every call. Other commands succeed silently.
// Used to exercise the full Launch → applyColorWithRetry → slog.Warn path.
type sequentialColorFailRunner struct {
	lsResponses [][]byte
	lsCallIdx   int
	setCalls    int
}

func (r *sequentialColorFailRunner) Run(_ string, args ...string) ([]byte, []byte, error) {
	for _, a := range args {
		switch a {
		case "ls":
			if r.lsCallIdx < len(r.lsResponses) {
				resp := r.lsResponses[r.lsCallIdx]
				r.lsCallIdx++
				return resp, nil, nil
			}
			return r.lsResponses[len(r.lsResponses)-1], nil, nil
		case "set-tab-color":
			r.setCalls++
			return nil, []byte("simulated failure"), errors.New("set-tab-color failed")
		}
	}
	return []byte{}, nil, nil
}

// TestLaunch_LogsWarnWhenColorRetryFails verifies the end-to-end surface:
// when SetTabColor fails for every retry, Launch emits a slog.Warn so the
// failure shows up in `amx daemon start --debug` logs instead of being
// swallowed silently. The session itself still succeeds (Launch returns
// nil) because the color is cosmetic.
func TestLaunch_LogsWarnWhenColorRetryFails(t *testing.T) {
	defer shortColorRetries(t, 2)()

	emptyLS := []byte(`[]`)
	postLaunch := []byte(`[{"id":1,"is_active":true,"tabs":[` +
		`{"id":100,"is_active":true,"title":"W:newproject","windows":[` +
		`{"id":101,"is_active":true,"title":"W:newproject"}` +
		`]}]}]`)
	runner := &sequentialColorFailRunner{
		// ls calls: dedup-check, compute-insert, then color-retry attempts.
		lsResponses: [][]byte{emptyLS, emptyLS, postLaunch, postLaunch, postLaunch},
	}
	client := kitty.NewClientWithSocket(runner, "")

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha"},
		Accounts: []config.Account{
			{Name: "work", ConfigDir: "~/.config/claude-work", Color: "blue"},
		},
	}
	sess := config.Session{Title: "newproject", Account: "work", Directory: "/tmp/newproject"}

	if err := Launch(client, sess, cfg.Accounts[0], cfg); err != nil {
		t.Fatalf("Launch returned err=%v; the launch itself should succeed even when color fails", err)
	}

	out := buf.String()
	if !strings.Contains(out, "applying tab color after launch") {
		t.Errorf("expected Warn log about color application, got %q", out)
	}
	if !strings.Contains(out, "W:newproject") {
		t.Errorf("expected Warn log to include the tab title, got %q", out)
	}
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("expected log level=WARN, got %q", out)
	}
	if runner.setCalls != colorMaxRetries {
		t.Errorf("expected %d set-tab-color attempts, got %d", colorMaxRetries, runner.setCalls)
	}
}

// TestApplyColorWithRetry_NoSleepAfterFinalAttempt verifies that the retry
// loop returns immediately after the last failed attempt rather than burning
// one final backoff sleep. Prior behavior padded every failed launch with
// ~500 ms of wall time the caller could not reclaim, slowing both interactive
// launches and the test suite. Uses a deliberately long backoff to make the
// regression obvious if it returns: 3 retries × 100 ms (no growth) means the
// pre-fix loop would have slept ~300 ms while the fixed loop sleeps ~200 ms.
func TestApplyColorWithRetry_NoSleepAfterFinalAttempt(t *testing.T) {
	origDelay, origMul, origRetries := colorInitialDelay, colorBackoffMul, colorMaxRetries
	t.Cleanup(func() {
		colorInitialDelay = origDelay
		colorBackoffMul = origMul
		colorMaxRetries = origRetries
	})
	colorInitialDelay = 100 * time.Millisecond
	colorBackoffMul = 1.0
	colorMaxRetries = 3

	runner := &colorFailRunner{
		lsResponse: []byte(`[]`), // tab never appears, force every retry path
	}
	client := kitty.NewClientWithSocket(runner, "")

	start := time.Now()
	err := applyColorWithRetry(client, map[int]bool{}, "W:absent", palette.Color{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error when tab never appears")
	}
	// We sleep between attempts (N-1) but not after the last attempt:
	// 3 retries with 100 ms backoff = 2 sleeps = ~200 ms total.
	// Allow generous headroom (≤ 280 ms) for CI noise but tight enough to
	// catch a regression where every iteration sleeps (would be ~300 ms).
	if elapsed > 280*time.Millisecond {
		t.Errorf("retry loop slept too long: %v (should be ~200 ms with 3 retries × 100 ms backoff and no final sleep)", elapsed)
	}
}

// TestApplyColorWithRetry_ReturnsNilOnSuccess guards the happy path so the
// new return-error refactor doesn't accidentally start surfacing nil.err
// (e.g. by stamping lastErr before the success break).
func TestApplyColorWithRetry_ReturnsNilOnSuccess(t *testing.T) {
	defer shortColorRetries(t, 3)()

	lsResp := []byte(`[{"id":1,"is_active":true,"tabs":[` +
		`{"id":100,"is_active":true,"title":"W:newproject","windows":[` +
		`{"id":101,"is_active":true,"title":"W:newproject"}` +
		`]}]}]`)
	runner := &colorFailRunner{
		lsResponse:  lsResp,
		setColorErr: nil, // success: SetTabColor returns no error
	}
	client := kitty.NewClientWithSocket(runner, "")

	err := applyColorWithRetry(client, map[int]bool{}, "W:newproject", palette.Color{
		ActiveBg: "#000000", InactiveBg: "#ffffff",
	})
	if err != nil {
		t.Fatalf("expected nil error on success, got %v", err)
	}
	if runner.setCalls != 1 {
		t.Errorf("expected exactly one set-tab-color attempt on success, got %d", runner.setCalls)
	}
}
