package debugger

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

func TestShareBundleExcludesSecretCorpusRawSourcesAndIsDeterministic(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 34, 56, 0, time.UTC)
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	secretCorpus := []string{
		"sk-abcdefghijklmnopqrstuvwxyz", "ghp_1234567890abcdefghij", "AKIA1234567890ABCDEF",
		"Bearer abc.def.ghi", "password=hunter2", "PROMPT-CANARY-DO-NOT-SHARE",
	}
	record := diagnostics.NewEnvelope("daemon.renderer.failed")
	record.TraceID = "trace-sensitive"
	record.RequestID = "request-sensitive"
	record.Profile = "personal-profile"
	record.WindowID = 42
	record.Outcome = "failed"
	record.Reason = "render_title"
	record.Evidence = map[string]string{"event": "PostToolUse", "message": strings.Join(secretCorpus, " ")}
	if err := journal.Append(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	launchID := "11111111-1111-4111-8111-111111111111"
	nativeDir, err := diagnostics.EnsureNativeLaunch(paths, launchID)
	if err != nil {
		t.Fatal(err)
	}
	nativePath := filepath.Join(nativeDir, "native.log")
	native := `{"timestamp":"2026-07-15T12:00:00Z","level":"ERROR","component":"transport","status":"failed","message":"` + strings.Join(secretCorpus, " ") + ` at /Users/alice/secret/project"}` + "\n"
	if err := os.WriteFile(nativePath, []byte(native), 0o600); err != nil {
		t.Fatal(err)
	}
	captureDir := filepath.Join(paths.CaptureDir, "22222222-2222-4222-8222-222222222222")
	if err := os.MkdirAll(captureDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(captureDir, "raw.raw.json"), []byte(strings.Join(secretCorpus, " ")), 0o600); err != nil {
		t.Fatal(err)
	}
	enabled := true
	cfg := &config.Config{Defaults: config.Defaults{Diagnostics: config.DiagnosticsConfig{
		JournalEnabled: &enabled, JournalMaxMiB: 1, NativeLogging: &enabled, NativeMaxMiB: 1, NativeSessionMaxMiB: 1,
	}}}
	status := &StatusReport{
		SchemaVersion: StatusSchemaVersion, GeneratedAt: now, Healthy: false,
		Findings: []Finding{{Verdict: VerdictWarning, Check: "planted", Message: strings.Join(secretCorpus, " ")}},
	}
	base := BundleOptions{
		Paths: paths, Mode: BundleModeShare, Selector: Selector{RequestID: "request-sensitive"}, Now: now,
		Journal: journal, Config: cfg, Status: status, Build: buildinfo.Info{Version: "test", BinaryHash: "binary-sensitive"},
		NativeSessions: []NativeSession{{WindowID: 42, Profile: "personal-profile", Harness: "codex", LaunchID: launchID, Coverage: "managed", LogPath: nativePath}},
	}
	first := filepath.Join(t.TempDir(), "first.tar.gz")
	second := filepath.Join(t.TempDir(), "second.tar.gz")
	base.OutputPath = first
	result, err := CreateBundle(t.Context(), base)
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != BundleModeShare || result.Manifest.LocalOnly {
		t.Fatalf("result = %+v", result)
	}
	base.OutputPath = second
	if _, err := CreateBundle(t.Context(), base); err != nil {
		t.Fatal(err)
	}
	firstBytes, _ := os.ReadFile(first)
	secondBytes, _ := os.ReadFile(second)
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("fixed-time bundles are not deterministic")
	}
	members := readBundleMembers(t, first)
	for _, name := range []string{"status.json", "doctor.json", "versions.json", "configuration.json", "causal-records.json", "asl-records.json", "explanation.json", "native-excerpts.jsonl", "manifest.json"} {
		if _, ok := members[name]; !ok {
			t.Errorf("bundle member %s missing", name)
		}
	}
	all := bytes.Join(mapValues(members), []byte("\n"))
	for _, secret := range secretCorpus {
		if bytes.Contains(all, []byte(secret)) {
			t.Errorf("share bundle contains planted secret %q", secret)
		}
	}
	if bytes.Contains(all, []byte(nativePath)) || bytes.Contains(all, []byte("personal-profile")) || bytes.Contains(all, []byte("trace-sensitive")) {
		t.Fatal("share bundle leaked paths or unhashed identifiers")
	}
	if !bytes.Contains(members["manifest.json"], []byte("raw capture material is never an archive input")) {
		t.Fatal("manifest does not explain raw capture exclusion")
	}
	if !bytes.Contains(members["causal-records.json"], []byte(`"journal_snapshot"`)) ||
		!bytes.Contains(members["causal-records.json"], []byte(`"records_matched": 1`)) ||
		!bytes.Contains(members["causal-records.json"], []byte(`"segment_files_read": 1`)) {
		t.Fatalf("share causal records omitted snapshot metadata: %s", members["causal-records.json"])
	}
	var asl struct {
		SchemaVersion        int                            `json:"schema_version"`
		JournalSnapshotScope string                         `json:"journal_snapshot_scope"`
		JournalSnapshot      diagnostics.SnapshotStatistics `json:"journal_snapshot"`
		Records              []diagnostics.TraceEnvelope    `json:"records"`
	}
	if err := json.Unmarshal(members["asl-records.json"], &asl); err != nil {
		t.Fatalf("decode ASL records: %v\n%s", err, members["asl-records.json"])
	}
	if asl.SchemaVersion != 2 || asl.JournalSnapshotScope != "source_snapshot" ||
		asl.JournalSnapshot.RecordsMatched != 1 || len(asl.Records) != 0 {
		t.Fatalf("share ASL snapshot scope = schema %d scope %q snapshot %+v records %d", asl.SchemaVersion, asl.JournalSnapshotScope, asl.JournalSnapshot, len(asl.Records))
	}
	var explanation struct {
		SchemaVersion   int                            `json:"schema_version"`
		Limits          ExplanationLimits              `json:"limits"`
		JournalSnapshot diagnostics.SnapshotStatistics `json:"journal_snapshot"`
	}
	if err := json.Unmarshal(members["explanation.json"], &explanation); err != nil {
		t.Fatalf("decode bounded explanation: %v\n%s", err, members["explanation.json"])
	}
	if explanation.SchemaVersion != 2 || explanation.Limits != (ExplanationLimits{
		GraphDepth: DefaultExplainGraphDepth, MaxNodes: DefaultExplainMaxNodes, MaxEdges: DefaultExplainMaxEdges,
	}) || explanation.JournalSnapshot.RecordsMatched != 1 {
		t.Fatalf("bundle explanation limits/snapshot = schema %d limits %+v snapshot %+v", explanation.SchemaVersion, explanation.Limits, explanation.JournalSnapshot)
	}
	if mode := mustMode(t, first); mode.Perm() != 0o600 {
		t.Fatalf("bundle mode = %o, want 0600", mode.Perm())
	}
}

func TestShareBundleScopeIdentifiersAreHMACRedacted(t *testing.T) {
	const rawScope = "session-sensitive/turn:turn-sensitive"
	const hashedScope = "hmac-sha256:scope-redacted"
	redactor := newBundleRedactor(BundleModeShare, func(value string) string {
		if value == rawScope {
			return hashedScope
		}
		return "hmac-sha256:identifier-redacted"
	}, nil)
	data, err := redactor.JSON(ExplanationReport{ExpectedSequences: []ExpectedSequenceDiagnostic{{
		Kind: "prompt_stop", Scope: rawScope, Status: CertaintyUncertain,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(rawScope)) || bytes.Contains(data, []byte("session-sensitive")) || bytes.Contains(data, []byte("turn-sensitive")) {
		t.Fatalf("share explanation leaked raw scope: %s", data)
	}
	if !bytes.Contains(data, []byte(hashedScope)) {
		t.Fatalf("share explanation omitted HMAC scope: %s", data)
	}
}

func TestShareBundlePreservesControlledExplanationTruncation(t *testing.T) {
	redactor := newBundleRedactor(BundleModeShare, func(string) string {
		return "hmac-sha256:identifier-redacted"
	}, nil)
	data, err := redactor.JSON(ExplanationReport{
		Limits: ExplanationLimits{GraphDepth: 2, MaxNodes: 250, MaxEdges: 1000},
		Truncation: ExplanationTruncation{
			Truncated: true, Reasons: []string{TruncationMaxNodes}, PrunedNodeCandidates: 3,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"reasons": [`)) || !bytes.Contains(data, []byte(`"max_nodes"`)) ||
		!bytes.Contains(data, []byte(`"pruned_node_candidates": 3`)) {
		t.Fatalf("share explanation lost controlled truncation metadata: %s", data)
	}
}

func TestShareBundleProfileIdentifiersAreNotEmbeddedInChecks(t *testing.T) {
	const profile = "personal-profile-sensitive"
	report := StatusReport{Findings: make([]Finding, 0)}
	report.addHookProfileFinding(HookProfile{
		Profile: profile, Verdict: VerdictWarning, CheckError: "profile hook warning",
	})
	if len(report.Findings) != 1 || report.Findings[0].Check != "hooks_profile" {
		t.Fatalf("profile finding = %+v", report.Findings)
	}
	report.Hooks = HookReport{Profiles: []HookProfile{{Profile: profile, Verdict: VerdictWarning}}}
	redactor := newBundleRedactor(BundleModeShare, func(string) string {
		return "hmac-sha256:identifier-redacted"
	}, nil)
	data, err := redactor.JSON(report)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(profile)) {
		t.Fatalf("share status leaked raw profile: %s", data)
	}
}

func TestBundleRedactionAllowsOnlySafeProcessLifecycleEvidence(t *testing.T) {
	input := map[string]any{"evidence": map[string]string{
		"event":                  "HarnessProcessExited",
		"process_harness":        "claude",
		"process_pid":            "4242",
		"process_start_token":    "Thu Jul 16 10:20:30 2026",
		"process_observation":    "replaced_instance",
		"process_mode":           "pipe",
		"withdrawn_facts":        "1",
		"terminal_hook_evidence": "absent",
		"render_generation":      "9",
		"command_line":           "claude -p PROMPT-CANARY",
		"command":                "claude -p COMMAND-CANARY",
		"args":                   "--prompt ARGS-CANARY",
		"prompt":                 "PROMPT-CANARY",
		"payload":                "PAYLOAD-CANARY",
		"path":                   "/Users/alice/private/project",
	}}
	for _, mode := range []BundleMode{BundleModeShare, BundleModeForensic} {
		t.Run(string(mode), func(t *testing.T) {
			redactor := newBundleRedactor(mode, func(string) string {
				return "hmac-sha256:identifier-redacted"
			}, nil)
			data, err := redactor.JSON(input)
			if err != nil {
				t.Fatal(err)
			}
			for _, safe := range []string{
				"HarnessProcessExited", "claude", "4242", "Thu Jul 16 10:20:30 2026",
				"replaced_instance", "pipe", "absent",
			} {
				if !bytes.Contains(data, []byte(safe)) {
					t.Errorf("%s bundle omitted safe process evidence %q: %s", mode, safe, data)
				}
			}
			for _, sensitive := range []string{
				"claude -p PROMPT-CANARY", "claude -p COMMAND-CANARY", "--prompt ARGS-CANARY",
				"PROMPT-CANARY", "PAYLOAD-CANARY", "/Users/alice/private/project",
			} {
				if bytes.Contains(data, []byte(sensitive)) {
					t.Errorf("%s bundle leaked lifecycle-adjacent value %q: %s", mode, sensitive, data)
				}
			}
		})
	}

	redactor := newBundleRedactor(BundleModeShare, func(string) string { return "hmac-sha256:redacted" }, nil)
	data, err := redactor.JSON(map[string]any{"process_start_token": "secret prompt content hidden here"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("secret prompt content hidden here")) || !bytes.Contains(data, []byte("[redacted]")) {
		t.Fatalf("mislabeled process token escaped redaction: %s", data)
	}
}

func TestForensicBundleRemainsRedactedAndCarriesLocalOnlyWarning(t *testing.T) {
	now := time.Date(2026, 7, 15, 13, 0, 0, 0, time.UTC)
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	record := diagnostics.NewEnvelope("hook.failure")
	record.Reason = "Bearer forensic-secret-token"
	record.Evidence = map[string]string{"prompt": "PROMPT-CANARY", "path": "/Users/alice/private/project"}
	if err := journal.Append(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "forensic.tar.gz")
	result, err := CreateBundle(t.Context(), BundleOptions{
		Paths: paths, OutputPath: output, Mode: BundleModeForensic, Now: now, Journal: journal,
		Build: buildinfo.Info{Version: "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Manifest.LocalOnly || !strings.Contains(result.Manifest.Warning, "LOCAL ONLY") {
		t.Fatalf("manifest = %+v", result.Manifest)
	}
	members := readBundleMembers(t, output)
	if !bytes.Contains(members["LOCAL_ONLY.txt"], []byte("Do not share")) {
		t.Fatal("forensic local-only warning missing")
	}
	all := bytes.Join(mapValues(members), []byte("\n"))
	for _, secret := range []string{"forensic-secret-token", "PROMPT-CANARY", "/Users/alice/private/project"} {
		if bytes.Contains(all, []byte(secret)) {
			t.Errorf("forensic bundle contains sensitive value %q", secret)
		}
	}
}

func TestBundleRejectsSymlinkTargetWithoutPartialPublication(t *testing.T) {
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	target := filepath.Join(directory, "real.tar.gz")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "bundle.tar.gz")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	_, err = CreateBundle(t.Context(), BundleOptions{Paths: paths, OutputPath: link, Mode: BundleModeShare, Journal: journal})
	if err == nil {
		t.Fatal("symlink target unexpectedly accepted")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "original" {
		t.Fatalf("target was modified: %q", data)
	}
	matches, _ := filepath.Glob(filepath.Join(directory, ".bundle.tar.gz.*.tmp"))
	if len(matches) != 0 {
		t.Fatalf("partial bundle files remain: %v", matches)
	}
}

func readBundleMembers(t *testing.T, path string) map[string][]byte {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gz.Close() }()
	reader := tar.NewReader(gz)
	members := make(map[string][]byte)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		members[header.Name] = data
	}
	return members
}

func mapValues(values map[string][]byte) [][]byte {
	result := make([][]byte, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func mustMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode()
}
