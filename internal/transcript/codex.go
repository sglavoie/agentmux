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

// CodexReader parses transcripts written by the Codex harness.
//
// Layout: <homeDir>/sessions/YYYY/MM/DD/rollout-*.jsonl
// Each line is a JSON envelope of the form {timestamp, type, payload}.
// The first line is always a `session_meta` record carrying the canonical
// session id and starting cwd; subsequent lines mix `response_item`,
// `event_msg`, `turn_context`, etc. The reader treats all fields as
// optional and skips anything it cannot understand.
type CodexReader struct{}

// NewCodexReader returns a ready-to-use Codex reader.
func NewCodexReader() CodexReader { return CodexReader{} }

// Harness reports the canonical harness name this reader speaks for.
func (CodexReader) Harness() string { return harness.Codex }

// codexMaxLineBytes mirrors claudeMaxLineBytes: Codex `function_call_output`
// payloads regularly exceed bufio.Scanner's 64 KiB default.
const codexMaxLineBytes = 16 << 20 // 16 MiB

// rolloutPrefix is the filename prefix Codex uses for every transcript.
const rolloutPrefix = "rollout-"

// codexEnvelope is the outer record carried on every line.
type codexEnvelope struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// codexSessionMetaPayload is the payload of the first record of every file.
type codexSessionMetaPayload struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	CWD       string `json:"cwd"`
}

// codexResponseItemPayload covers the four response_item flavours used:
// message, reasoning, function_call, function_call_output. Unused fields
// are simply absent on a given record.
type codexResponseItemPayload struct {
	Type    string              `json:"type"`
	Role    string              `json:"role"`
	Content []codexContentBlock `json:"content"`
	Name    string              `json:"name"`
	Output  string              `json:"output"`
}

type codexContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// codexEventMsgPayload covers the event_msg flavours. Only user_message is
// promoted into the conversation transcript; the rest (task_started,
// task_completed, ...) are ignored.
type codexEventMsgPayload struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Discover walks <homeDir>/sessions/YYYY/MM/DD/ and returns one SessionMeta
// per rollout-*.jsonl file. Per-file errors are logged at debug level so a
// single corrupt session cannot hide the rest.
func (r CodexReader) Discover(homeDir string, opts DiscoverOptions) ([]SessionMeta, error) {
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

	var out []SessionMeta
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			slog.Debug("transcript.codex: walk error", "path", path, "err", walkErr)
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
			slog.Debug("transcript.codex: skip file", "path", path, "err", err)
			return nil
		}
		if !opts.MinTime.IsZero() && info.ModTime().Before(opts.MinTime) {
			return nil
		}
		if opts.Cached != nil {
			if meta, ok := opts.Cached(path, info.Size(), info.ModTime()); ok {
				out = append(out, meta)
				return nil
			}
		}
		meta, err := r.parseFile(path, info)
		if err != nil {
			slog.Debug("transcript.codex: skip file", "path", path, "err", err)
			return nil
		}
		out = append(out, meta)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ReadMessages decodes a single transcript into role-tagged Message entries
// suitable for display and substring search. Non-conversation records
// (session_meta, turn_context, reasoning, task_started, ...) are skipped.
// User turns are taken from event_msg/user_message (the canonical, clean
// copy) so the auto-injected developer prompt and <environment_context>
// records do not pollute the transcript.
func (r CodexReader) ReadMessages(path string) ([]Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), codexMaxLineBytes)

	var msgs []Message
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var env codexEnvelope
		if err := json.Unmarshal(line, &env); err != nil {
			slog.Debug("transcript.codex: malformed line", "path", path, "err", err)
			continue
		}
		ts, _ := parseCodexTimestamp(env.Timestamp)
		switch env.Type {
		case "event_msg":
			var p codexEventMsgPayload
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				continue
			}
			if p.Type == "user_message" && p.Message != "" {
				msgs = append(msgs, Message{Role: "user", Text: p.Message, Timestamp: ts})
			}
		case "response_item":
			var p codexResponseItemPayload
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				continue
			}
			switch p.Type {
			case "message":
				// Skip developer/system injections and the user record
				// duplicated by event_msg/user_message above.
				if p.Role != "assistant" {
					continue
				}
				if text := flattenCodexContent(p.Content); text != "" {
					msgs = append(msgs, Message{Role: "assistant", Text: text, Timestamp: ts})
				}
			case "function_call":
				if p.Name != "" {
					msgs = append(msgs, Message{
						Role:      "tool",
						Text:      "[tool_use: " + p.Name + "]",
						Timestamp: ts,
					})
				}
			case "function_call_output":
				if p.Output != "" {
					msgs = append(msgs, Message{Role: "tool", Text: p.Output, Timestamp: ts})
				}
			}
		}
	}
	// Ignore scanner.Err(): partial trailing lines on live sessions are
	// expected and benign.
	return msgs, nil
}

// parseFile streams one transcript end to end to produce SessionMeta. The
// loop tracks first/last timestamp, message count, snippet, cwd, and id —
// using whichever subset of fields each line happens to carry.
func (r CodexReader) parseFile(path string, info os.FileInfo) (SessionMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return SessionMeta{}, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), codexMaxLineBytes)

	meta := SessionMeta{
		Harness:     harness.Codex,
		Path:        path,
		FileSize:    info.Size(),
		FileModTime: info.ModTime(),
	}
	var searchBuf strings.Builder

	for scanner.Scan() {
		var env codexEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &env); err != nil {
			continue
		}
		if ts, ok := parseCodexTimestamp(env.Timestamp); ok {
			if meta.StartedAt.IsZero() {
				meta.StartedAt = ts
			}
			meta.LastAt = ts
		}
		switch env.Type {
		case "session_meta":
			var p codexSessionMetaPayload
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				continue
			}
			if meta.ID == "" && p.ID != "" {
				meta.ID = p.ID
			}
			if meta.CWD == "" && p.CWD != "" {
				meta.CWD = p.CWD
			}
			// Prefer the session_meta inner timestamp (session start) over
			// the envelope timestamp (line write time) when both exist.
			if ts, ok := parseCodexTimestamp(p.Timestamp); ok {
				meta.StartedAt = ts
			}
		case "event_msg":
			var p codexEventMsgPayload
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				continue
			}
			if p.Type == "user_message" {
				meta.MessageCount++
				if meta.Snippet == "" && p.Message != "" {
					meta.Snippet = truncateSnippet(p.Message)
				}
				appendSearchText(&searchBuf, p.Message)
			}
		case "response_item":
			var p codexResponseItemPayload
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				continue
			}
			switch p.Type {
			case "message":
				if p.Role != "assistant" {
					continue
				}
				meta.MessageCount++
				if text := flattenCodexContent(p.Content); text != "" {
					appendSearchText(&searchBuf, text)
				}
			case "function_call":
				if p.Name != "" {
					appendSearchText(&searchBuf, "[tool_use: "+p.Name+"]")
				}
			case "function_call_output":
				if p.Output != "" {
					appendSearchText(&searchBuf, p.Output)
				}
			}
		}
	}
	meta.SearchText = searchBuf.String()

	// Fallback id: extract the UUID tail from the filename if no
	// session_meta record was found (corrupt or pre-session_meta vintage).
	if meta.ID == "" {
		meta.ID = codexIDFromFilename(filepath.Base(path))
	}

	return meta, nil
}

// flattenCodexContent reduces an assistant message's content array to a
// single display string. Assistant turns carry blocks of type
// "output_text"; user/developer turns (skipped before this is called) use
// "input_text". Both shapes share the same {type, text} structure.
func flattenCodexContent(blocks []codexContentBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, b := range blocks {
		if b.Text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(b.Text)
	}
	return sb.String()
}

// codexIDFromFilename strips the rollout- prefix, .jsonl suffix, and the
// 19-character ISO timestamp (YYYY-MM-DDTHH-MM-SS) to recover the UUID.
// Returns the trimmed filename as-is if it does not match the layout.
func codexIDFromFilename(name string) string {
	stem := strings.TrimSuffix(name, ".jsonl")
	stem = strings.TrimPrefix(stem, rolloutPrefix)
	const tsLen = len("2006-01-02T15-04-05")
	if len(stem) > tsLen+1 && stem[tsLen] == '-' {
		return stem[tsLen+1:]
	}
	return stem
}

func parseCodexTimestamp(s string) (time.Time, bool) {
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
