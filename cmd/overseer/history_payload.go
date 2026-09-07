package overseer

import (
	"fmt"
	"time"

	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/spf13/cobra"
)

// historyData is the versioned overseer history payload carried inside the
// shared schema-v1 data envelope.
//
// The first half of it is the same authorization evidence `overseer policy`
// returns — the decision, the repositories, the configuration fingerprint, the
// request digest — because a history answer is an authorization answer that
// happens to carry conversation excerpts, and a caller that logs one should not
// have to look elsewhere to find out what was permitted. The second half is the
// evidence itself, together with what was bounded away and what was refused.
//
// Like the policy payload it carries no token, expiry, or grant. It also
// carries the bounds it was taken under, inside the request digest as well as in
// the payload: an answer taken under a 10-message bound is not evidence for a
// request that asked for 200, and the digest says so.
type historyData struct {
	Contract contractView `json:"contract"`
	Plan     string       `json:"plan,omitempty"`
	Access   string       `json:"access"`
	// Allowed is the plan-level answer. A refused plan returns no conversations
	// and performs no discovery.
	Allowed           bool             `json:"allowed"`
	Reason            string           `json:"reason"`
	Summary           string           `json:"summary"`
	ConfigFingerprint string           `json:"config_fingerprint"`
	RequestDigest     string           `json:"request_digest"`
	Repositories      []repositoryView `json:"repositories"`
	// Limits are the bounds that were applied, after defaults and clamping.
	Limits limitsView `json:"limits"`
	// Harnesses and Since echo the narrowing the caller asked for, so an answer
	// read later says which corpus it covered.
	Harnesses []string `json:"harnesses,omitempty"`
	Since     string   `json:"since,omitempty"`
	// Examined is how many conversations were discovered by metadata before any
	// admission decision, which is what tells a small corpus apart from a mostly
	// refused one.
	Examined int `json:"examined"`
	// Truncated reports that admitted conversations were dropped to fit the
	// conversation bound.
	Truncated     bool               `json:"truncated"`
	Conversations []conversationView `json:"conversations"`
	// Withheld is every conversation that was discovered and not read.
	Withheld []withheldView `json:"withheld"`
	// Warnings name corpora that could not be enumerated. They are part of the
	// answer because an unreadable harness home and an empty one produce the same
	// conversation list.
	Warnings []string `json:"warnings,omitempty"`
}

// limitsView reports the bounds actually applied rather than the ones asked for.
type limitsView struct {
	Conversations int `json:"conversations"`
	Messages      int `json:"messages"`
	ExcerptChars  int `json:"excerpt_chars"`
}

// conversationView is one admitted conversation: who wrote it, which session it
// is, where it worked, which projects that directory participates in, and the
// bounded excerpts taken from it.
type conversationView struct {
	Harness  string   `json:"harness"`
	ID       string   `json:"id"`
	Profiles []string `json:"profiles,omitempty"`
	// Directory is the single working directory the conversation named. A
	// conversation that named several never reaches this view.
	Directory string   `json:"directory"`
	Projects  []string `json:"projects,omitempty"`
	Path      string   `json:"path"`
	StartedAt string   `json:"started_at,omitempty"`
	LastAt    string   `json:"last_at,omitempty"`
	// SessionClass is the harness's own classification — a main session, an
	// automated one, or unknown — so a caller can tell a human conversation from
	// a subagent's.
	SessionClass string `json:"session_class"`
	// MessageCount is every handoff-safe message in the transcript, which is what
	// makes Truncated legible: the answer says how much it did not return.
	MessageCount int           `json:"message_count"`
	Truncated    bool          `json:"truncated"`
	Excerpts     []excerptView `json:"excerpts"`
}

// excerptView is one bounded message. Index locates it inside the conversation's
// handoff-safe message sequence, so an excerpt says where it came from rather
// than only that the conversation contained it.
type excerptView struct {
	Index     int    `json:"index"`
	Role      string `json:"role"`
	Timestamp string `json:"timestamp,omitempty"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

// withheldView is one conversation that was discovered and not read. It names
// the harness, the session, and the reason, and nothing that locates it: the
// path, the directory, and the project are what an exclusion is about, so
// reporting them under the heading of explaining a refusal would hand back
// exactly what was withheld.
type withheldView struct {
	Harness string `json:"harness"`
	ID      string `json:"id"`
	Reason  string `json:"reason"`
}

func conversationViews(conversations []transcript.Conversation) []conversationView {
	views := make([]conversationView, 0, len(conversations))
	for _, conversation := range conversations {
		views = append(views, conversationView{
			Harness:      conversation.Harness,
			ID:           conversation.ID,
			Profiles:     conversation.Profiles,
			Directory:    conversation.Directory,
			Projects:     conversation.Projects,
			Path:         conversation.Path,
			StartedAt:    formatHistoryTime(conversation.StartedAt),
			LastAt:       formatHistoryTime(conversation.LastAt),
			SessionClass: string(conversation.SessionClass),
			MessageCount: conversation.MessageCount,
			Truncated:    conversation.Truncated,
			Excerpts:     excerptViews(conversation.Excerpts),
		})
	}
	return views
}

func excerptViews(excerpts []transcript.Excerpt) []excerptView {
	views := make([]excerptView, 0, len(excerpts))
	for _, excerpt := range excerpts {
		views = append(views, excerptView{
			Index:     excerpt.Index,
			Role:      excerpt.Role,
			Timestamp: formatHistoryTime(excerpt.Timestamp),
			Text:      excerpt.Text,
			Truncated: excerpt.Truncated,
		})
	}
	return views
}

func withheldViews(withheld []transcript.Withheld) []withheldView {
	views := make([]withheldView, 0, len(withheld))
	for _, entry := range withheld {
		views = append(views, withheldView{Harness: entry.Harness, ID: entry.ID, Reason: entry.Reason})
	}
	return views
}

func formatHistoryTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// historySummary is the one operator-facing line for an authorized answer. It
// reports what was returned and what was not, because a history answer that said
// only how many conversations it carried would read as a small corpus when it
// was a mostly refused one.
func historySummary(result transcript.HistoryResult) string {
	line := fmt.Sprintf("authorized: %d of %d conversations returned, %d withheld",
		len(result.Conversations), result.Examined, len(result.Withheld))
	if result.Truncated {
		line += " (truncated at the conversation limit)"
	}
	if len(result.Warnings) > 0 {
		line += fmt.Sprintf(" (%d corpora could not be enumerated)", len(result.Warnings))
	}
	return line
}

// writeHistoryText renders the same answer for a human, carrying every field the
// JSON payload does so a text-mode operator is not told less than a script.
func writeHistoryText(cmd *cobra.Command, data historyData) {
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "contract: %s/%d\n", data.Contract.Name, data.Contract.Version)
	if data.Plan != "" {
		_, _ = fmt.Fprintf(out, "plan: %s\n", data.Plan)
	}
	_, _ = fmt.Fprintf(out, "access: %s\n", data.Access)
	_, _ = fmt.Fprintf(out, "decision: %s\n", decisionWord(data.Allowed))
	_, _ = fmt.Fprintf(out, "reason: %s\n", data.Reason)
	_, _ = fmt.Fprintf(out, "config_fingerprint: %s\n", data.ConfigFingerprint)
	_, _ = fmt.Fprintf(out, "request_digest: %s\n", data.RequestDigest)
	_, _ = fmt.Fprintf(out, "limits: conversations=%d messages=%d excerpt_chars=%d\n",
		data.Limits.Conversations, data.Limits.Messages, data.Limits.ExcerptChars)
	if data.Since != "" {
		_, _ = fmt.Fprintf(out, "since: %s\n", data.Since)
	}
	_, _ = fmt.Fprintf(out, "summary: %s\n", data.Summary)
	for _, repository := range data.Repositories {
		_, _ = fmt.Fprintf(out, "repository\t%s\t%s\t%s\t%s\t%s\n",
			repositoryReference(repository), decisionWord(repository.Allowed), repository.Reason, repository.ResolvedPath, repository.Detail)
		for _, declaration := range repository.Declarations {
			_, _ = fmt.Fprintf(out, "  declaration\t%s\t%s\t%s\n",
				declaration.Title, declaration.ConfiguredDirectory, declarationAnswer(declaration))
		}
	}
	_, _ = fmt.Fprintf(out, "examined: %d\n", data.Examined)
	if data.Truncated {
		_, _ = fmt.Fprintln(out, "truncated: conversation limit reached")
	}
	for _, conversation := range data.Conversations {
		_, _ = fmt.Fprintf(out, "conversation\t%s\t%s\t%s\t%s\t%d messages\n",
			conversation.Harness, conversation.ID, conversation.SessionClass, conversation.Directory, conversation.MessageCount)
		if conversation.Truncated {
			_, _ = fmt.Fprintf(out, "  earlier messages omitted to fit the message limit\n")
		}
		for _, excerpt := range conversation.Excerpts {
			_, _ = fmt.Fprintf(out, "  [%d]\t%s\t%s\t%s%s\n",
				excerpt.Index, excerpt.Role, excerpt.Timestamp, excerpt.Text, truncationMark(excerpt.Truncated))
		}
	}
	for _, entry := range data.Withheld {
		_, _ = fmt.Fprintf(out, "withheld\t%s\t%s\t%s\n", entry.Harness, entry.ID, entry.Reason)
	}
	for _, warning := range data.Warnings {
		_, _ = fmt.Fprintf(out, "warning\t%s\n", warning)
	}
}

func truncationMark(truncated bool) string {
	if truncated {
		return " […]"
	}
	return ""
}
