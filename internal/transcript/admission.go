package transcript

// The metadata admission path: deciding whether a conversation may be read at
// all, before any of its message content is loaded.
//
// The existing discovery path cannot answer that question. Reader.Discover
// calls each harness's parseFile, which streams every message body to build the
// snippet, the message count, and the search text, and Index.Refresh then writes
// that text into a disposable search sidecar. By the time a refresh has decided
// anything about a transcript it has already read all of it. That is the correct
// shape for a cache the operator asked for and the wrong shape for a boundary:
// a boundary has to be able to say "no" while the file is still closed.
//
// So this is a second, narrower walk of the same corpus. It reads exactly the
// identity a decision needs — where the conversation lives on disk, which
// harness wrote it, which session it is, and which working directories it names
// — and it reads that identity through per-harness structs that have no field
// for message content at all, so a body cannot be materialized even by accident.
// Only once an Authorizer has admitted a conversation does anything here open it
// again to read messages.
//
// Three rules make the boundary fail closed rather than merely narrow:
//
//   - The index is not in the path. Nothing here loads, consults, or writes
//     transcripts.json or a search sidecar, so a refresh that has already cached
//     an excluded transcript's full text cannot serve it back through a history
//     request. The cache is not a permission-bypassing shortcut here because it
//     is not reachable from here.
//   - A conversation that names more than one working directory is unavailable.
//     Its messages may belong to several projects and the identity metadata
//     cannot say which belong to which; deciding would require reading the
//     content the decision is supposed to gate.
//   - A conversation that names no working directory at all is unavailable, for
//     the same reason in the other direction: an unidentifiable conversation
//     cannot be shown to be outside an exclusion.
//
// Every refusal is reported, because a history result that silently omitted what
// it declined to read would look like a corpus with nothing in it. A refusal
// carries the harness, the session id, and the reason, and deliberately not the
// path, the working directory, or the project: those are what an exclusion is
// about, and naming them in the answer would leak precisely what the operator
// withheld.

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

// Bounds on one history request. The defaults are what an unbounded request
// resolves to and the ceilings are what an over-large one is clamped to, so a
// caller cannot turn an evidence request into a corpus dump by asking for a
// large enough number.
const (
	DefaultHistoryConversations = 10
	DefaultHistoryMessages      = 20
	DefaultHistoryExcerptChars  = 2000

	MaxHistoryConversations = 100
	MaxHistoryMessages      = 200
	MaxHistoryExcerptChars  = 20000
)

// Admission reasons. They are stable diagnostic vocabulary: an operator asking
// why a conversation is not in a history answer gets one of these, and a caller
// recording the answer stores the string.
const (
	// AdmissionAllowed is the only admitting reason.
	AdmissionAllowed = "allowed"
	// AdmissionUnidentified is a conversation whose identity metadata names no
	// working directory, so there is no directory to authorize.
	AdmissionUnidentified = "unidentified"
	// AdmissionAmbiguous is a conversation whose identity metadata names more
	// than one working directory. Which messages belong to which project is a
	// question only the content could answer, so the conversation stays closed.
	AdmissionAmbiguous = "ambiguous_project"
	// AdmissionUnauthorized is the fallback reason for an Authorizer that
	// refused without naming one.
	AdmissionUnauthorized = "unauthorized"
	// AdmissionUnreadable is an admitted conversation whose messages could not
	// be read after admission. It is reported as withheld rather than as an
	// empty conversation so "nothing was said" and "nothing could be read" stay
	// apart.
	AdmissionUnreadable = "unreadable"
)

// MetadataOptions tunes one metadata-only discovery pass. It deliberately has
// no Cached hook: a cached SessionMeta was produced by the content-reading
// discovery path, and consulting one here would put the cache back inside the
// boundary.
type MetadataOptions struct {
	// MinTime skips files whose mtime is older. The zero value means no cutoff.
	MinTime time.Time
}

// Candidate is one discovered conversation as the admission decision sees it:
// where it is, what wrote it, which session it is, and which working directories
// it names. Nothing here comes from a message body.
type Candidate struct {
	Harness string
	ID      string
	// Profiles is the filter membership of the transcript home the conversation
	// was found under, which is configuration rather than content.
	Profiles []string
	// Directories is every distinct working directory the conversation's
	// identity records name, in first-seen order. One is the normal case; zero
	// and more than one are both refusals.
	Directories []string
	Path        string
	FileSize    int64
	FileModTime time.Time
	// StartedAt and LastAt are whatever the identity records happened to carry
	// and may be zero. They are reported, never relied on for ordering.
	StartedAt time.Time
	LastAt    time.Time
}

// Directory returns the conversation's single working directory. The boolean is
// false when the conversation names none or names several, which are the two
// shapes that cannot be authorized.
func (c Candidate) Directory() (string, bool) {
	if len(c.Directories) != 1 {
		return "", false
	}
	return c.Directories[0], true
}

// MetadataDiscoverer is implemented by a Reader that can enumerate conversation
// identity without decoding message content. A reader that does not implement it
// contributes no conversations at all: the alternative would be falling back to
// Discover, which is the content-reading path this boundary exists to precede.
type MetadataDiscoverer interface {
	DiscoverMetadata(homeDir string, opts MetadataOptions) ([]Candidate, error)
}

// Admission is one authorization answer about one directory.
type Admission struct {
	Allowed bool
	// Reason is the caller's own vocabulary for the answer, reported verbatim.
	Reason string
	Detail string
	// Projects names the projects the directory was identified as participating
	// in. It is reported on an admitted conversation and ignored otherwise, so a
	// refusal cannot name what it withheld.
	Projects []string
}

// Authorizer decides whether the conversation working in a directory may be
// read. It is asked once per conversation per request and its answer is never
// memoized, so a withdrawn opt-in takes effect on the next request rather than
// on the next process.
//
// It is handed a directory and nothing else. It cannot be given message content
// because none has been read when it is called, which is the property that makes
// this an admission boundary instead of a filter over material already loaded.
type Authorizer interface {
	Authorize(directory string) Admission
}

// HistoryLimits bounds one history answer.
type HistoryLimits struct {
	Conversations int
	Messages      int
	ExcerptChars  int
}

// Normalized resolves zero and negative values to the defaults and clamps
// everything above the ceilings, so the limits reported back are the limits that
// were actually applied.
func (l HistoryLimits) Normalized() HistoryLimits {
	return HistoryLimits{
		Conversations: clampLimit(l.Conversations, DefaultHistoryConversations, MaxHistoryConversations),
		Messages:      clampLimit(l.Messages, DefaultHistoryMessages, MaxHistoryMessages),
		ExcerptChars:  clampLimit(l.ExcerptChars, DefaultHistoryExcerptChars, MaxHistoryExcerptChars),
	}
}

func clampLimit(value, fallback, ceiling int) int {
	if value <= 0 {
		value = fallback
	}
	if value > ceiling {
		return ceiling
	}
	return value
}

// HistoryRequest is one bounded request for authorized conversation evidence.
type HistoryRequest struct {
	// Harnesses narrows the answer to these harnesses. Empty means all of them.
	Harnesses []string
	// Since skips conversations whose file has not been written since. The zero
	// value means no cutoff.
	Since  time.Time
	Limits HistoryLimits
}

// Excerpt is one bounded message from an admitted conversation.
//
// Index is the message's ordinal inside the transcript's handoff-safe message
// sequence, which is what locates it: an excerpt says where in the conversation
// it came from rather than merely that the conversation contained it.
type Excerpt struct {
	Index     int
	Role      string
	Timestamp time.Time
	Text      string
	// Truncated reports that the message was longer than the per-excerpt bound.
	Truncated bool
}

// Conversation is one admitted conversation and the bounded evidence taken from
// it.
type Conversation struct {
	Harness   string
	ID        string
	Profiles  []string
	Directory string
	Projects  []string
	Path      string
	StartedAt time.Time
	LastAt    time.Time
	// SessionClass is the harness's own classification of the conversation.
	SessionClass SessionClass
	// MessageCount is every handoff-safe message in the transcript, which is
	// what makes Truncated legible: an answer says how much it did not return.
	MessageCount int
	Excerpts     []Excerpt
	// Truncated reports that earlier messages were dropped to fit the bound.
	Truncated bool
}

// Withheld is one conversation that was discovered and not read.
//
// It names the harness, the session, and the reason, and nothing else. The path,
// the working directory, and the project are exactly what an exclusion is about,
// so reporting them would hand back the thing the operator withheld under the
// heading of explaining why it was withheld.
type Withheld struct {
	Harness string
	ID      string
	Reason  string
}

// HistoryResult is the answer to one HistoryRequest.
type HistoryResult struct {
	// Limits are the bounds that were applied, after normalization.
	Limits        HistoryLimits
	Conversations []Conversation
	Withheld      []Withheld
	// Examined is how many conversations the metadata walk found before any
	// admission decision, so a caller can tell a small corpus from a mostly
	// refused one.
	Examined int
	// Truncated reports that admitted conversations were dropped to fit the
	// conversation bound.
	Truncated bool
	Warnings  []string
}

// History discovers conversations by metadata, admits them one at a time, and
// reads message content only for the ones that were admitted.
//
// The order of operations is the contract. Every candidate is discovered without
// opening a message body; every candidate is then authorized; only an admitted
// candidate is opened again. A refusal at any point in the sequence leaves the
// transcript unread, and there is no path from here to the metadata cache that
// could serve its content from somewhere else.
func History(cfg *config.Config, readers map[string]Reader, authorizer Authorizer, req HistoryRequest) (HistoryResult, error) {
	if cfg == nil {
		return HistoryResult{}, errors.New("transcript: History requires a non-nil config")
	}
	if authorizer == nil {
		// A nil authorizer would have to mean either "permit everything" or
		// "refuse everything", and one of those answers is a permissive default
		// wearing the shape of a check. Neither is offered.
		return HistoryResult{}, errors.New("transcript: History requires an authorizer")
	}
	limits := req.Limits.Normalized()
	result := HistoryResult{Limits: limits}

	candidates, warnings := discoverCandidates(cfg, readers, MetadataOptions{MinTime: req.Since})
	result.Warnings = warnings
	candidates = filterCandidates(candidates, req)
	result.Examined = len(candidates)
	sortCandidates(candidates)

	for _, candidate := range candidates {
		directory, single := candidate.Directory()
		if !single {
			reason := AdmissionUnidentified
			if len(candidate.Directories) > 1 {
				reason = AdmissionAmbiguous
			}
			result.Withheld = append(result.Withheld, withhold(candidate, reason))
			continue
		}
		admission := authorizer.Authorize(directory)
		if !admission.Allowed {
			result.Withheld = append(result.Withheld, withhold(candidate, admissionReason(admission)))
			continue
		}
		if len(result.Conversations) >= limits.Conversations {
			// Admitted and over the bound: the answer is truncated rather than
			// silently short, and the conversation is not opened.
			result.Truncated = true
			continue
		}
		conversation, err := readConversation(readers[candidate.Harness], candidate, admission, limits)
		if err != nil {
			slog.Debug("transcript.admission: admitted conversation unreadable", "path", candidate.Path, "err", err)
			result.Withheld = append(result.Withheld, withhold(candidate, AdmissionUnreadable))
			continue
		}
		result.Conversations = append(result.Conversations, conversation)
	}
	return result, nil
}

// discoverCandidates walks every configured transcript home through the
// metadata-only interface. A harness whose reader cannot enumerate metadata
// contributes nothing and says so, which is the fail-closed direction: the
// alternative is reading its transcripts to find out whether they may be read.
func discoverCandidates(cfg *config.Config, readers map[string]Reader, opts MetadataOptions) ([]Candidate, []string) {
	var (
		candidates []Candidate
		warnings   []string
	)
	seen := make(map[string]struct{})
	for _, group := range transcriptHomeGroups(cfg) {
		reader, ok := readers[group.harnessName]
		if !ok {
			continue
		}
		discoverer, ok := reader.(MetadataDiscoverer)
		if !ok {
			warnings = append(warnings, fmt.Sprintf(
				"harness %s cannot be enumerated without reading message content, so its conversations are unavailable",
				group.harnessName))
			continue
		}
		found, err := discoverer.DiscoverMetadata(group.home, opts)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("discovering %s conversations under %s: %v", group.harnessName, group.home, err))
		}
		// A partial discovery is still usable; readers report what they found
		// alongside the error.
		for _, candidate := range found {
			if candidate.Path == "" || candidate.ID == "" {
				continue
			}
			if _, duplicate := seen[candidate.Path]; duplicate {
				continue
			}
			seen[candidate.Path] = struct{}{}
			candidate.Harness = group.harnessName
			candidate.Profiles = append([]string(nil), group.profiles...)
			candidates = append(candidates, candidate)
		}
	}
	return candidates, warnings
}

func filterCandidates(candidates []Candidate, req HistoryRequest) []Candidate {
	if len(req.Harnesses) == 0 && req.Since.IsZero() {
		return candidates
	}
	out := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !matchAny(candidate.Harness, req.Harnesses) {
			continue
		}
		if !req.Since.IsZero() && candidate.FileModTime.Before(req.Since) {
			continue
		}
		out = append(out, candidate)
	}
	return out
}

// sortCandidates orders by file modification time, newest first. That is
// filesystem metadata rather than a timestamp parsed out of the file, so the
// order is the same whether or not a transcript carried readable timestamps.
func sortCandidates(candidates []Candidate) {
	sort.Slice(candidates, func(a, b int) bool {
		if !candidates[a].FileModTime.Equal(candidates[b].FileModTime) {
			return candidates[a].FileModTime.After(candidates[b].FileModTime)
		}
		if candidates[a].Harness != candidates[b].Harness {
			return candidates[a].Harness < candidates[b].Harness
		}
		return candidates[a].ID < candidates[b].ID
	})
}

func withhold(candidate Candidate, reason string) Withheld {
	return Withheld{Harness: candidate.Harness, ID: candidate.ID, Reason: reason}
}

func admissionReason(admission Admission) string {
	if reason := strings.TrimSpace(admission.Reason); reason != "" {
		return reason
	}
	return AdmissionUnauthorized
}

// readConversation is the only place message content is opened, and it is
// reachable only from the admitted branch above.
//
// Excerpts come from Message.TextForHandoff rather than Message.Text: the
// display representation folds in thinking blocks and tool payloads, and an
// authorization to read a conversation is not an authorization to read the
// reasoning and tool traffic the harness kept out of the conversation itself.
func readConversation(reader Reader, candidate Candidate, admission Admission, limits HistoryLimits) (Conversation, error) {
	if reader == nil {
		return Conversation{}, errors.New("no reader for harness " + candidate.Harness)
	}
	messages, err := reader.ReadMessages(candidate.Path)
	if err != nil {
		return Conversation{}, err
	}

	directory, _ := candidate.Directory()
	conversation := Conversation{
		Harness:      candidate.Harness,
		ID:           candidate.ID,
		Profiles:     append([]string(nil), candidate.Profiles...),
		Directory:    directory,
		Projects:     append([]string(nil), admission.Projects...),
		Path:         candidate.Path,
		StartedAt:    candidate.StartedAt,
		LastAt:       candidate.LastAt,
		SessionClass: classifyAdmitted(reader, candidate.Path),
	}

	type located struct {
		index int
		msg   Message
		text  string
	}
	var safe []located
	for _, msg := range messages {
		text, ok := msg.TextForHandoff()
		if !ok {
			continue
		}
		safe = append(safe, located{index: len(safe), msg: msg, text: text})
	}
	conversation.MessageCount = len(safe)

	// The tail is what an incident is about: a run blocked now is explained by
	// what was said last, not by how the conversation opened.
	start := 0
	if len(safe) > limits.Messages {
		start = len(safe) - limits.Messages
		conversation.Truncated = true
	}
	for _, entry := range safe[start:] {
		text, truncated := clipRunes(entry.text, limits.ExcerptChars)
		conversation.Excerpts = append(conversation.Excerpts, Excerpt{
			Index:     entry.index,
			Role:      strings.ToLower(strings.TrimSpace(entry.msg.Role)),
			Timestamp: entry.msg.Timestamp,
			Text:      text,
			Truncated: truncated,
		})
	}
	return conversation, nil
}

// classifyAdmitted asks the reader for its own session classification. It runs
// after admission, not before it: classification is part of the evidence being
// returned rather than an input to the decision, and running it earlier would
// open a file the decision might refuse.
func classifyAdmitted(reader Reader, path string) SessionClass {
	classifier, ok := reader.(sessionClassifier)
	if !ok {
		return SessionClassUnknown
	}
	class, err := classifier.ClassifySession(path)
	if err != nil {
		return SessionClassUnknown
	}
	return normalizedSessionClass(class)
}

// clipRunes bounds an excerpt by runes so a multi-byte character cannot be cut
// in half. The boolean reports that clipping happened.
func clipRunes(text string, limit int) (string, bool) {
	if limit <= 0 {
		return "", text != ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text, false
	}
	return string(runes[:limit]), true
}
