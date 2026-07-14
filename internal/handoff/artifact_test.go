package handoff

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestArtifactStoreWritesPrivateDraftAndFinalWithLocalMetadata(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "handoffs")
	artifacts, err := NewArtifactStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	draftContent := []byte("# Draft\n\nsearchable handoff marker\n")
	draft, err := artifacts.Write(WriteRequest{
		ID: "handoff-1", Status: StatusDraft, Content: draftContent,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("Write draft: %v", err)
	}
	if filepath.Base(draft.Path) != "handoff-1.draft.md" || draft.ID != "handoff-1" || draft.Status != StatusDraft {
		t.Fatalf("draft = %#v", draft)
	}
	requireMode(t, directory, 0o700)
	requireMode(t, draft.Path, 0o600)
	requireHash(t, draft.ContentHash, draftContent)

	finalContent := []byte("# Final\n\nreviewed content\n")
	updatedAt := createdAt.Add(time.Hour)
	final, err := artifacts.Write(WriteRequest{
		ID: "handoff-1", Status: StatusFinal, Content: finalContent,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	})
	if err != nil {
		t.Fatalf("Write final: %v", err)
	}
	if filepath.Base(final.Path) != "handoff-1.md" || final.Status != StatusFinal {
		t.Fatalf("final artifact = %#v", final)
	}
	if !final.CreatedAt.Equal(createdAt) {
		t.Fatalf("creation time changed: got %v want %v", final.CreatedAt, createdAt)
	}
	if !final.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("updated time = %v, want %v", final.UpdatedAt, updatedAt)
	}
	requireMode(t, final.Path, 0o600)
	requireHash(t, final.ContentHash, finalContent)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("temporary artifact leaked: %s", entry.Name())
		}
	}
}

func TestArtifactStoreRejectsArtifactDirectorySymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	artifacts, err := NewArtifactStore(link)
	if err != nil {
		t.Fatal(err)
	}
	_, err = artifacts.Write(WriteRequest{ID: "safe", Status: StatusDraft, Content: []byte("content")})
	if err == nil || !strings.Contains(err.Error(), "not a real directory") {
		t.Fatalf("symlink-directory error = %v", err)
	}
}

func TestArtifactStoreReplacesRegularFileAndRejectsNonRegularTarget(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "handoffs")
	artifacts, err := NewArtifactStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	original := []byte("original draft")
	artifact, err := artifacts.Write(WriteRequest{ID: "replace", Status: StatusDraft, Content: original})
	if err != nil {
		t.Fatal(err)
	}
	replacement := []byte("replacement draft")
	replaced, err := artifacts.Write(WriteRequest{ID: "replace", Status: StatusDraft, Content: replacement})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(replaced.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(replacement) {
		t.Fatalf("replaced artifact = %q", got)
	}
	requireMode(t, artifact.Path, 0o600)

	target := filepath.Join(directory, "blocked.md")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = artifacts.Write(WriteRequest{ID: "blocked", Status: StatusFinal, Content: []byte("content")})
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("non-regular target error = %v", err)
	}
}

func requireMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode %s = %04o, want %04o", path, got, want)
	}
}

func requireHash(t *testing.T, got string, content []byte) {
	t.Helper()
	sum := sha256.Sum256(content)
	want := fmt.Sprintf("%x", sum[:])
	if got != want {
		t.Fatalf("hash = %q, want %q", got, want)
	}
}
