package runtimesession

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/conversation"
)

// Registration and lookup failures are sentinel errors so callers can map
// them to structured CLI and daemon errors without parsing messages.
var (
	ErrNotFound             = errors.New("runtimesession: no such runtime session")
	ErrIDCollision          = errors.New("runtimesession: runtime session ID already registered")
	ErrDuplicateLaunch      = errors.New("runtimesession: launch already registered")
	ErrTargetInUse          = errors.New("runtimesession: backend target already registered to a live session")
	ErrProcessInUse         = errors.New("runtimesession: process already registered to a live session")
	ErrConversationInUse    = errors.New("runtimesession: conversation already bound to another live session")
	ErrConversationMismatch = errors.New("runtimesession: runtime session is bound to a different conversation")
	ErrStaleTarget          = errors.New("runtimesession: backend target no longer matches its registration")
	ErrWrongInstance        = errors.New("runtimesession: observation is from a different backend server")
	ErrInvalidState         = errors.New("runtimesession: operation not allowed in this state")
	ErrAttachmentMismatch   = errors.New("runtimesession: attachment does not own this runtime session")
)

// State is where a runtime session is in its registration lifecycle.
type State string

const (
	// StatePending records a launch that has been reserved but whose backend
	// target has not been validated yet. A crash leaves it here.
	StatePending State = "pending"
	// StateLive has a validated target and process fingerprint.
	StateLive State = "live"
	// StateStale had a target that a later observation contradicted: the
	// backend restarted, the target vanished, or another process lives there.
	StateStale State = "stale"
	// StateEnded was ended explicitly.
	StateEnded State = "ended"
	// StateAbandoned was pending when its registration was interrupted and
	// recovery gave up on it.
	StateAbandoned State = "abandoned"
)

func (s State) valid() bool {
	switch s {
	case StatePending, StateLive, StateStale, StateEnded, StateAbandoned:
		return true
	}
	return false
}

// active states hold their launch, conversation, target, and process
// exclusively. Terminal states release them for reuse.
func (s State) active() bool {
	return s == StatePending || s == StateLive
}

// Relation records why a runtime session was launched relative to its parent.
type Relation string

const (
	RelationFresh   Relation = "fresh"
	RelationResume  Relation = "resume"
	RelationFork    Relation = "fork"
	RelationRestart Relation = "restart"
)

func (r Relation) valid() bool {
	switch r {
	case RelationFresh, RelationResume, RelationFork, RelationRestart:
		return true
	}
	return false
}

// Record is one runtime session's durable registration.
type Record struct {
	ID ID `json:"id"`
	// Project is the configured project label the launch came from.
	Project string `json:"project"`
	// LaunchID is the harness invocation's launch identity; it makes a
	// retried registration of the same launch detectable.
	LaunchID string   `json:"launch_id"`
	Relation Relation `json:"relation"`
	// Parent is the runtime session this one was resumed, forked, or
	// restarted from. Fresh launches have none.
	Parent       ID                     `json:"parent,omitempty"`
	Conversation *conversation.Identity `json:"conversation,omitempty"`
	Target       *Target                `json:"target,omitempty"`
	Process      *ProcessFingerprint    `json:"process,omitempty"`
	Attachment   *Attachment            `json:"attachment,omitempty"`
	State        State                  `json:"state"`
	// StaleReason explains a StateStale record for diagnostics.
	StaleReason string    `json:"stale_reason,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Validate checks one record in isolation. Cross-record exclusivity is the
// Registry's job.
func (r Record) Validate() error {
	if err := r.ID.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.Project) == "" {
		return fmt.Errorf("runtimesession: %s: project is required", r.ID)
	}
	if strings.TrimSpace(r.LaunchID) == "" {
		return fmt.Errorf("runtimesession: %s: launch ID is required", r.ID)
	}
	if !r.Relation.valid() {
		return fmt.Errorf("runtimesession: %s: unsupported relation %q", r.ID, r.Relation)
	}
	if r.Relation == RelationFresh && r.Parent != "" {
		return fmt.Errorf("runtimesession: %s: a fresh launch has no parent", r.ID)
	}
	if r.Relation != RelationFresh {
		if err := r.Parent.Validate(); err != nil {
			return fmt.Errorf("runtimesession: %s: %s launch needs a parent: %w", r.ID, r.Relation, err)
		}
		if r.Parent == r.ID {
			return fmt.Errorf("runtimesession: %s: a session cannot be its own parent", r.ID)
		}
	}
	if r.Conversation != nil && !r.Conversation.Valid() {
		return fmt.Errorf("runtimesession: %s: conversation identity is not canonical", r.ID)
	}
	if !r.State.valid() {
		return fmt.Errorf("runtimesession: %s: unsupported state %q", r.ID, r.State)
	}
	if r.State == StateLive && (r.Target == nil || r.Process == nil) {
		return fmt.Errorf("runtimesession: %s: a live session needs a target and process", r.ID)
	}
	if r.Target != nil {
		if err := r.Target.Validate(); err != nil {
			return fmt.Errorf("runtimesession: %s: %w", r.ID, err)
		}
	}
	if r.Process != nil {
		if err := r.Process.Validate(); err != nil {
			return fmt.Errorf("runtimesession: %s: %w", r.ID, err)
		}
	}
	if r.Attachment != nil {
		if r.State != StateLive {
			return fmt.Errorf("runtimesession: %s: only a live session can be attached", r.ID)
		}
		if err := r.Attachment.Validate(); err != nil {
			return fmt.Errorf("runtimesession: %s: %w", r.ID, err)
		}
	}
	if r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() {
		return fmt.Errorf("runtimesession: %s: timestamps are required", r.ID)
	}
	if r.UpdatedAt.Before(r.CreatedAt) {
		return fmt.Errorf("runtimesession: %s: updated before it was created", r.ID)
	}
	return nil
}

// Registry is the in-memory form of the registry file. Its methods are pure
// state transitions; Store makes them durable under the file lock.
type Registry struct {
	sessions map[ID]Record
	// newID is replaceable so tests can force generated-ID collisions.
	newID func() (ID, error)
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{sessions: map[ID]Record{}, newID: NewID}
}

func registryFromRecords(records []Record) (*Registry, error) {
	registry := NewRegistry()
	for _, record := range records {
		if err := record.Validate(); err != nil {
			return nil, err
		}
		if _, exists := registry.sessions[record.ID]; exists {
			return nil, fmt.Errorf("%w: %s appears twice", ErrIDCollision, record.ID)
		}
		registry.sessions[record.ID] = record
	}
	if err := registry.checkInvariants(); err != nil {
		return nil, err
	}
	return registry, nil
}

// checkInvariants rejects a registry where two active sessions claim the same
// launch, conversation, target, or process, or a parent is missing. A file
// that violates these was not written by this package and is not trusted.
func (r *Registry) checkInvariants() error {
	launches := map[string]ID{}
	conversations := map[conversation.Identity]ID{}
	targets := map[Target]ID{}
	processes := map[ProcessFingerprint]ID{}
	for _, record := range r.sortedRecords() {
		if record.Parent != "" {
			if _, ok := r.sessions[record.Parent]; !ok {
				return fmt.Errorf("runtimesession: %s: parent %s is not registered", record.ID, record.Parent)
			}
		}
		if !record.State.active() {
			continue
		}
		if other, ok := launches[record.LaunchID]; ok {
			return fmt.Errorf("%w: %s and %s share launch %q", ErrDuplicateLaunch, other, record.ID, record.LaunchID)
		}
		launches[record.LaunchID] = record.ID
		if record.Conversation != nil {
			if other, ok := conversations[*record.Conversation]; ok {
				return fmt.Errorf("%w: %s and %s", ErrConversationInUse, other, record.ID)
			}
			conversations[*record.Conversation] = record.ID
		}
		if record.Target != nil {
			if other, ok := targets[*record.Target]; ok {
				return fmt.Errorf("%w: %s and %s", ErrTargetInUse, other, record.ID)
			}
			targets[*record.Target] = record.ID
		}
		if record.Process != nil {
			if other, ok := processes[*record.Process]; ok {
				return fmt.Errorf("%w: %s and %s", ErrProcessInUse, other, record.ID)
			}
			processes[*record.Process] = record.ID
		}
	}
	return nil
}

// Get returns a copy of one record.
func (r *Registry) Get(id ID) (Record, error) {
	record, ok := r.sessions[id]
	if !ok {
		return Record{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return cloneRecord(record), nil
}

// List returns copies of every record ordered by creation time, then ID.
func (r *Registry) List() []Record {
	records := r.sortedRecords()
	for i := range records {
		records[i] = cloneRecord(records[i])
	}
	return records
}

func (r *Registry) sortedRecords() []Record {
	records := make([]Record, 0, len(r.sessions))
	for _, record := range r.sessions {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if !records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].CreatedAt.Before(records[j].CreatedAt)
		}
		return records[i].ID < records[j].ID
	})
	return records
}

// BeginRequest describes a launch about to be started.
type BeginRequest struct {
	// ID is normally empty so the registry generates one. A caller that
	// already handed an ID to the launched process passes it here.
	ID           ID
	Project      string
	LaunchID     string
	Relation     Relation
	Parent       ID
	Conversation *conversation.Identity
}

// maxIDAttempts bounds generated-ID retries; a real collision of 128 random
// bits means the generator is broken, not unlucky.
const maxIDAttempts = 4

// Begin reserves a pending runtime session before anything is launched, so a
// crash between launching and committing leaves evidence instead of an
// unregistered agent. Retrying the same launch returns ErrDuplicateLaunch
// together with the existing record so the caller can resume it.
func (r *Registry) Begin(request BeginRequest, now time.Time) (Record, error) {
	for _, record := range r.sessions {
		if record.State.active() && record.LaunchID == request.LaunchID {
			return cloneRecord(record), fmt.Errorf("%w: %q is %s as %s", ErrDuplicateLaunch, request.LaunchID, record.State, record.ID)
		}
	}
	id := request.ID
	if id == "" {
		generated, err := r.generateID()
		if err != nil {
			return Record{}, err
		}
		id = generated
	} else if _, exists := r.sessions[id]; exists {
		return Record{}, fmt.Errorf("%w: %s", ErrIDCollision, id)
	}
	if request.Parent != "" {
		if _, ok := r.sessions[request.Parent]; !ok {
			return Record{}, fmt.Errorf("%w: parent %s", ErrNotFound, request.Parent)
		}
	}
	record := Record{
		ID:        id,
		Project:   request.Project,
		LaunchID:  request.LaunchID,
		Relation:  request.Relation,
		Parent:    request.Parent,
		State:     StatePending,
		CreatedAt: now.UTC(),
		UpdatedAt: now.UTC(),
	}
	if request.Conversation != nil {
		canonical, ok := conversation.New(request.Conversation.Harness, request.Conversation.SessionID)
		if !ok {
			return Record{}, errors.New("runtimesession: conversation identity is incomplete")
		}
		if owner, ok := r.conversationOwner(canonical, id); ok {
			return Record{}, fmt.Errorf("%w: %s", ErrConversationInUse, owner)
		}
		record.Conversation = &canonical
	}
	if err := record.Validate(); err != nil {
		return Record{}, err
	}
	r.sessions[id] = record
	return cloneRecord(record), nil
}

func (r *Registry) generateID() (ID, error) {
	for range maxIDAttempts {
		id, err := r.newID()
		if err != nil {
			return "", err
		}
		if err := id.Validate(); err != nil {
			return "", err
		}
		if _, exists := r.sessions[id]; !exists {
			return id, nil
		}
	}
	return "", fmt.Errorf("%w: generator repeated an existing ID %d times", ErrIDCollision, maxIDAttempts)
}

// Commit publishes the validated backend target and process of a pending
// launch. Another active session holding the same target or process is a
// duplicate registration, never a takeover: the caller must first prove the
// other one stale through Validate.
func (r *Registry) Commit(id ID, target Target, process ProcessFingerprint, now time.Time) (Record, error) {
	record, ok := r.sessions[id]
	if !ok {
		return Record{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if record.State != StatePending {
		return Record{}, fmt.Errorf("%w: commit %s while %s", ErrInvalidState, id, record.State)
	}
	if err := target.Validate(); err != nil {
		return Record{}, err
	}
	if err := process.Validate(); err != nil {
		return Record{}, err
	}
	for _, other := range r.sessions {
		if other.ID == id || !other.State.active() {
			continue
		}
		if other.Target != nil && *other.Target == target {
			return Record{}, fmt.Errorf("%w: %s", ErrTargetInUse, other.ID)
		}
		if other.Process != nil && *other.Process == process {
			return Record{}, fmt.Errorf("%w: %s", ErrProcessInUse, other.ID)
		}
	}
	record.Target = &target
	record.Process = &process
	record.State = StateLive
	record.UpdatedAt = laterOf(record.UpdatedAt, now)
	r.sessions[id] = record
	return cloneRecord(record), nil
}

// BindConversation records the conversation a session turned out to run when
// it was not known at launch (a fresh conversation gets its harness ID only
// after starting). Rebinding to the same conversation is a no-op.
func (r *Registry) BindConversation(id ID, identity conversation.Identity, now time.Time) (Record, error) {
	record, ok := r.sessions[id]
	if !ok {
		return Record{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if !record.State.active() {
		return Record{}, fmt.Errorf("%w: bind conversation to %s while %s", ErrInvalidState, id, record.State)
	}
	canonical, valid := conversation.New(identity.Harness, identity.SessionID)
	if !valid {
		return Record{}, errors.New("runtimesession: conversation identity is incomplete")
	}
	if record.Conversation != nil {
		if *record.Conversation != canonical {
			return Record{}, fmt.Errorf("%w: %s", ErrConversationMismatch, id)
		}
		return cloneRecord(record), nil
	}
	if owner, found := r.conversationOwner(canonical, id); found {
		return Record{}, fmt.Errorf("%w: %s", ErrConversationInUse, owner)
	}
	record.Conversation = &canonical
	record.UpdatedAt = laterOf(record.UpdatedAt, now)
	r.sessions[id] = record
	return cloneRecord(record), nil
}

func (r *Registry) conversationOwner(identity conversation.Identity, except ID) (ID, bool) {
	for _, record := range r.sessions {
		if record.ID != except && record.State.active() && record.Conversation != nil && *record.Conversation == identity {
			return record.ID, true
		}
	}
	return "", false
}

// Observation is what a backend currently reports at a registered target.
type Observation struct {
	// Instance is the backend server now answering at the target's address,
	// including its current generation.
	Instance BackendInstance
	// Present reports whether the target reference still exists there.
	Present bool
	// Process is the fingerprint of the agent process at the target, when
	// one was found.
	Process *ProcessFingerprint
	// Exited reports that the target is still there but the agent in it has
	// ended: a pane its backend retains after its program exits. ExitDetail
	// says how ("status 1", "signal term") when the backend knows.
	Exited     bool
	ExitDetail string
}

// StaleReasonExited starts the stale reason of a session whose agent ended
// where it ran, as opposed to one whose target could no longer be found.
const StaleReasonExited = "agent exited"

// Outcome says how a runtime session that is no longer live came to be so.
type Outcome string

const (
	// OutcomeExited: the agent's own process ended and its backend saw it.
	OutcomeExited Outcome = "exited"
	// OutcomeLost: the place the agent ran is gone or holds something else
	// (the backend restarted, the target vanished), so what became of the
	// agent was not observed.
	OutcomeLost Outcome = "lost"
	// OutcomeEnded: the session was ended on purpose, or never started.
	OutcomeEnded Outcome = "ended"
)

// Outcome classifies a record that is not live; it is empty for a pending or
// live one. A session is never both: exited is something that was seen, lost
// is the absence of anything to see.
func (r Record) Outcome() Outcome {
	switch r.State {
	case StateStale:
		if strings.HasPrefix(r.StaleReason, StaleReasonExited) {
			return OutcomeExited
		}
		return OutcomeLost
	case StateEnded, StateAbandoned:
		return OutcomeEnded
	}
	return ""
}

// Validate compares a live session's registration with a fresh backend
// observation. Any disagreement marks the session stale and returns
// ErrStaleTarget, so a restarted server reusing pane IDs or a reused PID can
// never revive it. An observation of a different server is a caller error and
// changes nothing.
func (r *Registry) Validate(id ID, observed Observation, now time.Time) (Record, error) {
	record, ok := r.sessions[id]
	if !ok {
		return Record{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if record.State != StateLive {
		return cloneRecord(record), fmt.Errorf("%w: validate %s while %s", ErrInvalidState, id, record.State)
	}
	if !record.Target.Instance.SameServer(observed.Instance) {
		return cloneRecord(record), fmt.Errorf("%w: %s", ErrWrongInstance, id)
	}
	reason := ""
	switch {
	case observed.Instance.Generation != record.Target.Instance.Generation:
		reason = "backend generation changed"
	case !observed.Present:
		reason = "target is gone"
	case observed.Exited:
		reason = StaleReasonExited
		if detail := strings.TrimSpace(observed.ExitDetail); detail != "" {
			reason += " (" + detail + ")"
		}
	case observed.Process == nil:
		reason = "no process at target"
	case *observed.Process != *record.Process:
		reason = "different process at target"
	}
	if reason == "" {
		return cloneRecord(record), nil
	}
	record.State = StateStale
	record.StaleReason = reason
	record.Attachment = nil
	record.UpdatedAt = laterOf(record.UpdatedAt, now)
	r.sessions[id] = record
	return cloneRecord(record), fmt.Errorf("%w: %s: %s", ErrStaleTarget, id, reason)
}

// Resolve finds the live session registered at exactly target with exactly
// process. A matching pane ID from another server generation, or a matching
// PID with another start token, does not resolve.
func (r *Registry) Resolve(target Target, process ProcessFingerprint) (Record, bool) {
	for _, record := range r.sessions {
		if record.State == StateLive && *record.Target == target && *record.Process == process {
			return cloneRecord(record), true
		}
	}
	return Record{}, false
}

// Attach gives attachment interactive ownership of a live session. An
// attachment elsewhere replaces the previous owner; it never relaunches.
func (r *Registry) Attach(id ID, attachment Attachment, now time.Time) (Record, error) {
	record, ok := r.sessions[id]
	if !ok {
		return Record{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if record.State != StateLive {
		return Record{}, fmt.Errorf("%w: attach %s while %s", ErrInvalidState, id, record.State)
	}
	if err := attachment.Validate(); err != nil {
		return Record{}, err
	}
	record.Attachment = &attachment
	record.UpdatedAt = laterOf(record.UpdatedAt, now)
	r.sessions[id] = record
	return cloneRecord(record), nil
}

// Detach releases interactive ownership, but only for the attachment that
// holds it: a late detach from a view that already lost ownership must not
// release the current owner.
func (r *Registry) Detach(id ID, attachmentID string, now time.Time) (Record, error) {
	record, ok := r.sessions[id]
	if !ok {
		return Record{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if record.Attachment == nil || record.Attachment.ID != attachmentID {
		return Record{}, fmt.Errorf("%w: %s", ErrAttachmentMismatch, id)
	}
	record.Attachment = nil
	record.UpdatedAt = laterOf(record.UpdatedAt, now)
	r.sessions[id] = record
	return cloneRecord(record), nil
}

// End marks a pending or live session ended, releasing its launch,
// conversation, target, and process for later sessions.
func (r *Registry) End(id ID, now time.Time) (Record, error) {
	record, ok := r.sessions[id]
	if !ok {
		return Record{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if !record.State.active() {
		return Record{}, fmt.Errorf("%w: end %s while %s", ErrInvalidState, id, record.State)
	}
	record.State = StateEnded
	record.Attachment = nil
	record.UpdatedAt = laterOf(record.UpdatedAt, now)
	r.sessions[id] = record
	return cloneRecord(record), nil
}

// AbandonInterrupted marks pending registrations created before cutoff as
// abandoned and returns their IDs. A pending record that old belongs to a
// launcher that crashed or gave up between Begin and Commit.
func (r *Registry) AbandonInterrupted(cutoff, now time.Time) []ID {
	var abandoned []ID
	for _, record := range r.sortedRecords() {
		if record.State != StatePending || !record.CreatedAt.Before(cutoff) {
			continue
		}
		record.State = StateAbandoned
		record.UpdatedAt = laterOf(record.UpdatedAt, now)
		r.sessions[record.ID] = record
		abandoned = append(abandoned, record.ID)
	}
	return abandoned
}

// laterOf keeps UpdatedAt monotonic when a caller's clock steps backwards.
func laterOf(previous, now time.Time) time.Time {
	now = now.UTC()
	if now.Before(previous) {
		return previous
	}
	return now
}

func cloneRecord(record Record) Record {
	if record.Conversation != nil {
		value := *record.Conversation
		record.Conversation = &value
	}
	if record.Target != nil {
		value := *record.Target
		record.Target = &value
	}
	if record.Process != nil {
		value := *record.Process
		record.Process = &value
	}
	if record.Attachment != nil {
		value := *record.Attachment
		record.Attachment = &value
	}
	return record
}
