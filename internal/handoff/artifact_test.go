package handoff

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/datastore"
)

func TestArtifactStoreWritesPrivateDraftAndFinalAndIndexesMetadata(t *testing.T) {
	ctx := context.Background()
	store := datastore.OpenTest(t)
	repository, err := NewRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "handoffs")
	artifacts, err := NewArtifactStore(directory, repository)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	draftContent := []byte("# Draft\n\nsearchable handoff marker\n")
	draft, err := artifacts.Write(ctx, WriteRequest{
		ID: "handoff-1", Status: StatusDraft, Title: "Draft title", Content: draftContent,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("Write draft: %v", err)
	}
	if filepath.Base(draft.Record.Path) != "handoff-1.draft.md" {
		t.Fatalf("draft path = %q", draft.Record.Path)
	}
	requireMode(t, directory, 0o700)
	requireMode(t, draft.Record.Path, 0o600)
	requireHash(t, draft.Record.ContentHash, draftContent)

	var title, content string
	if err := store.DB().QueryRowContext(ctx, `
		SELECT title, normalized_content FROM search_documents
		WHERE entity_type = 'handoff' AND entity_id = 'handoff-1'
	`).Scan(&title, &content); err != nil {
		t.Fatalf("read search document: %v", err)
	}
	if title != "Draft title" || !strings.Contains(content, "searchable handoff marker") {
		t.Fatalf("search document = %q / %q", title, content)
	}

	finalContent := []byte("# Final\n\nreviewed content\n")
	updatedAt := createdAt.Add(time.Hour)
	final, err := artifacts.Write(ctx, WriteRequest{
		ID: "handoff-1", Status: StatusFinal, Title: "Final title", Content: finalContent,
		CreatedAt: updatedAt, UpdatedAt: updatedAt,
	})
	if err != nil {
		t.Fatalf("Write final: %v", err)
	}
	if filepath.Base(final.Record.Path) != "handoff-1.md" || final.Record.Status != StatusFinal {
		t.Fatalf("final record = %#v", final.Record)
	}
	if !final.Record.CreatedAt.Equal(createdAt) {
		t.Fatalf("creation time changed: got %v want %v", final.Record.CreatedAt, createdAt)
	}
	if !final.Record.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("updated time = %v, want %v", final.Record.UpdatedAt, updatedAt)
	}
	requireMode(t, final.Record.Path, 0o600)
	requireHash(t, final.Record.ContentHash, finalContent)
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

func TestRepositoryRepairsAvailableArtifactAndKeepsMissingPlaceholder(t *testing.T) {
	ctx := context.Background()
	store := datastore.OpenTest(t)
	repository, err := NewRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	availablePath := filepath.Join(root, "available.md")
	availableContent := []byte("# Available\nrecovered handoff")
	if err := os.WriteFile(availablePath, availableContent, 0o600); err != nil {
		t.Fatal(err)
	}
	missingContent := []byte("# Missing")
	for _, fixture := range []struct {
		id      string
		path    string
		content []byte
	}{
		{id: "available", path: availablePath, content: availableContent},
		{id: "missing", path: filepath.Join(root, "missing.md"), content: missingContent},
	} {
		sum := sha256.Sum256(fixture.content)
		_, err := repository.Save(ctx, Record{
			ID: fixture.id, Path: fixture.path, ContentHash: fmt.Sprintf("%x", sum[:]), Status: StatusFinal,
		}, fixture.id, fixture.content)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.DB().ExecContext(ctx, `DELETE FROM search_documents WHERE entity_type = 'handoff'`); err != nil {
		t.Fatal(err)
	}
	repaired, err := repository.RepairMissingSearchDocuments(ctx)
	if err != nil || repaired != 1 {
		t.Fatalf("RepairMissingSearchDocuments = %d, %v", repaired, err)
	}
	var count int
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM search_documents WHERE entity_type = 'handoff'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("handoff search documents = %d, %v", count, err)
	}
}

func TestRepositoryRejectsMismatchedHashAndArtifactDirectorySymlink(t *testing.T) {
	store := datastore.OpenTest(t)
	repository, err := NewRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repository.Save(context.Background(), Record{
		ID: "bad", Path: filepath.Join(t.TempDir(), "bad.md"), ContentHash: strings.Repeat("0", 64), Status: StatusFinal,
	}, "bad", []byte("different"))
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched-hash error = %v", err)
	}

	root := t.TempDir()
	target := filepath.Join(root, "real")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	artifacts, err := NewArtifactStore(link, repository)
	if err != nil {
		t.Fatal(err)
	}
	_, err = artifacts.Write(context.Background(), WriteRequest{ID: "safe", Status: StatusDraft, Content: []byte("content")})
	if err == nil || !strings.Contains(err.Error(), "not a real directory") {
		t.Fatalf("symlink-directory error = %v", err)
	}
}

func TestArtifactStoreRestoresFileWhenMetadataTransactionFails(t *testing.T) {
	ctx := context.Background()
	store := datastore.OpenTest(t)
	repository, err := NewRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "handoffs")
	artifacts, err := NewArtifactStore(directory, repository)
	if err != nil {
		t.Fatal(err)
	}
	original := []byte("original draft")
	artifact, err := artifacts.Write(ctx, WriteRequest{ID: "restore", Status: StatusDraft, Content: original})
	if err != nil {
		t.Fatal(err)
	}
	_, err = artifacts.Write(ctx, WriteRequest{
		ID: "restore", Status: StatusDraft, WorkspaceID: "missing-workspace", Content: []byte("must roll back"),
	})
	if err == nil {
		t.Fatal("foreign-key failure was not returned")
	}
	got, err := os.ReadFile(artifact.Record.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("artifact after metadata failure = %q", got)
	}
	requireMode(t, artifact.Record.Path, 0o600)

	_, err = artifacts.Write(ctx, WriteRequest{
		ID: "new-failure", Status: StatusFinal, WorkspaceID: "missing-workspace", Content: []byte("orphan"),
	})
	if err == nil {
		t.Fatal("new artifact foreign-key failure was not returned")
	}
	if _, err := os.Stat(filepath.Join(directory, "new-failure.md")); !os.IsNotExist(err) {
		t.Fatalf("failed metadata write left an artifact: %v", err)
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
