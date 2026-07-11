package daemon

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/activity"
	"github.com/sglavoie/agentmux/internal/datastore"
)

func TestMeaningfulActivityPersistsAfterReducerAndExcludesHookNoise(t *testing.T) {
	store := datastore.OpenTest(t)
	repo, err := activity.NewRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	writer := activity.NewWriter(activity.WriterOptions{Repository: repo, Keep: 30 * 24 * time.Hour})
	h := NewHandler(NewStateGuard(time.Hour), nil)
	h.SetActivityWriter(writer)
	t.Cleanup(writer.Close)

	deliver := func(requestID, event, source string, sequence int, sessionID string) {
		t.Helper()
		payload := fmt.Sprintf(`{"session_id":%q,"source":%q,"source_sequence":%q}`, sessionID, source, fmt.Sprintf("%d", sequence))
		resp := h.Handle(Request{
			Type:        TypeHookEvent,
			RequestID:   requestID,
			WindowID:    42,
			Harness:     "claude",
			Event:       event,
			PayloadJSON: payload,
		})
		if !resp.OK {
			t.Fatalf("%s response = %+v", event, resp)
		}
	}

	deliver("start", "SessionStart", "startup", 1, "session-1")
	deliver("prompt", "UserPromptSubmit", "", 2, "session-1")
	deliver("attention", "Elicitation", "", 3, "session-1")
	deliver("noise", "PreToolUse", "", 4, "session-1")
	deliver("failure", "PostToolUseFailure", "", 5, "session-1")
	deliver("terminal", "StopFailure", "", 6, "session-1")
	deliver("start-2", "SessionStart", "startup", 1, "session-2")
	completion := NormalizedEvent{
		Source:    SourceID{Kind: SourceHarness, Key: "claude:session-2"},
		Action:    "stop",
		SessionID: "session-2",
		Event:     "Stop",
		Seq:       2,
	}
	if resp := h.Handle(Request{Type: TypeStateUpdate, RequestID: "complete", WindowID: 42, Harness: "claude", Action: "stop", Event: "Stop", normalized: &completion}); !resp.OK {
		t.Fatalf("completion response = %+v", resp)
	}

	// Heartbeats advance reducer facts but are operational churn, not a loop
	// phase transition. The turn-start remains a meaningful phase record.
	for _, event := range []string{agentSessionLoopTurnStarted, agentSessionLoopTurnHeartbeat} {
		resp := h.Handle(Request{
			Type:      TypeHookEvent,
			RequestID: event,
			WindowID:  42,
			Harness:   "claude",
			Event:     event,
			PayloadJSON: fmt.Sprintf(
				`{"run_id":"run-1","source_sequence":%q}`, map[string]string{agentSessionLoopTurnStarted: "1", agentSessionLoopTurnHeartbeat: "2"}[event]),
		})
		if !resp.OK {
			t.Fatalf("%s response = %+v", event, resp)
		}
	}

	writer.Close()
	var kinds []string
	rows, err := store.DB().QueryContext(context.Background(), `SELECT kind FROM activity_items ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, kind)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"session_start", "prompt", "attention_input", "failure",
		"terminal_failure", "session_start", "completion", "loop_turn",
	}
	if fmt.Sprint(kinds) != fmt.Sprint(want) {
		t.Fatalf("activity kinds = %v, want %v", kinds, want)
	}
	var noise int
	if err := store.DB().QueryRowContext(context.Background(), `SELECT count(*) FROM activity_items WHERE dedupe_key = 'request:noise'`).Scan(&noise); err != nil {
		t.Fatal(err)
	}
	if noise != 0 {
		t.Fatalf("ordinary tool event created %d activity rows", noise)
	}
}

func TestMeaningfulActivityDedupesAcrossDaemonRestartByRequestID(t *testing.T) {
	store := datastore.OpenTest(t)
	repo, err := activity.NewRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	deliver := func(handler *Handler, writer *activity.Writer) {
		handler.SetActivityWriter(writer)
		normalized := NormalizedEvent{
			Source:    SourceID{Kind: SourceHarness, Key: "claude:restart-session"},
			Action:    "active",
			Prompt:    true,
			SessionID: "restart-session",
			Event:     "UserPromptSubmit",
			Seq:       1,
			At:        time.Now().Unix(),
		}
		handler.Handle(Request{
			Type:       TypeStateUpdate,
			RequestID:  "same-logical-hook",
			WindowID:   4,
			Harness:    "claude",
			Action:     "active",
			Event:      "UserPromptSubmit",
			normalized: &normalized,
		})
		writer.Close()
	}
	deliver(NewHandler(NewStateGuard(time.Hour), nil), activity.NewWriter(activity.WriterOptions{Repository: repo, Keep: 30 * 24 * time.Hour}))
	deliver(NewHandler(NewStateGuard(time.Hour), nil), activity.NewWriter(activity.WriterOptions{Repository: repo, Keep: 30 * 24 * time.Hour}))

	var count int
	if err := store.DB().QueryRowContext(context.Background(), `SELECT count(*) FROM activity_items WHERE dedupe_key = 'request:same-logical-hook'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("activity duplicate count = %d, want 1", count)
	}
}

func TestClassifyMeaningfulActivityDistinguishesSessionContinuitySources(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   string
	}{
		{source: "startup", want: "session_start"},
		{source: "resume", want: "resume"},
		{source: "fork", want: "fork"},
		{source: "handoff", want: "handoff"},
	} {
		t.Run(tc.source, func(t *testing.T) {
			kind, _, ok := classifyMeaningfulActivity(NormalizedEvent{Event: "SessionStart", Action: "idle"}, Request{Source: tc.source})
			if !ok || kind != tc.want {
				t.Fatalf("classify source %q = (%q, %v), want (%q, true)", tc.source, kind, ok, tc.want)
			}
		})
	}
}

func TestActivityWriterFailureCannotDelayHookAcknowledgement(t *testing.T) {
	blocked := make(chan struct{})
	release := make(chan struct{})
	var blockedOnce sync.Once
	writer := activity.NewWriter(activity.WriterOptions{
		Keep: time.Hour,
		OpenStore: func(context.Context) (*datastore.Store, error) {
			blockedOnce.Do(func() { close(blocked) })
			<-release
			return nil, fmt.Errorf("forced sqlite failure")
		},
	})
	<-blocked
	h := NewHandler(NewStateGuard(time.Hour), nil)
	h.SetActivityWriter(writer)
	start := time.Now()
	resp := h.Handle(Request{
		Type:      TypeStateUpdate,
		RequestID: "latency-check",
		WindowID:  9,
		Harness:   "claude",
		Action:    "active",
		Event:     "UserPromptSubmit",
		normalized: &NormalizedEvent{
			Source:    SourceID{Kind: SourceHarness, Key: "claude:latency"},
			Action:    "active",
			Prompt:    true,
			SessionID: "latency",
			Event:     "UserPromptSubmit",
			Seq:       1,
		},
	})
	if !resp.OK || time.Since(start) > 100*time.Millisecond {
		t.Fatalf("hook acknowledgement under SQLite fault = %+v after %s", resp, time.Since(start))
	}
	close(release)
	writer.Close()
	metrics := writer.Metrics()
	if !metrics.Degraded || metrics.WriteFailures == 0 {
		t.Fatalf("activity health = %+v, want degraded writer failure", metrics)
	}
}
