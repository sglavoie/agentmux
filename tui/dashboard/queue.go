// This file is what one queue line SAYS, decided before anything decides how
// wide it is or where it is drawn. The redesigned dashboard row is Session /
// Status / Time, so the three things a row has to answer for are: which session
// this is, what it is doing (and, when it is waiting, what it is waiting for),
// and how long that has been true.
//
// Everything here is a pure function of facts the daemon already stamped onto
// the session — state, prompt kind, unseen, BusySince, PresentationChangedAt,
// branch, directory, profile — plus a clock the caller passes in. Nothing reads
// the wall clock, probes Git, calls Kitty, or consults config, so the same rows
// and the same `now` always produce the same queue.
//
// Two boundaries are deliberate. Turning a wire-level prompt kind into a
// readable phrase is a PRESENTATION decision and lives here beside the reason
// tags for the same reason they do: internal/screenstate's pattern files are the
// user-editable record of what a harness's prompt LOOKS like, and an edit
// repairing detection must not silently rename a dashboard column. And a status
// is a sentence about the row, not a lookup key: nothing produced here is ever
// compared against a filter, stored on a session, or used to find a row again.

package dashboard

import (
	"time"

	"github.com/sglavoie/agentmux/internal/screenstate"
	"github.com/sglavoie/agentmux/internal/session"
)

// Emphasis is the semantic weight a cell is drawn with — what it MEANS, not
// which color it is. The queue's palette rules ("neutral names, muted metadata,
// amber attention labels, red failures") are a mapping from these four, applied
// by the renderer; keeping the meaning separate from the attribute is what lets
// the same facts be asserted in a test that has no terminal and no theme.
type Emphasis int

const (
	// EmphasisNeutral is the default weight: the session's own name, and the
	// status of a row that is simply working. It claims nothing.
	EmphasisNeutral Emphasis = iota
	// EmphasisMuted is metadata — the disambiguating qualifier, the elapsed time,
	// and the status of a row that has gone quiet. Present, and not competing.
	EmphasisMuted
	// EmphasisAttention is the amber a row wears when it is waiting on the
	// operator: a permission prompt, an elicitation, an unread completion.
	EmphasisAttention
	// EmphasisFailure is the red reserved for a turn that ended badly. It is
	// deliberately narrower than "needs you": everything red needs you, but a
	// permission prompt is not a failure and must not read as one.
	EmphasisFailure
)

// String names the emphasis for diagnostics and test failure messages.
func (e Emphasis) String() string {
	switch e {
	case EmphasisMuted:
		return "muted"
	case EmphasisAttention:
		return "attention"
	case EmphasisFailure:
		return "failure"
	default:
		return "neutral"
	}
}

// The status vocabulary. Each is a short sentence about the session rather than
// a state name: the operator reading the queue is deciding where to go next, and
// `bolt` is not an answer to that question.
//
// They are constants rather than literals at each site because the same phrase
// is produced by more than one input — a permission prompt reaches
// statusNeedsPermission from three different wire kinds and from the absence of
// any kind at all — and a vocabulary spelled inline is a vocabulary that drifts.
//
// This is the second readable state vocabulary in the package and deliberately
// not a rewording of the first. stateGlosses explains an ICON to a reader who is
// asking what it means, so it is per-state, lowercase, and says `stopped`; these
// describe the SESSION, so they are refined by what it is blocked on, aware of
// which bucket an iconless row fell into, and say `Done`. The two must keep
// covering the same states, which is what
// TestStatusVocabularyCoversTheSameStatesAsTheFooterGloss asserts: a state that
// gained one and not the other would show a footer explaining something the row
// never said.
const (
	statusFailed          = "Failed"
	statusDone            = "Done"
	statusToolFailed      = "Tool failed"
	statusWorking         = "Working"
	statusSubagent        = "Subagent"
	statusCompacting      = "Compacting"
	statusActive          = "Active"
	statusIdle            = "Idle"
	statusNeedsPermission = "Needs permission"
	statusNeedsEdit       = "Needs edit approval"
	statusNeedsTrust      = "Needs trust approval"
	statusNeedsInput      = "Needs input"
	statusAwaitingReply   = "Awaiting reply"
)

// reasonStatuses refines a blocked row's status with what it is actually
// blocked ON. It is the same daemon-owned prompt kind reasonTags glosses into
// the narrow `perm`/`edit` column, read here at sentence length: the queue's
// status column is the row's whole explanation, so it can afford the words the
// five-cell tag could not.
//
// Keys are the screenstate constants rather than string literals so a renamed
// kind is a compile error here instead of a status that quietly stops refining,
// and TestReasonStatusesCoverEveryPromptKind pins the other direction — a NEW
// kind cannot be added without deciding what it reads as. The mapping is
// many-to-one for the reason reasonTags is: Codex draws command approval and
// permission grants as two prompts and Claude draws one, and the operator's
// answer to all three is the same.
var reasonStatuses = map[string]string{
	// Codex: five kinds, classified from the prompt visible in the pane.
	string(screenstate.CodexPromptCommandApproval):        statusNeedsPermission,
	string(screenstate.CodexPromptPermissionGrant):        statusNeedsPermission,
	string(screenstate.CodexPromptEditApproval):           statusNeedsEdit,
	string(screenstate.CodexPromptRequestUserInput):       statusNeedsInput,
	string(screenstate.CodexPromptUnansweredConfirmation): statusAwaitingReply,

	// Claude: four kinds, refining an already-firing blocking-prompt veto.
	string(screenstate.ClaudePromptPermission):  statusNeedsPermission,
	string(screenstate.ClaudePromptEdit):        statusNeedsEdit,
	string(screenstate.ClaudePromptTrust):       statusNeedsTrust,
	string(screenstate.ClaudePromptElicitation): statusAwaitingReply,
}

// StatusText is what a row's Status column says.
//
// The state decides the sentence and the prompt kind refines it, which is why
// the two are read together rather than rendered as two columns: `Needs
// permission` and `Needs edit approval` are one fact said at two levels of
// detail, and a row that printed both would say the same thing twice.
//
// Three inputs collapse to the empty string, and saying nothing is the honest
// rendering of all three:
//
//   - A state name this build does not know. A daemon newer than the dashboard
//     reading it is the only way to reach this. The alternative — falling back to
//     the bucket the unknown state was sorted into — would print `Idle` over a
//     session that might be anything at all.
//   - A blocked row nothing labelled, which keeps the state's own default rather
//     than collapsing: Copilot has no screen adapter, a hook-raised 🔑 arrives
//     before any probe reads the pane, and a pattern override may have dropped
//     the clause that names the kind. Those rows still read `Needs permission`,
//     because the icon is evidence enough for the sentence even when nothing
//     refined it.
//   - Nothing at all: the group is passed in rather than recomputed so the status
//     and the bucket a row is drawn under cannot disagree about the same session.
func StatusText(s session.RunningSession, group int) string {
	switch s.State {
	case "error":
		return statusFailed
	case "key":
		if refined := reasonStatuses[s.Reason]; refined != "" {
			return refined
		}
		return statusNeedsPermission
	case "question":
		if refined := reasonStatuses[s.Reason]; refined != "" {
			return refined
		}
		return statusAwaitingReply
	case "stop":
		// One sentence for both halves of the stop. Seenness does not change what
		// happened to the session — the turn finished either way — it changes
		// whether the operator has looked at it, which QueueRow.Unread carries and
		// the notice gutter draws. A second word here would be the same claim said
		// twice in two columns.
		return statusDone
	case "warning":
		return statusToolFailed
	case "bolt":
		return statusWorking
	case "subagent":
		return statusSubagent
	case "compact":
		return statusCompacting
	case "":
		// The iconless row: whether it is Active or Idle is not a property of the
		// row at all but of how long the daemon has been silent about it, which is
		// exactly the judgement StateGroup already made.
		if group == idleGroup {
			return statusIdle
		}
		return statusActive
	}
	return ""
}

// StatusEmphasis is the weight the status is drawn with.
//
// It is keyed on the bucket and not on the state, with one exception: a terminal
// failure is red wherever it sits. That ordering is the point — everything in
// the attention bucket earns amber, so an unread completion and a permission
// prompt look alike (they are alike: both are the operator's turn), and only a
// failure escalates past them.
//
// A warning is deliberately neutral. It sits in Active because the harness
// recovered and kept going, and painting it amber in the queue would put a row
// that needs nothing beside the rows that do.
func StatusEmphasis(s session.RunningSession, group int) Emphasis {
	if s.State == "error" {
		return EmphasisFailure
	}
	switch group {
	case attentionGroup:
		return EmphasisAttention
	case idleGroup:
		return EmphasisMuted
	default:
		return EmphasisNeutral
	}
}

// ElapsedText is what a row's Time column says: how long the row has been in the
// state it is showing.
//
// Which clock that is depends on the bucket, and the two are not
// interchangeable. A running row is measured from BusySince, the daemon-owned
// start of the current busy stretch — the answer to "how long has this been
// working". A waiting or resting row is measured from PresentationChangedAt, the
// last visible transition — the answer to "how long has this been waiting for
// me", which is a different question about a different moment.
//
// Either clock may be zero: no daemon, a client too old to carry the field, a
// row that has not been busy since it appeared. Both answer `—`. Nothing here
// substitutes one clock for the other to fill the gap, because a running row
// whose BusySince is missing would then report the age of its last icon change
// as its run time — a number that looks exactly like the truth and is not it.
//
// The band vocabulary is formatRunElapsed's, shared rather than re-derived so an
// elapsed time reads the same in the queue as it does in the activity column.
func ElapsedText(s session.RunningSession, group int, now time.Time) string {
	if group == activeGroup {
		return formatRunElapsed(s.BusySince, now)
	}
	return formatRunElapsed(s.PresentationChangedAt, now)
}

// QueueRow is everything one queue line says about one session, with nothing
// left for the renderer to decide except how much of it fits.
//
// It carries the index of its row rather than a copy of the session, for the
// reason Item does: the row set stays the single source of truth, and a fact
// lives in exactly one place instead of in two that could disagree.
type QueueRow struct {
	// Row is the index into the row set this queue was built from.
	Row int
	// Name is the session's display title, unqualified and drawn neutral. It is
	// the raw title, not a lookup key composed for this queue: the filter still
	// matches this, and the cursor identity still travels on Base and the Kitty
	// IDs.
	Name string
	// Qualifier is the disambiguating context appended when another LIVE session
	// shows the same name — a branch, a worktree folder, a profile, or the window
	// ID — and "" when the name stands alone or when nothing separates the
	// collision. It is metadata and is drawn muted.
	Qualifier string
	// Status is the row's readable status, already refined by its blocked reason.
	// Empty when this build cannot say (an unrecognized state name).
	Status string
	// Emphasis is the semantic weight Status is drawn with.
	Emphasis Emphasis
	// Elapsed is how long the row has been in the state it shows, or `—` when the
	// clock it would be measured from is unknown.
	Elapsed string
	// Unread marks a completion the operator has not looked at yet. It is the one
	// fact the status sentence deliberately does not carry, because it is about
	// the operator rather than about the session.
	Unread bool
	// Group is the urgency bucket StateGroup sorted the row into, passed on so the
	// renderer's heading and the row's own emphasis cannot disagree.
	Group int
}

// QueueInput is what a queue is built from. It is a struct rather than a
// parameter list for the reason RowInput is: it takes two session slices that
// read identically as positional arguments and mean opposite things, and a
// caller that swapped them would get a queue that renames its rows as the filter
// changes — with nothing in the type system noticing.
type QueueInput struct {
	// Rows are the filtered, already urgency-sorted rows to present. One queue row
	// comes out per entry, in order.
	Rows []session.RunningSession
	// Live is the full live session set name collisions are measured across, so
	// filtering a colliding pair down to one row does not leave that row wearing a
	// qualifier explaining a difference from a session no longer on screen — and
	// so filtering an unrelated row away never renames the rows that remain.
	//
	// Nil means the caller has no separate live set and Rows is all there is,
	// which is what a direct caller with a single unfiltered list means.
	Live []session.RunningSession
	// LastActivity and IdleThreshold are StateGroup's inputs, used for the one
	// question it cannot answer from a row alone: whether an iconless session is
	// still active or has gone quiet.
	LastActivity  map[int]time.Time
	IdleThreshold time.Duration
	// Now is the clock elapsed times are measured against. It is passed in rather
	// than read so a queue is a pure function of its input and a test can pin one
	// moment.
	Now time.Time
}

// BuildQueue projects the filtered rows onto their presentation facts.
//
// The projection is index-preserving: queue row i describes Rows[i], so a caller
// holding an item list can pair a line with its facts without a second lookup.
func BuildQueue(in QueueInput) []QueueRow {
	live := in.Live
	if live == nil {
		live = in.Rows
	}
	names := NewNameDisambiguator(live)

	queue := make([]QueueRow, len(in.Rows))
	for i, s := range in.Rows {
		group := StateGroup(s, in.LastActivity, in.IdleThreshold)
		queue[i] = QueueRow{
			Row:       i,
			Name:      SessionDisplayTitle(s),
			Qualifier: names.Qualifier(s),
			Status:    StatusText(s, group),
			Emphasis:  StatusEmphasis(s, group),
			Elapsed:   ElapsedText(s, group, in.Now),
			Unread:    unreadStop(s),
			Group:     group,
		}
	}
	return queue
}

// Label composes the name and its qualifier for a surface that draws them as one
// span. A surface styling the halves differently reads Name and Qualifier.
func (q QueueRow) Label() string {
	if q.Qualifier == "" {
		return q.Name
	}
	return q.Name + " (" + q.Qualifier + ")"
}
