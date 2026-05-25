// Package transcript discovers, parses, and searches harness session
// transcripts (Claude, Copilot, Codex). Each harness writes its own JSONL
// layout under a per-profile home directory; per-harness Reader
// implementations expose a uniform view of those files to the rest of amx.
package transcript

import "time"

// SessionMeta describes a single discovered transcript. It is the cacheable
// per-session metadata used by the index, search, and TUI layers. The
// HiddenFromIndex flag is set by retention pruning (or per-session deletion
// from the TUI) and is preserved across rescans so old sessions do not
// resurrect when the underlying file is still present.
type SessionMeta struct {
	ID              string    `json:"id"`
	Harness         string    `json:"harness"`
	Profile         string    `json:"profile"`
	Project         string    `json:"project"`
	CWD             string    `json:"cwd"`
	StartedAt       time.Time `json:"started_at"`
	LastAt          time.Time `json:"last_at"`
	Path            string    `json:"path"`
	FileSize        int64     `json:"file_size"`
	FileModTime     time.Time `json:"file_mod_time"`
	MessageCount    int       `json:"message_count"`
	Snippet         string    `json:"snippet"`
	HiddenFromIndex bool      `json:"hidden_from_index,omitempty"`
}

// Message is one parsed conversation turn, flattened for display and
// substring search. Tool-use, thinking, and other harness-specific block
// types are collapsed into Text by the per-harness Reader.
type Message struct {
	Role      string    `json:"role"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
}

// Reader is the interface implemented by each harness adapter. Discover
// walks the harness's transcripts directory under homeDir (typically the
// path returned by harness.Impl.TranscriptsDir) and returns one SessionMeta
// per transcript file. ReadMessages parses a single transcript file at
// path and returns its flattened messages in file order.
//
// Implementations must be lenient: unknown fields ignored, malformed lines
// skipped, partial reads (sessions still being written) tolerated.
type Reader interface {
	Harness() string
	Discover(homeDir string) ([]SessionMeta, error)
	ReadMessages(path string) ([]Message, error)
}

// snippetMaxLen caps the per-session snippet length stored in the index.
const snippetMaxLen = 160

// truncateSnippet collapses interior whitespace and trims to snippetMaxLen
// runes, appending an ellipsis when truncated. Shared by the per-harness
// readers so the index keeps a consistent look across harnesses.
func truncateSnippet(s string) string {
	out := make([]rune, 0, snippetMaxLen)
	prevSpace := false
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			r = ' '
		}
		if r == ' ' {
			if prevSpace {
				continue
			}
			prevSpace = true
		} else {
			prevSpace = false
		}
		out = append(out, r)
		if len(out) >= snippetMaxLen {
			return string(out) + "…"
		}
	}
	return string(out)
}
