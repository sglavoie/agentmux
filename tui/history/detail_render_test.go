package history

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/tui/layout"
)

func TestFormatRelativeAge(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	cases := []struct {
		name string
		at   time.Time
		want string
	}{
		{"just now", now, "0s ago"},
		{"12 seconds", now.Add(-12 * time.Second), "12s ago"},
		{"3 minutes", now.Add(-3 * time.Minute), "3m ago"},
		{"59 minutes", now.Add(-59 * time.Minute), "59m ago"},
		{"1 hour", now.Add(-time.Hour), "1h ago"},
		{"23 hours", now.Add(-23 * time.Hour), "23h ago"},
		{"1 day", now.Add(-25 * time.Hour), "1d ago"},
		{"future clamps to zero", now.Add(5 * time.Second), "0s ago"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := layout.Ago(tc.at, now)
			if got != tc.want {
				t.Errorf("layout.Ago: got %q want %q", got, tc.want)
			}
		})
	}
}

func TestDetailRowShowsCompactTraceAndDecisiveReason(t *testing.T) {
	event := protocol.SessionEvent{
		At: time.Unix(100, 0).Unix(), Event: "PostToolUse", Action: "active",
		ToolName: "Bash", TraceID: "1234567890abcdef", DecisionReason: "accepted",
	}
	row := stripAnsi(detailRow(event, time.Unix(101, 0), 96))
	for _, want := range []string{"Bash", "t:12345678", "accepted"} {
		if !strings.Contains(row, want) {
			t.Fatalf("detail row missing %q: %q", want, row)
		}
	}
}

func TestDetailRowShowsASLQuestionPreview(t *testing.T) {
	event := protocol.SessionEvent{
		At: time.Unix(100, 0).Unix(), Event: "AgentSessionLoopWaitStarted", Action: "elicitation",
		QuestionPreview: "Should the shim keep the narrow branch?",
	}
	row := stripAnsi(detailRow(event, time.Unix(101, 0), 120))
	if !strings.Contains(row, "q: Should the shim keep the narrow branch?") {
		t.Fatalf("detail row missing question preview: %q", row)
	}
}

// The preview is model-controlled text rendered straight into the terminal.
// Hook decode is the boundary that removes terminal controls, so a row built
// from a decoded hostile payload carries no escape sequence of its own — only
// the CSI styling Lipgloss adds.

// The preview is model-controlled text rendered straight into the terminal.
// Hook decode is the boundary that removes terminal controls, so a row built
// from a decoded hostile payload carries no escape sequence of its own — only
// the CSI styling Lipgloss adds.
func TestDetailRowRendersSanitizedQuestionPreview(t *testing.T) {
	esc, bel := string(rune(0x1b)), string(rune(0x07))
	hostile, err := json.Marshal(map[string]string{
		"question_preview": esc + "[2JShip " + esc + "]0;pwned" + bel + "the shim?",
	})
	if err != nil {
		t.Fatalf("marshal hostile payload: %v", err)
	}
	payload, err := hookevent.DecodeHookPayload(strings.NewReader(string(hostile)))
	if err != nil {
		t.Fatalf("DecodeHookPayload error: %v", err)
	}

	event := protocol.SessionEvent{
		At: time.Unix(100, 0).Unix(), Event: "AgentSessionLoopWaitStarted", Action: "elicitation",
		QuestionPreview: payload.QuestionPreview,
	}
	row := stripAnsi(detailRow(event, time.Unix(101, 0), 120))
	if !strings.Contains(row, "q: Ship the shim?") {
		t.Fatalf("detail row missing sanitized preview: %q", row)
	}
	if strings.ContainsAny(row, esc+bel) {
		t.Fatalf("detail row still carries terminal controls: %q", row)
	}
}

// An event with no preview must not leave an empty labelled column behind —
// the producer omits the key whenever the question carries no text, and
// payload compaction drops it above 256 KiB.

// An event with no preview must not leave an empty labelled column behind —
// the producer omits the key whenever the question carries no text, and
// payload compaction drops it above 256 KiB.
func TestDetailRowOmitsEmptyQuestionPreview(t *testing.T) {
	event := protocol.SessionEvent{
		At: time.Unix(100, 0).Unix(), Event: "AgentSessionLoopWaitStarted", Action: "elicitation",
	}
	row := stripAnsi(detailRow(event, time.Unix(101, 0), 120))
	if strings.Contains(row, "q:") {
		t.Fatalf("detail row rendered an empty question preview: %q", row)
	}
}
