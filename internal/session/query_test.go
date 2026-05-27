package session_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatalf("loading fixture %s: %v", name, err)
	}
	return data
}

func newClientWithFixture(t *testing.T, fixture string) *kitty.SubprocessClient {
	t.Helper()
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ ls": loadFixture(t, fixture),
	})
	return kitty.NewClientWithSocket(runner, "")
}

func TestList_ReturnsManagedTabs(t *testing.T) {
	client := newClientWithFixture(t, "kitty_ls.json")
	sessions, err := session.List(client)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
}

func TestList_ExcludesUnmanagedTabs(t *testing.T) {
	client := newClientWithFixture(t, "kitty_ls_multi.json")
	sessions, err := session.List(client)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// 3 managed tabs, 1 unmanaged
	if len(sessions) != 3 {
		t.Fatalf("expected 3 managed sessions, got %d", len(sessions))
	}
	for _, s := range sessions {
		if s.Account == "" {
			t.Errorf("session %q has empty account — should be excluded", s.Title)
		}
	}
}

func TestList_UserVarOnlyDetection(t *testing.T) {
	// Account detection must use user vars only (no cmdline fallback).
	client := newClientWithFixture(t, "kitty_ls.json")
	sessions, err := session.List(client)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, s := range sessions {
		switch s.Base {
		case "P:dotfiles":
			if s.Account != "personal" {
				t.Errorf("P:dotfiles account: got %q, want %q", s.Account, "personal")
			}
			if s.Directory != "/Users/user/dotfiles" {
				t.Errorf("P:dotfiles dir: got %q", s.Directory)
			}
		case "W:api":
			if s.Account != "work" {
				t.Errorf("W:api account: got %q, want %q", s.Account, "work")
			}
			if s.Groups[0] != "backend" {
				t.Errorf("W:api groups: got %v, want [backend]", s.Groups)
			}
		default:
			t.Errorf("unexpected session base title %q", s.Base)
		}
	}
}

func TestList_EmptyKitty(t *testing.T) {
	client := newClientWithFixture(t, "kitty_ls_empty.json")
	sessions, err := session.List(client)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions for empty kitty, got %d", len(sessions))
	}
}

func TestFind_ByBaseTitle(t *testing.T) {
	client := newClientWithFixture(t, "kitty_ls.json")
	sessions, _ := session.List(client)

	s, found := session.Find(sessions, "W:api")
	if !found {
		t.Fatal("expected to find W:api")
	}
	if s.Account != "work" {
		t.Errorf("account: got %q, want %q", s.Account, "work")
	}
}

func TestListFromOSWindows_AndFindByWindowID(t *testing.T) {
	var osWindows []kitty.OSWindow
	if err := json.Unmarshal(loadFixture(t, "kitty_ls.json"), &osWindows); err != nil {
		t.Fatalf("Unmarshal fixture: %v", err)
	}

	sessions := session.ListFromOSWindows(osWindows)
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}

	rs, found := session.FindByWindowID(sessions, 5)
	if !found {
		t.Fatal("expected to find window 5")
	}
	if rs.Base != "W:api" || rs.Profile != "work" || rs.Harness != "claude" {
		t.Fatalf("session = %+v; want work/claude W:api", rs)
	}
}

func TestFind_NotFound(t *testing.T) {
	client := newClientWithFixture(t, "kitty_ls.json")
	sessions, _ := session.List(client)

	_, found := session.Find(sessions, "W:nonexistent")
	if found {
		t.Fatal("expected not to find W:nonexistent")
	}
}

func TestSessionKey_DifferentHarnessesDoNotCollide(t *testing.T) {
	claudeKey := session.SessionKey("claude-work", "api")
	copilotKey := session.SessionKey("copilot-work", "api")
	if claudeKey == copilotKey {
		t.Errorf("SessionKey collision: claude-work/api and copilot-work/api share key %q", claudeKey)
	}
}

func TestSessionKey_CaseInsensitive(t *testing.T) {
	a := session.SessionKey("Work", "API")
	b := session.SessionKey("work", "api")
	if a != b {
		t.Errorf("SessionKey case sensitivity: %q != %q", a, b)
	}
}
