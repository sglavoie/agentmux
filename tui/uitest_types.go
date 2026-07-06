package tui

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

// UITestActionType enumerates the scenario step kinds the first harness
// version understands. It intentionally omits a generic command action;
// scenarios drive the model with keys, literal text, resizes, and ticks.
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
)

// Valid reports whether t is one of the recognized action kinds.
func (t UITestActionType) Valid() bool {
	switch t {
	case UITestActionKey, UITestActionText, UITestActionResize, UITestActionTick:
		return true
	default:
		return false
	}
}

// UITestAction is a single scenario step. Only the fields relevant to Type are
// populated; the rest stay zero so the JSON stays compact and reviewable.
type UITestAction struct {
	Type   UITestActionType `json:"type"`
	Key    string           `json:"key,omitempty"`
	Text   string           `json:"text,omitempty"`
	Width  int              `json:"width,omitempty"`
	Height int              `json:"height,omitempty"`
	Count  int              `json:"count,omitempty"`
}

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
	// composition squeezes or reflows it (see uitest_modal.go).
	UITestIssueModalWidthOverflow UITestLayoutIssueType = "modal_width_overflow"
	// UITestIssueModalHeightOverflow marks a modal whose rendered height
	// exceeds the body area root composition gives it, so trailing rows are
	// dropped (see uitest_modal.go).
	UITestIssueModalHeightOverflow UITestLayoutIssueType = "modal_height_overflow"
	// UITestIssueModalClippedByRoot marks a modal that fits by intrinsic size
	// but is pushed past the body area by its placement offset (e.g. the
	// top-anchored config picker), so composition still drops content.
	UITestIssueModalClippedByRoot UITestLayoutIssueType = "modal_clipped_by_root"
)

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

// UITestResult is the JSON-ready outcome of a single scenario run.
type UITestResult struct {
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
)

// Valid reports whether t is one of the recognized assertion kinds.
func (t UITestAssertionType) Valid() bool {
	switch t {
	case UITestAssertNoLayoutIssues, UITestAssertContainsText, UITestAssertExcludesText,
		UITestAssertModeEquals, UITestAssertSelectedTitle, UITestAssertSelectedProfile,
		UITestAssertKittyCalls, UITestAssertCursorEquals:
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
	Value  string              `json:"value,omitempty"`  // selected_title / selected_profile
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
