//go:build tuitest

package tui

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestUITestActionTypeValid locks in the closed set of scenario action kinds:
// key/text/resize/tick/mouse name an input device, and command names a
// capability the harness reaches through the palette's own keys. Nothing else
// is accepted, so a misspelled kind fails the scenario instead of being ignored.
func TestUITestActionTypeValid(t *testing.T) {
	t.Parallel()

	valid := []UITestActionType{
		UITestActionKey, UITestActionText, UITestActionResize, UITestActionTick, UITestActionMouse,
		UITestActionCommand,
	}
	for _, at := range valid {
		if !at.Valid() {
			t.Errorf("UITestActionType(%q).Valid() = false, want true", at)
		}
	}
	for _, at := range []UITestActionType{"", "sleep", "KEY", "COMMAND"} {
		if at.Valid() {
			t.Errorf("UITestActionType(%q).Valid() = true, want false", at)
		}
	}
}

// TestUITestResultJSONRoundTrip verifies the JSON-facing contract types marshal
// and unmarshal without loss, so the CLI output stays stable across sessions.
func TestUITestResultJSONRoundTrip(t *testing.T) {
	t.Parallel()

	orig := uitestResult{
		Name:   "profile-picker-fits",
		Passed: false,
		Screen: UITestScreen{
			Size:   UITestSize{Width: 80, Height: 24},
			Lines:  []string{"header", "body line"},
			Width:  9,
			Height: 2,
			Issues: []UITestLayoutIssue{
				{Type: UITestIssueLineWidthOverflow, Line: 1, Actual: 90, Limit: 80},
				{Type: UITestIssueModalWidthOverflow, Actual: 42, Limit: 40, Detail: "actions overlay clipped"},
			},
		},
		Failures: []UITestAssertionFailure{
			{Kind: "layout", Message: "modal width overflow"},
		},
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal uitestResult: %v", err)
	}

	var got uitestResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal uitestResult: %v", err)
	}
	if !reflect.DeepEqual(got, orig) {
		t.Fatalf("round-trip mismatch:\n got  %+v\n want %+v", got, orig)
	}
}

// TestUITestActionJSONOmitsZeroFields confirms only the fields relevant to a
// given action Type are emitted, keeping scenario output compact and readable.
func TestUITestActionJSONOmitsZeroFields(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(UITestAction{Type: UITestActionKey, Key: "enter"})
	if err != nil {
		t.Fatalf("marshal UITestAction: %v", err)
	}
	got := string(data)
	if want := `{"type":"key","key":"enter"}`; got != want {
		t.Fatalf("key action JSON = %s, want %s", got, want)
	}

	resizeData, err := json.Marshal(UITestAction{Type: UITestActionResize, Width: 100, Height: 40})
	if err != nil {
		t.Fatalf("marshal resize UITestAction: %v", err)
	}
	if want := `{"type":"resize","width":100,"height":40}`; string(resizeData) != want {
		t.Fatalf("resize action JSON = %s, want %s", resizeData, want)
	}

	mouseData, err := json.Marshal(UITestAction{Type: UITestActionMouse, X: 10, Y: 5, Event: UITestMouseLeft})
	if err != nil {
		t.Fatalf("marshal mouse UITestAction: %v", err)
	}
	if want := `{"type":"mouse","x":10,"y":5,"event":"left"}`; string(mouseData) != want {
		t.Fatalf("mouse action JSON = %s, want %s", mouseData, want)
	}
}
