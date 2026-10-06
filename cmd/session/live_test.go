package session

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// A conversation is live only where `session continue` would focus it: the
// daemon names it and Kitty still shows that window owning its tab. A stale
// window, a conversation two tabs claim, and a daemon or Kitty that cannot be
// asked all leave it out, the last two reported as unknown.
func TestLiveConversationsFollowContinuesRule(t *testing.T) {
	open := conversation.Identity{Harness: "claude", SessionID: "open-1"}
	closed := conversation.Identity{Harness: "codex", SessionID: "closed-1"}
	twice := conversation.Identity{Harness: "codex", SessionID: "twice-1"}
	window := func(id int, harnessName string) kitty.Window {
		return kitty.Window{ID: id, IsActive: true, UserVars: kitty.UserVars{Profile: "work", Harness: harnessName}}
	}
	windows := []kitty.OSWindow{{Tabs: []kitty.Tab{
		{ID: 100, Title: "api", Windows: []kitty.Window{window(10, "claude")}},
		{ID: 200, Title: "web", Windows: []kitty.Window{window(20, "codex")}},
		{ID: 300, Title: "web", Windows: []kitty.Window{window(30, "codex")}},
	}}}
	states := map[int]protocol.WindowStateSnapshot{
		10: {Conversation: &open},
		// Window 40 is gone from Kitty: the daemon has not swept it yet.
		40: {Conversation: &closed},
		20: {Conversation: &twice},
		30: {Conversation: &twice},
	}
	daemon := func(protocol.Request) (protocol.Response, error) {
		return protocol.Response{OK: true, States: states}, nil
	}

	live, known := liveConversations(daemon, &sessionCommandKitty{windows: windows})
	if !known {
		t.Fatal("lookup reported unknown with both daemon and Kitty answering")
	}
	if len(live) != 1 || live["claude:open-1"] != (liveConversation{WindowID: 10, TabID: 100}) {
		t.Fatalf("live = %+v, want only claude:open-1 in window 10, tab 100", live)
	}

	down := func(protocol.Request) (protocol.Response, error) { return protocol.Response{}, errors.New("no daemon") }
	if live, known := liveConversations(down, &sessionCommandKitty{windows: windows}); known || live != nil {
		t.Fatalf("unreachable daemon = %+v, %t; want unknown", live, known)
	}
	rejected := func(protocol.Request) (protocol.Response, error) { return protocol.Response{OK: false}, nil }
	if _, known := liveConversations(rejected, &sessionCommandKitty{windows: windows}); known {
		t.Fatal("a rejected state query must read as unknown")
	}
}

// The LIVE column appears only when the lookup answered, so a table drawn
// without a daemon does not claim that nothing is open.
func TestSessionTableMarksLiveConversations(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	views := []sessionView{
		{Harness: "claude", Profile: "p", Project: "x", LastAt: now, ID: "aaaa1111", Snippet: "open one"},
		{Harness: "codex", Profile: "p", Project: "x", LastAt: now, ID: "bbbb2222", Snippet: "closed one"},
	}
	markLive(views, map[string]liveConversation{"claude:aaaa1111": {WindowID: 1, TabID: 2}}, 0)

	var out bytes.Buffer
	if err := writeSessionTable(&out, views, nil, now, 0, true); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if !strings.Contains(lines[0], " LIVE ") || !strings.Contains(lines[1], "just now  ●     aaaa1111") || strings.Contains(lines[2], "●") {
		t.Fatalf("table = %q, want a LIVE column marking only the open conversation", out.String())
	}

	out.Reset()
	if err := writeSessionTable(&out, views, nil, now, 0, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "LIVE") || strings.Contains(out.String(), "●") {
		t.Fatalf("table = %q, want no LIVE column when the lookup could not answer", out.String())
	}
}

// The conversation open in the window the command runs in reads "here" rather
// than a dot, in the table and in show, so the operator's own session is not
// mistaken for a sibling with the same opening prompt. Outside Kitty nothing
// is marked.
func TestLiveMarksTheConversationInThisWindow(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	live := map[string]liveConversation{
		"claude:aaaa1111": {WindowID: 7, TabID: 2},
		"claude:bbbb2222": {WindowID: 9, TabID: 3},
	}
	views := []sessionView{
		{Harness: "claude", Profile: "p", Project: "x", LastAt: now, ID: "aaaa1111", Snippet: "same prompt"},
		{Harness: "claude", Profile: "p", Project: "x", LastAt: now, ID: "bbbb2222", Snippet: "same prompt"},
	}
	markLive(views, live, 7)
	var out bytes.Buffer
	if err := writeSessionTable(&out, views, nil, now, 0, true); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if !strings.Contains(lines[1], "here") || !strings.Contains(lines[2], "●") || strings.Contains(lines[2], "here") {
		t.Fatalf("table = %q, want only window 7's conversation marked here", out.String())
	}
	if got, want := liveLine(views[0].Live), "open in this Kitty window (7)"; got != want {
		t.Fatalf("liveLine = %q; want %q", got, want)
	}
	if got := liveLine(views[1].Live); !strings.Contains(got, "window 9 (continue focuses it)") {
		t.Fatalf("liveLine for another window = %q", got)
	}

	markLive(views, live, 0)
	if views[0].Live.Here || views[1].Live.Here {
		t.Fatal("outside Kitty, no conversation may be marked here")
	}
}
