package session_test

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
)

func TestListWithConfig_AnchoredWindow_NoGhostEmitted(t *testing.T) {
	fixture := []byte(`[
  {
    "id": 1,
    "is_active": true,
    "tabs": [
      {
        "id": 60,
        "is_active": true,
        "title": "api",
        "windows": [
          {
            "id": 61,
            "is_active": true,
            "cwd": "/projects/api",
            "user_vars": {
              "cc_account": "work",
              "cc_cwd": "/projects/api"
            }
          },
          {
            "id": 62,
            "is_active": false,
            "cwd": "/tmp",
            "user_vars": {}
          }
        ]
      }
    ]
  }
]`)
	client := newClientFromBytes(t, fixture)

	cfg := &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: "claude"}},
		Sessions: []config.Session{{Title: "api", Account: "work", Directory: "/projects/api"}},
	}

	sessions, ghosts, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session via normal detection, got %d", len(sessions))
	}
	if len(ghosts) != 0 {
		t.Errorf("expected 0 ghosts when anchor window is alive, got %d", len(ghosts))
	}
	if sessions[0].WindowID != 61 {
		t.Errorf("WindowID: got %d, want 61 (anchor)", sessions[0].WindowID)
	}
}

func TestList_PrefersActiveManagedWindowMetadata(t *testing.T) {
	fixture := []byte(`[
  {
    "id": 1,
    "is_active": true,
    "tabs": [
      {
        "id": 10,
        "is_active": true,
        "title": "split",
        "windows": [
          {
            "id": 11,
            "is_active": false,
            "cwd": "/tmp/stale",
            "user_vars": {
              "cc_account": "work",
              "cc_cwd": "/tmp/stale",
              "cc_flags": "--stale",
              "cc_groups": "stale"
            }
          },
          {
            "id": 12,
            "is_active": true,
            "cwd": "/tmp/active",
            "user_vars": {
              "cc_account": "work",
              "cc_cwd": "/tmp/active",
              "cc_flags": "--active",
              "cc_groups": "live"
            }
          }
        ]
      }
    ]
  }
]`)
	client := newClientFromBytes(t, fixture)

	sessions, err := session.List(client)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	got := sessions[0]
	if got.WindowID != 12 {
		t.Fatalf("WindowID: got %d, want 12", got.WindowID)
	}
	if got.Directory != "/tmp/active" {
		t.Errorf("Directory: got %q, want %q", got.Directory, "/tmp/active")
	}
	if got.Args != "--active" {
		t.Errorf("Args: got %q, want %q", got.Args, "--active")
	}
	if len(got.Groups) != 1 || got.Groups[0] != "live" {
		t.Errorf("Groups: got %v, want [live]", got.Groups)
	}
}
