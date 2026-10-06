// Package transcript discovers, parses, and searches harness session
// transcripts (Claude, Copilot, Codex). Each harness writes its own JSONL
// layout under a per-profile home directory; per-harness Reader
// implementations expose a uniform view of those files to the rest of amx.
package transcript

import (
	"strings"
	"time"
)

// SessionClass records whether a transcript is known to be a human-started
// interactive session. The empty value is reserved for legacy cache entries
// that have not completed the one-time classification migration.
type SessionClass string

const (
	SessionClassMain      SessionClass = "main"
	SessionClassAutomated SessionClass = "automated"
	SessionClassUnknown   SessionClass = "unknown"
)

// SessionMeta describes a single discovered transcript. It is the cacheable
// per-session metadata used by the index, search, and TUI layers. The
// HiddenFromIndex flag is set by retention pruning (or per-session deletion
// from the TUI) and is preserved across rescans so old sessions do not
// resurrect when the underlying file is still present.
type SessionMeta struct {
	ID           string    `json:"id"`
	Harness      string    `json:"harness"`
	Profile      string    `json:"profile"`
	Profiles     []string  `json:"profiles,omitempty"`
	Project      string    `json:"project"`
	Projects     []string  `json:"projects,omitempty"`
	CWD          string    `json:"cwd"`
	StartedAt    time.Time `json:"started_at"`
	LastAt       time.Time `json:"last_at"`
	Path         string    `json:"path"`
	FileSize     int64     `json:"file_size"`
	FileModTime  time.Time `json:"file_mod_time"`
	MessageCount int       `json:"message_count"`
	Snippet      string    `json:"snippet"`
	// Title is the name the harness itself gave the conversation: the one
	// an operator set with /rename, else the one Claude Code generated, or
	// Codex's thread name. Empty when the harness named it nothing. Unlike
	// Snippet it can change while the transcript does not, so Codex titles
	// are reapplied on every discovery rather than trusted from the cache.
	Title        string       `json:"title,omitempty"`
	SessionClass SessionClass `json:"session_class,omitempty"`
	// ReaderVersion is the metadata rules the entry was parsed under. A
	// cached entry from older rules is reparsed once instead of trusted, so
	// a new rule reaches unchanged transcripts without a full rebuild.
	ReaderVersion int `json:"reader_version,omitempty"`
	// Empty marks a transcript that holds no conversation yet: only bare
	// slash commands such as /clear and their local output, with no prompt
	// and no agent reply. List leaves it out the way it leaves out hidden
	// entries, but unlike HiddenFromIndex it is re-derived on every parse, so
	// the session reappears as soon as its first prompt is written.
	Empty bool `json:"empty,omitempty"`
	// SearchText is the lowercased concatenation of every message body
	// in the transcript, joined by '\n', persisted so substring search
	// can run entirely against the in-memory index without reopening
	// the underlying JSONL file on every keystroke. Empty for entries
	// written by older versions of amx; Search falls back to a live
	// ReadMessages scan in that case.
	SearchText      string `json:"search_text,omitempty"`
	HiddenFromIndex bool   `json:"hidden_from_index,omitempty"`
}

// Summary is what a list row says a session is about: the harness's title
// when it gave one, else the opening prompt.
func (m SessionMeta) Summary() string {
	if m.Title != "" {
		return m.Title
	}
	return m.Snippet
}

// DisplayProfile returns the stable profile label to show for a session.
// Older indexes only have Profile; newer shared-home entries also carry
// Profiles for filter membership.
func (m SessionMeta) DisplayProfile() string {
	if m.Profile != "" {
		return m.Profile
	}
	for _, profile := range m.Profiles {
		if profile != "" {
			return profile
		}
	}
	return ""
}

// ProfileNames returns the filter-membership profiles for a session.
// New metadata uses Profiles; legacy metadata falls back to Profile.
func (m SessionMeta) ProfileNames() []string {
	if profiles := cleanProfileNames(m.Profiles); len(profiles) > 0 {
		return profiles
	}
	if m.Profile == "" {
		return nil
	}
	return []string{m.Profile}
}

// MatchesAnyProfile reports whether this session belongs to any requested
// profile. An empty requested set means no profile filter.
func (m SessionMeta) MatchesAnyProfile(profiles []string) bool {
	if len(profiles) == 0 {
		return true
	}
	for _, profile := range m.ProfileNames() {
		for _, requested := range profiles {
			if profile == requested {
				return true
			}
		}
	}
	return false
}

// ProjectNames returns the filter-membership projects for a session.
// New metadata uses Projects; legacy metadata falls back to Project.
func (m SessionMeta) ProjectNames() []string {
	if projects := cleanProjectNames(m.Projects); len(projects) > 0 {
		return projects
	}
	if m.Project == "" {
		return nil
	}
	return []string{m.Project}
}

// MatchesAnyProject reports whether this session belongs to any requested
// project. An empty requested set means no project filter.
func (m SessionMeta) MatchesAnyProject(projects []string) bool {
	if len(projects) == 0 {
		return true
	}
	for _, project := range m.ProjectNames() {
		for _, requested := range projects {
			if project == requested {
				return true
			}
		}
	}
	return false
}

func cleanProfileNames(profiles []string) []string {
	if len(profiles) == 0 {
		return nil
	}
	out := make([]string, 0, len(profiles))
	seen := make(map[string]struct{}, len(profiles))
	for _, profile := range profiles {
		if profile == "" {
			continue
		}
		if _, ok := seen[profile]; ok {
			continue
		}
		seen[profile] = struct{}{}
		out = append(out, profile)
	}
	return out
}

func cleanProjectNames(projects []string) []string {
	if len(projects) == 0 {
		return nil
	}
	out := make([]string, 0, len(projects))
	seen := make(map[string]struct{}, len(projects))
	for _, project := range projects {
		if project == "" {
			continue
		}
		if _, ok := seen[project]; ok {
			continue
		}
		seen[project] = struct{}{}
		out = append(out, project)
	}
	return out
}

// Message is one parsed conversation turn, flattened for display and
// substring search. Tool-use, thinking, and other harness-specific block
// types are collapsed into Text by the per-harness Reader.
type Message struct {
	Role      string    `json:"role"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`

	// handoffText is populated by readers whose display/search Text can also
	// contain hidden reasoning or tool payloads. A nil value means Text is
	// already safe for a normal user/assistant transcript handoff.
	handoffText *string
	// Claude user records can mix a prompt with tool results. These fields
	// preserve the source split without changing the displayed Text.
	sourceUserText    *string
	sourceHarnessText *string
}

// SearchTextForSource returns the text eligible for a message-source search.
// An empty source has the same meaning as "any".
func (m Message) SearchTextForSource(source string) string {
	if source == "" || source == "any" {
		return m.Text
	}
	if source == "user" {
		if m.sourceUserText != nil {
			return *m.sourceUserText
		}
		if strings.EqualFold(m.Role, "user") {
			return m.Text
		}
		return ""
	}
	if source == "harness" {
		if m.sourceHarnessText != nil {
			return *m.sourceHarnessText
		}
		if !strings.EqualFold(m.Role, "user") {
			return m.Text
		}
	}
	return ""
}

// TextForHandoff returns only content suitable for a generated context pack.
// The boolean is false for non-user/assistant roles, empty messages, and
// reader-recognized records containing only private/tool payloads.
func (m Message) TextForHandoff() (string, bool) {
	role := strings.ToLower(strings.TrimSpace(m.Role))
	if role != "user" && role != "assistant" {
		return "", false
	}
	text := m.Text
	if m.handoffText != nil {
		text = *m.handoffText
	}
	if text == "" {
		return "", false
	}
	return text, true
}

// LookupFunc lets Discover skip a full parse when the index already has a
// fresh entry for the file. Implementations return the cached SessionMeta
// and true when the supplied size and mtime match the cached snapshot.
type LookupFunc func(path string, size int64, mtime time.Time) (SessionMeta, bool)

// DiscoverOptions tunes a single Discover pass without changing the
// per-harness layout walk.
type DiscoverOptions struct {
	Cached  LookupFunc // optional; nil means always parse
	MinTime time.Time  // zero means no cutoff; files with older mtime are skipped
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
	Discover(homeDir string, opts DiscoverOptions) ([]SessionMeta, error)
	ReadMessages(path string) ([]Message, error)
}

// sessionClassifier is implemented by built-in readers so Refresh can
// classify legacy cached entries without rebuilding their message metadata or
// search sidecars.
type sessionClassifier interface {
	ClassifySession(path string) (SessionClass, error)
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
		if len(out) >= snippetMaxLen {
			return string(out) + "…"
		}
		out = append(out, r)
	}
	return string(out)
}

// appendSearchText concatenates one message body into the per-session
// search buffer used by SessionMeta.SearchText. Messages are joined with
// '\n' so the preview reconstruction in firstMatchLine can still extract
// a single-line excerpt around the match. Text is lowercased so substring
// matching in Search avoids per-keystroke ToLower of the haystack.
func appendSearchText(buf *strings.Builder, text string) {
	if text == "" {
		return
	}
	if buf.Len() > 0 {
		buf.WriteByte('\n')
	}
	buf.WriteString(strings.ToLower(text))
}
