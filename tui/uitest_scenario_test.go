package tui

import "testing"

func intPtr(n int) *int { return &n }

// TestUITestScenarioParseValid verifies a well-formed scenario decodes into the
// expected struct with actions and assertions preserved in order.
func TestUITestScenarioParseValid(t *testing.T) {
	data := []byte(`{
		"name": "open actions",
		"fixture": "basic",
		"size": {"width": 100, "height": 32},
		"actions": [{"type": "key", "key": "a"}],
		"assertions": [
			{"type": "mode_equals", "mode": "actions"},
			{"type": "kitty_calls", "op": "FocusTab", "count": 0}
		]
	}`)
	sc, err := ParseUITestScenario(data)
	if err != nil {
		t.Fatalf("ParseUITestScenario error: %v", err)
	}
	if sc.Name != "open actions" || sc.Fixture != "basic" {
		t.Errorf("unexpected header: %+v", sc)
	}
	if sc.Size != (UITestSize{Width: 100, Height: 32}) {
		t.Errorf("unexpected size: %+v", sc.Size)
	}
	if len(sc.Actions) != 1 || sc.Actions[0].Type != UITestActionKey || sc.Actions[0].Key != "a" {
		t.Errorf("unexpected actions: %+v", sc.Actions)
	}
	if len(sc.Assertions) != 2 || sc.Assertions[0].Type != UITestAssertModeEquals {
		t.Errorf("unexpected assertions: %+v", sc.Assertions)
	}
	if sc.Assertions[1].Count == nil || *sc.Assertions[1].Count != 0 {
		t.Errorf("expected explicit count 0 on kitty_calls, got %+v", sc.Assertions[1].Count)
	}
}

// TestUITestScenarioParseRejectsBadInput verifies parse-time validation rejects
// ambiguous or malformed scenarios with a path-aware error.
func TestUITestScenarioParseRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"unknown field":     `{"fixture": "basic", "size": {"width": 80, "height": 24}, "fixtures": "x"}`,
		"missing source":    `{"size": {"width": 80, "height": 24}}`,
		"both sources":      `{"fixture": "basic", "config": "c.toml", "size": {"width": 80, "height": 24}}`,
		"zero size":         `{"fixture": "basic", "size": {"width": 0, "height": 24}}`,
		"unknown action":    `{"fixture": "basic", "size": {"width": 80, "height": 24}, "actions": [{"type": "swipe"}]}`,
		"empty key action":  `{"fixture": "basic", "size": {"width": 80, "height": 24}, "actions": [{"type": "key"}]}`,
		"bad resize action": `{"fixture": "basic", "size": {"width": 80, "height": 24}, "actions": [{"type": "resize", "width": 0, "height": 10}]}`,
		"unknown assertion": `{"fixture": "basic", "size": {"width": 80, "height": 24}, "assertions": [{"type": "glows"}]}`,
		"empty text assert": `{"fixture": "basic", "size": {"width": 80, "height": 24}, "assertions": [{"type": "contains_text"}]}`,
		"unknown mode":      `{"fixture": "basic", "size": {"width": 80, "height": 24}, "assertions": [{"type": "mode_equals", "mode": "orbit"}]}`,
		"kitty no op":       `{"fixture": "basic", "size": {"width": 80, "height": 24}, "assertions": [{"type": "kitty_calls"}]}`,
		"trailing object":   `{"fixture": "basic", "size": {"width": 80, "height": 24}}{"fixture": "basic", "size": {"width": 80, "height": 24}}`,
		"trailing content":  `{"fixture": "basic", "size": {"width": 80, "height": 24}} not json`,
	}
	for name, body := range cases {
		if _, err := ParseUITestScenario([]byte(body)); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

// TestUITestScenarioParseAllowsTrailingWhitespace verifies the exactly-one-object
// rule still accepts trailing whitespace after the top-level object.
func TestUITestScenarioParseAllowsTrailingWhitespace(t *testing.T) {
	data := []byte("{\"fixture\": \"basic\", \"size\": {\"width\": 80, \"height\": 24}}\n\t  \n")
	sc, err := ParseUITestScenario(data)
	if err != nil {
		t.Fatalf("ParseUITestScenario error: %v", err)
	}
	if sc.Fixture != "basic" {
		t.Errorf("unexpected fixture: %q", sc.Fixture)
	}
}

// TestUITestScenarioRunFixturePasses verifies a passing scenario against a
// built-in fixture returns OK with no failures and a populated final
// observation.
func TestUITestScenarioRunFixturePasses(t *testing.T) {
	// The basic fixture lists dotfiles/personal in the first (grouped) row, and
	// renders cleanly at 120x40; a narrower width overflows its header, which
	// is exactly what no_layout_issues is meant to catch elsewhere.
	sc := UITestScenario{
		Name:    "open actions overlay",
		Fixture: "basic",
		Size:    UITestSize{Width: 120, Height: 40},
		Actions: []UITestAction{{Type: UITestActionKey, Key: "a"}},
		Assertions: []UITestAssertion{
			{Type: UITestAssertModeEquals, Mode: "actions"},
			{Type: UITestAssertSelectedTitle, Value: "dotfiles"},
			{Type: UITestAssertSelectedProfile, Value: "personal"},
			{Type: UITestAssertContainsText, Text: "dotfiles"},
			{Type: UITestAssertNoLayoutIssues},
			{Type: UITestAssertKittyCalls, Op: "LaunchTab", Count: intPtr(0)},
		},
	}
	res, err := RunUITestScenario(sc, UITestScenarioEnv{})
	if err != nil {
		t.Fatalf("RunUITestScenario error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected OK scenario, got failures: %+v", res.Failures)
	}
	if res.Scenario != "open actions overlay" {
		t.Errorf("unexpected scenario name %q", res.Scenario)
	}
	if res.FinalObservation.Mode != "actions" {
		t.Errorf("expected final mode actions, got %q", res.FinalObservation.Mode)
	}
	// Observations are opt-in; default omits them.
	if res.Observations != nil {
		t.Errorf("expected no observations without include_observations, got %d", len(res.Observations))
	}
}

// TestUITestScenarioRunRecordsFailures verifies unmet assertions produce an
// OK=false result whose failures preserve assertion order.
func TestUITestScenarioRunRecordsFailures(t *testing.T) {
	sc := UITestScenario{
		Name:    "deliberate mismatches",
		Fixture: "basic",
		Size:    UITestSize{Width: 100, Height: 32},
		Assertions: []UITestAssertion{
			{Type: UITestAssertModeEquals, Mode: "actions"},         // fails: still list
			{Type: UITestAssertSelectedTitle, Value: "nonexistent"}, // fails
			{Type: UITestAssertExcludesText, Text: "api"},           // fails: api is shown
		},
	}
	res, err := RunUITestScenario(sc, UITestScenarioEnv{})
	if err != nil {
		t.Fatalf("RunUITestScenario error: %v", err)
	}
	if res.OK {
		t.Fatal("expected OK=false for mismatched assertions")
	}
	if len(res.Failures) != 3 {
		t.Fatalf("expected 3 failures, got %d: %+v", len(res.Failures), res.Failures)
	}
	wantOrder := []string{"mode_equals", "selected_title", "excludes_text"}
	for i, want := range wantOrder {
		if res.Failures[i].Kind != want {
			t.Errorf("failure %d kind = %q, want %q", i, res.Failures[i].Kind, want)
		}
	}
}

// TestUITestScenarioKittyCalls verifies a scenario that focuses a tab records a
// FocusTab call the kitty_calls assertion can match.
func TestUITestScenarioKittyCalls(t *testing.T) {
	sc := UITestScenario{
		Name:    "focus records a call",
		Fixture: "basic",
		Size:    UITestSize{Width: 100, Height: 32},
		Actions: []UITestAction{{Type: UITestActionKey, Key: "enter"}},
		Assertions: []UITestAssertion{
			{Type: UITestAssertKittyCalls, Op: "FocusTab"}, // >= 1
			{Type: UITestAssertKittyCalls, Op: "FocusTab", Count: intPtr(1)},
			{Type: UITestAssertKittyCalls, Op: "LaunchTab", Count: intPtr(0)},
		},
	}
	res, err := RunUITestScenario(sc, UITestScenarioEnv{})
	if err != nil {
		t.Fatalf("RunUITestScenario error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected OK, got failures: %+v", res.Failures)
	}
}

// TestUITestScenarioIncludeObservations verifies the sole output control toggles
// whether every per-step observation is carried in the result.
func TestUITestScenarioIncludeObservations(t *testing.T) {
	sc := UITestScenario{
		Name:                "collect steps",
		Fixture:             "basic",
		Size:                UITestSize{Width: 100, Height: 32},
		Actions:             []UITestAction{{Type: UITestActionKey, Key: "a"}, {Type: UITestActionKey, Key: "esc"}},
		IncludeObservations: true,
	}
	res, err := RunUITestScenario(sc, UITestScenarioEnv{})
	if err != nil {
		t.Fatalf("RunUITestScenario error: %v", err)
	}
	// initial + one per action.
	if len(res.Observations) != len(sc.Actions)+1 {
		t.Fatalf("expected %d observations, got %d", len(sc.Actions)+1, len(res.Observations))
	}
	if res.FinalObservation.Mode != res.Observations[len(res.Observations)-1].Mode {
		t.Error("final observation should match the last collected observation")
	}
}

// TestUITestScenarioUnknownFixture verifies an unknown fixture name is returned
// as an error (invalid input), not an assertion failure.
func TestUITestScenarioUnknownFixture(t *testing.T) {
	sc := UITestScenario{
		Name:    "bad fixture",
		Fixture: "does-not-exist",
		Size:    UITestSize{Width: 80, Height: 24},
	}
	if _, err := RunUITestScenario(sc, UITestScenarioEnv{}); err == nil {
		t.Fatal("expected error for unknown fixture")
	}
}

// TestUITestActionValidation exercises validateUITestAction across the accepted
// action kinds and their failure modes.
func TestUITestActionValidation(t *testing.T) {
	ok := []UITestAction{
		{Type: UITestActionKey, Key: "enter"},
		{Type: UITestActionKey, Key: "ctrl+c"},
		{Type: UITestActionText, Text: "api"},
		{Type: UITestActionResize, Width: 80, Height: 24},
		{Type: UITestActionTick, Count: 0},
		{Type: UITestActionTick, Count: 3},
	}
	for _, a := range ok {
		if err := validateUITestAction(a); err != nil {
			t.Errorf("expected %+v valid, got %v", a, err)
		}
	}

	bad := []UITestAction{
		{Type: UITestActionType("bogus")},
		{Type: UITestActionKey},
		{Type: UITestActionKey, Key: "notakey"},
		{Type: UITestActionText},
		{Type: UITestActionResize, Width: -1, Height: 24},
		{Type: UITestActionTick, Count: -1},
	}
	for _, a := range bad {
		if err := validateUITestAction(a); err == nil {
			t.Errorf("expected %+v invalid, got nil", a)
		}
	}
}

// TestUITestAssertionValidation exercises validateUITestAssertion across the
// accepted assertion kinds and their failure modes.
func TestUITestAssertionValidation(t *testing.T) {
	modes := uitestModeNameSet()
	ok := []UITestAssertion{
		{Type: UITestAssertNoLayoutIssues},
		{Type: UITestAssertContainsText, Text: "api"},
		{Type: UITestAssertExcludesText, Text: "api"},
		{Type: UITestAssertModeEquals, Mode: "list"},
		{Type: UITestAssertSelectedTitle, Value: "api"},
		{Type: UITestAssertSelectedProfile, Value: "work"},
		{Type: UITestAssertKittyCalls, Op: "FocusTab"},
		{Type: UITestAssertKittyCalls, Op: "FocusTab", Count: intPtr(2)},
		{Type: UITestAssertCursorEquals, Cursor: 1},
	}
	for _, a := range ok {
		if err := validateUITestAssertion(a, modes); err != nil {
			t.Errorf("expected %+v valid, got %v", a, err)
		}
	}

	bad := []UITestAssertion{
		{Type: UITestAssertionType("glows")},
		{Type: UITestAssertContainsText},
		{Type: UITestAssertModeEquals},
		{Type: UITestAssertModeEquals, Mode: "orbit"},
		{Type: UITestAssertSelectedTitle},
		{Type: UITestAssertKittyCalls},
		{Type: UITestAssertKittyCalls, Op: "FocusTab", Count: intPtr(-1)},
		{Type: UITestAssertCursorEquals, Cursor: -1},
	}
	for _, a := range bad {
		if err := validateUITestAssertion(a, modes); err == nil {
			t.Errorf("expected %+v invalid, got nil", a)
		}
	}
}

// TestUITestAssertModeNamesAreValidatable guards that every mode name the
// observation layer can emit is accepted by mode_equals validation, so the two
// mappings never drift.
func TestUITestAssertModeNamesAreValidatable(t *testing.T) {
	modes := uitestModeNameSet()
	for mode := ModeList; mode <= ModeCommandPalette; mode++ {
		name := uitestModeName(mode)
		if name == "unknown" {
			t.Errorf("mode %d has no stable name", mode)
			continue
		}
		if !modes[name] {
			t.Errorf("mode name %q not accepted by validation", name)
		}
	}
}
