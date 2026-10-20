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
	"github.com/sglavoie/agentmux/internal/textutil"
)

// CopilotReader parses transcripts written by the GitHub Copilot CLI
// (copilot-agent).
//
// Layout: <homeDir>/session-state/<sessionUUID>/events.jsonl
// Each session is its own directory named with the session UUID. The
// transcript itself is `events.jsonl`; sibling files (plan.md, session.db,
// vscode.metadata.json, workspace.yaml, files/, research/, checkpoints/)
// are ignored. Some session directories exist without an events.jsonl
// (sessions interrupted before any event was written) — those are skipped.
//
// Each line is a JSON envelope of the form
// `{type, data, id, timestamp, parentId}`. The first line is always a
// `session.start` record carrying `data.context.cwd` and `data.startTime`.
// Subsequent records mix `user.message`, `assistant.message`,
// `assistant.turn_start/end`, `tool.execution_start/complete`,
// `session.model_change`, `session.mode_changed`, `session.task_complete`,
// and `session.shutdown`. Unknown event types are skipped silently.
//
// User-message decoding: `data.content` is the raw text the user typed.
// `data.transformedContent` is the model-facing prompt (with prelude like
// `<current_datetime>` and `<system_reminder>` blocks) — search and snippet
// use `data.content` to keep results clean.
//
// Assistant messages whose `data.content` is empty are tool-call-only
// turns (`data.toolRequests` populated, `data.reasoningText` is a short
// summary header). Those are not counted in MessageCount and are not
// emitted as messages — the underlying tool events surface separately.
type CopilotReader struct{}

// NewCopilotReader returns a ready-to-use Copilot reader.
func NewCopilotReader() CopilotReader { return CopilotReader{} }

// Harness reports the canonical harness name this reader speaks for.
func (CopilotReader) Harness() string { return harness.Copilot }

// copilotMaxLineBytes mirrors the other readers: tool.execution_complete
// payloads can exceed bufio.Scanner's 64 KiB default.
const copilotMaxLineBytes = 16 << 20 // 16 MiB

// copilotEnvelope is the outer record carried on every line.
type copilotEnvelope struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// copilotSessionStart is the payload of the first record of every file.
type copilotSessionStart struct {
	SessionID string `json:"sessionId"`
	StartTime string `json:"startTime"`
	Context   struct {
		CWD string `json:"cwd"`
	} `json:"context"`
}

// copilotUserMessage covers the user.message record. `content` is the raw
// user text; `transformedContent` is the model-facing variant and is
// deliberately ignored for snippet/search.
type copilotUserMessage struct {
	Content string `json:"content"`
}

// copilotAssistantMessage covers the assistant.message record. Empty
// content indicates a tool-only turn; content-bearing turns are the real
// assistant messages.
type copilotAssistantMessage struct {
	Content string `json:"content"`
}

// copilotToolStart covers tool.execution_start.
type copilotToolStart struct {
	ToolName string `json:"toolName"`
}

// copilotToolComplete covers tool.execution_complete. `result` is a raw
// JSON string carrying `{content, detailedContent}`; the reader emits it
// verbatim so substring search still hits inside tool output.
type copilotToolComplete struct {
	Result string `json:"result"`
}

// Discover walks <homeDir>/session-state/<UUID>/events.jsonl and returns
// one SessionMeta per transcript. Per-file errors are logged at debug
// level so a single corrupt session cannot hide the rest.
func (r CopilotReader) Discover(homeDir string, opts DiscoverOptions) ([]SessionMeta, error) {
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

	var out []SessionMeta
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), "events.jsonl")
		info, err := os.Stat(path)
		if err != nil {
			// Session directory without an events.jsonl — interrupted
			// before any event was written. Skip silently.
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
			slog.Debug("transcript.copilot: skip file", "path", path, "err", err)
			continue
		}
		out = append(out, meta)
	}
	return out, nil
}

// ReadMessages decodes a single transcript into role-tagged Message
// entries suitable for display and substring search. Infrastructure
// records (session.start, session.model_change, assistant.turn_start/end,
// session.shutdown, ...) and tool-only assistant turns are skipped.
func (r CopilotReader) ReadMessages(path string) ([]Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), copilotMaxLineBytes)

	var msgs []Message
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var env copilotEnvelope
		if err := json.Unmarshal(line, &env); err != nil {
			slog.Debug("transcript.copilot: malformed line", "path", path, "err", err)
			continue
		}
		ts, _ := parseCopilotTimestamp(env.Timestamp)
		switch env.Type {
		case "user.message":
			var p copilotUserMessage
			if err := json.Unmarshal(env.Data, &p); err != nil {
				continue
			}
			if p.Content != "" {
				msgs = append(msgs, Message{Role: "user", Text: p.Content, Timestamp: ts})
			}
		case "assistant.message":
			var p copilotAssistantMessage
			if err := json.Unmarshal(env.Data, &p); err != nil {
				continue
			}
			if p.Content != "" {
				msgs = append(msgs, Message{Role: "assistant", Text: p.Content, Timestamp: ts})
			}
		case "tool.execution_start":
			var p copilotToolStart
			if err := json.Unmarshal(env.Data, &p); err != nil {
				continue
			}
			if p.ToolName != "" {
				msgs = append(msgs, Message{
					Role:      "tool",
					Text:      "[tool_use: " + p.ToolName + "]",
					Timestamp: ts,
				})
			}
		case "tool.execution_complete":
			var p copilotToolComplete
			if err := json.Unmarshal(env.Data, &p); err != nil {
				continue
			}
			if p.Result != "" {
				msgs = append(msgs, Message{Role: "tool", Text: p.Result, Timestamp: ts})
			}
		}
	}
	// Ignore scanner.Err(): partial trailing lines on live sessions are
	// expected and benign.
	return msgs, nil
}

// ClassifySession verifies the indexed top-level Copilot conversation is
// readable. Copilot embeds subagent events in this parent event stream rather
// than creating separate transcript rows.
func (r CopilotReader) ClassifySession(path string) (SessionClass, error) {
	f, err := os.Open(path)
	if err != nil {
		return SessionClassUnknown, err
	}
	if err := f.Close(); err != nil {
		return SessionClassUnknown, err
	}
	return SessionClassMain, nil
}

// parseFile streams one transcript end to end to produce SessionMeta. The
// loop tracks first/last timestamp, message count, snippet, cwd, and id —
// using whichever subset of fields each line happens to carry.
func (r CopilotReader) parseFile(path string, info os.FileInfo, dirName string) (SessionMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return SessionMeta{}, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), copilotMaxLineBytes)

	meta := SessionMeta{
		Harness:      harness.Copilot,
		SessionClass: SessionClassMain,
		ID:           dirName,
		Path:         path,
		FileSize:     info.Size(),
		FileModTime:  info.ModTime(),
	}
	var searchBuf strings.Builder

	for scanner.Scan() {
		var env copilotEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &env); err != nil {
			continue
		}
		if ts, ok := parseCopilotTimestamp(env.Timestamp); ok {
			if meta.StartedAt.IsZero() {
				meta.StartedAt = ts
			}
			meta.LastAt = ts
		}
		switch env.Type {
		case "session.start":
			var p copilotSessionStart
			if err := json.Unmarshal(env.Data, &p); err != nil {
				continue
			}
			if p.SessionID != "" {
				meta.ID = p.SessionID
			}
			if meta.CWD == "" && p.Context.CWD != "" {
				meta.CWD = p.Context.CWD
			}
			// Prefer the inner startTime (session start) over the
			// envelope timestamp (line write time) when both exist.
			if ts, ok := parseCopilotTimestamp(p.StartTime); ok {
				meta.StartedAt = ts
			}
		case "user.message":
			var p copilotUserMessage
			if err := json.Unmarshal(env.Data, &p); err != nil {
				continue
			}
			if p.Content == "" {
				continue
			}
			meta.MessageCount++
			if meta.Snippet == "" {
				meta.Snippet = textutil.Snippet(p.Content, snippetMaxLen)
			}
			appendSearchText(&searchBuf, p.Content)
		case "assistant.message":
			var p copilotAssistantMessage
			if err := json.Unmarshal(env.Data, &p); err != nil {
				continue
			}
			if p.Content != "" {
				meta.MessageCount++
				appendSearchText(&searchBuf, p.Content)
				if strings.TrimSpace(p.Content) != "" {
					meta.Ending = textutil.Snippet(strings.TrimSpace(p.Content), snippetMaxLen)
				}
			}
		case "tool.execution_start":
			var p copilotToolStart
			if err := json.Unmarshal(env.Data, &p); err != nil {
				continue
			}
			if p.ToolName != "" {
				appendSearchText(&searchBuf, "[tool_use: "+p.ToolName+"]")
			}
		case "tool.execution_complete":
			var p copilotToolComplete
			if err := json.Unmarshal(env.Data, &p); err != nil {
				continue
			}
			if p.Result != "" {
				appendSearchText(&searchBuf, p.Result)
			}
		}
	}
	meta.SearchText = searchBuf.String()
	meta.ReaderVersion = copilotReaderVersion

	return meta, nil
}

// copilotReaderVersion is the version of the Copilot metadata rules parseFile
// applies. Version 1 added Ending; entries cached before it carry 0 and are
// reparsed once by cachePredatesRules.
const copilotReaderVersion = 1

func parseCopilotTimestamp(s string) (time.Time, bool) {
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
