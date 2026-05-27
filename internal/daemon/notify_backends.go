package daemon

// macOS notification backends: osascript (fallback) and alerter (preferred).

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// notifyDispatchTimeout caps how long a single osascript dispatch can run.
// macOS notification backends can block under user-mode pressure (e.g.
// Notification Center unresponsive, UI server restart), so the goroutine
// wrapping every dispatch must enforce its own deadline rather than trusting
// the external process to return promptly. osascript returns as soon as the
// notification is posted; alerter blocks waiting for user interaction and uses
// the longer alerterDispatchTimeout instead.
const notifyDispatchTimeout = 5 * time.Second

// alerterDispatchTimeout bounds how long a single alerter invocation may
// wait for user interaction. alerter exits when the user clicks, dismisses,
// or times out; this cap is an upper bound for the worst case where the user
// never interacts and Notification Center swallows the alert. The
// exec.CommandContext underlying runAlerter still terminates the subprocess
// promptly on daemon shutdown via context cancellation.
const alerterDispatchTimeout = 5 * time.Minute

// dispatchFn is the signature each backend implements. The bool reports
// whether the user clicked the notification body (alerter only); osascript
// always returns false because its notifications cannot relay interaction.
// windowID and account identify the focus target for click handling: a
// positive windowID maps to a real Kitty window; a non-empty account is
// used when windowID is synthetic (e.g. hook-silence sweeps). Backends
// ignore both parameters — the Notifier's focusForClick uses them after
// the backend returns.
type dispatchFn func(ctx context.Context, windowID int, account, title, subtitle string) (bool, error)

// dispatcher bundles a backend's dispatch function with the timeout the
// runDispatch goroutine should use. Bundling avoids a mismatch where the
// alerter backend is selected but runs under the short osascript timeout.
type dispatcher struct {
	fn      dispatchFn
	timeout time.Duration
}

// selectDispatcher picks the macOS notification backend once at construction.
// alerter is preferred when available; otherwise osascript is used.
func selectDispatcher() dispatcher {
	if _, err := exec.LookPath("alerter"); err == nil {
		slog.Info("notifier: using alerter backend")
		return dispatcher{fn: runAlerter, timeout: alerterDispatchTimeout}
	}
	slog.Info("notifier: using osascript backend (alerter not found in PATH)")
	return dispatcher{fn: runOsascript, timeout: notifyDispatchTimeout}
}

func runOsascript(ctx context.Context, _ int, _ string, title, subtitle string) (bool, error) {
	script := fmt.Sprintf(
		`display notification "" with title %s subtitle %s`,
		quoteAppleScript(title), quoteAppleScript(subtitle),
	)
	return false, exec.CommandContext(ctx, "osascript", "-e", script).Run()
}

// quoteAppleScript wraps s in a double-quoted AppleScript string literal.
// AppleScript only recognizes \", \\, \n, \r, and \t as string escapes; Go's
// %q can emit \uXXXX / \xNN forms (e.g. for U+2028) that AppleScript treats
// as a syntax error, dropping the notification silently. This routine
// escapes only the characters AppleScript understands and passes every other
// rune through verbatim — including any non-ASCII printable characters
// commonly found in session titles.
func quoteAppleScript(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func alerterArgs(title, subtitle string) []string {
	return []string{"--title", title, "--subtitle", subtitle, "--message", ""}
}

func runAlerter(ctx context.Context, _ int, _ string, title, subtitle string) (bool, error) {
	out, err := exec.CommandContext(ctx, "alerter", alerterArgs(title, subtitle)...).Output()
	if err != nil {
		return false, err
	}
	return parseAlerterOutput(out), nil
}

// parseAlerterOutput reports whether the user clicked the notification body.
// alerter prints exactly one of @CONTENTCLICKED, @ACTIONCLICKED, @CLOSED,
// @TIMEOUT, @REPLIED, or @CONTENTCLOSED on stdout when it exits. Only the
// two "click" outcomes count as an explicit request to focus; everything
// else (dismissal, timeout, reply, empty/garbled output) is treated as no
// click so the caller does not steal focus on a passive dismissal.
func parseAlerterOutput(stdout []byte) bool {
	line := strings.TrimSpace(string(stdout))
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	switch line {
	case "@CONTENTCLICKED", "@ACTIONCLICKED":
		return true
	default:
		return false
	}
}
