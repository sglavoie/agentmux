package worktree

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// MetadataSchemaVersion is the only sidecar schema this build accepts.
	MetadataSchemaVersion = 1
	// MetadataSuffix distinguishes durable Agent Mux metadata from the checkout.
	MetadataSuffix   = ".amx-worktree.json"
	metadataFileMode = 0o600
)

// Metadata is the durable auxiliary record for one Git-authoritative worktree.
// Git remains authoritative for checkout and branch state; this sidecar retains
// the provenance Git does not know about.
type Metadata struct {
	SchemaVersion        int       `json:"schema_version"`
	ID                   string    `json:"id"`
	ProjectLabel         string    `json:"project_label"`
	Path                 string    `json:"path"`
	Branch               string    `json:"branch"`
	GitCommonDir         string    `json:"git_common_dir"`
	SourceRoot           string    `json:"source_root"`
	SourceBranch         string    `json:"source_branch"`
	BaseCommit           string    `json:"base_commit"`
	Profile              string    `json:"profile"`
	Harness              string    `json:"harness"`
	SourceConversationID string    `json:"source_conversation_id"`
	CreatedAt            time.Time `json:"created_at"`
}

// SidecarPath returns the sidecar adjacent to checkoutPath.
func SidecarPath(checkoutPath string) string {
	return filepath.Clean(checkoutPath) + MetadataSuffix
}

// ReadMetadata decodes and validates one schema-v1 sidecar.
func ReadMetadata(path string) (Metadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Metadata{}, fmt.Errorf("worktree: reading metadata %s: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var metadata Metadata
	if err := decoder.Decode(&metadata); err != nil {
		return Metadata{}, fmt.Errorf("worktree: decoding metadata %s: %w", path, err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return Metadata{}, fmt.Errorf("worktree: decoding metadata %s: %w", path, err)
	}
	if err := metadata.Validate(); err != nil {
		return Metadata{}, fmt.Errorf("worktree: invalid metadata %s: %w", path, err)
	}
	return metadata, nil
}

// WriteMetadata validates and atomically replaces one private sidecar.
func WriteMetadata(path string, metadata Metadata) error {
	metadata.CreatedAt = metadata.CreatedAt.UTC()
	if err := metadata.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("worktree: encoding metadata: %w", err)
	}
	data = append(data, '\n')
	if err := atomicWriteMetadata(path, data); err != nil {
		return fmt.Errorf("worktree: writing metadata %s: %w", path, err)
	}
	return nil
}

// Validate rejects incomplete or unsupported metadata without consulting Git.
// This keeps stale-but-readable sidecars available for later recovery flows.
func (m Metadata) Validate() error {
	if m.SchemaVersion != MetadataSchemaVersion {
		return fmt.Errorf("unsupported schema version %d; want %d", m.SchemaVersion, MetadataSchemaVersion)
	}
	for _, field := range []struct{ name, value string }{
		{name: "id", value: m.ID},
		{name: "project label", value: m.ProjectLabel},
		{name: "path", value: m.Path},
		{name: "branch", value: m.Branch},
		{name: "Git common directory", value: m.GitCommonDir},
		{name: "source root", value: m.SourceRoot},
		{name: "base commit", value: m.BaseCommit},
		{name: "source conversation ID", value: m.SourceConversationID},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s is required", field.name)
		}
	}
	for _, field := range []struct{ name, path string }{
		{name: "path", path: m.Path},
		{name: "Git common directory", path: m.GitCommonDir},
		{name: "source root", path: m.SourceRoot},
	} {
		if !filepath.IsAbs(field.path) || filepath.Clean(field.path) != field.path {
			return fmt.Errorf("%s must be a clean absolute path", field.name)
		}
	}
	if m.CreatedAt.IsZero() {
		return errors.New("creation time is required")
	}
	_, offset := m.CreatedAt.Zone()
	if offset != 0 {
		return errors.New("creation time must be UTC")
	}
	return nil
}

func newWorktreeID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("worktree: generating stable ID: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func rejectTrailingJSON(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func atomicWriteMetadata(path string, data []byte) (retErr error) {
	directory := filepath.Dir(path)
	temp, err := os.CreateTemp(directory, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		if retErr != nil {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(metadataFileMode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return err
	}
	return errors.Join(directoryHandle.Sync(), directoryHandle.Close())
}
