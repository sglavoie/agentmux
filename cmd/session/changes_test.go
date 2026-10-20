package session

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/gitstate"
)

func TestChangesCommandEmitsCheckoutScopedJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.invalid"},
		{"-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "initial"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git: %s: %v", out, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("hello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := newChangesCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--json", "--directory", dir, "--file", "new.txt"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Schema int              `json:"schema_version"`
		Data   gitstate.Changes `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Schema != 1 || len(result.Data.Files) != 1 || !result.Data.Files[0].Untracked || result.Data.Patch == "" {
		t.Fatalf("answer: %s", out.String())
	}
}

func TestChangesCommandRequiresOneTarget(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"--json"}, {"--json", "--directory", "/tmp", "--runtime-session", "rs-bad"}} {
		cmd := newChangesCommand()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SilenceErrors, cmd.SilenceUsage = true, true
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("accepted %v", args)
		}
		if !bytes.Contains(out.Bytes(), []byte(`"invalid_arguments"`)) {
			t.Fatalf("answer: %s", out.String())
		}
	}
}

func TestReviewResolvesLivePaneDirectoryWithoutAttaching(t *testing.T) {
	f, launched := attachLaunchAgent(t)
	attachRunClient = func(*exec.Cmd) error { t.Fatal("review must not attach"); return nil }
	dir, err := changesSessionDirectory(string(launched.RuntimeSessionID))
	if err != nil {
		t.Fatal(err)
	}
	record, err := f.Load(t).Get(launched.RuntimeSessionID)
	if err != nil {
		t.Fatal(err)
	}
	panes, err := f.Backend.Discover()
	if err != nil {
		t.Fatal(err)
	}
	pane, ok := backend.FindPane(panes, backend.PaneID(record.Target.Ref))
	if !ok || dir == "" || dir != pane.CWD {
		t.Fatalf("directory %q, pane %+v", dir, pane)
	}
}
