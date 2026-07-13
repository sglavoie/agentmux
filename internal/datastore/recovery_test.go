package datastore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRecoverCorruptPathQuarantinesTruncatedSQLiteAndCreatesFreshDatabase(t *testing.T) {
	for _, withSidecars := range []bool{false, true} {
		name := "database-only"
		if withSidecars {
			name = "database-wal-shm"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "data", DatabaseFileName)
			preserved := createTruncatedSQLiteFixture(t, path, withSidecars)

			result, err := RecoverCorruptPath(context.Background(), path, sqliteCodeError(sqliteCorruptCode))
			if err != nil {
				t.Fatalf("RecoverCorruptPath: %v", err)
			}
			absPath, err := filepath.Abs(path)
			if err != nil {
				t.Fatal(err)
			}
			if !result.Replaced || result.DatabasePath != absPath || result.QuarantineDir == "" {
				t.Fatalf("recovery result = %+v", result)
			}
			if filepath.Dir(result.QuarantineDir) != filepath.Join(filepath.Dir(absPath), recoveryDirectoryName) {
				t.Fatalf("quarantine dir = %q", result.QuarantineDir)
			}
			assertMode(t, filepath.Dir(result.QuarantineDir), dataDirectoryMode)
			assertMode(t, result.QuarantineDir, dataDirectoryMode)
			assertMode(t, filepath.Join(result.QuarantineDir, RecoveryManifestFileName), databaseFileMode)
			assertMode(t, absPath, databaseFileMode)

			for originalPath, want := range preserved {
				quarantinedPath := filepath.Join(result.QuarantineDir, filepath.Base(originalPath))
				got, err := os.ReadFile(quarantinedPath)
				if err != nil {
					t.Fatalf("read quarantined %s: %v", quarantinedPath, err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("quarantined %s differs from corrupt original", quarantinedPath)
				}
				assertMode(t, quarantinedPath, databaseFileMode)
			}

			manifest := readRecoveryManifest(t, result.QuarantineDir)
			if manifest.DatabasePath != absPath || manifest.ReplacementOutcome != recoveryOutcomeReplaced || manifest.CompletedAt == nil {
				t.Fatalf("manifest = %+v", manifest)
			}
			if manifest.CorruptionReason == "" || len(manifest.Artifacts) != len(preserved) {
				t.Fatalf("manifest metadata = %+v", manifest)
			}
			for _, artifact := range manifest.Artifacts {
				want, ok := preserved[artifact.OriginalPath]
				if !ok {
					t.Fatalf("manifest contains unexpected artifact %+v", artifact)
				}
				hash := sha256.Sum256(want)
				if artifact.Size != int64(len(want)) || artifact.Mode != "0644" || artifact.SHA256 != hex.EncodeToString(hash[:]) {
					t.Fatalf("manifest artifact metadata = %+v", artifact)
				}
				if artifact.QuarantinePath != filepath.Join(result.QuarantineDir, filepath.Base(artifact.OriginalPath)) {
					t.Fatalf("manifest quarantine path = %q", artifact.QuarantinePath)
				}
			}

			fresh, err := OpenPath(context.Background(), absPath)
			if err != nil {
				t.Fatalf("open replacement: %v", err)
			}
			defer func() { _ = fresh.Close() }()
			if version, err := fresh.SchemaVersion(context.Background()); err != nil || version != CurrentSchemaVersion {
				t.Fatalf("replacement schema version = %d, %v", version, err)
			}
			check, err := pragmaCheck(context.Background(), fresh.DB(), `PRAGMA quick_check(1)`)
			if err != nil || check != "ok" {
				t.Fatalf("replacement quick_check = %q, %v", check, err)
			}
			var exists int
			if err := fresh.DB().QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'recovery_payloads'`).Scan(&exists); err != nil {
				t.Fatal(err)
			}
			if exists != 0 {
				t.Fatal("replacement retained a table from the corrupt database")
			}
		})
	}
}

func TestRecoverCorruptPathRequiresClassifiedCauseAndRechecksHealthyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	store, err := OpenPath(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`CREATE TABLE healthy_marker (value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if result, err := RecoverCorruptPath(context.Background(), path, errors.New("database disk image is malformed")); !errors.Is(err, ErrRecoveryRequiresCorruption) || result.Replaced {
		t.Fatalf("unclassified recovery = %+v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), recoveryDirectoryName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unclassified recovery created recovery directory: %v", err)
	}

	result, err := RecoverCorruptPath(context.Background(), path, fmt.Errorf("wrapped initialization failure: %w", sqliteCodeError(sqliteCorruptCode)))
	if err != nil {
		t.Fatalf("healthy recheck: %v", err)
	}
	if result.Replaced || result.DatabasePath != path || result.QuarantineDir != "" {
		t.Fatalf("healthy recheck result = %+v", result)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("healthy recheck modified the database")
	}
}

func TestRecoverCorruptPathAbortsWhenLockedRecheckFailsForUnrelatedReason(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	want := []byte("not a SQLite database")
	if err := os.WriteFile(path, want, databaseFileMode); err != nil {
		t.Fatal(err)
	}

	result, err := RecoverCorruptPath(context.Background(), path, sqliteCodeError(sqliteCorruptCode))
	if err == nil || IsCorruption(err) || result.Replaced {
		t.Fatalf("unrelated recheck result = %+v, %v", result, err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(got, want) {
		t.Fatalf("unrelated recheck modified database: %v", readErr)
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(path), recoveryDirectoryName)); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("unrelated recheck created quarantine: %v", statErr)
	}
}

func TestRecoverCorruptDefaultUsesDefaultDatastore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".local", "share", ApplicationName, DatabaseFileName)
	createTruncatedSQLiteFixture(t, path, false)

	result, err := RecoverCorruptDefault(context.Background(), sqliteCodeError(sqliteCorruptCode))
	if err != nil {
		t.Fatalf("RecoverCorruptDefault: %v", err)
	}
	if !result.Replaced || result.DatabasePath != path || result.QuarantineDir == "" {
		t.Fatalf("default recovery result = %+v", result)
	}
}

func TestConcurrentCorruptRecoveryReplacesExactlyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	createTruncatedSQLiteFixture(t, path, false)

	const attempts = 2
	start := make(chan struct{})
	results := make(chan RecoveryResult, attempts)
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := RecoverCorruptPath(context.Background(), path, sqliteCodeError(sqliteCorruptCode))
			results <- result
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent recovery: %v", err)
		}
	}
	replaced := 0
	for result := range results {
		if result.Replaced {
			replaced++
		}
	}
	if replaced != 1 {
		t.Fatalf("replacement count = %d, want 1", replaced)
	}
}

func TestRecoverCorruptPathRefusesSymlinkArtifactsAndHonorsCancellation(t *testing.T) {
	t.Run("database-symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target.db")
		if err := os.WriteFile(target, []byte("target stays intact"), databaseFileMode); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, DatabaseFileName)
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		if result, err := RecoverCorruptPath(context.Background(), path, sqliteCodeError(sqliteCorruptCode)); err == nil || result.Replaced {
			t.Fatalf("symlink recovery = %+v, %v", result, err)
		}
		got, err := os.ReadFile(target)
		if err != nil || string(got) != "target stays intact" {
			t.Fatalf("symlink target = %q, %v", got, err)
		}
	})

	t.Run("sidecar-symlink", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), DatabaseFileName)
		want := createTruncatedSQLiteFixture(t, path, false)[path]
		target := filepath.Join(filepath.Dir(path), "target-wal")
		if err := os.WriteFile(target, []byte("do not move"), databaseFileMode); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path+"-wal"); err != nil {
			t.Fatal(err)
		}
		if result, err := RecoverCorruptPath(context.Background(), path, sqliteCodeError(sqliteCorruptCode)); err == nil || result.Replaced {
			t.Fatalf("sidecar symlink recovery = %+v, %v", result, err)
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal("symlink refusal modified corrupt database")
		}
	})

	t.Run("canceled", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), DatabaseFileName)
		want := createTruncatedSQLiteFixture(t, path, false)[path]
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result, err := RecoverCorruptPath(ctx, path, sqliteCodeError(sqliteCorruptCode))
		if !errors.Is(err, context.Canceled) || result.Replaced {
			t.Fatalf("canceled recovery = %+v, %v", result, err)
		}
		got, readErr := os.ReadFile(path)
		if readErr != nil || !bytes.Equal(got, want) {
			t.Fatal("canceled recovery modified corrupt database")
		}
	})
}

func TestRecoverCorruptPathRollsBackPrepublicationFailures(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*recoveryOperations)
	}{
		{
			name: "manifest",
			mutate: func(ops *recoveryOperations) {
				realWrite := ops.writeManifest
				writes := 0
				ops.writeManifest = func(path string, manifest recoveryManifest) error {
					writes++
					if writes == 2 {
						return errors.New("injected manifest failure")
					}
					return realWrite(path, manifest)
				}
			},
		},
		{
			name: "migration",
			mutate: func(ops *recoveryOperations) {
				ops.openFresh = func(context.Context, string) (*Store, error) { return nil, errors.New("injected migration failure") }
			},
		},
		{
			name: "validation",
			mutate: func(ops *recoveryOperations) {
				ops.validateFresh = func(context.Context, *Store) error { return errors.New("injected validation failure") }
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), DatabaseFileName)
			want := createTruncatedSQLiteFixture(t, path, false)[path]
			ops := defaultRecoveryOperations()
			test.mutate(&ops)

			result, err := recoverCorruptPath(context.Background(), path, sqliteCodeError(sqliteCorruptCode), ops)
			if err == nil || result.Replaced {
				t.Fatalf("faulted recovery = %+v, %v", result, err)
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil || !bytes.Equal(got, want) {
				t.Fatalf("rollback did not restore corrupt database: %v", readErr)
			}
			assertMode(t, path, 0o644)
			if _, statErr := os.Stat(result.QuarantineDir); !errors.Is(statErr, fs.ErrNotExist) {
				t.Fatalf("successful rollback retained quarantine %q: %v", result.QuarantineDir, statErr)
			}
			if corrupt, checkErr := databaseStillCorrupt(context.Background(), path); checkErr != nil || !corrupt {
				t.Fatalf("restored database corruption = %t, %v", corrupt, checkErr)
			}
		})
	}
}

func TestRecoverCorruptPathReportsAndRetainsRollbackFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	want := createTruncatedSQLiteFixture(t, path, false)[path]
	injectedValidation := errors.New("injected validation failure")
	injectedRestore := errors.New("injected restore failure")
	ops := defaultRecoveryOperations()
	ops.validateFresh = func(context.Context, *Store) error { return injectedValidation }
	realRename := ops.rename
	ops.rename = func(oldPath, newPath string) error {
		if newPath == path && strings.Contains(oldPath, string(filepath.Separator)+recoveryDirectoryName+string(filepath.Separator)) {
			return injectedRestore
		}
		return realRename(oldPath, newPath)
	}

	result, err := recoverCorruptPath(context.Background(), path, sqliteCodeError(sqliteCorruptCode), ops)
	if err == nil || !strings.Contains(err.Error(), injectedValidation.Error()) || !strings.Contains(err.Error(), injectedRestore.Error()) {
		t.Fatalf("joined rollback error = %v", err)
	}
	if result.Replaced || result.QuarantineDir == "" {
		t.Fatalf("rollback failure result = %+v", result)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("partial replacement was not removed: %v", statErr)
	}
	quarantined := filepath.Join(result.QuarantineDir, filepath.Base(path))
	got, readErr := os.ReadFile(quarantined)
	if readErr != nil || !bytes.Equal(got, want) {
		t.Fatalf("rollback failure did not retain corrupt artifact: %v", readErr)
	}
	manifest := readRecoveryManifest(t, result.QuarantineDir)
	if manifest.ReplacementOutcome != recoveryOutcomeRollbackFailed || len(manifest.RollbackFailures) == 0 {
		t.Fatalf("rollback failure manifest = %+v", manifest)
	}
}

func TestRecoverCorruptPathRestoresEveryArtifactAfterArchivalFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	want := createTruncatedSQLiteFixture(t, path, true)
	ops := defaultRecoveryOperations()
	realRename := ops.rename
	renames := 0
	ops.rename = func(oldPath, newPath string) error {
		if filepath.Dir(oldPath) == filepath.Dir(path) && strings.Contains(newPath, string(filepath.Separator)+recoveryDirectoryName+string(filepath.Separator)) {
			renames++
			if renames == 2 {
				return errors.New("injected archival failure")
			}
		}
		return realRename(oldPath, newPath)
	}

	result, err := recoverCorruptPath(context.Background(), path, sqliteCodeError(sqliteCorruptCode), ops)
	if err == nil || result.Replaced {
		t.Fatalf("archival failure recovery = %+v, %v", result, err)
	}
	for artifactPath, wantBytes := range want {
		got, readErr := os.ReadFile(artifactPath)
		if readErr != nil || !bytes.Equal(got, wantBytes) {
			t.Fatalf("archival rollback did not preserve %s: %v", artifactPath, readErr)
		}
	}
	if _, statErr := os.Stat(result.QuarantineDir); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("archival rollback retained quarantine: %v", statErr)
	}
}

func TestRecoverCorruptPathLeavesOriginalWhenQuarantineCannotBeCreated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DatabaseFileName)
	want := createTruncatedSQLiteFixture(t, path, false)[path]
	if err := os.WriteFile(filepath.Join(dir, recoveryDirectoryName), []byte("blocks directory"), databaseFileMode); err != nil {
		t.Fatal(err)
	}
	result, err := RecoverCorruptPath(context.Background(), path, sqliteCodeError(sqliteCorruptCode))
	if err == nil || result.Replaced {
		t.Fatalf("blocked quarantine recovery = %+v, %v", result, err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(got, want) {
		t.Fatalf("quarantine creation failure modified database: %v", readErr)
	}
}

func createTruncatedSQLiteFixture(t *testing.T, path string, withSidecars bool) map[string][]byte {
	t.Helper()
	store, err := OpenPath(context.Background(), path)
	if err != nil {
		t.Fatalf("create fixture database: %v", err)
	}
	if _, err := store.DB().Exec(`
		CREATE TABLE recovery_payloads (id INTEGER PRIMARY KEY, payload BLOB NOT NULL);
		INSERT INTO recovery_payloads (payload) VALUES (zeroblob(8388608));
	`); err != nil {
		_ = store.Close()
		t.Fatalf("seed fixture database: %v", err)
	}
	if _, err := store.DB().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		_ = store.Close()
		t.Fatalf("checkpoint fixture database: %v", err)
	}

	sidecars := make(map[string][]byte)
	if withSidecars {
		if _, err := store.DB().Exec(`PRAGMA wal_autocheckpoint=0`); err != nil {
			_ = store.Close()
			t.Fatal(err)
		}
		if _, err := store.DB().Exec(`INSERT INTO recovery_payloads (payload) VALUES (zeroblob(4096))`); err != nil {
			_ = store.Close()
			t.Fatal(err)
		}
		for _, sidecarPath := range []string{path + "-wal", path + "-shm"} {
			data, err := os.ReadFile(sidecarPath)
			if err != nil {
				_ = store.Close()
				t.Fatalf("capture fixture sidecar %s: %v", sidecarPath, err)
			}
			if len(data) == 0 {
				_ = store.Close()
				t.Fatalf("fixture sidecar %s is empty", sidecarPath)
			}
			sidecars[sidecarPath] = append([]byte(nil), data...)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close fixture database: %v", err)
	}

	database, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(database) < 16*4096 {
		t.Fatalf("fixture database is unexpectedly small: %d", len(database))
	}
	truncated := append([]byte(nil), database[:len(database)/2]...)
	if err := os.WriteFile(path, truncated, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	preserved := map[string][]byte{path: truncated}
	for sidecarPath, data := range sidecars {
		if err := os.WriteFile(sidecarPath, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(sidecarPath, 0o644); err != nil {
			t.Fatal(err)
		}
		preserved[sidecarPath] = data
	}
	corrupt, err := databaseStillCorrupt(context.Background(), path)
	if err != nil {
		t.Fatalf("fixture corruption check: %v", err)
	}
	if !corrupt {
		t.Fatal("truncated fixture unexpectedly passed quick_check")
	}
	return preserved
}

func readRecoveryManifest(t *testing.T, quarantineDir string) recoveryManifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(quarantineDir, RecoveryManifestFileName))
	if err != nil {
		t.Fatalf("read recovery manifest: %v", err)
	}
	var manifest recoveryManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode recovery manifest: %v", err)
	}
	return manifest
}

func TestRecoveryTimestampCollisionGetsUniquePrivateDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	if err := os.MkdirAll(filepath.Dir(path), dataDirectoryMode); err != nil {
		t.Fatal(err)
	}
	ops := defaultRecoveryOperations()
	createdAt := time.Date(2026, 7, 13, 12, 0, 0, 123, time.UTC)
	first, err := createQuarantineDirectory(path, createdAt, ops)
	if err != nil {
		t.Fatal(err)
	}
	second, err := createQuarantineDirectory(path, createdAt, ops)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.HasSuffix(second, "-01") {
		t.Fatalf("collision directories = %q and %q", first, second)
	}
	assertMode(t, first, dataDirectoryMode)
	assertMode(t, second, dataDirectoryMode)
}
