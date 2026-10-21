//go:build tuitest

package tui

// Dashboard tests that reach the model through the scenario harness (its
// drivers, fixtures, or modal registry), so they build only with the harness:
// `go test -tags tuitest`.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/chrome"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

func TestFeedbackWrapsAndScrollsLongDiagnostic(t *testing.T) {
	t.Parallel()

	m := testModel()
	m.width, m.height, m.mode = 60, 18, ModeFeedback
	m.pushToast(Toast{Severity: toastError, Text: strings.Repeat("a long diagnostic ", 80) + "final detail"})
	first := renderFeedbackBox(m)
	next, _ := m.handleFeedbackKey(keyPress("G"))
	m = next.(Model)
	last := renderFeedbackBox(m)
	if first == last || !strings.Contains(strings.Join(feedbackLines(m), " "), "final detail") {
		t.Fatal("diagnostic cannot be read")
	}
	if modalBoxRenderer(ModeFeedback) == nil || !modalUsesResponsiveWidth(ModeFeedback) {
		t.Fatal("feedback missing from layout checks")
	}
}

// TestFooterHintASLRunsMixedAvailability covers the ASL Runs palette, whose
// availability is answered by the bridge rather than by a Model field: the
// fixture's row-verify permits stop and refuses abort, so one render must carry
// both styles at once.
func TestFooterHintASLRunsMixedAvailability(t *testing.T) {
	t.Parallel()

	d, _ := enterASLRunsDriver(t)
	selectASLRun(t, d, "row-verify")
	m := d.Model()
	m.width = 200

	if !aslStopAvailable(m) || aslAbortAvailable(m) {
		t.Fatalf("fixture changed: stop available = %v, abort available = %v, want true/false",
			aslStopAvailable(m), aslAbortAvailable(m))
	}

	lines := footerHintLines(m, aslRunsFooterPalette, footerHintMaxLines)
	raw := strings.Join(lines, "\n")
	if !strings.Contains(raw, chrome.Binding("s", "stop")) {
		t.Fatalf("permitted stop is not rendered in the available style: %q", hintText(lines))
	}
	if !strings.Contains(raw, chrome.UnavailableBinding(keyASLAbort, "abort")) {
		t.Fatalf("refused abort is not rendered in the muted style: %q", hintText(lines))
	}

	zero := footerHintLines(Model{width: 200}, aslRunsFooterPalette, footerHintMaxLines)
	if got, want := hintText(lines), hintText(zero); got != want {
		t.Fatalf("bridge availability changed the ASL Runs text\n got %q\nwant %q", got, want)
	}
}

func TestHealthModeIsRegisteredAcrossDispatch(t *testing.T) {
	t.Parallel()

	m := healthModel(t)
	m.openHealth(ModeList)

	// Key dispatch reaches the overlay's own handler rather than the list's.
	next, _ := m.Update(tea.KeyPressMsg{Code: 'j'})
	if got := next.(Model).healthOffset; got == 0 && len(dashboard.HealthLines(healthInput(m))) > healthVisibleHeight(m) {
		t.Fatal("j did not reach handleHealthKey through the mode dispatcher")
	}

	if got := ModeHealth.String(); got != "health" {
		t.Fatalf("uitest mode name = %q, want %q", got, "health")
	}
	if modalBoxRenderer(ModeHealth) == nil {
		t.Fatal("modal-fit detection has no renderer for ModeHealth")
	}
	if !modalUsesResponsiveWidth(ModeHealth) {
		t.Fatal("ModeHealth is not exempt from the clipping check it satisfies by wrapping")
	}
	if got := fallbackStatusText(m); got == "" {
		t.Fatal("status bar has no fallback text for ModeHealth")
	}

	footer := stripAnsi(renderFooter(m))
	for _, want := range []string{"j/k", "esc/q"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer missing %q:\n%s", want, footer)
		}
	}
}

// `a` means actions everywhere else, so the Runs view no longer spends it on
// abort; X (the actions menu's End Session letter) asks for the abort instead.
func TestASLRunsAbortMovedOffA(t *testing.T) {
	d, _ := enterASLRunsDriver(t)
	selectASLRun(t, d, "row-reply")
	if err := d.Key("a"); err != nil {
		t.Fatalf("pressing a: %v", err)
	}
	if m := d.Model(); m.mode != ModeASLRuns {
		t.Fatalf("a in Runs opened mode %v, want it inert", m.mode)
	}
	if err := d.Key("X"); err != nil {
		t.Fatalf("pressing X: %v", err)
	}
	if m := d.Model(); m.mode != ModeConfirm {
		t.Fatalf("X in Runs = mode %v, want the abort confirmation", m.mode)
	}
}
