// This file is the scenario language: the JSON a scenario is written in, the
// vocabulary of actions and assertions it may use, the key-name grammar its
// keystrokes are spelled with, and the runner that executes one parsed
// scenario and reports what it observed.

package tui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/kitty/kittytest"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// This file turns declarative JSON scenarios into driver actions, assertions,
// and a machine-readable result. It is the seam Session 6's hidden CLI plugs
// into: parse a scenario file, run it through the package-level driver (built
// from a built-in fixture or an isolated config), and get back a stable
// pass/fail object without scraping stderr. Parsing and validation are
// path-aware so malformed input fails with a useful message before any model is
// constructed; execution reuses the same runner and no-op Kitty client the rest
// of the harness relies on, so a scenario can never touch a live Kitty instance
// or the operator's real data.

// UITestScenarioEnv supplies the isolation seam a config-backed scenario needs:
// a throwaway staging root, a setenv callback that applies the isolation env
// vars, and the fake Kitty windows the no-op client should report. Fixture-backed
// scenarios ignore every field.
type UITestScenarioEnv struct {
	Root    string
	Setenv  func(string, string) error
	Windows []kitty.OSWindow
}

// parseUITestScenario decodes and validates a scenario from JSON bytes. Unknown
// fields are rejected so a typo like "fixtures" surfaces as an error rather than
// silently selecting nothing.
func parseUITestScenario(data []byte) (UITestScenario, error) {
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
	if err := validateUITestScenario(sc); err != nil {
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
	sc, err := parseUITestScenario(data)
	if err != nil {
		return UITestScenario{}, fmt.Errorf("%s: %w", path, err)
	}
	return sc, nil
}

// validateUITestScenario rejects ambiguous or malformed scenarios: a missing or
// doubled source, a non-positive size, an unknown/incomplete action, or an
// unknown/incomplete assertion. Errors are path-aware down to the offending
// action or assertion index.
func validateUITestScenario(sc UITestScenario) error {
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

	commandIDs := uitestCommandIDSet()
	for i, act := range sc.Actions {
		if err := validateUITestAction(act, commandIDs); err != nil {
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
// commandIDs is the set of catalog IDs a command action may name; it is passed
// in rather than rebuilt per action because the whole scenario is checked
// against one catalog.
func validateUITestAction(a UITestAction, commandIDs map[string]bool) error {
	if !a.Type.Valid() {
		return fmt.Errorf("unknown action type %q", a.Type)
	}
	switch a.Type {
	case UITestActionKey:
		if a.Key == "" {
			return fmt.Errorf("key action requires a non-empty key")
		}
		if _, err := parseUITestKey(a.Key); err != nil {
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
	case UITestActionMouse:
		if a.X < 0 || a.Y < 0 {
			return fmt.Errorf("mouse action coordinates must not be negative, got %d,%d", a.X, a.Y)
		}
		switch a.Event {
		case UITestMouseLeft, UITestMouseWheelUp, UITestMouseWheelDown:
		default:
			return fmt.Errorf("mouse action has unknown event %q", a.Event)
		}
	case UITestActionCommand:
		if a.CommandID == "" {
			return fmt.Errorf("command action requires a non-empty command_id")
		}
		// A typo'd or retired ID is caught here rather than at run time,
		// because a scenario that names nothing would otherwise report a
		// missing palette row — which reads like a visibility bug in the
		// screen under test instead of a broken scenario file.
		if !commandIDs[a.CommandID] {
			return fmt.Errorf("command action has unknown command_id %q", a.CommandID)
		}
	}
	return nil
}

// uitestCommandIDSet is the set of stable IDs a command action may name: the
// dashboard catalog, which is the complete list of commands the palette can
// ever offer.
func uitestCommandIDSet() map[string]bool {
	catalog := commandCatalog()
	set := make(map[string]bool, len(catalog))
	for _, candidate := range catalog {
		set[string(candidate.ID)] = true
	}
	return set
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
	case UITestAssertSelectedAction:
		if a.Value == "" {
			return fmt.Errorf("selected_action assertion requires a value (the action id)")
		}
		if !uitestKnownMenuAction(a.Value) {
			return fmt.Errorf("selected_action assertion has unknown action id %q", a.Value)
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
	if err := validateUITestScenario(sc); err != nil {
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
func scenarioDriver(sc UITestScenario, env UITestScenarioEnv) (*uitestDriver, *kittytest.Client, error) {
	switch {
	case sc.Fixture != "":
		fx, err := uitestFixtureByName(sc.Fixture)
		if err != nil {
			return nil, nil, err
		}
		d, client := newUITestFixtureDriver(fx, sc.Size)
		return d, client, nil
	case sc.Config != "":
		if env.Root == "" {
			return nil, nil, fmt.Errorf("uitest: scenario %q: config source requires a staging root", sc.Name)
		}
		if env.Setenv == nil {
			return nil, nil, fmt.Errorf("uitest: scenario %q: config source requires a setenv callback", sc.Name)
		}
		d, client, _, err := newUITestConfigDriver(env.Root, sc.Config, env.Windows, sc.Size, env.Setenv)
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
func evaluateUITestAssertions(sc UITestScenario, obs []UITestObservation, m Model, client *kittytest.Client) []UITestAssertionFailure {
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
func evaluateUITestAssertion(a UITestAssertion, final UITestObservation, m Model, client *kittytest.Client) *UITestAssertionFailure {
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
	case UITestAssertSelectedAction:
		if final.Mode != uitestModeName(ModeActions) {
			return uitestAssertionFail(a, fmt.Sprintf("mode is %q, so no action is selected", final.Mode))
		}
		if final.SelectedAction != a.Value {
			return uitestAssertionFail(a, fmt.Sprintf("selected action is %q, want %q", final.SelectedAction, a.Value))
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

// uitestKnownMenuAction reports whether id names an action the menu actually
// offers. Validating it here means a scenario with a misspelled identity is
// rejected outright rather than failing later with a message that reads like a
// selection bug.
func uitestKnownMenuAction(id string) bool {
	for _, definition := range sessionactions.MenuDefinitions() {
		if string(definition.ID) == id {
			return true
		}
	}
	return false
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
// is also the target of the actions overlay. It reports false when the item list
// is empty, when the cursor is out of range, and when the selected item is not a
// session leaf, so selection assertions observe exactly what the row-targeted
// actions would resolve rather than a parallel notion of "the current row".
func uitestSelectedSession(m Model) (session.RunningSession, bool) {
	return m.selectedSession()
}

// uitestModeNameSet builds the set of valid mode-name strings from the same
// mapping observations use, so scenario validation and observation output can
// never drift on mode names.
func uitestModeNameSet() map[string]bool {
	set := make(map[string]bool)
	for mode := ModeList; mode <= ModePlanRun; mode++ {
		set[uitestModeName(mode)] = true
	}
	return set
}

// This file is the package-level entry point that turns a set of options plus a
// list of actions into a sequence of observations. It is the seam Session 4's
// scenario parser and Session 6's CLI plug into: prepare config and a client,
// describe the initial size and the steps, and get back one normalized
// observation per step. The runner owns no CLI, filesystem, or Kitty concerns —
// those are supplied by the caller through cfg and client.

// runUITest builds a driver from cfg and client at opts.Size, then applies each
// action in order. It returns one observation per step: index 0 is the initial
// render before any action, and index i+1 is the screen after actions[i]. The
// returned slice therefore always has len(actions)+1 entries on success. On the
// first action error the observations captured so far are returned alongside the
// error so callers can still inspect how far the run got.
func runUITest(cfg *config.Config, client kitty.KittyClient, opts UITestOptions, actions []UITestAction) ([]UITestObservation, error) {
	return runUITestActions(newUITestDriver(cfg, client, opts.Size), actions)
}

// RunUITestFixture builds a driver from the named built-in fixture and applies
// each action, returning one observation per step (index 0 is the initial
// render before any action). opts.Size overrides the fixture's default size
// when non-zero. An unknown fixture name returns a structured
// *uitestFixtureError before any driver is constructed.
func RunUITestFixture(name string, opts UITestOptions, actions []UITestAction) ([]UITestObservation, error) {
	fx, err := uitestFixtureByName(name)
	if err != nil {
		return nil, err
	}
	d, _ := newUITestFixtureDriver(fx, opts.Size)
	return runUITestActions(d, actions)
}

// runUITestModel is runUITest for callers that have already prepared a Model
// (for example, one with sessions preloaded). opts.Size sets the initial
// viewport; the model's prior width/height are overwritten by the driver's
// initial resize.
func runUITestModel(m Model, opts UITestOptions, actions []UITestAction) ([]UITestObservation, error) {
	return runUITestActions(newUITestDriverModel(m, opts.Size), actions)
}

// runUITestActions captures the initial observation, then applies each action
// and captures an observation after it. Shared by both public entry points.
func runUITestActions(d *uitestDriver, actions []UITestAction) (obs []UITestObservation, retErr error) {
	defer func() { retErr = errors.Join(retErr, d.Close()) }()
	if err := d.Err(); err != nil {
		return nil, err
	}
	obs = make([]UITestObservation, 0, len(actions)+1)
	obs = append(obs, d.Observe())
	for i, a := range actions {
		if err := d.Apply(a); err != nil {
			return obs, fmt.Errorf("uitest: action %d (%s): %w", i, a.Type, err)
		}
		obs = append(obs, d.Observe())
	}
	return obs, nil
}

// parseUITestKey converts a scenario key spec into a Bubble Tea key-press
// message. It mirrors the key-code choices used by the in-package test helper
// (see keyPress in model_test.go) so harness-driven key events behave exactly
// like production key handling once fed to Model.Update.
//
// Accepted specs:
//   - named keys: enter, esc, tab, shift+tab, backspace, space, up, down, left, right,
//     home, end, pgup, pgdown
//   - control chords: ctrl+<printable>, e.g. ctrl+c, ctrl+d
//   - any single printable rune, e.g. "j", "/", "?"
func parseUITestKey(spec string) (tea.KeyPressMsg, error) {
	if spec == "" {
		return tea.KeyPressMsg{}, fmt.Errorf("uitest: empty key spec")
	}

	if named, ok := namedUITestKey(spec); ok {
		return named, nil
	}

	if rest, ok := strings.CutPrefix(spec, "ctrl+"); ok {
		r, err := singleUITestRune(rest)
		if err != nil {
			return tea.KeyPressMsg{}, fmt.Errorf("uitest: invalid ctrl chord %q: %w", spec, err)
		}
		return tea.KeyPressMsg(tea.Key{Code: r, Mod: tea.ModCtrl}), nil
	}

	r, err := singleUITestRune(spec)
	if err != nil {
		return tea.KeyPressMsg{}, fmt.Errorf("uitest: unknown key %q: %w", spec, err)
	}
	return tea.KeyPressMsg(tea.Key{Code: r, Text: spec}), nil
}

// namedUITestKey resolves the fixed set of named keys the harness supports.
// Space is intentionally created without Text so its String() reports "space",
// matching how production key handling sees the spacebar.
func namedUITestKey(spec string) (tea.KeyPressMsg, bool) {
	switch spec {
	case "enter":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}), true
	case "esc":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}), true
	case "tab":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}), true
	case "shift+tab":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}), true
	case "backspace":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}), true
	case "space":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeySpace}), true
	case "up":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}), true
	case "down":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}), true
	case "left":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}), true
	case "right":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyRight}), true
	case "home":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}), true
	case "end":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd}), true
	case "pgup":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}), true
	case "pgdown":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}), true
	default:
		return tea.KeyPressMsg{}, false
	}
}

// singleUITestRune returns the sole rune of s, erroring when s is empty, holds
// invalid UTF-8, or carries more than one rune.
func singleUITestRune(s string) (rune, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError && size <= 1 {
		return 0, fmt.Errorf("invalid UTF-8")
	}
	if size != len(s) {
		return 0, fmt.Errorf("expected a single character, got %q", s)
	}
	return r, nil
}

// This file defines the pure, JSON-facing contract shared by the TUI test
// harness CLI, scenario files, and the in-process model driver. It carries no
// dependency on Cobra, the filesystem, or a live Kitty instance so later
// sessions can import it without pulling in setup machinery. Field names are
// deliberately small and explicit so the harness output stays reviewable.

// UITestSize is a terminal viewport measured in character cells.
type UITestSize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// UITestActionType enumerates the scenario step kinds the harness understands.
// Every one of them is an input an operator could have produced: keys, literal
// text, resizes, ticks, mouse events routed through the rendered View, and a
// command named by its stable catalog ID.
//
// The command action is deliberately NOT a way to call a model operation
// directly. It types the palette's own key, walks its cursor with the palette's
// own navigation keys, and presses enter on the row — so a scenario that names
// a command still fails when the palette will not open, when the row is hidden
// or disabled, or when enter stops running it. What it buys is independence
// from the *spelling* of a dashboard shortcut: a scenario whose subject is
// another screen says which capability took it there, not which letter did,
// and survives the key retiring underneath it.
type UITestActionType string

const (
	// UITestActionKey presses a single named or printable key (see uitest_key.go).
	UITestActionKey UITestActionType = "key"
	// UITestActionText types a run of printable characters one key at a time.
	UITestActionText UITestActionType = "text"
	// UITestActionResize changes the terminal viewport to Width x Height.
	UITestActionResize UITestActionType = "resize"
	// UITestActionTick advances the model's timer Count times.
	UITestActionTick UITestActionType = "tick"
	// UITestActionMouse routes a click or wheel event through the rendered View.
	UITestActionMouse UITestActionType = "mouse"
	// UITestActionCommand runs the catalog command named by CommandID through
	// the command palette, exactly as an operator reaching it by name would.
	UITestActionCommand UITestActionType = "command"
)

// Valid reports whether t is one of the recognized action kinds.
func (t UITestActionType) Valid() bool {
	switch t {
	case UITestActionKey, UITestActionText, UITestActionResize, UITestActionTick, UITestActionMouse, UITestActionCommand:
		return true
	default:
		return false
	}
}

// UITestAction is a single scenario step. Only the fields relevant to Type are
// populated; the rest stay zero so the JSON stays compact and reviewable.
type UITestAction struct {
	Type      UITestActionType `json:"type"`
	Key       string           `json:"key,omitempty"`
	Text      string           `json:"text,omitempty"`
	Width     int              `json:"width,omitempty"`
	Height    int              `json:"height,omitempty"`
	Count     int              `json:"count,omitempty"`
	X         int              `json:"x,omitempty"`
	Y         int              `json:"y,omitempty"`
	Event     string           `json:"event,omitempty"`
	CommandID string           `json:"command_id,omitempty"`
}

const (
	UITestMouseLeft      = "left"
	UITestMouseWheelUp   = "wheel_up"
	UITestMouseWheelDown = "wheel_down"
)

// UITestLayoutIssueType names a layout regression the harness can detect.
type UITestLayoutIssueType string

const (
	// UITestIssueInvalidUTF8 marks a normalized line that is not valid UTF-8.
	UITestIssueInvalidUTF8 UITestLayoutIssueType = "invalid_utf8"
	// UITestIssueLineWidthOverflow marks a line wider than the terminal width.
	UITestIssueLineWidthOverflow UITestLayoutIssueType = "line_width_overflow"
	// UITestIssueScreenHeightOverflow marks a view taller than the terminal.
	UITestIssueScreenHeightOverflow UITestLayoutIssueType = "screen_height_overflow"
	// UITestIssueModalWidthOverflow marks a modal whose natural content is
	// wider than the width the box was forced to render at, so root
	// composition squeezes or reflows it (see detectModalFitIssues in
	// uitest_screen.go).
	UITestIssueModalWidthOverflow UITestLayoutIssueType = "modal_width_overflow"
	// UITestIssueModalHeightOverflow marks a modal whose rendered height
	// exceeds the body area root composition gives it, so trailing rows are
	// dropped (see detectModalFitIssues in uitest_screen.go).
	UITestIssueModalHeightOverflow UITestLayoutIssueType = "modal_height_overflow"
	// UITestIssueModalClippedByRoot marks a modal that fits by intrinsic size
	// but is pushed past the body area by its placement offset (e.g. the
	// top-anchored config picker), so composition still drops content.
	UITestIssueModalClippedByRoot UITestLayoutIssueType = "modal_clipped_by_root"
)

// uitestKnownLayoutIssueType reports whether s names a layout issue type the
// harness can emit. It gates the excludes_layout_issue assertion so a scenario
// naming a typo'd or renamed issue fails validation instead of passing
// vacuously (the issue it excludes could never appear).
func uitestKnownLayoutIssueType(s string) bool {
	switch UITestLayoutIssueType(s) {
	case UITestIssueInvalidUTF8, UITestIssueLineWidthOverflow,
		UITestIssueScreenHeightOverflow, UITestIssueModalWidthOverflow,
		UITestIssueModalHeightOverflow, UITestIssueModalClippedByRoot:
		return true
	default:
		return false
	}
}

// UITestLayoutIssue records one detected layout problem. Line is 0-based and
// only meaningful for per-line issues. Actual/Limit describe the overflow: for
// width issues they are measured cells vs. terminal width; for height issues
// they are line count vs. terminal height.
type UITestLayoutIssue struct {
	Type   UITestLayoutIssueType `json:"type"`
	Line   int                   `json:"line,omitempty"`
	Actual int                   `json:"actual,omitempty"`
	Limit  int                   `json:"limit,omitempty"`
	Detail string                `json:"detail,omitempty"`
}

// UITestScreen is a normalized observation of a rendered view: ANSI stripped,
// measured by display cells, with any layout issues attached. Width is the
// widest line and Height is the line count.
type UITestScreen struct {
	Size   UITestSize          `json:"size"`
	Lines  []string            `json:"lines"`
	Width  int                 `json:"width"`
	Height int                 `json:"height"`
	Issues []UITestLayoutIssue `json:"issues,omitempty"`
}

// UITestOptions configures a harness run before any actions are applied.
type UITestOptions struct {
	Name string     `json:"name,omitempty"`
	Size UITestSize `json:"size"`
}

// UITestAssertionFailure is one unmet expectation from a scenario run.
type UITestAssertionFailure struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// uitestResult is the JSON-ready outcome of a single scenario run.
type uitestResult struct {
	Name     string                   `json:"name,omitempty"`
	Passed   bool                     `json:"passed"`
	Screen   UITestScreen             `json:"screen"`
	Failures []UITestAssertionFailure `json:"failures,omitempty"`
}

// UITestAssertionType enumerates the checks a scenario can make about the model
// after its actions have been applied. Each kind reads a small, fixed subset of
// UITestAssertion's fields so the JSON stays compact and reviewable.
type UITestAssertionType string

const (
	// UITestAssertNoLayoutIssues passes when the final observation's screen has
	// no layout issues attached (line/height overflow, invalid UTF-8, and the
	// modal-fit issues layered on in a later session).
	UITestAssertNoLayoutIssues UITestAssertionType = "no_layout_issues"
	// UITestAssertExcludesLayoutIssue passes when the final observation's screen
	// carries no layout issue whose type equals Value. It scopes the layout
	// guarantee to a single issue kind so a scenario can assert (for example)
	// that a modal does not overflow the terminal height while tolerating the
	// deliberately-flagged width reflow modals report at narrow widths.
	UITestAssertExcludesLayoutIssue UITestAssertionType = "excludes_layout_issue"
	// UITestAssertContainsText passes when the final screen contains Text.
	UITestAssertContainsText UITestAssertionType = "contains_text"
	// UITestAssertExcludesText passes when the final screen does not contain Text.
	UITestAssertExcludesText UITestAssertionType = "excludes_text"
	// UITestAssertModeEquals passes when the final observation's mode equals Mode.
	UITestAssertModeEquals UITestAssertionType = "mode_equals"
	// UITestAssertSelectedTitle passes when the selected session's project title
	// equals Value.
	UITestAssertSelectedTitle UITestAssertionType = "selected_title"
	// UITestAssertSelectedProfile passes when the selected session's profile
	// equals Value.
	UITestAssertSelectedProfile UITestAssertionType = "selected_profile"
	// UITestAssertKittyCalls passes when the recorded Kitty calls for Op match
	// Count (exactly, when set) or number at least one (when Count is nil).
	UITestAssertKittyCalls UITestAssertionType = "kitty_calls"
	// UITestAssertCursorEquals passes when the final observation's cursor equals
	// Cursor.
	UITestAssertCursorEquals UITestAssertionType = "cursor_equals"
	// UITestAssertSelectedAction passes when the actions menu has the entry named
	// by Value under its cursor. It names the action by shared identity rather
	// than by row, because the row number is the one fact about a selection that
	// regrouping the menu is allowed to change.
	UITestAssertSelectedAction UITestAssertionType = "selected_action"
)

// Valid reports whether t is one of the recognized assertion kinds.
func (t UITestAssertionType) Valid() bool {
	switch t {
	case UITestAssertNoLayoutIssues, UITestAssertExcludesLayoutIssue,
		UITestAssertContainsText, UITestAssertExcludesText,
		UITestAssertModeEquals, UITestAssertSelectedTitle, UITestAssertSelectedProfile,
		UITestAssertKittyCalls, UITestAssertCursorEquals, UITestAssertSelectedAction:
		return true
	default:
		return false
	}
}

// UITestAssertion is a single expectation checked after a scenario's actions
// run. Only the fields relevant to Type are read; the rest stay zero.
type UITestAssertion struct {
	Type   UITestAssertionType `json:"type"`
	Text   string              `json:"text,omitempty"`   // contains_text / excludes_text
	Mode   string              `json:"mode,omitempty"`   // mode_equals
	Value  string              `json:"value,omitempty"`  // selected_title / selected_profile / selected_action (action id) / excludes_layout_issue (issue type)
	Op     string              `json:"op,omitempty"`     // kitty_calls operation name
	Count  *int                `json:"count,omitempty"`  // kitty_calls: exact count when set; >=1 when nil
	Cursor int                 `json:"cursor,omitempty"` // cursor_equals
}

// UITestScenario is the declarative JSON description of one harness run: where
// the model comes from (exactly one of Fixture or Config), the terminal size to
// render at, the ordered actions to apply, and the assertions to check
// afterward. IncludeObservations is the sole output control; when set, the
// result carries every per-step observation instead of only the final one.
type UITestScenario struct {
	Name                string            `json:"name,omitempty"`
	Fixture             string            `json:"fixture,omitempty"`
	Config              string            `json:"config,omitempty"`
	Size                UITestSize        `json:"size"`
	Actions             []UITestAction    `json:"actions,omitempty"`
	Assertions          []UITestAssertion `json:"assertions,omitempty"`
	IncludeObservations bool              `json:"include_observations,omitempty"`
}

// UITestScenarioResult is the machine-readable outcome of running a scenario. OK
// is true only when every assertion passed. Failures preserves assertion order
// so automation sees deterministic output. Observations is populated only when
// the scenario set IncludeObservations; FinalObservation is always the last
// captured screen so a reviewer can inspect the end state without opting in.
type UITestScenarioResult struct {
	OK               bool                     `json:"ok"`
	Scenario         string                   `json:"scenario,omitempty"`
	Observations     []UITestObservation      `json:"observations,omitempty"`
	Failures         []UITestAssertionFailure `json:"failures,omitempty"`
	FinalObservation UITestObservation        `json:"final_observation"`
}
