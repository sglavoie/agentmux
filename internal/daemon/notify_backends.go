package daemon

// macOS notification backends: osascript (fallback) and alerter (preferred).

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
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

// alerterProtocolTimeout is sent to alerter so a notification that receives
// no interaction still exits successfully with @TIMEOUT. The process context
// gets a bounded grace period beyond that protocol timeout. Notification
// Center can delay delivery enough that alerter's own countdown starts several
// seconds after process launch, so a one-minute grace avoids racing the
// backend's successful @TIMEOUT token while still bounding a genuinely stuck
// process.
const (
	alerterProtocolTimeout = 5 * time.Minute
	alerterTimeoutGrace    = time.Minute
	alerterDispatchTimeout = alerterProtocolTimeout + alerterTimeoutGrace
)

type deliveryOutcome string

const deliveryDelivered deliveryOutcome = "delivered"

type interactionOutcome string

const (
	interactionClicked     interactionOutcome = "clicked"
	interactionTimeout     interactionOutcome = "interaction_timeout"
	interactionDismissed   interactionOutcome = "dismissed"
	interactionReplied     interactionOutcome = "replied"
	interactionUnavailable interactionOutcome = "interaction_unavailable"
	interactionUnknown     interactionOutcome = "interaction_unknown"
)

// dispatchResult keeps successful delivery separate from the user's terminal
// interaction. Backends return an error when delivery itself fails; a
// successful result always records deliveryDelivered and an explicit
// interaction value.
type dispatchResult struct {
	delivery    deliveryOutcome
	interaction interactionOutcome
}

func deliveredResult(interaction interactionOutcome) dispatchResult {
	return dispatchResult{delivery: deliveryDelivered, interaction: interaction}
}

// dispatchFn is the signature each backend implements. The result reports
// both successful delivery and the terminal interaction (when available).
// windowID and account identify the focus target for click handling: a
// positive windowID maps to a real Kitty window; a non-empty account is
// used when windowID is synthetic (e.g. hook-silence sweeps). Backends
// ignore both parameters — the Notifier's focusForClick uses them after
// the backend returns.
type dispatchFn func(ctx context.Context, windowID int, account, title, subtitle string) (dispatchResult, error)

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

func runOsascript(ctx context.Context, _ int, _ string, title, subtitle string) (dispatchResult, error) {
	script := fmt.Sprintf(
		`display notification "" with title %s subtitle %s`,
		quoteAppleScript(title), quoteAppleScript(subtitle),
	)
	if err := exec.CommandContext(ctx, "osascript", "-e", script).Run(); err != nil {
		return dispatchResult{}, err
	}
	return deliveredResult(interactionUnavailable), nil
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
	timeoutSeconds := strconv.FormatInt(int64(alerterProtocolTimeout/time.Second), 10)
	return []string{"--title", title, "--subtitle", subtitle, "--message", "", "--timeout", timeoutSeconds}
}

func runAlerter(ctx context.Context, _ int, _ string, title, subtitle string) (dispatchResult, error) {
	out, err := exec.CommandContext(ctx, "alerter", alerterArgs(title, subtitle)...).Output()
	if err != nil {
		return dispatchResult{}, err
	}
	return parseAlerterOutput(out), nil
}

// parseAlerterOutput reports the terminal delivery and interaction outcome.
// alerter prints exactly one of @CONTENTCLICKED, @ACTIONCLICKED, @CLOSED,
// @TIMEOUT, @REPLIED, or @CONTENTCLOSED on stdout when it exits. Unknown or
// empty successful output remains a delivered notification but never requests
// focus.
func parseAlerterOutput(stdout []byte) dispatchResult {
	line := strings.TrimSpace(string(stdout))
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	switch line {
	case "@CONTENTCLICKED", "@ACTIONCLICKED":
		return deliveredResult(interactionClicked)
	case "@TIMEOUT":
		return deliveredResult(interactionTimeout)
	case "@CLOSED", "@CONTENTCLOSED":
		return deliveredResult(interactionDismissed)
	case "@REPLIED":
		return deliveredResult(interactionReplied)
	default:
		return deliveredResult(interactionUnknown)
	}
}
