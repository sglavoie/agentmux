package transcript

// Metadata-only discovery for the three harnesses.
//
// Each DiscoverMetadata below walks the same corpus its Discover twin walks and
// reads a strictly smaller thing: where the transcript is, which session it is,
// and which working directories it names. None of them decodes a message, a
// thinking block, or a tool payload, and none of them writes anything.
//
// Two independent guarantees hold that line, because one of them alone would be
// a promise rather than a mechanism:
//
//  1. A byte pre-filter. A line is handed to encoding/json only if it contains
//     the literal bytes of an identity marker — `"cwd"` for Claude, `session_meta`
//     or `turn_context` for Codex, `session.start` for Copilot. The filter is
//     sound in the direction that matters: it can pass a line that turns out to
//     carry no identity, which costs a wasted decode, but a line carrying an
//     identity field necessarily contains that field's name, so it cannot drop
//     one. Message-bearing lines never reach a parser at all.
//  2. Identity-only structs. Each type below has fields for identity and no
//     field for content, and none of them carries a json.RawMessage catch-all.
//     encoding/json skips an unknown field without materializing it, so even a
//     line that passes the pre-filter yields nothing but identity. There is no
//     variable in scope in any of these functions that a message body could be
//     assigned to.
//
// The working directory is collected as a set rather than a single value, and
// every record that names one contributes. A conversation that moved between
// directories therefore reports several, which the admission path treats as
// unavailable. That is the whole reason the scan keeps reading after the first
// identity record instead of stopping at it: stopping early would report a
// mixed-project conversation as a single-project one and authorize it against a
// directory that covers only part of its content.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
)

// directorySet accumulates distinct working directories in first-seen order.
// Order is preserved so a diagnostic listing them is stable, and the count is
// what the admission decision actually reads.
type directorySet struct {
	seen  map[string]struct{}
	order []string
}

func (s *directorySet) add(dir string) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return
	}
	if s.seen == nil {
		s.seen = make(map[string]struct{})
	}
	if _, ok := s.seen[dir]; ok {
		return
	}
	s.seen[dir] = struct{}{}
	s.order = append(s.order, dir)
}

func (s *directorySet) values() []string { return s.order }

// scanIdentityLines streams a transcript line by line, hands each line that
// survives the byte pre-filter to visit, and never retains a line.
//
// A record too large to scan must return an error: skipping one that named a
// second working directory would turn an ambiguous conversation into an
// apparently unambiguous one.
func scanIdentityLines(path string, markers [][]byte, visit func(line []byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	scanner := newJSONLScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if !containsAnyMarker(line, markers) {
			continue
		}
		visit(line)
	}
	return scanner.Err()
}

func containsAnyMarker(line []byte, markers [][]byte) bool {
	for _, marker := range markers {
		if bytes.Contains(line, marker) {
			return true
		}
	}
	return false
}

// --- Claude -----------------------------------------------------------------

// claudeIdentityRecord is the Claude line as identity only. `message` and
// `content` — the two fields carrying what was said — have no counterpart here,
// so decoding a Claude message line through this type produces an empty struct.
type claudeIdentityRecord struct {
	Timestamp string `json:"timestamp"`
	CWD       string `json:"cwd"`
	SessionID string `json:"sessionId"`
}

var claudeIdentityMarkers = [][]byte{[]byte(`"cwd"`)}

// DiscoverMetadata enumerates Claude conversations without reading a message.
func (r ClaudeReader) DiscoverMetadata(homeDir string, opts MetadataOptions) ([]Candidate, error) {
	if homeDir == "" {
		return nil, nil
	}
	root := harness.Lookup(harness.Claude).TranscriptsDir(homeDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var out []Candidate
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		projDir := filepath.Join(root, entry.Name())
		files, err := os.ReadDir(projDir)
		if err != nil {
			slog.Debug("transcript.claude: skip project dir for metadata", "dir", projDir, "err", err)
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(projDir, f.Name())
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			if !opts.MinTime.IsZero() && info.ModTime().Before(opts.MinTime) {
				continue
			}
			candidate, err := claudeCandidate(path, info, entry.Name(), f.Name())
			if err != nil {
				slog.Debug("transcript.claude: skip file for metadata", "path", path, "err", err)
				continue
			}
			out = append(out, candidate)
		}
	}
	return out, nil
}

func claudeCandidate(path string, info os.FileInfo, encodedDir, fileName string) (Candidate, error) {
	candidate := Candidate{
		ID:          strings.TrimSuffix(fileName, ".jsonl"),
		Path:        path,
		FileSize:    info.Size(),
		FileModTime: info.ModTime(),
	}
	var dirs directorySet
	err := scanIdentityLines(path, claudeIdentityMarkers, func(line []byte) {
		var rec claudeIdentityRecord
		if json.Unmarshal(line, &rec) != nil {
			return
		}
		dirs.add(rec.CWD)
		if rec.SessionID != "" {
			candidate.ID = rec.SessionID
		}
		if ts, ok := parseClaudeTimestamp(rec.Timestamp); ok {
			noteCandidateTime(&candidate, ts)
		}
	})
	if err != nil {
		return Candidate{}, err
	}
	candidate.Directories = dirs.values()
	if len(candidate.Directories) == 0 && encodedDir != "" {
		// Claude encodes the working directory into the project directory name.
		// It is a lossy encoding — a real hyphen in a path segment is
		// indistinguishable from a separator — so it is a last resort rather
		// than the primary source, exactly as it is in the reader's own parse.
		// A wrong directory here fails closed anyway: it authorizes against a
		// project the conversation does not belong to only if the operator has
		// opted that project in, and any mismatch shows up as a refusal.
		candidate.Directories = []string{"/" + strings.ReplaceAll(strings.TrimPrefix(encodedDir, "-"), "-", "/")}
	}
	return candidate, nil
}

// --- Codex ------------------------------------------------------------------

// codexIdentityEnvelope keeps the envelope's type and timestamp and takes the
// payload as raw bytes, because the type is what decides whether the payload is
// worth looking at. The payload is only ever decoded through
// codexIdentityPayload below, which has no content fields; a `response_item`
// payload is never unmarshalled at all.
type codexIdentityEnvelope struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// codexIdentityPayload covers the identity fields of both session_meta and
// turn_context. A turn_context record is what makes a mid-session directory
// change visible: Codex writes one per turn and it carries the cwd in force for
// that turn, so a conversation that moved shows two directories here.
type codexIdentityPayload struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	CWD       string `json:"cwd"`
}

var codexIdentityMarkers = [][]byte{[]byte("session_meta"), []byte("turn_context")}

// DiscoverMetadata enumerates Codex conversations without reading a message.
func (r CodexReader) DiscoverMetadata(homeDir string, opts MetadataOptions) ([]Candidate, error) {
	if homeDir == "" {
		return nil, nil
	}
	root := harness.Lookup(harness.Codex).TranscriptsDir(homeDir)
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var out []Candidate
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			slog.Debug("transcript.codex: metadata walk error", "path", path, "err", walkErr)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, rolloutPrefix) || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil
		}
		if !opts.MinTime.IsZero() && info.ModTime().Before(opts.MinTime) {
			return nil
		}
		candidate, err := codexCandidate(path, info, name)
		if err != nil {
			slog.Debug("transcript.codex: skip file for metadata", "path", path, "err", err)
			return nil
		}
		out = append(out, candidate)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func codexCandidate(path string, info os.FileInfo, fileName string) (Candidate, error) {
	candidate := Candidate{
		ID:          codexIDFromFilename(fileName),
		Path:        path,
		FileSize:    info.Size(),
		FileModTime: info.ModTime(),
	}
	var dirs directorySet
	err := scanIdentityLines(path, codexIdentityMarkers, func(line []byte) {
		var env codexIdentityEnvelope
		if json.Unmarshal(line, &env) != nil {
			return
		}
		if env.Type != "session_meta" && env.Type != "turn_context" {
			// The pre-filter matched the bytes somewhere else in the line. The
			// payload is not identity, so it is not decoded.
			return
		}
		if ts, ok := parseCodexTimestamp(env.Timestamp); ok {
			noteCandidateTime(&candidate, ts)
		}
		if len(env.Payload) == 0 {
			return
		}
		var payload codexIdentityPayload
		if json.Unmarshal(env.Payload, &payload) != nil {
			return
		}
		dirs.add(payload.CWD)
		if env.Type == "session_meta" {
			if payload.ID != "" {
				candidate.ID = payload.ID
			}
			if ts, ok := parseCodexTimestamp(payload.Timestamp); ok {
				noteCandidateTime(&candidate, ts)
			}
		}
	})
	if err != nil {
		return Candidate{}, err
	}
	candidate.Directories = dirs.values()
	return candidate, nil
}

// --- Copilot ----------------------------------------------------------------

// copilotIdentityEnvelope mirrors the Codex shape: type and timestamp decide
// whether the data is identity, and the data is only ever decoded through
// copilotIdentityData, which has no content field.
type copilotIdentityEnvelope struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

type copilotIdentityData struct {
	SessionID string `json:"sessionId"`
	StartTime string `json:"startTime"`
	Context   struct {
		CWD string `json:"cwd"`
	} `json:"context"`
}

var copilotIdentityMarkers = [][]byte{[]byte("session.start")}

// DiscoverMetadata enumerates Copilot conversations without reading a message.
func (r CopilotReader) DiscoverMetadata(homeDir string, opts MetadataOptions) ([]Candidate, error) {
	if homeDir == "" {
		return nil, nil
	}
	root := harness.Lookup(harness.Copilot).TranscriptsDir(homeDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var out []Candidate
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), "events.jsonl")
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if !opts.MinTime.IsZero() && info.ModTime().Before(opts.MinTime) {
			continue
		}
		candidate, err := copilotCandidate(path, info, entry.Name())
		if err != nil {
			slog.Debug("transcript.copilot: skip file for metadata", "path", path, "err", err)
			continue
		}
		out = append(out, candidate)
	}
	return out, nil
}

func copilotCandidate(path string, info os.FileInfo, sessionDir string) (Candidate, error) {
	candidate := Candidate{
		// The session directory is named with the session UUID, so the fallback
		// identity is already correct before a single byte is read.
		ID:          sessionDir,
		Path:        path,
		FileSize:    info.Size(),
		FileModTime: info.ModTime(),
	}
	var dirs directorySet
	err := scanIdentityLines(path, copilotIdentityMarkers, func(line []byte) {
		var env copilotIdentityEnvelope
		if json.Unmarshal(line, &env) != nil {
			return
		}
		if env.Type != "session.start" {
			return
		}
		if ts, ok := parseCopilotTimestamp(env.Timestamp); ok {
			noteCandidateTime(&candidate, ts)
		}
		if len(env.Data) == 0 {
			return
		}
		var data copilotIdentityData
		if json.Unmarshal(env.Data, &data) != nil {
			return
		}
		dirs.add(data.Context.CWD)
		if data.SessionID != "" {
			candidate.ID = data.SessionID
		}
		if ts, ok := parseCopilotTimestamp(data.StartTime); ok {
			noteCandidateTime(&candidate, ts)
		}
	})
	if err != nil {
		return Candidate{}, err
	}
	candidate.Directories = dirs.values()
	return candidate, nil
}

// noteCandidateTime widens the candidate's observed time span. Only identity
// records carry timestamps on this path, so the span covers when the
// conversation was set up and last re-anchored rather than when the last message
// was written; the file's modification time remains the ordering key precisely
// because it does not depend on a harness having written a parseable timestamp.
func noteCandidateTime(candidate *Candidate, ts time.Time) {
	if ts.IsZero() {
		return
	}
	if candidate.StartedAt.IsZero() || ts.Before(candidate.StartedAt) {
		candidate.StartedAt = ts
	}
	if candidate.LastAt.IsZero() || ts.After(candidate.LastAt) {
		candidate.LastAt = ts
	}
}
