package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupPreservesIgnoredFiles(t *testing.T) {
	t.Parallel()
	repo, root, workflow, created := cleanupFixture(t)
	if err := os.WriteFile(filepath.Join(repo, ".git", "info", "exclude"), []byte("private/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(created.Path, "private", "secret.txt")
	if err := os.MkdirAll(filepath.Dir(secret), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status := gitOutput(t, created.Path, "status", "--porcelain"); status != "" {
		t.Fatalf("ignored file appeared in ordinary Git status: %q", status)
	}

	manager := testInventory(root)
	entry, err := manager.Resolve(context.Background(), created.ID)
	if err != nil || !entry.Ignored || entry.Dirty || !entry.StatusAvailable {
		t.Fatalf("Resolve = %+v, %v; want ignored files distinguished from dirty status", entry, err)
	}
	for _, remove := range []struct {
		name string
		call func() error
	}{
		{"user removal", func() error {
			_, err := manager.Remove(context.Background(), created.ID, RemoveOptions{DeleteBranch: true})
			return err
		}},
		{"failed-launch cleanup", func() error { return workflow.RemoveUnused(context.Background(), created) }},
	} {
		if err := remove.call(); !errors.Is(err, ErrWorktreeDirty) {
			t.Errorf("%s = %v; want ErrWorktreeDirty", remove.name, err)
		}
		if content, err := os.ReadFile(secret); err != nil || string(content) != "keep me\n" {
			t.Errorf("%s lost ignored file: %q, %v", remove.name, content, err)
		}
		if _, err := os.Stat(created.MetadataPath); err != nil {
			t.Errorf("%s lost recovery metadata: %v", remove.name, err)
		}
	}
}

func TestRemoveUnusedUnregistersAnAlreadyMissingCheckout(t *testing.T) {
	t.Parallel()
	repo, _, workflow, created := cleanupFixture(t)
	if err := os.RemoveAll(created.Path); err != nil {
		t.Fatal(err)
	}
	if err := workflow.RemoveUnused(context.Background(), created); err != nil {
		t.Fatalf("RemoveUnused: %v", err)
	}
	if _, err := os.Stat(created.MetadataPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("metadata remains after cleanup: %v", err)
	}
	if exists, err := gitBranchExists(context.Background(), repo, created.Branch); err != nil || exists {
		t.Fatalf("branch exists = %v, err = %v", exists, err)
	}
}
