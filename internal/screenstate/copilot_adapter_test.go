package screenstate

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/harness"
)

// This file is the half of "run the fixture harness over the new files" that a
// capture alone does not buy. There is no discovery anywhere in this package:
// fixture_test.go is a reader and every fingerprint case names its captures in a
// literal, so a file dropped into testdata/ with no test case beside it is read
// by nothing and passes the suite silently — which is exactly the failure mode
// the Copilot adapter was held back for. Every committed copilot_*.txt is named
// in the table below.
//
// The captures were taken with `kitten @ get-text --extent=screen`, the same
// call and the same extent the daemon reads panes with (internal/kitty:
// GetText(match, "screen")), against GitHub Copilot CLI 1.0.82 driven live in
// Kitty. Naming the build is the same precaution codex_adapter_test.go records
// twice over: when a harness release moves the copy, the fixture that has to be
// re-taken is identified by the version it came from.

// copilotObservation classifies through the registry rather than the concrete
// type, so a broken For() registration fails these tests too. It mirrors
// codexObservation.
func copilotObservation(t *testing.T, screen string) Observation {
	t.Helper()

	adapter, ok := For(harness.Copilot)
	if !ok {
		t.Fatal(`For("copilot") returned no adapter`)
	}
	return adapter.Classify(screen)
}

// TestCopilotCaptureContract is the per-capture expectation table: what the
// installed pattern set makes of each real Copilot screen.
//
// The two rows the session that took these captures exists for are
// "queued input" and "interrupted". The first must produce VetoComposerBusy, or
// text a human typed and did not send would not stop a settle. The second must
// not read as Working — the same trap codex_background_test.go records having
// been hit once already for Codex.
//
// Read the Vetoes column against the State column rather than on its own. Half
// these screens are veto-FREE and still Unknown, which is not a gap: this
// adapter has no Terminal branch at all (see copilot.go), so a screen with
// nothing to object to is still not a screen that can settle anything.
func TestCopilotCaptureContract(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		state   State
		vetoes  Veto
		note    string
	}{
		{
			name: "a live turn is refused on both working signals",
			// The wording-independent footer veto and the interrupt affordance
			// agree here. They are kept as separate clauses because the two
			// screens below show each one surviving the other's absence.
			fixture: "copilot_working_t0.txt",
			state:   Working, vetoes: VetoLiveStatusLine | VetoInterruptHint,
		},
		{
			name:    "the same pane 3s later is still refused",
			fixture: "copilot_working_t1.txt",
			state:   Working, vetoes: VetoLiveStatusLine | VetoInterruptHint,
		},
		{
			name:    "a live turn during a different session is refused the same way",
			fixture: "copilot_working.txt",
			state:   Working, vetoes: VetoLiveStatusLine | VetoInterruptHint,
		},
		{
			name: "the solid spinner frame is still a status line",
			// ● is the LAST frame of Copilot's ○ ◎ ◉ ● spinner and also the
			// bullet it prefixes ordinary output with. This capture is why the
			// glyph cannot be the discriminator; see
			// TestCopilotContentBulletsAreNotStatusLines for the other side.
			fixture: "copilot_working_solid_spinner.txt",
			state:   Working, vetoes: VetoLiveStatusLine | VetoInterruptHint,
		},
		{
			name: "a cancelling turn is held by the status line alone",
			// Copilot drops "esc interrupt" the moment a cancel is requested,
			// while the turn is still winding down. If working_status_line had
			// been keyed on the interrupt affordance — the obvious shortcut,
			// since every Working screen carries it — this pane would have read
			// as quiet while it was still running.
			fixture: "copilot_cancelling.txt",
			state:   Working, vetoes: VetoLiveStatusLine,
		},
		{
			name: "a finished turn objects to nothing and still cannot settle",
			// The headline result of the capture pass: Copilot renders NO
			// completion marker. A finished turn is its output plus the ordinary
			// footer, and is byte-identical in shape to an interrupted one. There
			// is nothing here a screen classifier could settle on, which is the
			// empirical backing for the refusal-only composition in copilot.go.
			fixture: "copilot_idle_completed.txt",
			state:   Unknown,
		},
		{
			name:    "the completed pair is read identically at t0",
			fixture: "copilot_idle_completed_t0.txt",
			state:   Unknown,
		},
		{
			name:    "and at t1",
			fixture: "copilot_idle_completed_t1.txt",
			state:   Unknown,
		},
		{
			name: "a fresh session with no completion marker is quiet",
			// Claude's quiescent tier has no Copilot counterpart: this screen is
			// indistinguishable to the adapter from the finished turn above.
			fixture: "copilot_idle_bare_composer.txt",
			state:   Unknown,
		},
		{
			name: "an interrupted turn does not read as working",
			// The capture is a turn stopped with ctrl+c mid-sentence. Its footer
			// has already returned to the idle hint bar, so nothing marks it as
			// interrupted at all — the only trace is prose that stops in the
			// middle of a word.
			fixture: "copilot_interrupted.txt",
			state:   Unknown,
		},
		{
			name: "queued input the human never sent raises composer_busy",
			// The contract row. The composer holds "and now do the other thing";
			// if this settled, a pane the operator is mid-thought on would be
			// reported as resting.
			fixture: "copilot_queued_input.txt",
			state:   Unknown, vetoes: VetoComposerBusy,
		},
		{
			name: "a tool-permission prompt is blocked",
			// The one reading that does real work in production: a Copilot pane
			// waiting on a human is a fact the JSONL transcript structurally
			// cannot carry, because the prompt is not a transcript event. The
			// modal covers the composer, so composer_busy rides along exactly as
			// it does on codex_prompt_command_approval.txt.
			fixture: "copilot_permission_prompt.txt",
			state:   Blocked, vetoes: VetoBlockingPrompt | VetoComposerBusy,
		},
		{
			name: "the folder-trust prompt is blocked the same way",
			// A second prompt KIND, and the reason blocking_prompt did not need
			// the [group] kind-split patterns_claude.toml grew: both prompts are
			// caught by the same two clauses.
			fixture: "copilot_trust_prompt.txt",
			state:   Blocked, vetoes: VetoBlockingPrompt | VetoComposerBusy,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := copilotObservation(t, readScreenFixture(t, tc.fixture))

			// The invariant first, because it is the one that makes the rest
			// safe: no Copilot screen may ever be settleable.
			if got.SettleableTerminal() {
				t.Fatalf("%s settled: state %s, tier %s, vetoes %s",
					tc.fixture, got.State, got.Tier, got.Vetoes)
			}
			if got.State != tc.state {
				t.Errorf("State = %s, want %s (vetoes %s)", got.State, tc.state, got.Vetoes)
			}
			if got.Vetoes != tc.vetoes {
				t.Errorf("Vetoes = %s, want %s", got.Vetoes, tc.vetoes)
			}
			if got.Tier != TierNone {
				t.Errorf("Tier = %s, want none; the Copilot screen is refusal-only", got.Tier)
			}
			if got.Fingerprint == 0 {
				t.Error("Fingerprint = 0, want the never-zero screen hash")
			}
		})
	}
}

// TestCopilotContentBulletsAreNotStatusLines certifies the provenance of the
// quiet captures: each one must actually CONTAIN the trap it exists to pose.
//
// Copilot prefixes ordinary assistant output with ●, the last frame of its own
// spinner, so "a spinner glyph followed by a word" — the pattern any reasonable
// author would write first — matches finished output. Since
// copilotLatestWorkingLine takes the LAST match on the screen, such a pattern
// raises VetoLiveStatusLine on a pane that stopped working minutes ago, and the
// refusal never clears. A capture pass that quietly replaced these files with
// bullet-free screens would leave the suite green while removing the only
// evidence the trap is real.
func TestCopilotContentBulletsAreNotStatusLines(t *testing.T) {
	patterns := embeddedCopilotPatterns(t)

	// The captures that hold the trap in view. A bullet is only on screen while
	// the output it prefixes is: copilot_idle_completed.txt and
	// copilot_interrupted.txt are quiet panes whose answers ran long enough to
	// scroll their bullets off the top, which is why they are asserted below for
	// the match but not for provenance.
	for _, name := range []string{
		"copilot_idle_completed_t0.txt",
		"copilot_idle_completed_t1.txt",
		"copilot_idle_bare_composer.txt",
	} {
		t.Run(name, func(t *testing.T) {
			screen := readScreenFixture(t, name)

			bullets := 0
			for _, line := range strings.Split(screen, "\n") {
				if strings.HasPrefix(strings.TrimLeft(line, " \t"), "●") {
					bullets++
				}
			}
			if bullets == 0 {
				t.Fatalf("%s carries no ● content bullet, so it no longer poses the "+
					"trap it was kept for", name)
			}
			if line := copilotLatestWorkingLine(patterns, screen); line != "" {
				t.Errorf("a content bullet was read as a live status line: %q", line)
			}
		})
	}

	// Every quiet capture, bullets on screen or not, must produce no status line
	// at all. This is the assertion that matters in production; the provenance
	// loop above only certifies that the trap is still represented.
	for _, name := range []string{
		"copilot_idle_completed.txt",
		"copilot_interrupted.txt",
	} {
		t.Run(name, func(t *testing.T) {
			screen := readScreenFixture(t, name)
			if line := copilotLatestWorkingLine(patterns, screen); line != "" {
				t.Errorf("a quiet pane matched a live status line: %q", line)
			}
		})
	}

	// The same glyph on a real working footer must still match, so the assertion
	// above is not passing by refusing to recognize ● at all.
	solid := readScreenFixture(t, "copilot_working_solid_spinner.txt")
	line := copilotLatestWorkingLine(patterns, solid)
	if line == "" {
		t.Fatal("copilot_working_solid_spinner.txt matched no status line; the ● spinner " +
			"frame is no longer recognized and every fourth tick of a live turn loses its veto")
	}
	if !strings.HasPrefix(strings.TrimLeft(line, " \t"), "●") {
		t.Errorf("matched %q, want the solid-bullet footer this capture was taken for", line)
	}
}

// TestCopilotSpinnerFramesAreAllCaptured certifies that every glyph in
// working_status_line's character class was observed rather than inferred from
// the other three.
//
// Copilot's spinner is a filling circle with four frames, and a footer holding
// any one of them is a live turn. A class written from a single capture would
// have missed three quarters of the ticks — the veto would fire intermittently,
// which is worse than not firing at all, because the pane would look settled
// only sometimes and the bug would be blamed on timing.
func TestCopilotSpinnerFramesAreAllCaptured(t *testing.T) {
	patterns := embeddedCopilotPatterns(t)

	frames := map[string]string{
		"○": "copilot_working.txt",
		"◎": "copilot_working_t0.txt",
		"◉": "copilot_working_t1.txt",
		"●": "copilot_working_solid_spinner.txt",
	}

	for frame, fixture := range frames {
		t.Run(frame, func(t *testing.T) {
			line := copilotLatestWorkingLine(patterns, readScreenFixture(t, fixture))
			if line == "" {
				t.Fatalf("%s matched no status line, so spinner frame %s is unverified", fixture, frame)
			}
			if got := strings.TrimLeft(line, " \t"); !strings.HasPrefix(got, frame) {
				t.Errorf("%s now shows %q, not the %s frame it was captured for", fixture, got, frame)
			}
		})
	}
}

// TestCopilotEchoedUserMessageIsNotAPrompt pins the false positive the modal
// frame in choice_line exists to exclude.
//
// Copilot echoes the human's own submitted message with a ❯ prefix and no frame
// ("  ❯ Run the shell command: rm -f notes.txt"), so an unframed choice-line
// pattern reads a message that merely begins "1. " as an open permission prompt
// and refuses every settle for that pane until it scrolls away.
func TestCopilotEchoedUserMessageIsNotAPrompt(t *testing.T) {
	patterns := embeddedCopilotPatterns(t)

	// The trap as Copilot really renders it, taken from copilot_working.txt.
	const echoed = "  ❯ 1. Yes, do the thing I asked about\n"
	if patterns.Match(copilotClauseChoiceLine, echoed, strings.ToLower(echoed)) {
		t.Error("an unframed ❯ line matched choice_line; a user message beginning " +
			`"1. " would be read as an open prompt`)
	}
	if got := copilotObservation(t, echoed); got.Vetoes&VetoBlockingPrompt != 0 {
		t.Errorf("echoed user message raised %s", got.Vetoes)
	}

	// A capture that really contains an echoed user message, to prove the line
	// above is the shape Copilot renders rather than one invented for the test.
	working := readScreenFixture(t, "copilot_working.txt")
	if !strings.Contains(working, "❯ Run the shell command") {
		t.Fatal("copilot_working.txt no longer contains an echoed user message; the " +
			"capture no longer demonstrates the unframed ❯ this test guards")
	}
	if got := copilotObservation(t, working); got.Vetoes&VetoBlockingPrompt != 0 {
		t.Errorf("copilot_working.txt raised a blocking veto with no prompt on screen: %s", got.Vetoes)
	}

	// And the framed form still vetoes, so the frame requirement did not simply
	// disable the clause.
	framed := readScreenFixture(t, "copilot_permission_prompt.txt")
	if !patterns.Match(copilotClauseChoiceLine, framed, strings.ToLower(framed)) {
		t.Error("the real permission prompt no longer matches choice_line")
	}
}

// TestCopilotComposerIsNotTheScrollbar pins the column-0 anchor in
// composer_line.
//
// Copilot draws its scrollbar track in the last column with the same ┃ glyph it
// draws the composer box with, so a blank row of scrollable output is spaces
// followed by ┃ — a line a leading-whitespace-tolerant pattern matches. Matching
// one would clear VetoComposerBusy on a screen whose composer is not visible at
// all, which is precisely what happens on the two prompt captures.
func TestCopilotComposerIsNotTheScrollbar(t *testing.T) {
	patterns := embeddedCopilotPatterns(t)

	for _, name := range []string{"copilot_permission_prompt.txt", "copilot_trust_prompt.txt"} {
		t.Run(name, func(t *testing.T) {
			screen := readScreenFixture(t, name)
			if patterns.Match(copilotClauseComposerLine, screen, strings.ToLower(screen)) {
				t.Error("a modal-covered screen matched composer_line; the scrollbar " +
					"column is being read as a ready composer")
			}
		})
	}

	// The capture that proves the risk is real rather than theoretical: an idle
	// screen whose output is long enough to scroll carries indented ┃ rows, and
	// its composer must be recognized on the flush-left one.
	idle := readScreenFixture(t, "copilot_idle_completed.txt")
	indented := 0
	for _, line := range strings.Split(idle, "\n") {
		if trimmed := strings.TrimLeft(line, " \t"); trimmed == "┃" && line != trimmed {
			indented++
		}
	}
	if indented == 0 {
		t.Fatal("copilot_idle_completed.txt has no indented ┃ scrollbar row left; it no " +
			"longer demonstrates the collision the column anchor resolves")
	}
	if !patterns.Match(copilotClauseComposerLine, idle, strings.ToLower(idle)) {
		t.Error("the flush-left composer on a scrolled idle screen was not matched")
	}
}

// TestCopilotInterruptHintIsNotThePromptFooter pins the one phrase the capture
// pass deliberately did NOT add to interrupt_hint.
//
// Copilot spells the live-turn affordance "esc interrupt" and the modal-prompt
// affordance "esc to cancel". Adding the latter to interrupt_hint — which the
// replaced provisional copy effectively did, carrying "ctrl+c to cancel" — would
// raise VetoInterruptHint on every prompt. That costs nothing today, since
// Blocked outranks Working and neither settles, but it would report a pane
// waiting on a human as a pane that is busy.
func TestCopilotInterruptHintIsNotThePromptFooter(t *testing.T) {
	for _, name := range []string{"copilot_permission_prompt.txt", "copilot_trust_prompt.txt"} {
		t.Run(name, func(t *testing.T) {
			screen := readScreenFixture(t, name)
			if !strings.Contains(strings.ToLower(screen), "esc to cancel") {
				t.Fatalf("%s no longer carries the prompt footer this test is about", name)
			}
			if got := copilotObservation(t, screen); got.Vetoes&VetoInterruptHint != 0 {
				t.Errorf("prompt footer raised the interrupt-hint veto: %s", got.Vetoes)
			}
		})
	}

	// The live spelling must still fire, so the assertions above are not passing
	// because interrupt_hint matches nothing at all.
	working := readScreenFixture(t, "copilot_working_t0.txt")
	if got := copilotObservation(t, working); got.Vetoes&VetoInterruptHint == 0 {
		t.Errorf("a live turn raised no interrupt-hint veto: %s", got.Vetoes)
	}
}
