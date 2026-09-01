package aslbridge

// The protocol-2 wire objects: the frames the daemon sends and the payloads it
// reads back. Everything here is shape only -- the vocabulary these fields draw
// on lives in protocol.go.

import (
	"encoding/json"
	"time"
)

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
