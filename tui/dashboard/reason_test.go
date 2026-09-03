package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/screenstate"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

// attentionGroup is the grouping the reason column is measured against in these
// tests. Using the real StateGroup rather than a stub is deliberate: the
// column's whole reservation rule is "some row in group 0 has a reason", so a
// test that invented its own grouping would stop proving the rule the moment
// StateGroup's buckets moved.
func attentionGroup(s session.RunningSession) int {
	return StateGroup(s, nil, time.Minute)
}

// TestReasonTagsCoverEveryPromptKind is the totality guard the untyped wire
// field cannot give us. state.WindowState.PromptKind is a bare string so Claude
// and Codex kinds can share it without a shared enum, which means nothing in the
// type system notices when a tenth kind is added and the dashboard silently
// renders it as blank. This is that notice: every kind either harness can stamp
// must have a tag decided for it here.
func TestReasonTagsCoverEveryPromptKind(t *testing.T) {
	kinds := []string{
		string(screenstate.CodexPromptEditApproval),
		string(screenstate.CodexPromptRequestUserInput),
		string(screenstate.CodexPromptUnansweredConfirmation),
		string(screenstate.CodexPromptCommandApproval),
		string(screenstate.CodexPromptPermissionGrant),
		string(screenstate.ClaudePromptPermission),
		string(screenstate.ClaudePromptTrust),
		string(screenstate.ClaudePromptEdit),
		string(screenstate.ClaudePromptElicitation),
	}
	for _, kind := range kinds {
		tag, ok := reasonTags[kind]
		if !ok || tag == "" {
			t.Errorf("prompt kind %q has no reason tag; decide what it reads as in reasonTags", kind)
		}
	}
	if len(reasonTags) != len(kinds) {
		t.Errorf("reasonTags has %d entries for %d known kinds: an entry here that no harness can stamp is dead presentation",
			len(reasonTags), len(kinds))
	}
}

// TestReasonTagsAreShortEnoughToBeAColumn pins the sizing premise the column
// rests on. Every reserved cell is width the directory column gives up on EVERY
// row, not just the row being explained, so a long tag is charged to the whole
// view. Five cells is the budget the vocabulary was chosen against.
func TestReasonTagsAreShortEnoughToBeAColumn(t *testing.T) {
	const maxTagCells = 5
	for kind, tag := range reasonTags {
		if w := layout.Width(tag); w > maxTagCells {
			t.Errorf("tag %q for kind %q is %d cells, over the %d-cell budget", tag, kind, w, maxTagCells)
		}
	}
}

// TestReasonTagGlossesBothHarnessesToOneVocabulary is the load-bearing mapping
// case: Codex and Claude name a permission prompt differently on the wire and
// the row must not. It also pins the deliberate collapse of Codex's two
// permission-class prompts, which is why the mapping is many-to-one at all.
func TestReasonTagGlossesBothHarnessesToOneVocabulary(t *testing.T) {
	cases := []struct {
		kind string
		want string
	}{
		{string(screenstate.CodexPromptCommandApproval), "perm"},
		{string(screenstate.CodexPromptPermissionGrant), "perm"},
		{string(screenstate.ClaudePromptPermission), "perm"},
		{string(screenstate.CodexPromptEditApproval), "edit"},
		{string(screenstate.ClaudePromptEdit), "edit"},
		{string(screenstate.CodexPromptRequestUserInput), "input"},
		{string(screenstate.CodexPromptUnansweredConfirmation), "ask"},
		{string(screenstate.ClaudePromptElicitation), "ask"},
		{string(screenstate.ClaudePromptTrust), "trust"},
	}
	for _, tc := range cases {
		s := session.RunningSession{State: "key", Reason: tc.kind}
		if got := reasonTag(s, 0); got != tc.want {
			t.Errorf("reasonTag(%q) = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

// TestReasonTagSaysNothingWhenItHasNothingToSay walks the three separate
// situations that collapse into an empty tag. All three are ordinary states, not
// errors, and the renderer must be unable to tell them apart — an unrecognized
// kind in particular must never reach the screen raw.
func TestReasonTagSaysNothingWhenItHasNothingToSay(t *testing.T) {
	cases := []struct {
		name  string
		s     session.RunningSession
		group int
	}{
		{"blocked but unlabelled", session.RunningSession{State: "key"}, 0},
		{"kind this build does not know", session.RunningSession{State: "key", Reason: "teleportation_approval"}, 0},
		{"not in the attention group", session.RunningSession{State: "bolt", Reason: "permission"}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reasonTag(tc.s, tc.group); got != "" {
				t.Fatalf("reasonTag = %q, want empty", got)
			}
		})
	}
}

// TestReasonColumnNotReservedWithoutAReason is the conditional-reservation rule
// the activity and loop columns already follow: a view with nothing to explain
// gives the whole column back to the directory rather than padding every row
// around an empty slot. The second case is the one that matters — attention rows
// are present, they simply have no reason between them.
func TestReasonColumnNotReservedWithoutAReason(t *testing.T) {
	cases := []struct {
		name string
		rows []session.RunningSession
	}{
		{"no rows at all", nil},
		{"attention rows nothing labelled", []session.RunningSession{
			{State: "key"},
			{State: "question"},
			{State: "error"},
		}},
		{"a reason on a row that is not waiting", []session.RunningSession{
			{State: "bolt", Reason: "permission"},
			{State: "stop", Reason: "edit_approval"},
		}},
		{"only kinds this build does not know", []session.RunningSession{
			{State: "key", Reason: "teleportation_approval"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reasonColumnWidth(tc.rows, attentionGroup); got != 0 {
				t.Fatalf("reasonColumnWidth = %d, want 0 (column must not be reserved)", got)
			}
			for _, s := range tc.rows {
				if cell := renderReasonCell(s, attentionGroup(s), 0); cell != "" {
					t.Fatalf("renderReasonCell at width 0 = %q, want empty", cell)
				}
			}
		})
	}
}

// TestReasonColumnMeasuredOnceForTheWholePass pins the two halves of the
// once-per-pass measurement together: the width is the widest tag in the view
// (not the widest tag that exists), and every row pads to it — including the
// blocked row nothing labelled and the row that is not blocked at all. Ragged
// cells here would move the badge, harness, and title columns row by row, which
// is the entire reason the width is measured before the walk instead of during
// it.
func TestReasonColumnMeasuredOnceForTheWholePass(t *testing.T) {
	rows := []session.RunningSession{
		{State: "key", Reason: string(screenstate.CodexPromptCommandApproval)},       // perm  (4)
		{State: "question", Reason: string(screenstate.CodexPromptRequestUserInput)}, // input (5)
		{State: "key"}, // blocked, unlabelled
		{State: "bolt", BusySince: time.Unix(1, 0)},
	}
	width := reasonColumnWidth(rows, attentionGroup)
	if want := layout.Width("input") + 1; width != want {
		t.Fatalf("reasonColumnWidth = %d, want %d (widest tag in the view plus one trailing space)", width, want)
	}
	for i, s := range rows {
		cell := renderReasonCell(s, attentionGroup(s), width)
		if got := layout.StyledWidth(cell); got != width {
			t.Errorf("row %d cell %q is %d cells, want %d", i, stripAnsi(cell), got, width)
		}
	}

	// Drop the widest tag and the column shrinks with it, rather than staying
	// sized for a vocabulary the view is not using.
	narrower := reasonColumnWidth(rows[:1], attentionGroup)
	if want := layout.Width("perm") + 1; narrower != want {
		t.Fatalf("reasonColumnWidth without the input row = %d, want %d", narrower, want)
	}
}

// TestFormatRowPlacesTheReasonBesideTheIcon pins the tag's position in the row.
// Beside the icon rather than out by the directory is the point: the icon says a
// row is waiting and the tag says what for, so they have to be read together.
func TestFormatRowPlacesTheReasonBesideTheIcon(t *testing.T) {
	s := session.RunningSession{
		WindowID:  1,
		Account:   "work",
		Project:   "api",
		Base:      "api",
		Directory: "/tmp/api",
		State:     "key",
		Reason:    string(screenstate.ClaudePromptPermission),
	}
	cell := renderReasonCell(s, 0, reasonColumnWidth([]session.RunningSession{s}, attentionGroup))
	row := stripAnsi(FormatRow(s, map[string]string{"work": "blue"}, nil, nil, 120, "", "", cell, ""))

	icon := palette.StateIcon("key")
	want := icon + " perm "
	if !strings.Contains(row, want) {
		t.Fatalf("row %q does not place the tag beside the icon (want %q)", row, want)
	}
	if idx := strings.Index(row, "perm"); idx < 0 || idx > strings.Index(row, "[W]") {
		t.Fatalf("row %q renders the tag after the account badge, want before it", row)
	}
}

// TestFormatRowReasonCellIsChargedToTheDirectory pins the width arithmetic. A
// new fixed-width cell has to come out of the one column that can absorb it, and
// FormatRow's directory budget is where every other reserved column is already
// charged. A cell that was reserved in the layout but not subtracted from the
// budget would push the row past the viewport edge.
func TestFormatRowReasonCellIsChargedToTheDirectory(t *testing.T) {
	s := session.RunningSession{
		WindowID:  1,
		Account:   "work",
		Project:   "api",
		Base:      "api",
		Directory: "/a/very/long/directory/path/that/will/be/left/truncated/api",
		State:     "key",
		Reason:    string(screenstate.CodexPromptRequestUserInput),
	}
	const width = 100
	without := layout.StyledWidth(FormatRow(s, nil, nil, nil, width, "", "", "", ""))
	cell := renderReasonCell(s, 0, reasonColumnWidth([]session.RunningSession{s}, attentionGroup))
	with := layout.StyledWidth(FormatRow(s, nil, nil, nil, width, "", "", cell, ""))
	if with != without {
		t.Fatalf("row width with the reason cell = %d, without = %d: the cell must be charged to the directory budget, not added to the row",
			with, without)
	}
}
