package session

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func TestSessionShowPrintsOneConversationBySelector(t *testing.T) {
	resetSessionCommandsTestState(t)
	oldOut := sessionShowCmd.OutOrStdout()
	t.Cleanup(func() {
		sessionShowCmd.SetOut(oldOut)
		sessionShowCmd.SetErr(oldOut)
		_ = sessionShowCmd.Flags().Set("json", "false")
	})
	base, indexPath := sessionCommandMeta(t, harness.Codex)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	sessionCommandsNow = func() time.Time { return now }
	meta := base
	meta.ID = "7f67dc81-aaaa"
	meta.Snippet = "fix the\nparser"
	meta.Title = "Parser fix"
	meta.Ending = "The parser\nnow accepts tabs."
	meta.MessageCount = 4
	meta.StartedAt, meta.LastAt = now.Add(-3*time.Hour), now.Add(-2*time.Hour)
	writeSessionCommandIndex(t, indexPath, meta)
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Codex}}}, nil
	}
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Codex: sessionCommandReader{harness: harness.Codex, discovered: []transcript.SessionMeta{meta}}}
	}
	run := func(jsonOutput bool, args ...string) (string, error) {
		_ = sessionShowCmd.Flags().Set("json", map[bool]string{true: "true", false: "false"}[jsonOutput])
		var out bytes.Buffer
		sessionShowCmd.SetOut(&out)
		sessionShowCmd.SetErr(&out)
		err := sessionShowCmd.RunE(sessionShowCmd, args)
		return out.String(), err
	}

	out, err := run(false, "7f67")
	if err != nil {
		t.Fatalf("show: %v\n%s", err, out)
	}
	for _, want := range []string{
		"session:    codex:7f67dc81-aaaa\n",
		"directory:  " + meta.CWD + "\n",
		"(2h ago)\n",
		"messages:   4\n",
		"transcript: " + meta.Path + "\n",
		"title:      Parser fix\n",
		"summary:    fix the parser\n",
		"ended:      The parser now accepts tabs.\n",
		"continue:   amx session continue codex:7f67dc81-aaaa\n",
		"fork:       amx session fork codex:7f67dc81-aaaa\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("show output missing %q:\n%s", want, out)
		}
	}

	out, err = run(true, "codex:7f67dc81-aaaa")
	if err != nil {
		t.Fatalf("show --json: %v", err)
	}
	var envelope struct {
		SchemaVersion int             `json:"schema_version"`
		Data          sessionShowData `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if envelope.SchemaVersion != 1 || envelope.Data.Session.ID != meta.ID || envelope.Data.Session.CWD != meta.CWD {
		t.Fatalf("envelope = %+v", envelope)
	}

	if strings.Contains(out, "live:") || envelope.Data.Session.Live != nil {
		t.Fatalf("show claimed a live tab while the daemon and Kitty could not be asked:\n%s", out)
	}

	// With the lookup answering, show says whether continue would focus a tab
	// or resume, and --json carries the tab.
	sessionCommandsLiveConversations = func() (map[string]liveConversation, bool) {
		return map[string]liveConversation{"codex:7f67dc81-aaaa": {WindowID: 12, TabID: 3}}, true
	}
	if out, err = run(false, "7f67"); err != nil || !strings.Contains(out, "live:       open in Kitty window 12 (continue focuses it)\n") {
		t.Fatalf("show with a live tab = %v:\n%s", err, out)
	}
	if out, err = run(true, "7f67"); err != nil || !strings.Contains(out, `"live":{"window_id":12,"tab_id":3}`) {
		t.Fatalf("show --json with a live tab = %v:\n%s", err, out)
	}
	sessionCommandsLiveConversations = func() (map[string]liveConversation, bool) { return map[string]liveConversation{}, true }
	if out, err = run(false, "7f67"); err != nil || !strings.Contains(out, "live:       no\n") {
		t.Fatalf("show with no live tab = %v:\n%s", err, out)
	}

	if _, err := run(false, "ffffffff"); err == nil || !strings.Contains(err.Error(), "amx session list") {
		t.Fatalf("miss = %v, want the not-found hint", err)
	}
}

// A conversation two profiles share lists both under a plural label; one
// profile keeps the singular.
func TestSessionShowPluralizesSharedProfiles(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	view := sessionView{Harness: "claude", ID: "abc", Profiles: []string{"personal", "work"}, Projects: []string{"api"}}
	if err := writeSessionShow(&out, view, now, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"profiles:   personal, work\n", "project:    api\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("show output missing %q:\n%s", want, out.String())
		}
	}
}

// --last reads the transcript and appends its closing user and assistant
// messages, oldest first; tool records and empty messages are not counted.
func TestSessionShowLastPrintsTheClosingMessages(t *testing.T) {
	resetSessionCommandsTestState(t)
	oldOut := sessionShowCmd.OutOrStdout()
	t.Cleanup(func() {
		sessionShowCmd.SetOut(oldOut)
		sessionShowCmd.SetErr(oldOut)
		_ = sessionShowCmd.Flags().Set("json", "false")
		_ = sessionShowCmd.Flags().Set("last", "0")
		_ = sessionShowCmd.Flags().Set("find", "")
		_ = sessionShowCmd.Flags().Set("match", "1")
		_ = sessionShowCmd.Flags().Set("from", "-1")
		_ = sessionShowCmd.Flags().Set("count", "50")
		_ = sessionShowCmd.Flags().Set("at", "")
	})
	base, indexPath := sessionCommandMeta(t, harness.Codex)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	sessionCommandsNow = func() time.Time { return now }
	meta := base
	meta.ID = "7f67dc81-aaaa"
	writeSessionCommandIndex(t, indexPath, meta)
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	sessionCommandsLoadConfig = func() (*config.Config, error) {
		return &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Codex}}}, nil
	}
	messages := map[string][]transcript.Message{meta.Path: {
		{Role: "user", Text: "fix the parser", Timestamp: now.Add(-3 * time.Hour)},
		{Role: "tool", Text: "go test ./..."},
		{Role: "assistant", Text: "Fixed it.\n\nTests pass.", Timestamp: now.Add(-2 * time.Hour)},
		{Role: "assistant", Text: "  "},
		{Role: "user", Text: "commit it"},
	}}
	sessionCommandsSessionReaders = func() map[string]transcript.Reader {
		return map[string]transcript.Reader{harness.Codex: searchCommandReader{
			sessionCommandReader{harness: harness.Codex, discovered: []transcript.SessionMeta{meta}}, messages,
		}}
	}
	run := func(jsonOutput bool, last string) (string, error) {
		_ = sessionShowCmd.Flags().Set("json", map[bool]string{true: "true", false: "false"}[jsonOutput])
		_ = sessionShowCmd.Flags().Set("last", last)
		var out bytes.Buffer
		sessionShowCmd.SetOut(&out)
		sessionShowCmd.SetErr(&out)
		err := sessionShowCmd.RunE(sessionShowCmd, []string{"7f67"})
		return out.String(), err
	}

	out, err := run(false, "2")
	if err != nil {
		t.Fatalf("show --last 2: %v\n%s", err, out)
	}
	want := "\nlast 2 of 3 user and assistant messages:\n" +
		"\n[assistant] " + formatTime(now.Add(-2*time.Hour)) + " (2h ago)\nFixed it.\n\nTests pass.\n" +
		"\n[user]\ncommit it\n"
	if !strings.HasSuffix(out, want) {
		t.Fatalf("show --last 2 output ends with:\n%s\nwant suffix:\n%s", out, want)
	}

	if out, err = run(false, "10"); err != nil || !strings.Contains(out, "\nall 3 user and assistant messages:\n\n[user] ") {
		t.Fatalf("show --last 10 = %v:\n%s", err, out)
	}

	out, err = run(true, "1")
	if err != nil {
		t.Fatalf("show --last 1 --json: %v", err)
	}
	var envelope struct {
		Data sessionShowData `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	got := envelope.Data
	if got.MessagesTotal == nil || *got.MessagesTotal != 3 || len(got.Messages) != 1 || got.Messages[0].Text != "commit it" || got.Messages[0].Role != "user" {
		t.Fatalf("--json tail = %+v", got)
	}
	if strings.Contains(out, `"timestamp"`) {
		t.Fatalf("a message without a time carried a zero timestamp:\n%s", out)
	}

	// Without --last the JSON payload stays exactly as it was.
	if out, err = run(true, "0"); err != nil || strings.Contains(out, "messages_total") || strings.Contains(out, `"messages":[`) {
		t.Fatalf("show --json without --last = %v:\n%s", err, out)
	}

	if _, err := run(false, "-1"); err == nil || !strings.Contains(err.Error(), "--last -1") {
		t.Fatalf("show --last -1 = %v; want a refusal", err)
	}

	// A search result before the tail opens directly, with message ordinals
	// that a desktop client can use to page and navigate without guessing.
	for range 120 {
		messages[meta.Path] = append(messages[meta.Path], transcript.Message{Role: "assistant", Text: "Later reply"})
	}
	_ = sessionShowCmd.Flags().Set("find", `"fix the parser"`)
	out, err = run(true, "0")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatal(err)
	}
	got = envelope.Data
	if got.MessagesStart == nil || *got.MessagesStart != 0 || got.FocusMessage == nil || *got.FocusMessage != 0 || len(got.Messages) != 50 || len(got.MatchMessages) != 1 {
		t.Fatalf("match window: %+v", got)
	}
	if len(got.MatchTerms) != 1 || got.MatchTerms[0] != "fix the parser" {
		t.Fatalf("phrase terms: %v", got.MatchTerms)
	}
	if _, err := run(true, "10"); err == nil {
		t.Fatal("--last and --find must conflict")
	}
	_ = sessionShowCmd.Flags().Set("from", "50")
	out, err = run(true, "0")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatal(err)
	}
	if *envelope.Data.MessagesStart != 50 || len(envelope.Data.Messages) != 50 {
		t.Fatalf("page: %s", out)
	}
	_ = sessionShowCmd.Flags().Set("find", "")
	_ = sessionShowCmd.Flags().Set("from", "-1")
	_ = sessionShowCmd.Flags().Set("at", now.Format(time.RFC3339Nano))
	out, err = run(true, "0")
	envelope.Data = sessionShowData{}
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.FocusMessage == nil || *envelope.Data.FocusMessage != 1 || len(envelope.Data.Messages) != 50 {
		t.Fatalf("event location: %s", out)
	}
	for _, flag := range []string{"find", "from", "last"} {
		value := "needle"
		if flag != "find" {
			value = "1"
		}
		_ = sessionShowCmd.Flags().Set(flag, value)
		last := "0"
		if flag == "last" {
			last = "1"
		}
		out, err = run(true, last)
		if err == nil || !strings.Contains(out, "invalid_arguments") {
			t.Fatalf("--at with --%s: %v %s", flag, err, out)
		}
		reset := ""
		if flag == "from" {
			reset = "-1"
		}
		if flag == "last" {
			reset = "0"
		}
		_ = sessionShowCmd.Flags().Set(flag, reset)
	}
	_ = sessionShowCmd.Flags().Set("at", "not-a-time")
	if out, err = run(true, "0"); err == nil || !strings.Contains(out, "invalid_arguments") {
		t.Fatalf("invalid event time: %v %s", err, out)
	}

}

// Each way of choosing messages wrongly is named on its own, --at included,
// rather than one message restating every rule.
func TestSessionShowNamesEachMessageFlagMistake(t *testing.T) {
	t.Parallel()

	type flags struct {
		last          int
		query         string
		match, from   int
		count         int
		at            string
		wantErrSubstr string
	}
	ok := flags{match: 1, from: -1, count: 20}
	for name, tc := range map[string]flags{
		"negative last":      {last: -1, match: 1, from: -1, count: 20, wantErrSubstr: "--last -1"},
		"negative from":      {match: 1, from: -2, count: 20, wantErrSubstr: "--from -2"},
		"count too large":    {match: 1, from: -1, count: 501, wantErrSubstr: "--count must be from 1 to 500, got 501"},
		"count zero":         {match: 1, from: -1, count: 0, wantErrSubstr: "--count must be from 1 to 500, got 0"},
		"match zero":         {query: "x", match: 0, from: -1, count: 20, wantErrSubstr: "--match 0"},
		"match without find": {match: 2, from: -1, count: 20, wantErrSubstr: "--match picks among the matches of --find"},
		"last with find":     {last: 3, query: "x", match: 1, from: -1, count: 20, wantErrSubstr: "--last cannot be combined with --find or --from"},
		"last with from":     {last: 3, match: 1, from: 0, count: 20, wantErrSubstr: "--last cannot be combined"},
		"at with find":       {query: "x", match: 1, from: -1, count: 20, at: "2026-10-24T09:30:00Z", wantErrSubstr: "--at cannot be combined with --find"},
		"at with from+last":  {last: 1, match: 1, from: 0, count: 20, at: "2026-10-24T09:30:00Z", wantErrSubstr: "--at cannot be combined with --from or --last"},
		"at unparseable":     {match: 1, from: -1, count: 20, at: "yesterday", wantErrSubstr: `--at "yesterday": want an RFC 3339 time`},
	} {
		_, err := sessionShowMessageFlags(tc.last, tc.query, tc.match, tc.from, tc.count, tc.at)
		if err == nil || !strings.Contains(err.Error(), tc.wantErrSubstr) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.wantErrSubstr)
		}
	}
	at, err := sessionShowMessageFlags(ok.last, ok.query, ok.match, ok.from, ok.count, "2026-10-24T09:30:00Z")
	if err != nil || !at.Equal(time.Date(2026, 10, 24, 9, 30, 0, 0, time.UTC)) {
		t.Fatalf("valid --at: %v %v", at, err)
	}
	if _, err := sessionShowMessageFlags(0, "x", 2, 5, 20, ""); err != nil {
		t.Fatalf("--find with --from and --match: %v", err)
	}
}

func TestConversationMatchWindowsKeepProseIdentityAndBoundaries(t *testing.T) {
	t.Parallel()

	messages := []transcript.Message{
		{Role: "tool", Text: "needle"},
		{Role: "user", Text: "first Needle"},
		{Role: "assistant", Text: "middle"},
		{Role: "assistant", Text: "last NEEDLE"},
	}
	window := conversationMessageWindow(messages, "needle", 2, -1, 2)
	if window.Total != 3 || window.Start != 1 || *window.Focus != 2 || len(window.Matches) != 2 || window.Messages[1].Text != "last NEEDLE" {
		t.Fatalf("window = %+v", window)
	}
	missing := conversationMessageWindow(messages, "absent", 1, -1, 50)
	if missing.Focus != nil || len(missing.Matches) != 0 || len(missing.Messages) != 3 {
		t.Fatalf("missing = %+v", missing)
	}
	end := conversationMessageWindow(messages, "needle", 99, 100, 50)
	if end.Start != 3 || len(end.Messages) != 0 || *end.Focus != 2 {
		t.Fatalf("end = %+v", end)
	}
}

func TestConversationEventWindowLocatesOldSupportingOutput(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	messages := []transcript.Message{
		{Role: "assistant", Text: "older", Timestamp: at.Add(-time.Hour)},
		{Role: "assistant", Text: "nearest", Timestamp: at},
		{Role: "user", Text: "not supporting assistant output", Timestamp: at},
		{Role: "assistant", Text: "no timestamp"},
	}
	for range 200 {
		messages = append(messages, transcript.Message{Role: "assistant", Text: "later", Timestamp: at.Add(time.Hour)})
	}
	window := conversationMessageAt(messages, at, 50)
	if window.Focus == nil || *window.Focus != 1 || window.Start != 0 || len(window.Messages) != 50 || window.Total != 204 {
		t.Fatalf("event window: %+v", window)
	}
	missing := conversationMessageAt(messages, at.Add(-2*time.Hour), 50)
	if missing.Focus != nil || len(missing.Messages) != 50 {
		t.Fatalf("must not attribute newer or untimestamped prose to an event: %+v", missing)
	}
	// Capture ordering does not always equal event time ordering.
	messages = append(messages, transcript.Message{Role: "assistant", Text: "late older record", Timestamp: at.Add(-time.Minute)})
	if got := conversationMessageAt(messages, at, 2); got.Focus == nil || *got.Focus != 1 {
		t.Fatalf("wrong chronological anchor: %+v", got)
	}
}
