package transcript

import (
	"bufio"
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

// ClaudeReader parses transcripts written by the Claude harness.
//
// Layout: <homeDir>/projects/<encoded-cwd>/<session-uuid>.jsonl
// Each line is a JSON record. The schema is best-effort and varies by
// Claude CLI version; this reader treats all fields as optional and skips
// anything it cannot understand.
type ClaudeReader struct{}

// NewClaudeReader returns a ready-to-use Claude reader.
func NewClaudeReader() ClaudeReader { return ClaudeReader{} }

// Harness reports the canonical harness name this reader speaks for.
func (ClaudeReader) Harness() string { return harness.Claude }

// claudeMaxLineBytes lifts bufio.Scanner above its default 64 KiB cap so
// long thinking/tool_use blocks do not cause silent line skips.
const claudeMaxLineBytes = 16 << 20 // 16 MiB

// claudeRecord is the lenient envelope used for both metadata extraction
// and per-message decoding. Every field is optional; the reader inspects
// whichever values are present on a given line.
type claudeRecord struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	CWD       string          `json:"cwd"`
	SessionID string          `json:"sessionId"`
	IsMeta    bool            `json:"isMeta"`
	Message   *claudeMessage  `json:"message"`
	Summary   string          `json:"summary"`
	Content   json.RawMessage `json:"content"`
}

type claudeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// Discover walks <homeDir>/projects and returns one SessionMeta per .jsonl
// file. Per-directory or per-file errors are logged at debug level and
// otherwise skipped — a single corrupt session must not hide the rest.
func (r ClaudeReader) Discover(homeDir string, opts DiscoverOptions) ([]SessionMeta, error) {
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

	var out []SessionMeta
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		projDir := filepath.Join(root, entry.Name())
		files, err := os.ReadDir(projDir)
		if err != nil {
			slog.Debug("transcript.claude: skip project dir", "dir", projDir, "err", err)
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(projDir, f.Name())
			info, err := os.Stat(path)
			if err != nil {
				slog.Debug("transcript.claude: skip file", "path", path, "err", err)
				continue
			}
			if !opts.MinTime.IsZero() && info.ModTime().Before(opts.MinTime) {
				continue
			}
			if opts.Cached != nil {
				if meta, ok := opts.Cached(path, info.Size(), info.ModTime()); ok {
					out = append(out, meta)
					continue
				}
			}
			meta, err := r.parseFile(path, info, entry.Name())
			if err != nil {
				slog.Debug("transcript.claude: skip file", "path", path, "err", err)
				continue
			}
			out = append(out, meta)
		}
	}
	return out, nil
}

// ReadMessages decodes a single transcript into role-tagged Message entries
// suitable for display and substring search. Non-conversation records
// (snapshots, permission-mode markers, etc.) are skipped silently.
func (r ClaudeReader) ReadMessages(path string) ([]Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), claudeMaxLineBytes)

	var msgs []Message
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec claudeRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			slog.Debug("transcript.claude: malformed line", "path", path, "err", err)
			continue
		}
		msg, ok := messageFromRecord(rec)
		if !ok {
			continue
		}
		msgs = append(msgs, msg)
	}
	// Ignore scanner.Err() for unexpected EOF / token-too-long: callers
	// treat partial reads (sessions still being written) as success.
	return msgs, nil
}

// parseFile streams one transcript end to end to produce SessionMeta. The
// loop tracks first/last timestamp, message count, snippet, and CWD — all
// using whatever subset of fields each line happens to carry.
func (r ClaudeReader) parseFile(path string, info os.FileInfo, encodedDir string) (SessionMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return SessionMeta{}, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), claudeMaxLineBytes)

	meta := SessionMeta{
		Harness:     harness.Claude,
		ID:          strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Path:        path,
		FileSize:    info.Size(),
		FileModTime: info.ModTime(),
	}
	var searchBuf strings.Builder

	for scanner.Scan() {
		var rec claudeRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue
		}
		if meta.CWD == "" && rec.CWD != "" {
			meta.CWD = rec.CWD
		}
		if rec.SessionID != "" && meta.ID == "" {
			meta.ID = rec.SessionID
		}
		if ts, ok := parseClaudeTimestamp(rec.Timestamp); ok {
			if meta.StartedAt.IsZero() {
				meta.StartedAt = ts
			}
			meta.LastAt = ts
		}
		if !rec.IsMeta && (rec.Type == "user" || rec.Type == "assistant") {
			meta.MessageCount++
		}
		if !rec.IsMeta && rec.Message != nil && (rec.Type == "user" || rec.Type == "assistant") {
			if text := flattenClaudeContent(rec.Message.Content); text != "" {
				if meta.Snippet == "" && rec.Type == "user" {
					meta.Snippet = truncateSnippet(text)
				}
				appendSearchText(&searchBuf, text)
			}
		}
	}
	meta.SearchText = searchBuf.String()

	// Last-resort CWD: decode the project dir name. Claude encodes the
	// absolute path by replacing path separators with '-', which is lossy
	// for any path containing literal '-'. We only fall back here when no
	// record on the file carried a `cwd` field.
	if meta.CWD == "" && encodedDir != "" {
		meta.CWD = "/" + strings.ReplaceAll(strings.TrimPrefix(encodedDir, "-"), "-", "/")
	}

	return meta, nil
}

// messageFromRecord lifts a single line into a Message when the record is a
// top-level user or assistant turn (ignoring meta markers). All other line
// types — snapshots, summaries, permission-mode flips, etc. — are skipped.
func messageFromRecord(rec claudeRecord) (Message, bool) {
	if rec.IsMeta || rec.Message == nil {
		return Message{}, false
	}
	if rec.Type != "user" && rec.Type != "assistant" {
		return Message{}, false
	}
	text := flattenClaudeContent(rec.Message.Content)
	if text == "" {
		return Message{}, false
	}
	ts, _ := parseClaudeTimestamp(rec.Timestamp)
	role := rec.Message.Role
	if role == "" {
		role = rec.Type
	}
	return Message{Role: role, Text: text, Timestamp: ts}, true
}

// flattenClaudeContent reduces the polymorphic `message.content` payload to
// a single display string. Claude encodes user content as a plain string
// and assistant content as an array of `{type, ...}` blocks; this helper
// concatenates text/thinking blocks and renders tool_use / tool_result as
// brief placeholders so searches can still find tool-related sessions.
func flattenClaudeContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try string form first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Otherwise expect an array of blocks.
	var blocks []map[string]any
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var sb strings.Builder
	for _, b := range blocks {
		blockType, _ := b["type"].(string)
		switch blockType {
		case "text", "thinking":
			if t, ok := b[blockType].(string); ok {
				if sb.Len() > 0 {
					sb.WriteByte('\n')
				}
				sb.WriteString(t)
			}
		case "tool_use":
			if name, ok := b["name"].(string); ok {
				if sb.Len() > 0 {
					sb.WriteByte('\n')
				}
				sb.WriteString("[tool_use: ")
				sb.WriteString(name)
				sb.WriteByte(']')
			}
		case "tool_result":
			for _, content := range flattenClaudeToolResultContent(b["content"]) {
				appendClaudeContentLine(&sb, content)
			}
		}
	}
	return sb.String()
}

func appendClaudeContentLine(sb *strings.Builder, text string) {
	if text == "" {
		return
	}
	if sb.Len() > 0 {
		sb.WriteByte('\n')
	}
	sb.WriteString(text)
}

func flattenClaudeToolResultContent(raw any) []string {
	switch content := raw.(type) {
	case string:
		return []string{content}
	case []any:
		out := make([]string, 0, len(content))
		for _, item := range content {
			switch v := item.(type) {
			case string:
				if v != "" {
					out = append(out, v)
				}
			case map[string]any:
				if text, ok := v["text"].(string); ok && text != "" {
					out = append(out, text)
				}
			}
		}
		return out
	default:
		return nil
	}
}

func parseClaudeTimestamp(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	return time.Time{}, false
}
