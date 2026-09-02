package debugger

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

type replaySource struct {
	input       ReplayInput
	records     []replayRecord
	diagnostics []ReplayDiagnostic
}

func readReplaySource(ctx context.Context, inputPath string) (replaySource, error) {
	inputPath = strings.TrimSpace(inputPath)
	if inputPath == "" {
		return replaySource{}, fmt.Errorf("debugger: replay input path is empty")
	}
	info, err := os.Lstat(inputPath)
	if err != nil {
		return replaySource{}, fmt.Errorf("debugger: opening replay input: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return replaySource{}, fmt.Errorf("debugger: replay input must not be a symbolic link")
	}
	if info.IsDir() {
		return readReplayJournalDirectory(ctx, inputPath)
	}
	if !info.Mode().IsRegular() {
		return replaySource{}, fmt.Errorf("debugger: replay rejects live endpoints and non-regular paths")
	}
	if info.Size() > maxReplayInputBytes {
		return replaySource{}, fmt.Errorf("debugger: replay input exceeds %d bytes", maxReplayInputBytes)
	}
	file, err := os.Open(inputPath)
	if err != nil {
		return replaySource{}, err
	}
	defer func() { _ = file.Close() }()
	var magic [2]byte
	n, _ := file.Read(magic[:])
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return replaySource{}, err
	}
	if n == 2 && magic == [2]byte{0x1f, 0x8b} {
		return readReplayBundle(ctx, inputPath, file)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxReplayInputBytes+1))
	if err != nil {
		return replaySource{}, err
	}
	if int64(len(data)) > maxReplayInputBytes {
		return replaySource{}, fmt.Errorf("debugger: replay input exceeds %d bytes", maxReplayInputBytes)
	}
	return decodeReplayFile(inputPath, data)
}

func readReplayJournalDirectory(ctx context.Context, directory string) (replaySource, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return replaySource{}, err
	}
	type journalFile struct {
		path    string
		name    string
		modTime time.Time
	}
	files := make([]journalFile, 0)
	var totalBytes int64
	for _, entry := range entries {
		name := entry.Name()
		isJournalSegment := name == "events.jsonl" || strings.HasPrefix(name, "events-") && strings.HasSuffix(name, ".jsonl")
		if !isJournalSegment {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return replaySource{}, fmt.Errorf("debugger: journal member %s is not a regular file", name)
		}
		info, err := entry.Info()
		if err != nil {
			return replaySource{}, err
		}
		if info.Size() < 0 || info.Size() > maxReplayInputBytes || totalBytes+info.Size() > maxReplayInputBytes {
			return replaySource{}, fmt.Errorf("debugger: journal directory exceeds the %d-byte replay limit", maxReplayInputBytes)
		}
		totalBytes += info.Size()
		files = append(files, journalFile{path: filepath.Join(directory, name), name: name, modTime: info.ModTime()})
	}
	if len(files) == 0 {
		return replaySource{}, fmt.Errorf("debugger: directory is not a diagnostic journal (no events*.jsonl files)")
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].name == "events.jsonl" {
			return false
		}
		if files[j].name == "events.jsonl" {
			return true
		}
		if files[i].modTime.Equal(files[j].modTime) {
			return files[i].name < files[j].name
		}
		return files[i].modTime.Before(files[j].modTime)
	})
	source := replaySource{input: ReplayInput{Path: directory, Format: "journal", SchemaVersion: diagnostics.SchemaVersion}}
	for _, item := range files {
		if err := ctx.Err(); err != nil {
			return replaySource{}, err
		}
		file, err := os.Open(item.path)
		if err != nil {
			return replaySource{}, err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maxReplayInputBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			return replaySource{}, readErr
		}
		if closeErr != nil {
			return replaySource{}, closeErr
		}
		if int64(len(data)) > maxReplayInputBytes {
			return replaySource{}, fmt.Errorf("debugger: journal member %s exceeds the replay limit", item.name)
		}
		records, corrupt, err := decodeJournalJSONL(data)
		if err != nil {
			return replaySource{}, fmt.Errorf("debugger: %s: %w", item.name, err)
		}
		source.records = append(source.records, records...)
		source.input.CorruptLines += corrupt
	}
	source.input.Records = len(source.records)
	if source.input.CorruptLines > 0 {
		source.diagnostics = append(source.diagnostics, ReplayDiagnostic{Severity: "warning", Code: "corrupt_journal_lines", Message: fmt.Sprintf("%d corrupt journal line(s) were skipped without modifying the input", source.input.CorruptLines)})
	}
	return source, nil
}

func decodeReplayFile(inputPath string, data []byte) (replaySource, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return replaySource{}, fmt.Errorf("debugger: replay input is empty")
	}
	if json.Valid(trimmed) {
		var header map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &header); err != nil {
			return replaySource{}, err
		}
		if _, ok := header["windows"]; ok {
			return decodeEventsExport(inputPath, trimmed)
		}
		if _, ok := header["records"]; ok {
			records, schema, corrupt, err := decodeCausalRecords(trimmed)
			if err != nil {
				return replaySource{}, err
			}
			return replaySource{input: ReplayInput{Path: inputPath, Format: "causal-records", SchemaVersion: schema, Records: len(records), CorruptLines: corrupt}, records: records}, nil
		}
		record, err := decodeFlexibleTrace(trimmed)
		if err == nil {
			return replaySource{input: ReplayInput{Path: inputPath, Format: "journal", SchemaVersion: diagnostics.SchemaVersion, Records: 1}, records: []replayRecord{record}}, nil
		}
	}
	records, corrupt, err := decodeJournalJSONL(data)
	if err != nil {
		return replaySource{}, err
	}
	if len(records) == 0 {
		return replaySource{}, fmt.Errorf("debugger: input is neither a supported journal nor a diagnostic bundle")
	}
	source := replaySource{input: ReplayInput{Path: inputPath, Format: "journal", SchemaVersion: diagnostics.SchemaVersion, Records: len(records), CorruptLines: corrupt}, records: records}
	if corrupt > 0 {
		source.diagnostics = append(source.diagnostics, ReplayDiagnostic{Severity: "warning", Code: "corrupt_journal_lines", Message: fmt.Sprintf("%d corrupt journal line(s) were skipped without modifying the input", corrupt)})
	}
	return source, nil
}

func readReplayBundle(ctx context.Context, inputPath string, reader io.Reader) (replaySource, error) {
	gz, err := gzip.NewReader(reader)
	if err != nil {
		return replaySource{}, fmt.Errorf("debugger: invalid replay bundle gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tarReader := tar.NewReader(gz)
	members := make(map[string][]byte)
	var total int64
	memberCount := 0
	for {
		if err := ctx.Err(); err != nil {
			return replaySource{}, err
		}
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return replaySource{}, fmt.Errorf("debugger: invalid replay bundle tar: %w", err)
		}
		memberCount++
		if memberCount > maxReplayMembers {
			return replaySource{}, fmt.Errorf("debugger: replay bundle exceeds member-count limit of %d", maxReplayMembers)
		}
		clean := path.Clean(header.Name)
		if clean != header.Name || path.IsAbs(header.Name) || strings.Contains(clean, "/") || clean == "." || strings.HasPrefix(clean, "..") {
			return replaySource{}, fmt.Errorf("debugger: unsafe bundle member %q", header.Name)
		}
		if header.Typeflag != tar.TypeReg {
			return replaySource{}, fmt.Errorf("debugger: bundle member %q is not a regular file", header.Name)
		}
		if header.Size < 0 || header.Size > maxReplayMemberBytes || total+header.Size > maxReplayInputBytes {
			return replaySource{}, fmt.Errorf("debugger: bundle member %q exceeds replay limits", header.Name)
		}
		if _, duplicate := members[header.Name]; duplicate {
			return replaySource{}, fmt.Errorf("debugger: duplicate bundle member %q", header.Name)
		}
		data, err := io.ReadAll(io.LimitReader(tarReader, maxReplayMemberBytes+1))
		if err != nil {
			return replaySource{}, err
		}
		if int64(len(data)) != header.Size {
			return replaySource{}, fmt.Errorf("debugger: truncated bundle member %q", header.Name)
		}
		total += int64(len(data))
		members[header.Name] = data
	}
	manifestData := members["manifest.json"]
	if len(manifestData) == 0 {
		return replaySource{}, fmt.Errorf("debugger: bundle is incomplete: manifest.json is missing")
	}
	var manifest BundleManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return replaySource{}, fmt.Errorf("debugger: invalid bundle manifest: %w", err)
	}
	if !supportedBundleSchema(manifest.SchemaVersion) {
		return replaySource{}, fmt.Errorf("debugger: unsupported bundle schema %d", manifest.SchemaVersion)
	}
	if manifest.Mode != BundleModeShare && manifest.Mode != BundleModeForensic {
		return replaySource{}, fmt.Errorf("debugger: unsupported bundle mode %q", manifest.Mode)
	}
	if manifest.LocalOnly != (manifest.Mode == BundleModeForensic) {
		return replaySource{}, fmt.Errorf("debugger: bundle mode and local_only policy disagree")
	}
	var causalEntry *BundleManifestEntry
	causalEntries := 0
	for index := range manifest.Entries {
		entry := &manifest.Entries[index]
		if entry.Member == "causal-records.json" {
			causalEntries++
			causalEntry = entry
		}
	}
	if causalEntries != 1 || causalEntry == nil || causalEntry.Decision != "included" {
		return replaySource{}, fmt.Errorf("debugger: bundle is incomplete: manifest does not include causal-records.json")
	}
	causalData := members["causal-records.json"]
	if len(causalData) == 0 {
		return replaySource{}, fmt.Errorf("debugger: bundle is incomplete: causal-records.json is missing")
	}
	if causalEntry.Bytes > 0 && causalEntry.Bytes != len(causalData) {
		return replaySource{}, fmt.Errorf("debugger: bundle causal-records.json size does not match the manifest")
	}
	records, schema, corrupt, err := decodeCausalRecords(causalData)
	if err != nil {
		return replaySource{}, err
	}
	if schema != manifest.SchemaVersion {
		return replaySource{}, fmt.Errorf("debugger: bundle schema %d does not match causal-records schema %d", manifest.SchemaVersion, schema)
	}
	source := replaySource{
		input: ReplayInput{Path: inputPath, Format: "bundle", SchemaVersion: schema, BundleMode: manifest.Mode,
			LocalOnly: manifest.LocalOnly, Records: len(records), CorruptLines: corrupt, Truncated: manifest.Truncated},
		records: records,
	}
	if manifest.LocalOnly {
		source.diagnostics = append(source.diagnostics, ReplayDiagnostic{Severity: "warning", Code: "forensic_local_only", Message: firstNonEmptyReplay(manifest.Warning, "forensic bundle is local-only and must not be shared")})
	}
	if manifest.Truncated {
		source.diagnostics = append(source.diagnostics, ReplayDiagnostic{Severity: "warning", Code: "bundle_truncated", Message: "the bundle manifest reports truncated evidence; missing prefixes are treated as incomplete"})
	}
	if corrupt > 0 {
		source.diagnostics = append(source.diagnostics, ReplayDiagnostic{Severity: "warning", Code: "corrupt_journal_lines", Message: fmt.Sprintf("the bundle reports %d corrupt journal line(s)", corrupt)})
	}
	return source, nil
}

func supportedBundleSchema(schema int) bool {
	return schema == bundleSchemaVersionV1 || schema == bundleSchemaVersionV2
}

func decodeEventsExport(inputPath string, data []byte) (replaySource, error) {
	var header struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return replaySource{}, err
	}
	if header.SchemaVersion != protocol.EventsExportSchemaVersion &&
		header.SchemaVersion != protocol.EventsExportSchemaV12 &&
		header.SchemaVersion != protocol.EventsExportSchemaV11 &&
		header.SchemaVersion != protocol.EventsExportSchemaV10 &&
		header.SchemaVersion != protocol.EventsExportSchemaV9 &&
		header.SchemaVersion != protocol.EventsExportSchemaV8 {
		return replaySource{}, fmt.Errorf("debugger: unsupported events export schema %d", header.SchemaVersion)
	}
	var payload struct {
		Windows []struct {
			WindowID int                     `json:"window_id"`
			Events   []protocol.SessionEvent `json:"events"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return replaySource{}, fmt.Errorf("debugger: invalid events export: %w", err)
	}
	records := make([]replayRecord, 0)
	for _, window := range payload.Windows {
		for _, event := range window.Events {
			records = append(records, replayRecordFromSessionEvent(window.WindowID, event, header.SchemaVersion))
		}
	}
	source := replaySource{input: ReplayInput{Path: inputPath, Format: "events-export", SchemaVersion: header.SchemaVersion, Records: len(records)}, records: records}
	if header.SchemaVersion == protocol.EventsExportSchemaV8 {
		source.diagnostics = append(source.diagnostics, ReplayDiagnostic{Severity: "warning", Code: "schema_v8_partial", Message: "schema-v8 exports lack reducer decisions and complete normalized inputs; replay compares only cases with sufficient source/action/icon evidence"})
	}
	return source, nil
}
