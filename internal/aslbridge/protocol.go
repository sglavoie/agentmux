// Package aslbridge speaks protocol 2 of `asl bridge serve --stdio` from the
// Agent Mux daemon.
//
// The daemon owns exactly one bridge subprocess and is the only Go component
// that speaks this protocol. Five properties are structural here, mirroring the
// guarantees the Python side makes:
//
//   - Stdout is protocol-only. A frame that is not a well-formed protocol
//     object is child failure, never something to skip past.
//   - Question bodies are never cached, logged, journaled, or exported. They
//     are fetched deliberately and handed straight to the caller.
//   - Losing the child disables mutations immediately. Stale read-only data
//     survives for a bounded window and is then removed.
//   - The bridge is optional. Nothing in this package can delay or fail core
//     daemon startup.
//   - Compatibility is identity, not negotiation. The hello names one
//     contract-set ID; a bridge naming any other one is refused outright, and
//     there is no protocol-1 parser left to fall back to.
//
// The last property is why nothing here inspects a version number to decide how
// much of a frame it understands. The daemon vendors ASL's corpus at
// internal/aslcontract, compares the advertised ID against it exactly, and
// takes no snapshot and permits no mutation until session.handshake has matched
// that ID against a snapshot digest the bridge still holds. A partial match has
// no meaning: the ID is a digest over the whole corpus, so it either describes
// the wire this build implements or it does not.
package aslbridge

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/sglavoie/agentmux/internal/aslcontract"
)

// ContractSetID is the one contract set this daemon implements, vendored from
// ASL rather than restated. Every comparison against it is exact.
const ContractSetID = aslcontract.ContractSetID

const (
	// ProtocolVersion is the only bridge protocol this daemon speaks. The child
	// is started with --protocol 2 and a hello announcing anything else is a
	// child failure rather than the start of a negotiation.
	ProtocolVersion = 2

	// MaxFrameBytes mirrors the ceiling the bridge advertises. A frame ceiling
	// only helps if both ends agree on it before parsing, so it is a constant
	// enforced on read and on write instead of a value taken from the hello.
	MaxFrameBytes = 1 << 20

	// StderrTailBytes bounds the private diagnostic tail retained per child.
	StderrTailBytes = 64 << 10

	// StaleWindow is how long the last rich snapshot survives a disconnect as
	// visibly stale read-only data before it is removed entirely.
	StaleWindow = 10 * time.Second

	// HealthyResetInterval is how long one child must stay connected before its
	// restart backoff returns to the start of the schedule.
	HealthyResetInterval = 60 * time.Second

	// SnapshotRetryInterval is how often a connected child is re-asked for a
	// snapshot while the daemon holds none for the current handshake. Refreshes
	// are otherwise event-driven, and the bridge emits runs.changed only when
	// the corpus actually moves, so without this a single dropped first
	// snapshot would leave a healthy child read-only until something happened
	// to change on disk.
	SnapshotRetryInterval = 2 * time.Second
)

// Timeouts. Reads are short because a wedged child must degrade to "stale"
// quickly; an explicit action gets a longer acceptance budget because it may
// append to a mailbox or spawn a resume, and it is never retried.
const (
	HandshakeTimeout = 5 * time.Second
	ReadTimeout      = 2 * time.Second
	ActionTimeout    = 5 * time.Second

	// ShutdownGrace is how long a child may take to notice its closed stdin
	// before it is terminated. It is deliberately far below the daemon's own
	// teardown barrier: the daemon holds its single-instance lock until every
	// child is reaped, so a long grace here turns a restart into a duplicate
	// start that finds the lock still held. An idle child exits in
	// milliseconds; one that is mid-projection cannot read its stdin at all
	// until the projection ends, and it holds nothing durable, so terminating
	// it costs a projection and nothing else.
	ShutdownGrace = 250 * time.Millisecond

	// SnapshotTimeout is the exception to the short read budget. A snapshot is a
	// whole-corpus projection whose cost scales with the number of plans, and on
	// a corpus of several hundred it legitimately takes seconds. Refusing it at
	// the interactive read budget leaves the daemon permanently read-only on
	// exactly the corpora the cockpit exists for. Nothing interactive waits on
	// it, so a slow projection costs staleness rather than a blocked operator.
	SnapshotTimeout = 30 * time.Second
)

// restartBackoff is the bounded exponential schedule applied to unexpected
// child exits. The final entry repeats for every later attempt.
var restartBackoff = []time.Duration{
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	16 * time.Second,
	30 * time.Second,
}

// missingProbeBackoff is the schedule for re-probing an executable that was not
// found, in auto mode only.
//
// A missing `asl` used to be terminal in every mode, which is right for the
// condition it was named after — a machine that simply has no ASL — and wrong
// for the one that actually happens: a paired install where ASL is upgraded, or
// installed for the first time, while the daemon is already running. That
// daemon then reports the integration unavailable until someone restarts it,
// with a reason that describes a state which is no longer true.
//
// The schedule is short at first so an install completed seconds ago is picked
// up promptly, and ends at a five-minute ceiling that repeats forever. Five
// minutes is chosen to be far cheaper than the condition it is polling for: a
// resolution attempt is a stat of one directory plus at most a PATH walk, so
// even a machine that never installs ASL pays a negligible, bounded cost rather
// than the unbounded spin the original terminal behaviour existed to prevent.
var missingProbeBackoff = []time.Duration{
	5 * time.Second,
	15 * time.Second,
	60 * time.Second,
	MissingProbeCeiling,
}

// MissingProbeCeiling is the interval auto mode settles on when re-probing a
// bridge executable that is still not installed.
const MissingProbeCeiling = 5 * time.Minute

// Protocol method names.
const (
	MethodSessionHandshake    = "session.handshake"
	MethodRunsSnapshot        = "runs.snapshot"
	MethodQuestionGet         = "question.get"
	MethodQuestionReply       = "question.reply"
	MethodQuestionResolve     = "question.resolve"
	MethodRunResume           = "run.resume"
	MethodRunStopAfterCurrent = "run.stop_after_current"
	MethodRunAbort            = "run.abort"
	MethodWorkbenchEnsure     = "workbench.ensure"
)

// Protocol event names. Protocol 2 publishes these separately from the method
// list, so "can I call this?" and "will I be sent this?" are different
// questions with different answers.
const (
	EventRunsChanged      = "runs.changed"
	EventOperationUpdated = "operation.updated"
)

// MutationMethods is the subset of the methods that changes ASL state. The
// bridge refuses every one of them until a handshake has matched, and the
// daemon refuses to issue one until it holds that grant, so the gate is closed
// from both ends rather than trusted to one.
var MutationMethods = []string{
	MethodQuestionReply,
	MethodQuestionResolve,
	MethodRunResume,
	MethodRunStopAfterCurrent,
	MethodRunAbort,
}

// Operation statuses. Accepted means durable in ASL's own records; every other
// value is settled by ASL observing its own corpus.
const (
	OperationAccepted   = "accepted"
	OperationConsumed   = "consumed"
	OperationRejected   = "rejected"
	OperationExpired    = "expired"
	OperationSuperseded = "superseded"
)

// Bridge error codes. The vocabulary is closed and shared: protocol failures
// and service-layer refusals live in one space so a client maps outcomes by
// code rather than by parsing a bridge-authored message.
const (
	CodeMalformedJSON        = "malformed-json"
	CodeInvalidRequest       = "invalid-request"
	CodeUnsupportedProtocol  = "unsupported-protocol"
	CodeUnknownMethod        = "unknown-method"
	CodeFrameTooLarge        = "frame-too-large"
	CodeInternalError        = "internal-error"
	CodeMutationFailed       = "mutation-failed"
	CodeWorkbenchUnavailable = "workbench-unavailable"
	CodeInvalidPlan          = "invalid-plan"
	CodeNoActiveRun          = "no-active-run"
	CodeRunMismatch          = "run-mismatch"
	CodeNotPaused            = "not-paused"
	CodeNoOpenQuestion       = "no-open-question"
	CodeQuestionMismatch     = "question-mismatch"
	CodeStaleActionToken     = "stale-action-token"
	CodeActionNotPermitted   = "action-not-permitted"
	CodeInvalidAction        = "invalid-action"
	CodeEmptyText            = "empty-text"
	CodeQuestionBodyTooLarge = "question-body-too-large"
	CodeMissingQuestionBody  = "missing-question-body"
	// CodeContractSetMismatch is never retryable: the offered set is not the one
	// the peer implements, and no amount of reissuing changes which corpus each
	// side was built from.
	CodeContractSetMismatch = "contract-set-mismatch"
	// CodeStaleSnapshot is retryable in the narrow sense that refreshing the
	// snapshot and handshaking again is the fix. It never authorizes resending
	// the refused mutation against the old digest.
	CodeStaleSnapshot = "stale-snapshot"
)

// ErrorCodes is the declared order of the shared vocabulary. Every surface that
// maps a refusal to operator-facing text is audited against this list, so a
// code added by a newer bridge cannot silently fall through to a generic
// sentence without a test naming it.
var ErrorCodes = []string{
	CodeMalformedJSON,
	CodeInvalidRequest,
	CodeUnsupportedProtocol,
	CodeUnknownMethod,
	CodeFrameTooLarge,
	CodeInternalError,
	CodeMutationFailed,
	CodeWorkbenchUnavailable,
	CodeInvalidPlan,
	CodeNoActiveRun,
	CodeRunMismatch,
	CodeNotPaused,
	CodeNoOpenQuestion,
	CodeQuestionMismatch,
	CodeStaleActionToken,
	CodeActionNotPermitted,
	CodeInvalidAction,
	CodeEmptyText,
	CodeQuestionBodyTooLarge,
	CodeMissingQuestionBody,
	CodeContractSetMismatch,
	CodeStaleSnapshot,
}

// Workbench deep-link focus keys.
const (
	FocusReply        = "reply"
	FocusVerification = "verification"
	FocusActive       = "active"
	FocusEvents       = "events"
)

// Closure resolutions accepted by question.resolve.
const (
	ResolutionContinue = "continue"
	ResolutionPause    = "pause"
)

// AbortGraceSeconds mirrors the grace the bridge applies to run.abort before
// the in-flight turn is terminated. It is stated in the confirmation overlay so
// the operator sees the same number the loop will honor.
const AbortGraceSeconds = 120

// Mode is the configured [defaults.asl] tri-state. Disabled never probes;
// Required reports an unavailable integration without making the core daemon
// unready; Auto silently stays unavailable when ASL is absent.
type Mode string

const (
	ModeAuto     Mode = "auto"
	ModeRequired Mode = "required"
	ModeDisabled Mode = "disabled"
)

// SecretText carries operator-authored reply text through the daemon without
// letting it reach a formatter. It marshals as an ordinary JSON string, but
// every fmt and slog path renders a fixed placeholder, so a debug log line, a
// journal record, or a %v on a surrounding struct cannot leak an answer.
type SecretText string

func (SecretText) String() string { return "[redacted]" }

func (SecretText) GoString() string { return `"[redacted]"` }

func (SecretText) LogValue() slog.Value { return slog.StringValue("[redacted]") }

// Reveal returns the underlying text. Callers use it only where the value is
// about to leave the process on the protocol wire.
func (s SecretText) Reveal() string { return string(s) }

// Error is a bridge-reported refusal. The code vocabulary is shared between
// protocol failures (malformed-json, unknown-method) and the service layer's
// own refusal reasons (stale-action-token, no-active-run), so a client sees one
// error space rather than two.
type Error struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return "asl bridge error"
	}
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

// Hello is the server-first frame every session begins with. It carries the
// identity both ends must already share rather than an offer to negotiate one:
// ContractSetID names the corpus the bridge was built from, and the protocol
// and ASL versions describe the process serving it. There is no per-component
// version list because the set behind the ID pins each component's schema
// version, so one identifier is the whole answer.
type Hello struct {
	Protocol            int      `json:"protocol"`
	ASLVersion          string   `json:"asl_version"`
	ContractSetID       string   `json:"contract_set_id"`
	Capabilities        []string `json:"capabilities"`
	Events              []string `json:"events"`
	MutationMethods     []string `json:"mutation_methods"`
	HandshakeRequired   bool     `json:"handshake_required"`
	MaxFrameBytes       int      `json:"max_frame_bytes"`
	PollIntervalSeconds float64  `json:"poll_interval_seconds"`
}

// Handshake is session.handshake's result. MutationsEnabled is the bridge's
// answer, not the daemon's request: it is true only when the offered contract
// set matched exactly and the offered digest is one the bridge still holds, so
// an operator can never act on a row that moved between the projection and the
// click. SnapshotDigest always names the digest that is current on the bridge,
// which is what a refused handshake refreshes towards.
type Handshake struct {
	ContractSetID    string `json:"contract_set_id"`
	HandshakeID      string `json:"handshake_id"`
	MutationsEnabled bool   `json:"mutations_enabled"`
	SnapshotDigest   string `json:"snapshot_digest"`
}

// Window is the projected Kitty binding of one run.
type Window struct {
	KittyWindowID int `json:"kitty_window_id"`
}

// Question is body-free question metadata. The prose an operator answers is
// never part of a snapshot; QuestionBody fetches it on demand.
type Question struct {
	ContextKind                string   `json:"context_kind"`
	QuestionID                 string   `json:"question_id"`
	UnansweredCount            int      `json:"unanswered_count"`
	QuestionIDs                []string `json:"question_ids"`
	AskedAt                    string   `json:"asked_at"`
	SessionID                  string   `json:"session_id"`
	TurnIndex                  int      `json:"turn_index"`
	WaitingStatus              string   `json:"waiting_status"`
	HardDeadline               string   `json:"hard_deadline"`
	UnsupervisedCeilingSeconds int      `json:"unsupervised_ceiling_seconds"`
	LastLeaseActivityAt        string   `json:"last_lease_activity_at"`
}

// Row is one projected active or actionable run.
type Row struct {
	RowID              string            `json:"row_id"`
	PlanDir            string            `json:"plan_dir"`
	Label              string            `json:"label"`
	RunID              string            `json:"run_id"`
	SessionID          string            `json:"session_id"`
	PrimaryState       string            `json:"primary_state"`
	UrgencyRank        int               `json:"urgency_rank"`
	UrgencyGroup       string            `json:"urgency_group"`
	RecommendedAction  string            `json:"recommended_action"`
	ActivityLabel      string            `json:"activity_label"`
	Phase              string            `json:"phase"`
	Harness            string            `json:"harness"`
	WorkspaceRoot      string            `json:"workspace_root"`
	RegistryDigest     string            `json:"registry_digest"`
	RepositoryCount    int               `json:"repository_count"`
	Window             *Window           `json:"window"`
	RunStartedAt       string            `json:"run_started_at"`
	RunFinishedAt      string            `json:"run_finished_at"`
	LastActivityAt     string            `json:"last_activity_at"`
	LastActivitySource string            `json:"last_activity_source"`
	PauseReason        string            `json:"pause_reason"`
	Question           *Question         `json:"question"`
	Actions            []string          `json:"actions"`
	DisabledReasons    map[string]string `json:"disabled_reasons"`
	ActionToken        string            `json:"action_token"`
	Warnings           []string          `json:"warnings"`
}

// Projection is the bridge's own account of how fresh a snapshot can be. The
// bridge walks the corpus on its own cadence rather than on request, so the
// declared PollIntervalSeconds from the hello is a floor, not a measurement: a
// corpus that takes longer to walk than the interval is rebuilt back to back and
// the observed interval is the walk. StalenessBoundSeconds is the number a UI
// should label, because it is the worst age the data on the wire can already
// have. It is attached when a snapshot is served rather than when it is built,
// so it is deliberately excluded from Digest and never moves runs.changed.
type Projection struct {
	IntervalSeconds         float64 `json:"interval_seconds"`
	ObservedIntervalSeconds float64 `json:"observed_interval_seconds"`
	BuildSeconds            float64 `json:"build_seconds"`
	AgeSeconds              float64 `json:"age_seconds"`
	StalenessBoundSeconds   float64 `json:"staleness_bound_seconds"`
	FailedProjections       int     `json:"failed_projections"`
}

// Snapshot is the deterministic projection of every active or actionable plan.
// ContractSetID is inside the digested material rather than beside it, so a
// snapshot cannot be quoted back under a different contract set than the one it
// was built under.
type Snapshot struct {
	Protocol      int    `json:"protocol"`
	ContractSetID string `json:"contract_set_id"`

	Rows       []Row       `json:"rows"`
	RowCount   int         `json:"row_count"`
	Warnings   []string    `json:"warnings"`
	Truncated  bool        `json:"truncated"`
	Digest     string      `json:"digest"`
	Projection *Projection `json:"projection"`
}

// SnapshotMeta describes where a snapshot came from rather than what it holds.
// Stale marks data served from the disconnect window: it is read-only and the
// UI is expected to say so.
type SnapshotMeta struct {
	Generation uint64    `json:"generation"`
	ReceivedAt time.Time `json:"received_at"`
	AgeSeconds int64     `json:"age_seconds"`
	Stale      bool      `json:"stale"`
}

// QuestionBody is the deliberately fetched prose for one question. Body is a
// SecretText so it cannot reach a log or a formatted diagnostic; callers hold
// it only while a response UI is open.
type QuestionBody struct {
	PlanDir     string     `json:"plan_dir"`
	RowID       string     `json:"row_id"`
	RunID       string     `json:"run_id"`
	QuestionID  string     `json:"question_id"`
	ContextKind string     `json:"context_kind"`
	Body        SecretText `json:"body"`
	BodyBytes   int        `json:"body_bytes"`
	ActionToken string     `json:"action_token"`
}

// Operation is a body-free receipt for one accepted mutation.
type Operation struct {
	OperationID string `json:"operation_id"`
	Method      string `json:"method"`
	Status      string `json:"status"`
	PlanDir     string `json:"plan_dir"`
	RowID       string `json:"row_id"`
	RunID       string `json:"run_id"`
	QuestionID  string `json:"question_id"`
	ClosureID   string `json:"closure_id"`
	CommandID   string `json:"command_id"`
	Detail      string `json:"detail"`
}

// WorkbenchLink is a tokenized loopback deep link into one plan's detail view.
type WorkbenchLink struct {
	PlanDir string `json:"plan_dir"`
	RowID   string `json:"row_id"`
	Focus   string `json:"focus"`
	URL     string `json:"url"`
}

// QuestionRequest selects one question body.
type QuestionRequest struct {
	PlanDir    string
	QuestionID string
}

// ActionRequest is one explicit mutation. Text is the operator's answer and is
// never retried, never cached, and never formatted.
type ActionRequest struct {
	Method      string
	PlanDir     string
	RunID       string
	QuestionID  string
	OperationID string
	ActionToken string
	Resolution  string
	Note        string
	Text        SecretText
}

// WorkbenchRequest asks for a deep link into one plan.
type WorkbenchRequest struct {
	PlanDir string
	Focus   string
}

// frame is the union of every inbound protocol object. Decoding into one
// struct keeps "is this a known frame at all" a single decision, which is what
// stdout-contamination detection needs.
type frame struct {
	Type                string          `json:"type"`
	Protocol            int             `json:"protocol"`
	ID                  string          `json:"id"`
	OK                  bool            `json:"ok"`
	Result              json.RawMessage `json:"result"`
	Error               *Error          `json:"error"`
	Seq                 uint64          `json:"seq"`
	Event               string          `json:"event"`
	Data                json.RawMessage `json:"data"`
	ASLVersion          string          `json:"asl_version"`
	ContractSetID       string          `json:"contract_set_id"`
	Capabilities        []string        `json:"capabilities"`
	Events              []string        `json:"events"`
	MutationMethods     []string        `json:"mutation_methods"`
	HandshakeRequired   bool            `json:"handshake_required"`
	MaxFrameBytes       int             `json:"max_frame_bytes"`
	PollIntervalSeconds float64         `json:"poll_interval_seconds"`
}

// outboundRequest is one correlated protocol request.
type outboundRequest struct {
	Type     string         `json:"type"`
	Protocol int            `json:"protocol"`
	ID       string         `json:"id"`
	Method   string         `json:"method"`
	Params   map[string]any `json:"params,omitempty"`
}

func newOutbound(id, method string, params map[string]any) outboundRequest {
	return outboundRequest{
		Type:     "request",
		Protocol: ProtocolVersion,
		ID:       id,
		Method:   method,
		Params:   params,
	}
}

// backoffFor returns the delay before restart attempt n (zero-based).
func backoffFor(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt >= len(restartBackoff) {
		return restartBackoff[len(restartBackoff)-1]
	}
	return restartBackoff[attempt]
}

// missingProbeBackoffFor returns the auto-mode delay before re-probe n
// (zero-based) for an executable that was not found, saturating at the ceiling.
func missingProbeBackoffFor(probes int) time.Duration {
	if probes < 0 {
		probes = 0
	}
	if probes >= len(missingProbeBackoff) {
		return missingProbeBackoff[len(missingProbeBackoff)-1]
	}
	return missingProbeBackoff[probes]
}

func contaminationError(kind string) error {
	return fmt.Errorf("asl bridge: stdout carried a non-protocol %s frame", kind)
}
