package debugger

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

const (
	bundleSchemaVersionV1 = 1
	bundleSchemaVersionV2 = 2
	BundleSchemaVersion   = bundleSchemaVersionV2
	maxBundleRecords      = 10000
	maxNativeFileBytes    = int64(256 << 10)
	maxNativeTotalBytes   = int64(2 << 20)
)

type BundleOptions struct {
	Paths          diagnostics.Paths
	OutputPath     string
	Mode           BundleMode
	Selector       Selector
	Now            time.Time
	Journal        *diagnostics.Journal
	Config         *config.Config
	ConfigError    error
	Status         *StatusReport
	Doctor         any
	Build          buildinfo.Info
	NativeSessions []NativeSession
}

type BundleManifestEntry struct {
	Member    string `json:"member"`
	Source    string `json:"source"`
	Decision  string `json:"decision"`
	Transform string `json:"transform,omitempty"`
	Bytes     int    `json:"bytes,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type BundleManifest struct {
	SchemaVersion   int                   `json:"schema_version"`
	GeneratedAt     time.Time             `json:"generated_at"`
	Mode            BundleMode            `json:"mode"`
	LocalOnly       bool                  `json:"local_only"`
	Warning         string                `json:"warning,omitempty"`
	Selector        any                   `json:"selector"`
	Entries         []BundleManifestEntry `json:"entries"`
	RedactionPolicy []string              `json:"redaction_policy"`
	Truncated       bool                  `json:"truncated"`
	RawSources      string                `json:"raw_sources"`
}

type BundleResult struct {
	SchemaVersion int            `json:"schema_version"`
	Path          string         `json:"path"`
	Mode          BundleMode     `json:"mode"`
	Bytes         int64          `json:"bytes"`
	Manifest      BundleManifest `json:"manifest"`
}

type bundleMember struct {
	name string
	data []byte
}

// CreateBundle reads diagnostic evidence without reconciling spool state and
// publishes one private archive atomically. Raw native logs and raw hook
// captures are never members; only the redacted products built here are added.
func CreateBundle(ctx context.Context, options BundleOptions) (BundleResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if options.Mode == "" {
		options.Mode = BundleModeShare
	}
	if options.Mode != BundleModeShare && options.Mode != BundleModeForensic {
		return BundleResult{}, fmt.Errorf("debugger: unsupported bundle mode %q", options.Mode)
	}
	if err := options.Selector.Normalize(); err != nil {
		return BundleResult{}, err
	}
	if options.Now.IsZero() {
		options.Now = time.Now().UTC()
	} else {
		options.Now = options.Now.UTC()
	}
	if options.Build.Version == "" {
		options.Build = buildinfo.Current()
	}
	if options.Paths.Root == "" {
		return BundleResult{}, errors.New("debugger: diagnostic paths are unavailable")
	}
	hash, err := diagnostics.LocalIdentifierHasher(options.Paths)
	if err != nil {
		return BundleResult{}, fmt.Errorf("debugger: loading private identifier hasher: %w", err)
	}
	home, _ := os.UserHomeDir()
	redactor := newBundleRedactor(options.Mode, hash, map[string]string{
		home: "[HOME]", options.Paths.Root: "[STATE]", os.TempDir(): "[TMP]",
	})
	selectorData, err := redactor.JSON(options.Selector)
	if err != nil {
		return BundleResult{}, err
	}
	var manifestSelector any
	if err := json.Unmarshal(selectorData, &manifestSelector); err != nil {
		return BundleResult{}, err
	}
	manifest := BundleManifest{
		SchemaVersion: BundleSchemaVersion, GeneratedAt: options.Now, Mode: options.Mode,
		LocalOnly: options.Mode == BundleModeForensic, Selector: manifestSelector,
		Entries:    make([]BundleManifestEntry, 0),
		RawSources: "raw hook captures and raw native logs are always excluded",
	}
	if options.Mode == BundleModeShare {
		manifest.RedactionPolicy = []string{
			"drop structured fields outside the share allowlist",
			"drop prompt tool message transcript payload credential and secret fields",
			"HMAC identifiers with the private local diagnostic key",
			"replace filesystem paths with root aliases without retaining path tails",
			"extract only controlled timestamp level status error-class and hashed component values from native logs",
		}
	} else {
		manifest.RedactionPolicy = []string{
			"redact prompt tool message transcript payload credential and secret fields",
			"scrub credential patterns and filesystem paths from broader diagnostic text",
			"HMAC identifiers with the private local diagnostic key",
			"never include raw native logs or raw hook captures",
		}
	}
	if manifest.LocalOnly {
		manifest.Warning = "LOCAL ONLY: forensic bundles remain redacted but may contain broader diagnostic text; do not share them."
	}
	members := make([]bundleMember, 0, 10)
	addJSON := func(name, source, transform string, value any) error {
		data, err := redactor.JSON(value)
		if err != nil {
			return err
		}
		members = append(members, bundleMember{name: name, data: data})
		manifest.Entries = append(manifest.Entries, BundleManifestEntry{
			Member: name, Source: source, Decision: "included", Transform: transform, Bytes: len(data),
		})
		return nil
	}

	status := options.Status
	if status == nil {
		unavailable := map[string]any{"schema_version": StatusSchemaVersion, "availability": "unavailable"}
		if err := addJSON("status.json", "debug status snapshot", "structured redaction", unavailable); err != nil {
			return BundleResult{}, err
		}
	} else if err := addJSON("status.json", "debug status snapshot", "structured redaction", status); err != nil {
		return BundleResult{}, err
	}
	doctor := options.Doctor
	if doctor == nil {
		doctor = doctorSnapshot(status)
	}
	if err := addJSON("doctor.json", "doctor snapshot", "structured redaction", doctor); err != nil {
		return BundleResult{}, err
	}
	if err := addJSON("versions.json", "CLI and daemon build facts", "identifiers hashed in share mode", versionSnapshot(options.Build, status)); err != nil {
		return BundleResult{}, err
	}
	if err := addJSON("configuration.json", "effective configuration facts", "names and paths omitted", configSnapshot(options.Config, options.ConfigError)); err != nil {
		return BundleResult{}, err
	}

	journal := options.Journal
	if journal == nil {
		policy := config.DiagnosticsConfig{}
		if options.Config != nil {
			policy = options.Config.Defaults.Diagnostics
		}
		journal, err = diagnostics.NewJournal(options.Paths, diagnostics.JournalOptions{
			KeepDays: policy.JournalKeepDays, MaxBytes: int64(policy.JournalMaxMiB) << 20,
		})
		if err != nil {
			return BundleResult{}, err
		}
	}
	snapshot, err := journal.Snapshot(ctx, options.Selector.JournalSelector(maxBundleRecords), nil)
	if err != nil {
		return BundleResult{}, err
	}
	records := snapshot.Records
	manifest.Truncated = snapshot.Statistics.RecordsMatched > len(records)
	causal := struct {
		SchemaVersion   int                            `json:"schema_version"`
		Selector        Selector                       `json:"selector"`
		JournalSnapshot diagnostics.SnapshotStatistics `json:"journal_snapshot"`
		Records         []diagnostics.TraceEnvelope    `json:"records"`
	}{BundleSchemaVersion, options.Selector, snapshot.Statistics, records}
	if err := addJSON("causal-records.json", "diagnostic journal", "tolerant read and structured redaction", causal); err != nil {
		return BundleResult{}, err
	}
	aslRecords := make([]diagnostics.TraceEnvelope, 0)
	for _, record := range records {
		if record.RunID != "" || strings.Contains(strings.ToLower(record.Stage), "asl") || record.Evidence["asl_run_id"] != "" {
			aslRecords = append(aslRecords, record)
		}
	}
	asl := struct {
		SchemaVersion        int                            `json:"schema_version"`
		JournalSnapshotScope string                         `json:"journal_snapshot_scope"`
		JournalSnapshot      diagnostics.SnapshotStatistics `json:"journal_snapshot"`
		Records              []diagnostics.TraceEnvelope    `json:"records"`
	}{BundleSchemaVersion, "source_snapshot", snapshot.Statistics, aslRecords}
	if err := addJSON("asl-records.json", "applicable diagnostic journal records", "ASL/run filter and structured redaction", asl); err != nil {
		return BundleResult{}, err
	}
	explanation, explainErr := ExplainRecords(ExplainOptions{
		Selector: options.Selector, Recent: 10, Now: options.Now,
		GraphDepth: DefaultExplainGraphDepth, MaxNodes: DefaultExplainMaxNodes, MaxEdges: DefaultExplainMaxEdges,
	}, records)
	if explainErr != nil {
		manifest.Entries = append(manifest.Entries, BundleManifestEntry{Member: "explanation.json", Source: "causal explanation", Decision: "unavailable", Reason: "explanation could not be generated"})
	} else {
		explanation.JournalSnapshot = snapshot.Statistics
		manifest.Truncated = manifest.Truncated || explanation.Truncation.Truncated
		if err := addJSON("explanation.json", "causal explanation", "bounded pure offline explanation and structured redaction", explanation); err != nil {
			return BundleResult{}, err
		}
	}

	nativeData, nativeCount, nativeTruncated, nativeErr := redactedNativeExcerpts(options.Paths, options.NativeSessions, options.Selector, redactor)
	if nativeErr != nil {
		manifest.Entries = append(manifest.Entries, BundleManifestEntry{Member: "native-excerpts.jsonl", Source: "managed native logs", Decision: "unavailable", Reason: "native excerpts could not be read safely"})
	} else if len(nativeData) == 0 {
		manifest.Entries = append(manifest.Entries, BundleManifestEntry{Member: "native-excerpts.jsonl", Source: "managed native logs", Decision: "unavailable", Reason: "no matching recognized redacted records"})
	} else {
		members = append(members, bundleMember{name: "native-excerpts.jsonl", data: nativeData})
		manifest.Entries = append(manifest.Entries, BundleManifestEntry{
			Member: "native-excerpts.jsonl", Source: "managed native logs", Decision: "included",
			Transform: fmt.Sprintf("always-redacted excerpts from %d regular file(s)", nativeCount), Bytes: len(nativeData),
		})
		manifest.Truncated = manifest.Truncated || nativeTruncated
	}
	manifest.Entries = append(manifest.Entries,
		BundleManifestEntry{Member: "(none)", Source: "raw hook captures", Decision: "dropped", Reason: "raw capture material is never an archive input"},
		BundleManifestEntry{Member: "(none)", Source: "raw native logs", Decision: "transformed", Reason: "only redacted excerpts may be included"},
	)
	if manifest.LocalOnly {
		warning := []byte("LOCAL ONLY\n\nThis forensic bundle remains redacted but contains broader diagnostic text. Do not share it.\n")
		members = append(members, bundleMember{name: "LOCAL_ONLY.txt", data: warning})
		manifest.Entries = append(manifest.Entries, BundleManifestEntry{Member: "LOCAL_ONLY.txt", Source: "bundle policy", Decision: "included", Bytes: len(warning)})
	}
	manifest.Entries = append(manifest.Entries, BundleManifestEntry{Member: "manifest.json", Source: "redaction and inclusion manifest", Decision: "included", Transform: "controlled metadata"})
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return BundleResult{}, err
	}
	members = append(members, bundleMember{name: "manifest.json", data: append(manifestData, '\n')})

	output, err := normalizeBundleOutput(options.OutputPath, options.Now, options.Mode)
	if err != nil {
		return BundleResult{}, err
	}
	if err := writeBundleAtomic(output, options.Now, members); err != nil {
		return BundleResult{}, err
	}
	info, err := os.Stat(output)
	if err != nil {
		return BundleResult{}, err
	}
	return BundleResult{SchemaVersion: BundleSchemaVersion, Path: output, Mode: options.Mode, Bytes: info.Size(), Manifest: manifest}, nil
}

func doctorSnapshot(status *StatusReport) any {
	if status == nil {
		return map[string]any{"schema_version": 1, "availability": "unavailable"}
	}
	return map[string]any{
		"schema_version": 1, "generated_at": status.GeneratedAt, "healthy": status.Healthy,
		"daemon": status.Daemon, "findings": status.Findings,
		"source": "bundle_offline_snapshot",
	}
}

func versionSnapshot(cli buildinfo.Info, status *StatusReport) any {
	result := map[string]any{"schema_version": BundleSchemaVersion, "cli": cli}
	if status != nil && status.Daemon.Build != nil {
		result["daemon"] = status.Daemon.Build
		result["build_matches"] = status.Daemon.BuildMatches
	}
	return result
}

func configSnapshot(cfg *config.Config, configErr error) any {
	if configErr != nil || cfg == nil {
		message := "configuration unavailable"
		if configErr != nil {
			message = configErr.Error()
		}
		return map[string]any{"schema_version": BundleSchemaVersion, "availability": "unavailable", "error": message}
	}
	harnessCounts := make(map[string]int)
	for _, profile := range cfg.Profiles {
		harnessCounts[profile.EffectiveHarness()]++
	}
	type harnessCount struct {
		Harness string `json:"harness"`
		Count   int    `json:"count"`
	}
	counts := make([]harnessCount, 0, len(harnessCounts))
	for harnessName, count := range harnessCounts {
		counts = append(counts, harnessCount{Harness: harnessName, Count: count})
	}
	sort.Slice(counts, func(i, j int) bool { return counts[i].Harness < counts[j].Harness })
	d := cfg.Defaults.Diagnostics
	return map[string]any{
		"schema_version": BundleSchemaVersion, "availability": "available",
		"profile_count": len(cfg.Profiles), "project_count": len(cfg.Projects), "harness_counts": counts,
		"diagnostics": map[string]any{
			"journal_enabled": d.JournalEnabledValue(), "journal_keep_days": d.JournalKeepDays, "journal_max_mib": d.JournalMaxMiB,
			"native_logging": d.NativeLoggingValue(), "native_keep_days": d.NativeKeepDays,
			"native_max_mib": d.NativeMaxMiB, "native_session_max_mib": d.NativeSessionMaxMiB,
		},
	}
}

func redactedNativeExcerpts(paths diagnostics.Paths, sessions []NativeSession, selector Selector, redactor *bundleRedactor) ([]byte, int, bool, error) {
	candidates := make(map[string]struct{})
	for _, session := range sessions {
		if session.Coverage != "managed" || !nativeSessionMatches(session, selector) || strings.TrimSpace(session.LogPath) == "" {
			continue
		}
		path := filepath.Clean(session.LogPath)
		if pathWithin(paths.NativeDir, path) {
			candidates[path] = struct{}{}
		}
	}
	// Offline bundles may not have Kitty metadata. The managed native root is
	// still owned by Agent Mux, so an unscoped request may inspect it safely.
	if len(candidates) == 0 && selector.WindowID == 0 && selector.SessionID == "" && selector.ASLRunID == "" && selector.Harness == "" && selector.Profile == "" {
		candidates[paths.NativeDir] = struct{}{}
	}
	files := make([]string, 0)
	for candidate := range candidates {
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, 0, false, err
		}
		if info.Mode().IsRegular() {
			files = append(files, candidate)
			continue
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		err = filepath.WalkDir(candidate, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if path == candidate {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !entry.Type().IsRegular() || !pathWithin(paths.NativeDir, path) {
				return nil
			}
			files = append(files, path)
			return nil
		})
		if err != nil {
			return nil, 0, false, err
		}
	}
	sort.Strings(files)
	var output strings.Builder
	var readBytes int64
	truncated := false
	for _, path := range files {
		if readBytes >= maxNativeTotalBytes {
			truncated = true
			break
		}
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			_ = file.Close()
			continue
		}
		start := int64(0)
		limit := info.Size()
		if limit > maxNativeFileBytes {
			start = limit - maxNativeFileBytes
			limit = maxNativeFileBytes
			truncated = true
		}
		if remaining := maxNativeTotalBytes - readBytes; limit > remaining {
			start += limit - remaining
			limit = remaining
			truncated = true
		}
		_, _ = file.Seek(start, io.SeekStart)
		scanner := bufio.NewScanner(io.LimitReader(file, limit))
		scanner.Buffer(make([]byte, 64<<10), int(maxNativeFileBytes))
		for scanner.Scan() {
			if line := redactor.nativeLine(scanner.Text()); line != "" {
				output.WriteString(line)
				output.WriteByte('\n')
			}
		}
		_ = file.Close()
		readBytes += limit
	}
	return []byte(output.String()), len(files), truncated, nil
}

func pathWithin(root, path string) bool {
	root, path = filepath.Clean(root), filepath.Clean(path)
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func normalizeBundleOutput(path string, now time.Time, mode BundleMode) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = fmt.Sprintf("agent-mux-debug-%s-%s.tar.gz", now.Format("20060102T150405Z"), mode)
	}
	if !strings.HasSuffix(strings.ToLower(path), ".tar.gz") {
		path += ".tar.gz"
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func writeBundleAtomic(path string, now time.Time, members []bundleMember) (retErr error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
		return fmt.Errorf("debugger: unsafe bundle output directory: %s is not a real directory", directory)
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("debugger: bundle target %s is not a regular file", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
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
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	sort.Slice(members, func(i, j int) bool { return members[i].name < members[j].name })
	gzipWriter := gzip.NewWriter(temp)
	gzipWriter.ModTime = now
	gzipWriter.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	for _, member := range members {
		header := &tar.Header{
			Name: member.name, Mode: 0o600, Size: int64(len(member.data)), ModTime: now,
			AccessTime: time.Time{}, ChangeTime: time.Time{}, Uid: 0, Gid: 0, Uname: "", Gname: "",
			Format: tar.FormatPAX,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			_ = temp.Close()
			return err
		}
		if _, err := tarWriter.Write(member.data); err != nil {
			_ = temp.Close()
			return err
		}
	}
	if err := errors.Join(tarWriter.Close(), gzipWriter.Close(), temp.Sync(), temp.Close()); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
