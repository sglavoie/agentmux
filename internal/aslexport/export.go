// Package aslexport reads agent-session-loop's public sanitized diagnostics
// export.
//
// A debug bundle has to explain one causal path that crosses two processes, and
// half of the evidence is ASL's. The tempting way to get it is to read ASL's
// state.json, events.jsonl and mailbox.jsonl directly — they are on the same
// disk, and agentloop already parses the projection block out of state.json for
// discovery. That is exactly what this package exists to avoid for diagnostics:
// those files carry question bodies, reply text, control messages and whatever
// a harness wrote into a reason string, their shapes change whenever the loop
// changes, and a bundle that parsed them would leak prose into an artifact
// meant to be shareable while becoming a second consumer of every private
// format ASL owns.
//
// `asl diagnostics export --agent-mux` is ASL's supported answer: bounded,
// deterministic, control-plane only, with its own published limits and its own
// omission ledger. This package invokes it, bounds what it will read back,
// validates that the document is one this build understands, and decodes it
// into typed values.
//
// Decoding is deliberately typed rather than raw. A newer ASL that adds a field
// cannot inject it into an AgentMux bundle unreviewed; the field is dropped
// here, at the boundary, and the document's own schema version is reported
// beside the data so a reader can tell a narrowed view from a complete one.
//
// Nothing here fails a bundle. Every way this can go wrong — ASL absent, too
// old to know the subcommand, hung, or emitting something unreadable — resolves
// to an Availability carrying a named reason, because a debug bundle captured
// while the other half of the system is broken is the bundle that matters most.
package aslexport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// Kind is the document kind ASL stamps into the export. A document that does
// not name itself is not this contract, however well it decodes.
const Kind = "asl-agent-mux-diagnostics"

// SchemaVersion is the export contract this build implements. ASL bumps it only
// when an existing field changes meaning, so a newer minor document still
// decodes; a different major is refused rather than guessed at.
const SchemaVersion = 1

// DefaultTimeout bounds one export. The export walks every plan state directory
// under the root and reads a bounded prefix of two logs per plan, which is the
// same order of work as a pairing survey but without the migration pass.
const DefaultTimeout = 60 * time.Second

// DefaultMaxBytes bounds what is read back from the child. ASL caps its own
// document at 1 MiB; this is the independent ceiling, because a bound that only
// the other side enforces is not a bound.
const DefaultMaxBytes = int64(4 << 20)

// Availability reasons. Each names a distinct thing an operator can do, and
// each is a stable string a JSON consumer may branch on.
const (
	// ReasonOK means the document was read and decoded.
	ReasonOK = "ok"
	// ReasonNotRequested means --include-asl was not passed.
	ReasonNotRequested = "not-requested"
	// ReasonDisabled means configuration turned the integration off, so no
	// subprocess may be started at all.
	ReasonDisabled = "disabled"
	// ReasonNotFound means no asl executable could be resolved.
	ReasonNotFound = "executable-not-found"
	// ReasonUnsupported means the executable ran but does not implement the
	// export — an ASL predating the subcommand.
	ReasonUnsupported = "export-unsupported"
	// ReasonTimeout means the export did not finish in time.
	ReasonTimeout = "timeout"
	// ReasonOversize means the child wrote more than this build will read.
	ReasonOversize = "oversize"
	// ReasonMalformed means the output was not a readable export document.
	ReasonMalformed = "malformed"
	// ReasonUnsupportedSchema means the document names a schema version this
	// build does not implement.
	ReasonUnsupportedSchema = "unsupported-schema"
	// ReasonForeignContract means the document names a contract set this build
	// does not implement, so its field meanings are not ours to assume.
	ReasonForeignContract = "foreign-contract-set"
	// ReasonFailed means the export ran and reported its own failure, such as a
	// state root it could not scan.
	ReasonFailed = "export-failed"
)

// Options configures one export invocation.
type Options struct {
	// Executable is the absolute path to the asl entry point. Resolution is the
	// caller's, because the daemon and the CLI resolve it differently and this
	// package must not acquire an opinion about PATH.
	Executable string
	// StateRoot is the canonical ASL state root. Empty lets ASL choose its own,
	// which is correct only when the two sides are known to agree.
	StateRoot string
	// RunID narrows the document to one run.
	RunID string
	// PlanDir narrows the document to one plan.
	PlanDir string
	// Timeout bounds the invocation. Zero means DefaultTimeout.
	Timeout time.Duration
	// MaxBytes bounds what is read back. Zero means DefaultMaxBytes.
	MaxBytes int64
	// ContractSetID is the contract set this build implements. A document
	// naming a different one is refused. Empty skips the check, which is for
	// tests only.
	ContractSetID string
}

// Availability reports whether the export could be obtained, and why not.
// Available is false for every reason other than ReasonOK, so a consumer
// branches on one boolean and reads Reason only to explain it.
//
// Reason serializes as reason_code rather than reason because it is a closed
// enum owned by this package. Bundle redaction hashes free-form reason strings
// on the reasonable assumption that any producer may one day wrap an operating
// system error into one; naming the field for what it actually is keeps the
// single most useful fact about a paired install readable in a shared bundle.
// Detail is the free-form half and is redacted in share mode.
type Availability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason_code"`
	Detail    string `json:"detail,omitempty"`
}

func unavailable(reason, detail string) Availability {
	return Availability{Available: false, Reason: reason, Detail: detail}
}

// Build is ASL's build identity: which release it claims to be, and which bytes
// actually ran.
type Build struct {
	Product                 string `json:"product"`
	Version                 string `json:"version"`
	Executable              string `json:"executable"`
	BinaryHash              string `json:"binary_hash"`
	ContractSetID           string `json:"contract_set_id"`
	StateSchemaVersion      int    `json:"state_schema_version"`
	ProjectionSchemaVersion int    `json:"projection_schema_version"`
	HookSchemaVersion       int    `json:"hook_schema_version"`
	BridgeProtocol          int    `json:"bridge_protocol"`
}

// Omission is one entry of the export's own withholding ledger: what was left
// out, why, how much of it, and a digest identifying the withheld payloads.
type Omission struct {
	Section string `json:"section"`
	Reason  string `json:"reason"`
	Count   int    `json:"count"`
	SHA256  string `json:"sha256"`
}

// StatusTally, ActionTally, KindTally and EventTally are ASL's counted
// breakdowns. The export ships each breakdown as a list of labelled records
// rather than a map keyed by the tallied value, so that a redactor working from
// a key allowlist sees two known keys instead of one unknown key per value.
type StatusTally struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
}

// ActionTally counts control-queue actions.
type ActionTally struct {
	Action string `json:"action"`
	Count  int    `json:"count"`
}

// KindTally counts mailbox receipt kinds.
type KindTally struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// EventTally counts lifecycle event names.
type EventTally struct {
	Event string `json:"event"`
	Count int    `json:"count"`
}

// Projection is the run position ASL publishes for the cockpit, copied through
// the projection contract's own field list.
type Projection struct {
	SchemaVersion           int    `json:"schema_version"`
	ContractSetID           string `json:"contract_set_id"`
	RunID                   string `json:"run_id"`
	Harness                 string `json:"harness"`
	Phase                   string `json:"phase"`
	SessionID               string `json:"session_id"`
	TurnIndex               *int   `json:"turn_index"`
	QuestionID              string `json:"question_id"`
	LifecycleSourceSequence int    `json:"lifecycle_source_sequence"`
	ProjectionGeneration    int    `json:"projection_generation"`
	StartedAt               string `json:"started_at"`
	UpdatedAt               string `json:"updated_at"`
	FinishedAt              string `json:"finished_at"`
}

// Delivery is one lifecycle notification ASL attempted to deliver. SourceEventID
// and RequestID are the join to the daemon journal's record for the same
// notification.
type Delivery struct {
	RunID               string `json:"run_id"`
	AgentMuxEvent       string `json:"agent_mux_event"`
	Harness             string `json:"harness"`
	SourceEventID       string `json:"source_event_id"`
	SourceSequence      *int   `json:"source_sequence"`
	RequestID           string `json:"request_id"`
	TraceID             string `json:"trace_id"`
	AttemptCount        *int   `json:"attempt_count"`
	FinalStatus         string `json:"final_status"`
	Reason              string `json:"reason"`
	ErrorClassification string `json:"error_classification"`
	Acknowledged        bool   `json:"acknowledged"`
}

// DeliverySection bounds the delivery records with the totals they were drawn
// from, so a reader never mistakes a cap for an absence.
type DeliverySection struct {
	Total        int           `json:"total"`
	Exported     int           `json:"exported"`
	Acknowledged int           `json:"acknowledged"`
	ByStatus     []StatusTally `json:"by_status"`
	Records      []Delivery    `json:"records"`
}

// Control is one durable control-queue observation. CommandID is the bridge
// operation ID, which is what joins a bridge call to the run that observed it.
type Control struct {
	Action           string `json:"action"`
	CommandID        string `json:"command_id"`
	QueueCommand     string `json:"queue_command"`
	StopAfterCurrent bool   `json:"stop_after_current"`
	Session          string `json:"session"`
	TurnIndex        *int   `json:"turn_index"`
	TotalSessions    *int   `json:"total_sessions"`
}

// ControlSection bounds the control records and lists the operation IDs found.
type ControlSection struct {
	Total        int           `json:"total"`
	Exported     int           `json:"exported"`
	ByAction     []ActionTally `json:"by_action"`
	OperationIDs []string      `json:"operation_ids"`
	Records      []Control     `json:"records"`
}

// Receipt is one mailbox record reduced to its identities and its body
// *lengths*. TextChars and NoteChars are what let a reader tell "no reply" from
// "a reply this export will not show you"; the bodies themselves are never
// exported by ASL and have no field here to arrive in.
type Receipt struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Seq       *int   `json:"seq"`
	To        string `json:"to"`
	Action    string `json:"action"`
	ClosureID string `json:"closure_id"`
	RunID     string `json:"run_id"`
	SessionID string `json:"session_id"`
	TurnIndex *int   `json:"turn_index"`
	CreatedAt string `json:"created_at"`
	TextChars int    `json:"text_chars"`
	NoteChars int    `json:"note_chars"`
}

// ReceiptSection bounds the mailbox receipts and lists the closure IDs found.
type ReceiptSection struct {
	Available  bool        `json:"available"`
	Reason     string      `json:"reason"`
	Total      int         `json:"total"`
	Exported   int         `json:"exported"`
	ByKind     []KindTally `json:"by_kind"`
	ClosureIDs []string    `json:"closure_ids"`
	Records    []Receipt   `json:"records"`
}

// EventSection is the run's event log reduced to counts.
type EventSection struct {
	Available bool         `json:"available"`
	Reason    string       `json:"reason"`
	Scanned   int          `json:"scanned"`
	Rejected  int          `json:"rejected"`
	Truncated bool         `json:"truncated"`
	Total     int          `json:"total"`
	ByName    []EventTally `json:"by_name"`
}

// Assurance is the run's terminal audit verdict without any finding body.
type Assurance struct {
	State               string   `json:"state"`
	Eligible            bool     `json:"eligible"`
	Reason              string   `json:"reason"`
	IssueIDs            []string `json:"issue_ids"`
	IssueCount          int      `json:"issue_count"`
	NewIssueCount       int      `json:"new_issue_count"`
	RecurringIssueCount int      `json:"recurring_issue_count"`
	AttemptCount        int      `json:"attempt_count"`
	ArtifactCount       int      `json:"artifact_count"`
	StartedAt           string   `json:"started_at"`
	CompletedAt         string   `json:"completed_at"`
	DurationSeconds     *float64 `json:"duration_seconds"`
}

// Run is one plan's whole correlated record.
type Run struct {
	// PlanDigest is sha256(plan_dir). It is the correlation key that survives a
	// share-mode bundle's path aliasing, because both sides can compute it from
	// the same plan without either publishing the path.
	PlanDigest string `json:"plan_digest"`

	RunID                   string          `json:"run_id"`
	StateSchemaVersion      *int            `json:"state_schema_version"`
	StateAvailable          bool            `json:"state_available"`
	StateReason             string          `json:"state_reason"`
	ProjectionStatus        string          `json:"projection_status"`
	Projection              *Projection     `json:"projection"`
	Phase                   string          `json:"phase"`
	ProjectionGeneration    *int            `json:"projection_generation"`
	LifecycleSourceSequence *int            `json:"lifecycle_source_sequence"`
	Assurance               *Assurance      `json:"assurance"`
	Events                  EventSection    `json:"events"`
	Delivery                DeliverySection `json:"delivery"`
	Control                 ControlSection  `json:"control"`
	Receipts                ReceiptSection  `json:"receipts"`
}

// Limits is the export's published bounds. Carried through so a bundle reader
// can tell a cap that bit from evidence that was never there.
type Limits struct {
	MaxRuns            int `json:"max_runs"`
	MaxDeliveryRecords int `json:"max_delivery_records"`
	MaxControlRecords  int `json:"max_control_records"`
	MaxReceiptRecords  int `json:"max_receipt_records"`
	MaxLogLines        int `json:"max_log_lines"`
	MaxExportBytes     int `json:"max_export_bytes"`
}

// Counts is the export's own tally of what it scanned and emitted.
type Counts struct {
	PlansScanned    int `json:"plans_scanned"`
	RunsMatched     int `json:"runs_matched"`
	RunsExported    int `json:"runs_exported"`
	DeliveryRecords int `json:"delivery_records"`
	ControlRecords  int `json:"control_records"`
	ReceiptRecords  int `json:"receipt_records"`
}

// RedactionPolicyEntry is one standing statement of what the export refuses to
// carry, shipped inside the document so an auditor reading one bundle member
// need not find ASL's source to know what it could not have contained.
type RedactionPolicyEntry struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// Document is one decoded export.
type Document struct {
	SchemaVersion    int                    `json:"schema_version"`
	Kind             string                 `json:"kind"`
	ContractSetID    string                 `json:"contract_set_id"`
	Build            Build                  `json:"build"`
	StateRoot        string                 `json:"state_root"`
	StateRootPresent bool                   `json:"state_root_present"`
	Limits           Limits                 `json:"limits"`
	Counts           Counts                 `json:"counts"`
	RedactionPolicy  []RedactionPolicyEntry `json:"redaction_policy"`
	Truncated        bool                   `json:"truncated"`
	Runs             []Run                  `json:"runs"`
	Omissions        []Omission             `json:"omissions"`

	// Digest is ASL's SHA-256 over the rest of its document. Recorded so a
	// bundle can prove two captures read the same evidence.
	Digest string `json:"digest"`

	OK     bool   `json:"ok"`
	Reason string `json:"reason"`
	Error  string `json:"error"`
}

// Result pairs a document with the reason it may be absent. Document is nil for
// every unavailable outcome; Availability is always populated.
type Result struct {
	Availability Availability
	Document     *Document
	// Bytes is what the child actually wrote, recorded even for a malformed
	// document because "it wrote 3 MB of something" is itself a diagnosis.
	Bytes int
}

// runner is the subprocess seam. Tests replace it; nothing else may.
var runner = runCommand

// Fetch invokes the export and decodes it, never returning an error for a
// condition a bundle should survive.
//
// The error return is reserved for a caller mistake — no executable named at
// all — so an unavailable ASL and a misconfigured AgentMux are distinguishable
// at the call site rather than only in a string.
func Fetch(ctx context.Context, options Options) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(options.Executable) == "" {
		return Result{Availability: unavailable(ReasonNotFound, "no asl executable was resolved")}, nil
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	maxBytes := options.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}

	args := []string{"diagnostics", "export", "--agent-mux"}
	if root := strings.TrimSpace(options.StateRoot); root != "" {
		args = append(args, "--state-dir", root)
	}
	if runID := strings.TrimSpace(options.RunID); runID != "" {
		args = append(args, "--run-id", runID)
	}
	if planDir := strings.TrimSpace(options.PlanDir); planDir != "" {
		args = append(args, "--plan-dir", planDir)
	}

	stdout, stderr, exitCode, err := runner(ctx, options.Executable, args, timeout, maxBytes)
	result := Result{Bytes: len(stdout)}
	switch {
	case errors.Is(err, errOversize):
		result.Availability = unavailable(ReasonOversize,
			fmt.Sprintf("the export wrote more than %d bytes", maxBytes))
		return result, nil
	case errors.Is(err, context.DeadlineExceeded):
		result.Availability = unavailable(ReasonTimeout,
			fmt.Sprintf("the export did not finish within %s", timeout))
		return result, nil
	case errors.Is(err, exec.ErrNotFound):
		result.Availability = unavailable(ReasonNotFound, options.Executable+" could not be executed")
		return result, nil
	}

	// An ASL predating the subcommand exits 2 from argparse with an unknown-
	// command diagnostic and writes nothing to stdout. That is a version fact,
	// not a failure, and it is the case a paired install hits during a staggered
	// upgrade — so it is named rather than reported as unreadable output.
	if len(strings.TrimSpace(string(stdout))) == 0 {
		if exitCode == 2 || err != nil {
			return Result{Availability: unavailable(ReasonUnsupported,
				summarize(stderr))}, nil
		}
		return Result{Availability: unavailable(ReasonMalformed, "the export wrote no output")}, nil
	}

	var document Document
	decoder := json.NewDecoder(strings.NewReader(string(stdout)))
	if decodeErr := decoder.Decode(&document); decodeErr != nil {
		result.Availability = unavailable(ReasonMalformed, summarizeDecodeError(decodeErr, stderr))
		return result, nil
	}
	if document.Kind != Kind {
		result.Availability = unavailable(ReasonMalformed,
			fmt.Sprintf("document names kind %q, not %q", document.Kind, Kind))
		return result, nil
	}
	if document.SchemaVersion != SchemaVersion {
		result.Availability = unavailable(ReasonUnsupportedSchema,
			fmt.Sprintf("document is schema version %d; this build implements %d",
				document.SchemaVersion, SchemaVersion))
		return result, nil
	}
	if want := strings.TrimSpace(options.ContractSetID); want != "" && document.ContractSetID != want {
		result.Availability = unavailable(ReasonForeignContract,
			fmt.Sprintf("document names contract set %q; this build implements %q",
				document.ContractSetID, want))
		return result, nil
	}
	if !document.OK {
		result.Document = &document
		result.Availability = unavailable(ReasonFailed, exportFailureDetail(document))
		return result, nil
	}

	result.Document = &document
	result.Availability = Availability{Available: true, Reason: ReasonOK}
	return result, nil
}

func exportFailureDetail(document Document) string {
	reason := strings.TrimSpace(document.Reason)
	message := strings.TrimSpace(document.Error)
	switch {
	case reason != "" && message != "":
		return reason + ": " + message
	case reason != "":
		return reason
	case message != "":
		return message
	default:
		return "the export reported failure without a reason"
	}
}

// errOversize marks output that exceeded the read ceiling. It is its own
// sentinel because a truncated read and a failed read are different diagnoses.
var errOversize = errors.New("aslexport: output exceeded the read limit")

// runCommand executes the export with a hard output ceiling.
//
// stdout and stderr are captured separately: the export writes its document to
// stdout and its diagnostics to stderr, and merging them would make a warning
// indistinguishable from a corrupt document. stdin is /dev/null, because a child
// that inherited this process's stdin could block a bundle forever waiting for
// input nobody will type.
func runCommand(
	ctx context.Context,
	executable string,
	args []string,
	timeout time.Duration,
	maxBytes int64,
) (stdout []byte, stderr string, exitCode int, err error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Stdin = nil
	var out limitedBuffer
	out.limit = maxBytes
	var errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &limitedWriter{writer: &errBuf, remaining: 8 << 10}

	runErr := cmd.Run()
	if out.exceeded {
		return out.data, errBuf.String(), 0, errOversize
	}
	if ctx.Err() != nil {
		return out.data, errBuf.String(), 0, context.DeadlineExceeded
	}
	var exit *exec.ExitError
	if errors.As(runErr, &exit) {
		return out.data, errBuf.String(), exit.ExitCode(), nil
	}
	return out.data, errBuf.String(), 0, runErr
}

// limitedBuffer accumulates output up to a ceiling and then records that the
// ceiling was hit. It keeps the prefix rather than discarding everything,
// because a truncated document still tells a reader which build produced it.
type limitedBuffer struct {
	data     []byte
	limit    int64
	exceeded bool
}

func (b *limitedBuffer) Write(chunk []byte) (int, error) {
	if int64(len(b.data)) >= b.limit {
		b.exceeded = true
		return len(chunk), nil
	}
	room := b.limit - int64(len(b.data))
	if int64(len(chunk)) > room {
		b.data = append(b.data, chunk[:room]...)
		b.exceeded = true
		return len(chunk), nil
	}
	b.data = append(b.data, chunk...)
	return len(chunk), nil
}

// limitedWriter caps a text stream without failing the child that writes it.
type limitedWriter struct {
	writer    io.Writer
	remaining int
}

func (w *limitedWriter) Write(chunk []byte) (int, error) {
	if w.remaining <= 0 {
		return len(chunk), nil
	}
	if len(chunk) > w.remaining {
		chunk = chunk[:w.remaining]
	}
	w.remaining -= len(chunk)
	_, _ = w.writer.Write(chunk)
	return len(chunk), nil
}

// summarize reduces a child's stderr to one line for a Detail field. Detail is
// carried into a bundle, so it is bounded here rather than at the far end.
func summarize(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "the export produced no output and no diagnostics"
	}
	if index := strings.IndexByte(trimmed, '\n'); index >= 0 {
		trimmed = trimmed[:index]
	}
	if len(trimmed) > 200 {
		trimmed = trimmed[:200]
	}
	return trimmed
}

// summarizeDecodeError names the decode failure without quoting the output.
// The output is the one thing that might not be an export document, so it is
// the one thing that must not be echoed into a shareable artifact.
func summarizeDecodeError(err error, stderr string) string {
	detail := "the export emitted no readable JSON"
	if summary := strings.TrimSpace(stderr); summary != "" {
		return detail + ": " + summarize(stderr)
	}
	return detail
}
