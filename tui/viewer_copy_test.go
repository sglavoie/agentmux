package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/transcript"
)

func runViewerCopyCmd(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	if cmd == nil {
		t.Fatal("copy command is nil")
	}

	prev := copyToClipboard
	var copied []string
	copyToClipboard = func(text string) error {
		copied = append(copied, text)
		return nil
	}
	t.Cleanup(func() { copyToClipboard = prev })

	msg := cmd()
	done, ok := msg.(copyDoneMsg)
	if !ok {
		t.Fatalf("copy command returned %T, want copyDoneMsg", msg)
	}
	if done.err != nil {
		t.Fatalf("copy command err = %v, want nil", done.err)
	}
	if len(copied) != 1 {
		t.Fatalf("copy calls = %d, want 1", len(copied))
	}
	return copied[0]
}

func TestSessionViewerYCopiesSelectedMessage(t *testing.T) {
	m := Model{
		mode: ModeSessionView,
		viewerMessages: []transcript.Message{
			{Role: "user", Text: "first"},
			{Role: "assistant", Text: "selected"},
		},
		viewerCursor: 1,
		width:        80,
		height:       20,
	}

	updated, cmd := m.Update(keyPress("y"))
	got := updated.(Model)
	if got.mode != ModeSessionView {
		t.Fatalf("mode = %v, want ModeSessionView", got.mode)
	}
	if copied := runViewerCopyCmd(t, cmd); copied != "selected" {
		t.Fatalf("copied = %q, want selected message text", copied)
	}
}

func TestSessionViewerYCopiesBackingMultilineText(t *testing.T) {
	raw := "first line\nsecond\tline   still raw"
	if collapsed := collapseWhitespace(raw); collapsed == raw {
		t.Fatal("setup: raw text should differ from collapsed rendered text")
	}
	m := Model{
		mode: ModeSessionView,
		viewerMessages: []transcript.Message{
			{Role: "user", Text: raw},
		},
		viewerCursor: 0,
		width:        24,
		height:       20,
	}

	updated, cmd := m.Update(keyPress("y"))
	got := updated.(Model)
	if got.mode != ModeSessionView {
		t.Fatalf("mode = %v, want ModeSessionView", got.mode)
	}
	if copied := runViewerCopyCmd(t, cmd); copied != raw {
		t.Fatalf("copied = %q, want raw Message.Text %q", copied, raw)
	}
}

func TestSessionViewerYNoOpForEmptyOrInvalidSelection(t *testing.T) {
	cases := []struct {
		name string
		m    Model
	}{
		{
			name: "empty",
			m:    Model{mode: ModeSessionView},
		},
		{
			name: "cursor below zero",
			m: Model{
				mode:           ModeSessionView,
				viewerMessages: []transcript.Message{{Text: "present"}},
				viewerCursor:   -1,
			},
		},
		{
			name: "cursor past end",
			m: Model{
				mode:           ModeSessionView,
				viewerMessages: []transcript.Message{{Text: "present"}},
				viewerCursor:   1,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			updated, cmd := tc.m.Update(keyPress("y"))
			got := updated.(Model)
			if got.mode != ModeSessionView {
				t.Fatalf("mode = %v, want ModeSessionView", got.mode)
			}
			if cmd != nil {
				t.Fatalf("cmd = %T, want nil", cmd)
			}
		})
	}
}

func TestSessionSearchCopyPathCopiesSelectedTranscriptPath(t *testing.T) {
	m := Model{
		mode: ModeSessionSearch,
		sessionResults: []transcript.Result{
			{Meta: transcript.SessionMeta{Path: "/tmp/first.jsonl"}},
			{Meta: transcript.SessionMeta{Path: "/tmp/selected.jsonl"}},
		},
		sessionCursor: 1,
		width:         80,
		height:        20,
	}

	updated, cmd := m.Update(keyPress("Y"))
	got := updated.(Model)
	if got.mode != ModeSessionSearch {
		t.Fatalf("mode = %v, want ModeSessionSearch", got.mode)
	}
	if copied := runViewerCopyCmd(t, cmd); copied != "/tmp/selected.jsonl" {
		t.Fatalf("copied = %q, want selected transcript path", copied)
	}
}

func TestSessionSearchCopyPathNoOpForEmptyOrInvalidSelection(t *testing.T) {
	cases := []struct {
		name string
		m    Model
	}{
		{
			name: "empty results",
			m:    Model{mode: ModeSessionSearch},
		},
		{
			name: "cursor below zero",
			m: Model{
				mode:           ModeSessionSearch,
				sessionResults: []transcript.Result{{Meta: transcript.SessionMeta{Path: "/tmp/present.jsonl"}}},
				sessionCursor:  -1,
			},
		},
		{
			name: "cursor past end",
			m: Model{
				mode:           ModeSessionSearch,
				sessionResults: []transcript.Result{{Meta: transcript.SessionMeta{Path: "/tmp/present.jsonl"}}},
				sessionCursor:  1,
			},
		},
		{
			name: "empty path",
			m: Model{
				mode:           ModeSessionSearch,
				sessionResults: []transcript.Result{{Meta: transcript.SessionMeta{Path: ""}}},
				sessionCursor:  0,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			updated, cmd := tc.m.Update(keyPress("Y"))
			got := updated.(Model)
			if got.mode != ModeSessionSearch {
				t.Fatalf("mode = %v, want ModeSessionSearch", got.mode)
			}
			if cmd != nil {
				t.Fatalf("cmd = %T, want nil", cmd)
			}
		})
	}
}

func TestSessionSearchCopyPathIgnoredDuringTextEntry(t *testing.T) {
	m := Model{
		mode:              ModeSessionSearch,
		sessionSearchMode: true,
		sessionQuery:      "path:",
		sessionResults: []transcript.Result{
			{Meta: transcript.SessionMeta{Path: "/tmp/selected.jsonl"}},
		},
		sessionCursor: 0,
		width:         80,
		height:        20,
	}

	updated, cmd := m.Update(keyPress("Y"))
	got := updated.(Model)
	if got.sessionQuery != "path:Y" {
		t.Fatalf("sessionQuery = %q, want typed uppercase Y appended", got.sessionQuery)
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want search dispatch command")
	}
}

func TestSessionSearchCopyIDCopiesSelectedSessionID(t *testing.T) {
	const selectedID = "019f8b51-a61b-7531-95da-94149ba2852e"
	m := Model{
		mode: ModeSessionSearch,
		sessionResults: []transcript.Result{
			{Meta: transcript.SessionMeta{ID: "first-id", Path: "/tmp/first.jsonl"}},
			{Meta: transcript.SessionMeta{
				ID:   selectedID,
				Path: "/Users/sglavoie/.codex/sessions/2026/07/22/rollout-2026-07-22T13-33-31-" + selectedID + ".jsonl",
			}},
		},
		sessionCursor: 1,
		width:         80,
		height:        20,
	}

	updated, cmd := m.Update(keyPress("i"))
	got := updated.(Model)
	if got.mode != ModeSessionSearch {
		t.Fatalf("mode = %v, want ModeSessionSearch", got.mode)
	}
	if copied := runViewerCopyCmd(t, cmd); copied != selectedID {
		t.Fatalf("copied = %q, want selected session ID %q", copied, selectedID)
	}
}

func TestSessionSearchCopyIDNoOpForEmptyOrInvalidSelection(t *testing.T) {
	cases := []struct {
		name string
		m    Model
	}{
		{
			name: "empty results",
			m:    Model{mode: ModeSessionSearch},
		},
		{
			name: "cursor below zero",
			m: Model{
				mode:           ModeSessionSearch,
				sessionResults: []transcript.Result{{Meta: transcript.SessionMeta{ID: "present"}}},
				sessionCursor:  -1,
			},
		},
		{
			name: "cursor past end",
			m: Model{
				mode:           ModeSessionSearch,
				sessionResults: []transcript.Result{{Meta: transcript.SessionMeta{ID: "present"}}},
				sessionCursor:  1,
			},
		},
		{
			name: "empty ID",
			m: Model{
				mode:           ModeSessionSearch,
				sessionResults: []transcript.Result{{Meta: transcript.SessionMeta{Path: "/tmp/present.jsonl"}}},
				sessionCursor:  0,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			updated, cmd := tc.m.Update(keyPress("i"))
			got := updated.(Model)
			if got.mode != ModeSessionSearch {
				t.Fatalf("mode = %v, want ModeSessionSearch", got.mode)
			}
			if cmd != nil {
				t.Fatalf("cmd = %T, want nil", cmd)
			}
		})
	}
}

func TestSessionSearchCopyIDIgnoredDuringTextEntry(t *testing.T) {
	m := Model{
		mode:              ModeSessionSearch,
		sessionSearchMode: true,
		sessionQuery:      "session:",
		sessionResults: []transcript.Result{
			{Meta: transcript.SessionMeta{ID: "selected"}},
		},
		sessionCursor: 0,
		width:         80,
		height:        20,
	}

	updated, cmd := m.Update(keyPress("i"))
	got := updated.(Model)
	if got.sessionQuery != "session:i" {
		t.Fatalf("sessionQuery = %q, want typed i appended", got.sessionQuery)
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want search dispatch command")
	}
}

func TestSessionViewerCopyPathCopiesTranscriptPath(t *testing.T) {
	m := Model{
		mode:          ModeSessionView,
		viewerSession: &transcript.SessionMeta{Path: "/tmp/current.jsonl"},
		width:         80,
		height:        20,
	}

	updated, cmd := m.Update(keyPress("Y"))
	got := updated.(Model)
	if got.mode != ModeSessionView {
		t.Fatalf("mode = %v, want ModeSessionView", got.mode)
	}
	if copied := runViewerCopyCmd(t, cmd); copied != "/tmp/current.jsonl" {
		t.Fatalf("copied = %q, want current transcript path", copied)
	}
}

func TestSessionViewerCopyPathNoOpForNilOrEmptyPath(t *testing.T) {
	cases := []struct {
		name string
		m    Model
	}{
		{
			name: "nil session",
			m:    Model{mode: ModeSessionView},
		},
		{
			name: "empty path",
			m: Model{
				mode:          ModeSessionView,
				viewerSession: &transcript.SessionMeta{},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			updated, cmd := tc.m.Update(keyPress("Y"))
			got := updated.(Model)
			if got.mode != ModeSessionView {
				t.Fatalf("mode = %v, want ModeSessionView", got.mode)
			}
			if cmd != nil {
				t.Fatalf("cmd = %T, want nil", cmd)
			}
		})
	}
}
