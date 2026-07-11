package buildinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

func withFixture(t *testing.T, bi *debug.BuildInfo, binary string) {
	t.Helper()
	oldRead := readBuildInfo
	oldExecutable := executable
	mu.Lock()
	oldVersion := version
	oldCached := cached
	cached = nil
	mu.Unlock()
	t.Cleanup(func() {
		readBuildInfo = oldRead
		executable = oldExecutable
		mu.Lock()
		version = oldVersion
		cached = oldCached
		mu.Unlock()
	})
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		if bi == nil {
			return nil, false
		}
		return bi, true
	}
	executable = func() (string, error) { return binary, nil }
}

func TestCurrentCollectsVCSStampAndBinaryHash(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "amx")
	content := []byte("fake amx binary payload")
	if err := os.WriteFile(binary, content, 0o755); err != nil {
		t.Fatal(err)
	}
	withFixture(t, &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "0123456789abcdef0123"},
		{Key: "vcs.modified", Value: "true"},
		{Key: "vcs.time", Value: "2026-07-10T12:00:00Z"},
	}}, binary)
	SetVersion("9.9.9")

	info := Current()
	if info.Version != "9.9.9" {
		t.Errorf("Version = %q, want 9.9.9", info.Version)
	}
	if info.Revision != "0123456789abcdef0123" || !info.Dirty || info.BuildTime != "2026-07-10T12:00:00Z" {
		t.Errorf("VCS stamp not collected: %+v", info)
	}
	if info.GoVersion == "" {
		t.Error("GoVersion is empty")
	}
	sum := sha256.Sum256(content)
	if info.BinaryHash != hex.EncodeToString(sum[:]) {
		t.Errorf("BinaryHash = %q, want hash of binary content", info.BinaryHash)
	}
	if got := info.ShortRevision(); got != "0123456789ab-dirty" {
		t.Errorf("ShortRevision = %q", got)
	}
	if s := info.String(); !strings.HasPrefix(s, "9.9.9 (revision 0123456789ab-dirty") {
		t.Errorf("String = %q", s)
	}

	// The identity is cached: a second Current returns the same hash even if
	// the on-disk binary is replaced afterwards (the running artifact did not
	// change).
	if err := os.WriteFile(binary, []byte("replaced"), 0o755); err != nil {
		t.Fatal(err)
	}
	if again := Current(); again.BinaryHash != info.BinaryHash {
		t.Errorf("Current not cached: hash changed from %q to %q", info.BinaryHash, again.BinaryHash)
	}
}

func TestCurrentWithoutVCSOrBinaryDegradesToEmptyFields(t *testing.T) {
	withFixture(t, nil, "")
	executable = func() (string, error) { return "", os.ErrNotExist }
	SetVersion("1.0.0")

	info := Current()
	if info.Revision != "" || info.Dirty || info.BuildTime != "" || info.BinaryHash != "" {
		t.Errorf("expected empty VCS/hash fields, got %+v", info)
	}
	if got := info.ShortRevision(); got != "unknown" {
		t.Errorf("ShortRevision = %q, want unknown", got)
	}
}

func TestSameArtifactPrefersHashesThenFallsBack(t *testing.T) {
	a := Info{Version: "1.0.0", Revision: "aaa", BinaryHash: "h1"}
	b := Info{Version: "2.0.0", Revision: "bbb", BinaryHash: "h1"}
	if !a.SameArtifact(b) {
		t.Error("matching hashes must compare equal regardless of version")
	}
	b.BinaryHash = "h2"
	if a.SameArtifact(b) {
		t.Error("differing hashes must compare unequal")
	}
	// No hash on one side: fall back to version/revision/dirty.
	c := Info{Version: "1.0.0", Revision: "aaa"}
	if !a.SameArtifact(c) {
		t.Error("hash-less comparison must fall back to version+revision")
	}
	c.Dirty = true
	if a.SameArtifact(c) {
		t.Error("dirty flag must participate in the fallback comparison")
	}
}
