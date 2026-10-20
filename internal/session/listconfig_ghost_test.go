package session_test

import (
	"os"
	"os/exec"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

func TestListWithConfig_ProjectDirectoryWithoutHarnessIsNotGhost(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	client := newClientFromWindows(t, []kitty.OSWindow{{
		ID:       1,
		IsActive: true,
		Tabs: []kitty.Tab{
			{
				ID:       100,
				IsActive: true,
				Title:    "amx",
				Windows: []kitty.Window{{
					ID:       101,
					IsActive: true,
					CWD:      projectDir,
					ForegroundProcesses: []kitty.ForegroundProcess{{
						Cmdline: []string{"amx", "--window-id", "99"},
						CWD:     projectDir,
					}},
				}},
			},
			{
				ID:    102,
				Title: "shell",
				Windows: []kitty.Window{{
					ID:       103,
					IsActive: true,
					CWD:      projectDir,
					ForegroundProcesses: []kitty.ForegroundProcess{{
						Cmdline: []string{"zsh"},
						CWD:     projectDir,
					}},
				}},
			},
		},
	}})

	cfg := &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: "codex"}},
		Sessions: []config.Session{{Title: "Katana", Account: "work", Directory: projectDir}},
	}

	sessions, ghosts, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 0 || len(ghosts) != 0 {
		t.Fatalf("non-harness project tabs became managed: sessions=%+v ghosts=%+v", sessions, ghosts)
	}
}

func TestListWithConfig_StaleRecoveredMetadataWithoutHarnessIsIgnored(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	client := newClientFromWindows(t, []kitty.OSWindow{{
		ID:       1,
		IsActive: true,
		Tabs: []kitty.Tab{{
			ID:       110,
			IsActive: true,
			Title:    "Katana",
			Windows: []kitty.Window{{
				ID:       111,
				IsActive: true,
				CWD:      projectDir,
				UserVars: kitty.UserVars{
					Profile:      "work",
					Harness:      "codex",
					Project:      "Katana",
					AMXDirectory: projectDir,
				},
				ForegroundProcesses: []kitty.ForegroundProcess{{
					Cmdline: []string{"zsh"},
					CWD:     projectDir,
				}},
			}},
		}},
	}})

	cfg := &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: "codex"}},
		Sessions: []config.Session{{Title: "Katana", Account: "work", Directory: projectDir}},
	}

	sessions, ghosts, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 0 || len(ghosts) != 0 {
		t.Fatalf("stale recovered metadata remained live: sessions=%+v ghosts=%+v", sessions, ghosts)
	}
}

func TestListWithConfig_CWDFallbackTargetsHarnessBelowActiveDashboardOverlay(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	client := newClientFromWindows(t, []kitty.OSWindow{{
		ID:       1,
		IsActive: true,
		Tabs: []kitty.Tab{{
			ID:       120,
			IsActive: true,
			Title:    "amx",
			Windows: []kitty.Window{
				{
					ID:       121,
					IsActive: true,
					CWD:      projectDir,
					ForegroundProcesses: []kitty.ForegroundProcess{{
						Cmdline: []string{"amx", "--window-id", "122"},
						CWD:     projectDir,
					}},
				},
				{
					ID:  122,
					CWD: projectDir,
					ForegroundProcesses: []kitty.ForegroundProcess{{
						Cmdline: []string{"codex", "--yolo"},
						CWD:     projectDir,
					}},
				},
			},
		}},
	}})

	cfg := &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: "codex"}},
		Sessions: []config.Session{{Title: "Katana", Account: "work", Directory: projectDir}},
	}

	sessions, ghosts, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 1 || len(ghosts) != 1 {
		t.Fatalf("expected one live harness ghost, got sessions=%+v ghosts=%+v", sessions, ghosts)
	}
	if sessions[0].WindowID != 122 || ghosts[0].WindowID != 122 {
		t.Fatalf("recovery targeted overlay instead of harness: session=%+v ghost=%+v", sessions[0], ghosts[0])
	}
}

func requireSessionGit(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	return git
}

func runSessionGit(t *testing.T, git, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Agent Mux",
		"GIT_AUTHOR_EMAIL=agentmux@example.invalid",
		"GIT_COMMITTER_NAME=Agent Mux",
		"GIT_COMMITTER_EMAIL=agentmux@example.invalid",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}
