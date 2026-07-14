package tui

// This file turns declarative JSON scenarios into driver actions, assertions,
// and a machine-readable result. It is the seam Session 6's hidden CLI plugs
// into: parse a scenario file, run it through the package-level driver (built
// from a built-in fixture or an isolated config), and get back a stable
// pass/fail object without scraping stderr. Parsing and validation are
// path-aware so malformed input fails with a useful message before any model is
// constructed; execution reuses the same runner and no-op Kitty client the rest
// of the harness relies on, so a scenario can never touch a live Kitty instance
// or the operator's real data.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

// UITestScenarioEnv supplies the isolation seam a config-backed scenario needs:
// a throwaway staging root, a setenv callback that applies the isolation env
// vars, and the fake Kitty windows the no-op client should report. Fixture-backed
// scenarios ignore every field.
type UITestScenarioEnv struct {
	Root    string
	Setenv  func(string, string) error
	Windows []kitty.OSWindow
}

// ParseUITestScenario decodes and validates a scenario from JSON bytes. Unknown
// fields are rejected so a typo like "fixtures" surfaces as an error rather than
// silently selecting nothing.
func ParseUITestScenario(data []byte) (UITestScenario, error) {
	var sc UITestScenario
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sc); err != nil {
		return UITestScenario{}, fmt.Errorf("uitest: parsing scenario: %w", err)
	}
	// A scenario file must hold exactly one top-level object. Decoding again must
	// reach EOF (past any trailing whitespace, which the decoder skips); anything
	// else means a second JSON value or stray content trails the object, which we
	// reject so a copy-paste mistake can't silently drop half the file.
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		if err == nil {
			return UITestScenario{}, fmt.Errorf("uitest: parsing scenario: unexpected trailing content after scenario object")
		}
		return UITestScenario{}, fmt.Errorf("uitest: parsing scenario: unexpected trailing content after scenario object: %w", err)
	}
	if err := ValidateUITestScenario(sc); err != nil {
		return UITestScenario{}, err
	}
	return sc, nil
}

// LoadUITestScenario reads and parses a scenario file, wrapping any error with
// the path so CLI callers report which file was bad.
func LoadUITestScenario(path string) (UITestScenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return UITestScenario{}, fmt.Errorf("uitest: reading scenario %s: %w", path, err)
	}
	sc, err := ParseUITestScenario(data)
	if err != nil {
		return UITestScenario{}, fmt.Errorf("%s: %w", path, err)
	}
	return sc, nil
}

// ValidateUITestScenario rejects ambiguous or malformed scenarios: a missing or
// doubled source, a non-positive size, an unknown/incomplete action, or an
// unknown/incomplete assertion. Errors are path-aware down to the offending
// action or assertion index.
func ValidateUITestScenario(sc UITestScenario) error {
	where := "uitest: scenario"
	if sc.Name != "" {
		where = fmt.Sprintf("uitest: scenario %q", sc.Name)
	}

	switch {
	case sc.Fixture != "" && sc.Config != "":
		return fmt.Errorf("%s: set only one of fixture or config, not both", where)
	case sc.Fixture == "" && sc.Config == "":
		return fmt.Errorf("%s: missing fixture or config source", where)
	}

	if sc.Size.Width <= 0 || sc.Size.Height <= 0 {
		return fmt.Errorf("%s: size must have positive width and height, got %dx%d", where, sc.Size.Width, sc.Size.Height)
	}

	for i, act := range sc.Actions {
		if err := validateUITestAction(act); err != nil {
			return fmt.Errorf("%s: actions[%d]: %w", where, i, err)
		}
	}

	modeNames := uitestModeNameSet()
	for i, a := range sc.Assertions {
		if err := validateUITestAssertion(a, modeNames); err != nil {
			return fmt.Errorf("%s: assertions[%d]: %w", where, i, err)
		}
	}
	return nil
}

// validateUITestAction checks one action's type and the fields that type reads.
func validateUITestAction(a UITestAction) error {
	if !a.Type.Valid() {
		return fmt.Errorf("unknown action type %q", a.Type)
	}
	switch a.Type {
	case UITestActionKey:
		if a.Key == "" {
			return fmt.Errorf("key action requires a non-empty key")
		}
		if _, err := ParseUITestKey(a.Key); err != nil {
			return err
		}
	case UITestActionText:
		if a.Text == "" {
			return fmt.Errorf("text action requires non-empty text")
		}
	case UITestActionResize:
		if a.Width <= 0 || a.Height <= 0 {
			return fmt.Errorf("resize action requires positive width and height, got %dx%d", a.Width, a.Height)
		}
	case UITestActionTick:
		if a.Count < 0 {
			return fmt.Errorf("tick action count must not be negative")
		}
	}
	return nil
}

// validateUITestAssertion checks one assertion's type and required fields.
func validateUITestAssertion(a UITestAssertion, modeNames map[string]bool) error {
	if !a.Type.Valid() {
		return fmt.Errorf("unknown assertion type %q", a.Type)
	}
	switch a.Type {
	case UITestAssertContainsText, UITestAssertExcludesText:
		if a.Text == "" {
			return fmt.Errorf("%s assertion requires non-empty text", a.Type)
		}
	case UITestAssertModeEquals:
		if a.Mode == "" {
			return fmt.Errorf("mode_equals assertion requires a mode")
		}
		if !modeNames[a.Mode] {
			return fmt.Errorf("mode_equals assertion has unknown mode %q", a.Mode)
		}
	case UITestAssertSelectedTitle, UITestAssertSelectedProfile:
		if a.Value == "" {
			return fmt.Errorf("%s assertion requires a value", a.Type)
		}
	case UITestAssertExcludesLayoutIssue:
		if a.Value == "" {
			return fmt.Errorf("excludes_layout_issue assertion requires a value (the issue type)")
		}
		if !uitestKnownLayoutIssueType(a.Value) {
			return fmt.Errorf("excludes_layout_issue assertion has unknown issue type %q", a.Value)
		}
	case UITestAssertKittyCalls:
		if a.Op == "" {
			return fmt.Errorf("kitty_calls assertion requires an op")
		}
		if a.Count != nil && *a.Count < 0 {
			return fmt.Errorf("kitty_calls assertion count must not be negative")
		}
	case UITestAssertCursorEquals:
		if a.Cursor < 0 {
			return fmt.Errorf("cursor_equals assertion cursor must not be negative")
		}
	}
	return nil
}

// RunUITestScenario validates the scenario, builds a driver from its fixture or
// isolated config, applies each action, and evaluates every assertion against
// the final state. It returns a structured result whose OK flag and ordered
// Failures reflect the assertions in scenario order. A validation error, an
// unknown fixture, an isolated-config staging failure, or an action that fails
// to apply is returned as an error rather than an assertion failure, so callers
// (Session 6's CLI) can distinguish invalid input from a genuine regression.
func RunUITestScenario(sc UITestScenario, env UITestScenarioEnv) (UITestScenarioResult, error) {
	if err := ValidateUITestScenario(sc); err != nil {
		return UITestScenarioResult{}, err
	}

	d, client, err := scenarioDriver(sc, env)
	if err != nil {
		return UITestScenarioResult{}, err
	}

	obs, err := runUITestActions(d, sc.Actions)
	if err != nil {
		return UITestScenarioResult{}, err
	}

	final := obs[len(obs)-1]
	failures := evaluateUITestAssertions(sc, obs, d.Model(), client)

	result := UITestScenarioResult{
		OK:               len(failures) == 0,
		Scenario:         sc.Name,
		Failures:         failures,
		FinalObservation: final,
	}
	if sc.IncludeObservations {
		result.Observations = obs
	}
	return result, nil
}

// scenarioDriver resolves a scenario's source into a live driver plus the
// recording client it drives. Fixture sources build directly; config sources
// stage an isolated environment under env.Root using env.Setenv so no real user
// data is touched.
func scenarioDriver(sc UITestScenario, env UITestScenarioEnv) (*UITestDriver, *UITestKittyClient, error) {
	switch {
	case sc.Fixture != "":
		fx, err := UITestFixtureByName(sc.Fixture)
		if err != nil {
			return nil, nil, err
		}
		d, client := NewUITestFixtureDriver(fx, sc.Size)
		return d, client, nil
	case sc.Config != "":
		if env.Root == "" {
			return nil, nil, fmt.Errorf("uitest: scenario %q: config source requires a staging root", sc.Name)
		}
		if env.Setenv == nil {
			return nil, nil, fmt.Errorf("uitest: scenario %q: config source requires a setenv callback", sc.Name)
		}
		d, client, _, err := NewUITestConfigDriver(env.Root, sc.Config, env.Windows, sc.Size, env.Setenv)
		if err != nil {
			return nil, nil, err
		}
		return d, client, nil
	default:
		// Unreachable after validation, but kept so this function is safe to
		// call independently.
		return nil, nil, fmt.Errorf("uitest: scenario %q: missing fixture or config source", sc.Name)
	}
}

// evaluateUITestAssertions runs every assertion in order and collects the
// failures, preserving scenario order for deterministic output.
func evaluateUITestAssertions(sc UITestScenario, obs []UITestObservation, m Model, client *UITestKittyClient) []UITestAssertionFailure {
	final := obs[len(obs)-1]
	var failures []UITestAssertionFailure
	for _, a := range sc.Assertions {
		if f := evaluateUITestAssertion(a, final, m, client); f != nil {
			failures = append(failures, *f)
		}
	}
	return failures
}

// evaluateUITestAssertion checks one assertion against the final observation,
// model, and recording client. It returns nil when the assertion holds, or a
// populated failure describing the mismatch.
func evaluateUITestAssertion(a UITestAssertion, final UITestObservation, m Model, client *UITestKittyClient) *UITestAssertionFailure {
	switch a.Type {
	case UITestAssertNoLayoutIssues:
		if n := len(final.Screen.Issues); n > 0 {
			return uitestAssertionFail(a, fmt.Sprintf("found %d layout issue(s); first: %s", n, final.Screen.Issues[0].Type))
		}
	case UITestAssertExcludesLayoutIssue:
		want := UITestLayoutIssueType(a.Value)
		for _, iss := range final.Screen.Issues {
			if iss.Type == want {
				return uitestAssertionFail(a, fmt.Sprintf("screen unexpectedly has %s issue: %s", want, iss.Detail))
			}
		}
	case UITestAssertContainsText:
		if !strings.Contains(uitestScreenText(final), a.Text) {
			return uitestAssertionFail(a, fmt.Sprintf("screen does not contain %q", a.Text))
		}
	case UITestAssertExcludesText:
		if strings.Contains(uitestScreenText(final), a.Text) {
			return uitestAssertionFail(a, fmt.Sprintf("screen unexpectedly contains %q", a.Text))
		}
	case UITestAssertModeEquals:
		if final.Mode != a.Mode {
			return uitestAssertionFail(a, fmt.Sprintf("mode is %q, want %q", final.Mode, a.Mode))
		}
	case UITestAssertCursorEquals:
		if final.Cursor != a.Cursor {
			return uitestAssertionFail(a, fmt.Sprintf("cursor is %d, want %d", final.Cursor, a.Cursor))
		}
	case UITestAssertSelectedTitle:
		sess, ok := uitestSelectedSession(m)
		if !ok {
			return uitestAssertionFail(a, "no session is selected")
		}
		if got := sess.EffectiveProject(); got != a.Value {
			return uitestAssertionFail(a, fmt.Sprintf("selected title is %q, want %q", got, a.Value))
		}
	case UITestAssertSelectedProfile:
		sess, ok := uitestSelectedSession(m)
		if !ok {
			return uitestAssertionFail(a, "no session is selected")
		}
		if got := sess.EffectiveProfile(); got != a.Value {
			return uitestAssertionFail(a, fmt.Sprintf("selected profile is %q, want %q", got, a.Value))
		}
	case UITestAssertKittyCalls:
		got := len(client.CallsFor(a.Op))
		if a.Count != nil {
			if got != *a.Count {
				return uitestAssertionFail(a, fmt.Sprintf("recorded %d %s call(s), want %d", got, a.Op, *a.Count))
			}
		} else if got == 0 {
			return uitestAssertionFail(a, fmt.Sprintf("expected at least one %s call, got none", a.Op))
		}
	}
	return nil
}

// uitestAssertionFail builds a failure keyed by the assertion type.
func uitestAssertionFail(a UITestAssertion, msg string) *UITestAssertionFailure {
	return &UITestAssertionFailure{Kind: string(a.Type), Message: msg}
}

// uitestScreenText joins a screen's normalized lines so text assertions can run
// a single substring search across the whole view.
func uitestScreenText(obs UITestObservation) string {
	return strings.Join(obs.Screen.Lines, "\n")
}

// uitestSelectedSession returns the running session under the list cursor, which
// is also the target of the actions overlay. It reports false when the filtered
// list is empty or the cursor is out of range so selection assertions fail
// cleanly instead of panicking.
func uitestSelectedSession(m Model) (session.RunningSession, bool) {
	if m.cursor < 0 || m.cursor >= len(m.filtered) {
		return session.RunningSession{}, false
	}
	return m.filtered[m.cursor], true
}

// uitestModeNameSet builds the set of valid mode-name strings from the same
// mapping observations use, so scenario validation and observation output can
// never drift on mode names.
func uitestModeNameSet() map[string]bool {
	set := make(map[string]bool)
	for mode := ModeList; mode <= ModePlanView; mode++ {
		set[uitestModeName(mode)] = true
	}
	return set
}
