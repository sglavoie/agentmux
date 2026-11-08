package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func TestConversationIdentityAcceptsBothSelectorShapes(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"codex", "session-1"},
		{"codex:session-1"},
		{" Codex:session-1"},
	} {
		identity, err := conversationIdentityFromArgs(sessionContinueCmd, false, "continue", args)
		if err != nil {
			t.Fatalf("args %q: %v", args, err)
		}
		if identity.Harness != harness.Codex || identity.SessionID != "session-1" {
			t.Fatalf("args %q: identity = %+v", args, identity)
		}
	}
	if _, err := conversationIdentityFromArgs(sessionContinueCmd, false, "continue", []string{"codex:"}); err == nil {
		t.Fatal("an empty session ID after the colon must be refused")
	}
}

// A command run with no selector at all asks for the conversation rather than
// for a harness, since a bare session ID or prefix no longer needs one.
func TestMissingSelectorAsksForTheConversation(t *testing.T) {
	t.Parallel()

	_, err := conversationIdentityFromArgs(sessionContinueCmd, false, "continue", nil)
	if err == nil || !strings.HasPrefix(err.Error(), "conversation is required: ") {
		t.Fatalf("text error = %v, want conversation is required with its selector forms", err)
	}
	for _, want := range []string{"<harness>:<session-id>", "unique prefix", "`amx session list`"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("text error = %v, want it to name %q", err, want)
		}
	}

	var out bytes.Buffer
	sessionShowCmd.SetOut(&out)
	t.Cleanup(func() { sessionShowCmd.SetOut(nil) })
	if _, err := conversationIdentityFromArgs(sessionShowCmd, true, "show", []string{" "}); err == nil {
		t.Fatal("a blank selector must be refused")
	}
	var payload cli.JSONErrorEnvelope
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("JSON = %v\n%s", err, out.String())
	}
	if payload.Error.Code != "missing_parameter" || len(payload.Error.Parameters) != 1 || payload.Error.Parameters[0].Name != "conversation" {
		t.Fatalf("payload = %+v, want missing_parameter naming conversation", payload.Error)
	}
	if !strings.Contains(payload.Error.Parameters[0].Description, "unique prefix") {
		t.Fatalf("description = %q, want every selector form named", payload.Error.Parameters[0].Description)
	}
}

func TestParseSinceWindow(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]time.Duration{"7d": 7 * 24 * time.Hour, "12h": 12 * time.Hour, " 90m ": 90 * time.Minute, "2w": 14 * 24 * time.Hour, "106751d": 106751 * 24 * time.Hour, "15250w": 15250 * 7 * 24 * time.Hour} {
		if got, err := parseSinceWindow(in); err != nil || got != want {
			t.Errorf("parseSinceWindow(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "0d", "-1d", "1.5d", "-2h", "week", "0w", "1.5w", "106752d", "15251w", "213504d"} {
		if _, err := parseSinceWindow(bad); err == nil {
			t.Errorf("parseSinceWindow(%q) accepted a bad window", bad)
		}
	}
}

func TestBareSessionIDResolvesAcrossHarnessesAndRefusesToGuess(t *testing.T) {
	resetSessionCommandsTestState(t)
	codexMeta, indexPath := sessionCommandMeta(t, harness.Codex)
	claudeMeta := codexMeta
	claudeMeta.Harness = harness.Claude
	uniqueMeta := codexMeta
	uniqueMeta.ID = "only-codex"
	writeSessionCommandIndex(t, indexPath, codexMeta, claudeMeta, uniqueMeta)
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Codex}, {Name: "home", Harness: harness.Claude}}}
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	readers := map[string]transcript.Reader{
		harness.Codex:  sessionCommandReader{harness: harness.Codex, discovered: []transcript.SessionMeta{codexMeta, uniqueMeta}},
		harness.Claude: sessionCommandReader{harness: harness.Claude, discovered: []transcript.SessionMeta{claudeMeta}},
	}

	identity, err := conversationIdentityFromArgs(sessionContinueCmd, false, "continue", []string{"only-codex"})
	if err != nil || identity.Harness != "" || identity.SessionID != "only-codex" {
		t.Fatalf("bare ID identity = %+v, %v", identity, err)
	}
	meta, err := resolveIndexedConversation(cfg, readers, identity)
	if err != nil || meta == nil || meta.Harness != harness.Codex || meta.ID != "only-codex" {
		t.Fatalf("unique bare ID resolved to %+v, %v", meta, err)
	}

	_, err = resolveIndexedConversation(cfg, readers, conversation.Identity{SessionID: "session-1"})
	var actionErr *actions.Error
	if !errors.As(err, &actionErr) || actionErr.Code != "ambiguous_conversation" {
		t.Fatalf("shared bare ID error = %v, want ambiguous_conversation", err)
	}
	var ids []string
	for _, candidate := range actionErr.Candidates {
		ids = append(ids, candidate.ID)
	}
	if got := strings.Join(ids, ","); got != "claude:session-1,codex:session-1" {
		t.Fatalf("candidates = %s, want ordered harness-qualified selectors", got)
	}

	if meta, err := resolveIndexedConversation(cfg, readers, conversation.Identity{SessionID: "missing"}); err != nil || meta != nil {
		t.Fatalf("unknown bare ID = %+v, %v; want a plain miss", meta, err)
	}

	// A lone harness name is still a harness missing its session ID.
	if _, err := conversationIdentityFromArgs(sessionContinueCmd, false, "continue", []string{"Codex"}); err == nil {
		t.Fatal("a lone harness name must report the missing session ID")
	}
}

func TestSessionIDPrefixResolvesLikeAShortGitHash(t *testing.T) {
	resetSessionCommandsTestState(t)
	base, indexPath := sessionCommandMeta(t, harness.Codex)
	meta := func(harnessName, id string) transcript.SessionMeta {
		m := base
		m.Harness, m.ID = harnessName, id
		return m
	}
	codex := []transcript.SessionMeta{meta(harness.Codex, "7f67dc81-aaaa"), meta(harness.Codex, "7f67dc81-bbbb"), meta(harness.Codex, "abcd")}
	claude := []transcript.SessionMeta{meta(harness.Claude, "7f6799ee"), meta(harness.Claude, "abcdef")}
	writeSessionCommandIndex(t, indexPath, append(append([]transcript.SessionMeta{}, codex...), claude...)...)
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Codex}, {Name: "home", Harness: harness.Claude}}}
	sessionCommandsSessionIndexPath = func() string { return indexPath }
	readers := map[string]transcript.Reader{
		harness.Codex:  sessionCommandReader{harness: harness.Codex, discovered: codex},
		harness.Claude: sessionCommandReader{harness: harness.Claude, discovered: claude},
	}

	for _, tc := range []struct {
		name, harness, id, want string
	}{
		{"unique prefix", "", "7f67dc81-a", "codex:7f67dc81-aaaa"},
		{"prefix within a harness", harness.Claude, "7f67", "claude:7f6799ee"},
		{"an exact ID beats a longer one it prefixes", "", "abcd", "codex:abcd"},
		{"the harness still scopes the prefix", harness.Claude, "abcd", "claude:abcdef"},
		{"too short to be a prefix", "", "7f6", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveIndexedConversation(cfg, readers, conversation.Identity{Harness: tc.harness, SessionID: tc.id})
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			selector := ""
			if got != nil {
				selector = got.Harness + ":" + got.ID
			}
			if selector != tc.want {
				t.Fatalf("resolved %q, want %q", selector, tc.want)
			}
		})
	}

	_, err := resolveIndexedConversation(cfg, readers, conversation.Identity{SessionID: "7f67"})
	var actionErr *actions.Error
	if !errors.As(err, &actionErr) || actionErr.Code != "ambiguous_conversation" {
		t.Fatalf("shared prefix error = %v, want ambiguous_conversation", err)
	}
	var ids []string
	for _, candidate := range actionErr.Candidates {
		ids = append(ids, candidate.ID)
	}
	if got := strings.Join(ids, ","); got != "claude:7f6799ee,codex:7f67dc81-aaaa,codex:7f67dc81-bbbb" {
		t.Fatalf("candidates = %s", got)
	}
	if len(actionErr.MissingParameters) != 1 || actionErr.MissingParameters[0].Name != "session-id" {
		t.Fatalf("missing parameters = %+v, want a longer session-id", actionErr.MissingParameters)
	}

	if !actionErr.MissingParameters[0].Positional {
		t.Fatal("the session ID is an argument, not a flag; its hint must say so")
	}

	// A miss says what to do next: a too-short prefix is named as such, and
	// anything else points at the list.
	for id, hint := range map[string]string{"7f6": "at least 4 characters", "ffffffff": "amx session list"} {
		_, err := resolveConversationOrFail(sessionContinueCmd, false, cfg, readers, conversation.Identity{SessionID: id})
		if err == nil || !strings.Contains(err.Error(), hint) {
			t.Fatalf("miss for %q = %v, want hint %q", id, err, hint)
		}
	}
}
