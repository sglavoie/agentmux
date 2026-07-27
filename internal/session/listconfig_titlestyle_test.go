package session_test

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
)

func TestListWithConfig_TitleFallback_StripsStateIcon(t *testing.T) {
	fixture := []byte(`[
  {
    "id": 1,
    "is_active": true,
    "tabs": [
      {
        "id": 30,
        "is_active": true,
        "title": "⚡api",
        "windows": [
          {
            "id": 31,
            "is_active": true,
            "cwd": "/tmp",
            "foreground_processes": [
              {"cmdline": ["claude"], "cwd": "/tmp"}
            ],
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
	if len(sessions) != 1 || len(ghosts) != 1 {
		t.Fatalf("expected 1 session + 1 ghost, got %d/%d", len(sessions), len(ghosts))
	}
	rs := sessions[0]
	if rs.State != "bolt" {
		t.Errorf("State: got %q, want %q", rs.State, "bolt")
	}
	if rs.Base != "api" {
		t.Errorf("Base: got %q, want %q", rs.Base, "api")
	}
	if rs.Project != "api" {
		t.Errorf("Project: got %q, want %q", rs.Project, "api")
	}
}

func TestListWithConfig_TitleFallback_PreservesMuteIcon(t *testing.T) {
	fixture := []byte(`[
  {
    "id": 1,
    "is_active": true,
    "tabs": [
      {
        "id": 32,
        "is_active": true,
        "title": "🔇⚡api",
        "windows": [
          {
            "id": 33,
            "is_active": true,
            "cwd": "/tmp",
            "foreground_processes": [
              {"cmdline": ["claude"], "cwd": "/projects/api"}
            ],
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
	if len(sessions) != 1 || len(ghosts) != 1 {
		t.Fatalf("expected 1 session + 1 ghost, got %d/%d", len(sessions), len(ghosts))
	}
	rs := sessions[0]
	if !rs.Muted {
		t.Errorf("Muted: got false, want true")
	}
	if rs.State != "bolt" {
		t.Errorf("State: got %q, want %q", rs.State, "bolt")
	}
	if rs.Base != "api" {
		t.Errorf("Base: got %q, want %q", rs.Base, "api")
	}
}

func TestListWithConfig_TitleFallback_LegacyPrefixedTitle_UniqueMatch(t *testing.T) {
	fixture := []byte(`[
  {
    "id": 1,
    "is_active": true,
    "tabs": [
      {
        "id": 45,
        "is_active": true,
        "title": "W:api",
        "windows": [
          {
            "id": 46,
            "is_active": true,
            "cwd": "/tmp",
            "foreground_processes": [
              {"cmdline": ["claude"], "cwd": "/projects/api"}
            ],
            "user_vars": {}
          }
        ]
      }
    ]
  }
]`)
	client := newClientFromBytes(t, fixture)

	cfg := &config.Config{
		Accounts: []config.Account{
			{Name: "work", Harness: "claude"},
			{Name: "personal", Harness: "claude"},
		},
		Sessions: []config.Session{
			{Title: "api", Account: "work", Directory: "/projects/api"},
			{Title: "api", Account: "personal", Directory: "/projects/personal-api"},
		},
	}

	sessions, ghosts, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 1 || len(ghosts) != 1 {
		t.Fatalf("expected 1 session + 1 ghost, got %d/%d", len(sessions), len(ghosts))
	}
	if sessions[0].Profile != "work" || sessions[0].Project != "api" {
		t.Errorf("session: got %+v, want work/api", sessions[0])
	}
	if ghosts[0].Profile != "work" || ghosts[0].Project != "api" {
		t.Errorf("ghost: got %+v, want work/api", ghosts[0])
	}
}

func TestListWithConfig_TitleFallback_ExactTitleBeatsLegacyPrefix(t *testing.T) {
	fixture := []byte(`[
  {
    "id": 1,
    "is_active": true,
    "tabs": [
      {
        "id": 47,
        "is_active": true,
        "title": "W:api",
        "windows": [
          {
            "id": 48,
            "is_active": true,
            "cwd": "/tmp",
            "foreground_processes": [
              {"cmdline": ["claude"], "cwd": "/projects/personal-w-api"}
            ],
            "user_vars": {}
          }
        ]
      }
    ]
  }
]`)
	client := newClientFromBytes(t, fixture)

	cfg := &config.Config{
		Accounts: []config.Account{
			{Name: "work", Harness: "claude"},
			{Name: "personal", Harness: "claude"},
		},
		Sessions: []config.Session{
			{Title: "api", Account: "work", Directory: "/projects/api"},
			{Title: "W:api", Account: "personal", Directory: "/projects/personal-w-api"},
		},
	}

	sessions, ghosts, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 1 || len(ghosts) != 1 {
		t.Fatalf("expected 1 session + 1 ghost, got %d/%d", len(sessions), len(ghosts))
	}
	if sessions[0].Profile != "personal" || sessions[0].Project != "W:api" {
		t.Errorf("session: got %+v, want personal/W:api", sessions[0])
	}
	if ghosts[0].Profile != "personal" || ghosts[0].Project != "W:api" {
		t.Errorf("ghost: got %+v, want personal/W:api", ghosts[0])
	}
}

func TestListWithConfig_TitleFallback_LegacyPrefixedTitle_AmbiguousMatchSkipped(t *testing.T) {
	fixture := []byte(`[
  {
    "id": 1,
    "is_active": true,
    "tabs": [
      {
        "id": 49,
        "is_active": true,
        "title": "W:api",
        "windows": [
          {
            "id": 50,
            "is_active": true,
            "cwd": "/tmp",
            "foreground_processes": [
              {"cmdline": ["claude"], "cwd": "/tmp"}
            ],
            "user_vars": {}
          }
        ]
      }
    ]
  }
]`)
	client := newClientFromBytes(t, fixture)

	cfg := &config.Config{
		Accounts: []config.Account{
			{Name: "work", Harness: "claude"},
			{Name: "warehouse", Harness: "claude"},
		},
		Sessions: []config.Session{
			{Title: "api", Account: "work", Directory: "/projects/api"},
			{Title: "api", Account: "warehouse", Directory: "/projects/wh-api"},
		},
	}

	sessions, ghosts, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions (ambiguous legacy prefix), got %d", len(sessions))
	}
	if len(ghosts) != 0 {
		t.Errorf("expected 0 ghosts (ambiguous legacy prefix), got %d", len(ghosts))
	}
}
