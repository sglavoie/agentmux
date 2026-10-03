package aslbridge

// The redirect and recovery payloads contract asl-amx-1.85ec3041b9f03f47 added
// to protocol 2: a correction's receipt, a plan's durable redirect standing,
// and the manual recovery a pause is waiting on. Shape and closed vocabularies
// only. Like a question, a correction's words never ride along: an operation
// is identified by its digest and byte count, and strict decoding refuses a
// frame that carries the text anyway.

import "fmt"

// RedirectStatuses is a correction's durable standing, recomputed from the
// journal. It is a different vocabulary from the operation statuses: a redirect
// outlives the bridge session that submitted it, so "consumed" means nothing
// here.
var RedirectStatuses = []string{
	"submitted", "unanswered", "accepted", "delivered", "acknowledged", "rejected", "expired", "uncertain",
}

// RedirectStages is the same standing as an operator reads it.
var RedirectStages = []string{
	"submitted", "unaccepted", "interrupting", "stopped", "resuming", "acknowledged", "rejected", "blocked", "uncertain",
}

// RedirectJournalStates is the last state the append-only journal recorded.
var RedirectJournalStates = []string{"submitted", "accepted", "rejected", "expired"}

// RedirectReasons explains a refusal or a settlement. Absent while in flight.
var RedirectReasons = []string{
	"invalid-plan", "no-active-run", "run-paused", "no-active-turn", "stale-target", "terminal-audit",
	"remote-owner", "stale-lock", "urgent-channel-unavailable", "expired", "run-ended", "superseded",
	"operator-cancelled", "submitted",
}

// RedirectOutcomes is what one submission learned. Absent on a replay.
var RedirectOutcomes = []string{"accepted", "rejected", "unavailable", "uncertain"}

// OwnerLiveness says who still answers for a plan's redirects. "unknown" is an
// answer, not a failure.
var OwnerLiveness = []string{"live", "gone", "unknown"}

// RecoveryDecisions are the only recoveries a person decides. Absent when the
// run is not paused on one; a client cannot spell a decision that resumes
// itself.
var RecoveryDecisions = []string{"fresh-conversation", "predecessor-not-stopped"}

// ConversationEvidence is what the conversation ledger can still say about a
// plan.
type ConversationEvidence struct {
	Detail          *string `json:"detail"`
	Readable        bool    `json:"readable"`
	Records         int     `json:"records"`
	Trustworthy     bool    `json:"trustworthy"`
	UnreadableLines int     `json:"unreadable_lines"`
	Usable          bool    `json:"usable"`
}

// RedirectTarget is the exact turn a correction is bound to. Identity is
// (RunID, SessionID, TurnIndex); Harness is informational.
type RedirectTarget struct {
	Harness   *string `json:"harness"`
	RunID     string  `json:"run_id"`
	SessionID string  `json:"session_id"`
	TurnIndex int     `json:"turn_index"`
}

// RedirectOperation is one correction as the journal still describes it.
type RedirectOperation struct {
	Continuation          *RedirectTarget `json:"continuation"`
	ContinuationRecovered bool            `json:"continuation_recovered"`
	CreatedAt             string          `json:"created_at"`
	ExpiresAt             string          `json:"expires_at"`
	FeedbackBytes         int             `json:"feedback_bytes"`
	FeedbackDigest        string          `json:"feedback_digest"`
	JournalState          string          `json:"journal_state"`
	OperationID           string          `json:"operation_id"`
	Reason                *string         `json:"reason"`
	Stage                 string          `json:"stage"`
	StageDescription      string          `json:"stage_description"`
	Status                string          `json:"status"`
	Target                *RedirectTarget `json:"target"`
}

// RedirectReceipt is the answer to one run.redirect. Operation is re-read from
// the journal, so a retried request returns the same durable record.
type RedirectReceipt struct {
	JournalComplete bool              `json:"journal_complete"`
	Operation       RedirectOperation `json:"operation"`
	OperationID     *string           `json:"operation_id"`
	Outcome         *string           `json:"outcome"`
	PlanDir         string            `json:"plan_dir"`
	Reason          *string           `json:"reason"`
	Replayed        bool              `json:"replayed"`
	RowID           string            `json:"row_id"`
	RunID           *string           `json:"run_id"`
	Target          *RedirectTarget   `json:"target"`
	WaitedSeconds   float64           `json:"waited_seconds"`
}

// RedirectStatusResult is a plan's durable redirect state, projected without
// writing.
type RedirectStatusResult struct {
	ActionToken           string               `json:"action_token"`
	CheckedAt             string               `json:"checked_at"`
	Conversations         ConversationEvidence `json:"conversations"`
	JournalComplete       bool                 `json:"journal_complete"`
	JournalPath           string               `json:"journal_path"`
	LiveTarget            *RedirectTarget      `json:"live_target"`
	Operations            []RedirectOperation  `json:"operations"`
	Owner                 string               `json:"owner"`
	OwnerRunID            *string              `json:"owner_run_id"`
	PlanDir               string               `json:"plan_dir"`
	RowID                 string               `json:"row_id"`
	RunID                 *string              `json:"run_id"`
	TornTail              bool                 `json:"torn_tail"`
	Truncated             bool                 `json:"truncated"`
	UncertainOperationIDs []string             `json:"uncertain_operation_ids"`
	UnreadableLines       int                  `json:"unreadable_lines"`
}

// RecoveryOptions reports which manual recovery a pause is waiting on and how
// to authorize it. A report only: performing it is the operator's command.
type RecoveryOptions struct {
	AuthorizationCommand *string              `json:"authorization_command"`
	AuthorizedBy         string               `json:"authorized_by"`
	AutoResume           bool                 `json:"auto_resume"`
	Conversations        ConversationEvidence `json:"conversations"`
	Decision             *string              `json:"decision"`
	InspectCommand       *string              `json:"inspect_command"`
	Note                 *string              `json:"note"`
	OneUse               bool                 `json:"one_use"`
	PauseReason          *string              `json:"pause_reason"`
	PlanDir              string               `json:"plan_dir"`
	RecoveryRequired     bool                 `json:"recovery_required"`
	RetainedOperations   []RedirectOperation  `json:"retained_operations"`
	RowID                string               `json:"row_id"`
	RunID                *string              `json:"run_id"`
}

// Validate refuses an operation whose closed vocabularies hold a value this
// build does not declare, so a newer bridge cannot reach operator-facing text
// through a value nothing here knows how to render.
func (o RedirectOperation) Validate() error {
	if err := oneOf("redirect status", o.Status, RedirectStatuses); err != nil {
		return err
	}
	if err := oneOf("redirect stage", o.Stage, RedirectStages); err != nil {
		return err
	}
	if err := oneOf("journal state", o.JournalState, RedirectJournalStates); err != nil {
		return err
	}
	return optionalOneOf("redirect reason", o.Reason, RedirectReasons)
}

// Validate checks a receipt's own vocabularies and its operation's.
func (r RedirectReceipt) Validate() error {
	if err := optionalOneOf("redirect outcome", r.Outcome, RedirectOutcomes); err != nil {
		return err
	}
	if err := optionalOneOf("redirect reason", r.Reason, RedirectReasons); err != nil {
		return err
	}
	return r.Operation.Validate()
}

// Validate checks the owner vocabulary and every listed operation.
func (r RedirectStatusResult) Validate() error {
	if err := oneOf("owner liveness", r.Owner, OwnerLiveness); err != nil {
		return err
	}
	for _, op := range r.Operations {
		if err := op.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks the decision vocabulary and every retained operation.
func (r RecoveryOptions) Validate() error {
	if err := optionalOneOf("recovery decision", r.Decision, RecoveryDecisions); err != nil {
		return err
	}
	for _, op := range r.RetainedOperations {
		if err := op.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func oneOf(what, value string, declared []string) error {
	for _, candidate := range declared {
		if value == candidate {
			return nil
		}
	}
	return fmt.Errorf("asl bridge: undeclared %s %q", what, value)
}

func optionalOneOf(what string, value *string, declared []string) error {
	if value == nil {
		return nil
	}
	return oneOf(what, *value, declared)
}
