package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMetadataRoundTripPermissionsAndAtomicReplacement(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "checkout"+MetadataSuffix)
	metadata := validTestMetadata(directory)
	if err := WriteMetadata(path, metadata); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	metadata.Profile = "updated"
	if err := WriteMetadata(path, metadata); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != metadataFileMode {
		t.Fatalf("metadata mode = %o, want %o", info.Mode().Perm(), metadataFileMode)
	}
	got, err := ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != metadata {
		t.Fatalf("metadata = %+v, want %+v", got, metadata)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("metadata directory entries = %v, want only published sidecar", entries)
	}
}

func TestMetadataStrictlyRejectsUnsupportedVersionsAndFields(t *testing.T) {
	directory := t.TempDir()
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{name: "future version", body: `{"schema_version":2}`, want: "unsupported schema version 2"},
		{name: "unknown field", body: `{"schema_version":1,"future":true}`, want: "unknown field"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(directory, strings.ReplaceAll(tc.name, " ", "-")+".json")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := ReadMetadata(path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ReadMetadata error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestWriteMetadataRejectsInvalidVersionWithoutPublishing(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "invalid"+MetadataSuffix)
	metadata := validTestMetadata(directory)
	metadata.SchemaVersion = 0
	if err := WriteMetadata(path, metadata); err == nil {
		t.Fatal("WriteMetadata accepted schema version 0")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid metadata stat error = %v, want not exist", err)
	}
}

func validTestMetadata(directory string) Metadata {
	return Metadata{
		SchemaVersion:        MetadataSchemaVersion,
		ID:                   "11111111-2222-4333-8444-555555555555",
		ProjectLabel:         "api",
		Path:                 filepath.Join(directory, "checkout"),
		Branch:               "amx/api/source-1",
		GitCommonDir:         filepath.Join(directory, ".git"),
		SourceRoot:           directory,
		SourceBranch:         "main",
		BaseCommit:           "0123456789012345678901234567890123456789",
		Profile:              "work",
		Harness:              "codex",
		SourceConversationID: "source-1",
		CreatedAt:            time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC),
	}
}
