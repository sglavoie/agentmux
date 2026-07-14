package transcript

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sglavoie/agentmux/internal/harness"
)

// CopilotLifecycleState is the conservative main-turn ownership state derived
// from a bounded tail of a Copilot events.jsonl transcript. It deliberately
// distinguishes three outcomes so a caller never confuses "no trustworthy
// evidence" with a settled turn.
type CopilotLifecycleState int

const (
	// CopilotLifecycleUnknown means the tail carried no state-bearing event,
	// or the evidence could not be trusted (fail-closed default).
	CopilotLifecycleUnknown CopilotLifecycleState = iota
	// CopilotLifecycleTerminal means the latest state-bearing event settled
	// the main turn: assistant.turn_end, a user-initiated abort, or
	// session.shutdown.
	CopilotLifecycleTerminal
	// CopilotLifecycleActive means the latest state-bearing event shows the
	// main turn is still working: a later user.message, assistant.turn_start,
	// or tool execution start/complete.
	CopilotLifecycleActive
)

func (s CopilotLifecycleState) String() string {
	switch s {
	case CopilotLifecycleTerminal:
		return "terminal"
	case CopilotLifecycleActive:
		return "active"
	default:
		return "unknown"
	}
}

// CopilotLifecycleEvidence is the classified result of a bounded-tail read.
// EventType records the raw event that determined the state (empty when the
// state is Unknown) so callers can attach it to diagnostics.
type CopilotLifecycleEvidence struct {
	State     CopilotLifecycleState
	EventType string
	// Size is the transcript's total byte size at the moment it was classified.
	// It is a monotonic generation fingerprint: any appended record grows it.
	// Callers use it to detect a transcript-only append (e.g. a new
	// assistant.turn_start) between an intermediate terminal read and the point a
	// synthetic stop would be applied — a write the reducer receipt sequence
	// cannot see because no reducer event lands. Zero on an error or empty file.
	Size int64
}

// copilotLifecycleMaxTailBytes bounds the classification tail — how much of the
// transcript's end the authoritative reader treats as the "latest events" whose
// last state-bearing record settles the turn. 256 KiB comfortably spans the last
// several turns' worth of envelopes.
const copilotLifecycleMaxTailBytes = 256 << 10 // 256 KiB

// copilotLifecycleAnchorScanBytes is the additional span, read before the
// classification tail, the reader inspects to establish the subagent span depth
// at the window boundary. Reading back one extra tail's worth lets the reader
// cross a single oversized in-span record (e.g. a >256 KiB tool result) to a
// trustworthy main-session anchor (a user.message, a session.start, or the start
// of the file). When no such anchor is found within the bounded window the reader
// fails closed and never classifies an unanchored assistant.turn_end — one whose
// opening subagent.started may have scrolled off the top with the span still
// open — as a main terminal.
const copilotLifecycleAnchorScanBytes = 256 << 10 // 256 KiB

// copilotLifecycleMaxWindowBytes bounds the total transcript the reader inspects
// (classification tail plus anchor scan), keeping the read O(1) on long sessions.
const copilotLifecycleMaxWindowBytes = copilotLifecycleMaxTailBytes + copilotLifecycleAnchorScanBytes

// ErrCopilotLifecycleMalformed reports that a complete line in the inspected
// tail failed to parse. Unlike the fail-soft display reader, the authoritative
// lifecycle reader refuses to trust a tail containing a malformed record.
var ErrCopilotLifecycleMalformed = errors.New("transcript: malformed copilot lifecycle evidence")

// errInvalidCopilotSessionID rejects session IDs that could escape the
// per-home session-state directory.
var errInvalidCopilotSessionID = errors.New("transcript: invalid copilot session id")

// copilotAbortPayload carries the reason on an `abort` record. Only a
// user-initiated abort is treated as a terminal main-turn event; other abort
// reasons (errors, resource limits) are left to other evidence.
type copilotAbortPayload struct {
	Reason string `json:"reason"`
}

// ReadCopilotLifecycle reads at most the last copilotLifecycleMaxWindowBytes of
// a Copilot transcript and classifies the latest state-bearing event. It fails
// closed:
//
//   - a missing or unreadable file returns the underlying error and Unknown;
//   - a malformed complete line returns ErrCopilotLifecycleMalformed and
//     Unknown (the window is not trusted);
//   - a readable window with no state-bearing event returns Unknown, nil.
//
// The initial partial line (when the window starts mid-file) and an incomplete
// final line (a live in-flight write) are discarded before classification.
// Bookkeeping noise — session.task_complete, hook records, subagent
// completion, and unknown types — is ignored so it cannot override the real
// last main-turn event.
//
// Subagent spans are tracked across the window: a subagent runs its own nested
// assistant turns, each emitting an assistant.turn_end that carries no marker
// distinguishing it from the main turn's terminal. Between a subagent.started
// and its matching subagent.completed the span is open, and an
// assistant.turn_end observed there is the subagent's — never the main
// session's terminal — so it must not settle the parent interactive turn.
//
// The classification depends on a trustworthy zero-depth main boundary. Span
// depth is meaningful only relative to such a boundary: the start of the file,
// or a visible main-session anchor (a user.message or a session.start, at which
// no subagent span can be open). When the window begins at the start of the file,
// or once an anchor is observed, the boundary is trusted and a genuine main
// assistant.turn_end (with every observed span closed) settles terminal.
//
// When the bounded window instead begins mid-file with no anchor yet observed,
// the boundary is ambiguous: an opening subagent.started may have scrolled off
// the top with its span still open, so a nested assistant.turn_end at apparent
// depth zero is indistinguishable from a genuine main terminal. The reader fails
// closed there — such an unanchored assistant.turn_end is never classified as a
// main terminal — until a visible anchor re-establishes a trusted boundary.
// Whole-session terminals (session.shutdown, a user-initiated abort) settle
// regardless, since they end the process or cancel every span without depending
// on the boundary depth.
func ReadCopilotLifecycle(path string) (CopilotLifecycleEvidence, error) {
	lines, startedMidFile, size, err := readCopilotLifecycleWindow(path)
	if err != nil {
		return CopilotLifecycleEvidence{}, err
	}

	// Parse every complete line once, failing closed on any unparseable record,
	// so the classification pass works from one trusted view of the window.
	envs := make([]copilotEnvelope, 0, len(lines))
	for _, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var env copilotEnvelope
		if err := json.Unmarshal(line, &env); err != nil {
			// A complete record we cannot parse: refuse to trust the window.
			return CopilotLifecycleEvidence{}, ErrCopilotLifecycleMalformed
		}
		envs = append(envs, env)
	}

	evidence := CopilotLifecycleEvidence{State: CopilotLifecycleUnknown, Size: size}
	// subagentDepth counts subagent.started spans not yet closed by a matching
	// subagent.completed; while it is positive an assistant.turn_end belongs to a
	// nested subagent turn, not the main session.
	subagentDepth := 0
	// anchored records whether we have a trustworthy zero-depth main boundary. A
	// window that begins at the start of the file starts anchored; a window that
	// begins mid-file is not anchored until a main-session anchor (a user.message
	// or a session.start) is observed. While unanchored, an assistant.turn_end at
	// apparent depth zero cannot be trusted as a main terminal.
	anchored := !startedMidFile
	for _, env := range envs {
		state, ok := classifyCopilotLifecycleEvent(env, &subagentDepth, &anchored)
		if !ok {
			continue // bookkeeping / subagent / unanchored / unknown — not state-bearing.
		}
		evidence = CopilotLifecycleEvidence{State: state, EventType: env.Type, Size: size}
	}
	return evidence, nil
}

// classifyCopilotLifecycleEvent maps a single envelope to a main-turn state,
// advancing subagentDepth for the subagent.started/subagent.completed spans it
// crosses and setting anchored when it observes a trustworthy zero-depth main
// boundary. The bool is false for events that carry no main-turn ownership
// signal, which leaves the running classification untouched.
func classifyCopilotLifecycleEvent(env copilotEnvelope, subagentDepth *int, anchored *bool) (CopilotLifecycleState, bool) {
	switch env.Type {
	case "user.message":
		// A fresh main-turn prompt: no subagent span can be open at this point, so
		// it is a trustworthy zero-depth boundary and the main turn is now active.
		*anchored = true
		*subagentDepth = 0
		return CopilotLifecycleActive, true
	case "session.start":
		// A new session begins with no open spans: a trustworthy boundary. Not
		// itself state-bearing, so it does not override the last real event.
		*anchored = true
		*subagentDepth = 0
		return CopilotLifecycleUnknown, false
	case "subagent.started":
		// A subagent is delegated work by the still-open main turn: the parent
		// session is active, and every assistant.turn_end until the matching
		// subagent.completed belongs to the nested subagent.
		*subagentDepth++
		return CopilotLifecycleActive, true
	case "subagent.completed":
		// Close the innermost open span. This is subagent bookkeeping, not a
		// main-turn terminal, so it does not itself settle the session. It is also
		// not a trustworthy main boundary: an outer span may still be open off the
		// top of an unanchored window, so it never sets anchored.
		if *subagentDepth > 0 {
			*subagentDepth--
		}
		return CopilotLifecycleUnknown, false
	case "assistant.turn_end":
		if *subagentDepth > 0 || !*anchored {
			// Either a nested subagent turn ending, or a window whose boundary depth
			// is ambiguous (mid-file, no anchor yet): not a trusted main terminal.
			return CopilotLifecycleUnknown, false
		}
		return CopilotLifecycleTerminal, true
	case "session.shutdown":
		// The CLI process is exiting: a whole-session terminal regardless of any
		// unclosed subagent span or ambiguous boundary.
		return CopilotLifecycleTerminal, true
	case "abort":
		var p copilotAbortPayload
		if err := json.Unmarshal(env.Data, &p); err != nil {
			return CopilotLifecycleUnknown, false
		}
		if p.Reason == "user_initiated" {
			// A user interrupt cancels the whole session, subagents included, so it
			// is terminal even inside an open span or an ambiguous boundary.
			return CopilotLifecycleTerminal, true
		}
		// Non-user aborts (errors, limits) are not our terminal signal.
		return CopilotLifecycleUnknown, false
	case "assistant.turn_start",
		"tool.execution_start",
		"tool.execution_complete":
		return CopilotLifecycleActive, true
	default:
		// assistant.message, session.model_change, session.mode_changed,
		// session.task_complete (legacy subagent completion), hook bookkeeping, and
		// any unknown type: not state-bearing.
		return CopilotLifecycleUnknown, false
	}
}

// readCopilotLifecycleWindow returns the complete newline-delimited lines from
// the last copilotLifecycleMaxWindowBytes of path, dropping an initial partial
// line (present only when the window starts mid-file) and an incomplete final
// line (a live write in progress). The bool reports whether the window began
// mid-file — i.e. earlier history scrolled off the top — so the caller knows its
// leading boundary depth is not anchored to the start of the file. The int64 is
// the transcript's total byte size, which the caller keeps as a monotonic
// generation fingerprint to detect a later transcript-only append.
func readCopilotLifecycleWindow(path string) ([][]byte, bool, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, 0, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, false, 0, err
	}
	size := info.Size()
	if size == 0 {
		return nil, false, 0, nil
	}

	var offset int64
	readLen := size
	midFile := false
	if size > copilotLifecycleMaxWindowBytes {
		offset = size - copilotLifecycleMaxWindowBytes
		readLen = copilotLifecycleMaxWindowBytes
		midFile = true
	}

	buf := make([]byte, readLen)
	if _, err := f.ReadAt(buf, offset); err != nil && !errors.Is(err, io.EOF) {
		return nil, false, 0, err
	}

	lines := bytes.Split(buf, []byte{'\n'})
	// Split leaves a trailing empty element after a final newline (a complete
	// last line); otherwise the last element is an incomplete in-flight line.
	// Either way the last element is not a usable complete record, so drop it.
	if len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	// When the window starts mid-file the first element is the tail of a line
	// whose start was cut off — discard it.
	if midFile && len(lines) > 0 {
		lines = lines[1:]
	}
	return lines, midFile, size, nil
}

// ResolveCopilotTranscript builds the events.jsonl path for a Copilot session,
// preferring a configured profile home that actually holds the transcript and
// falling back to the default Copilot home only when no profile matches. The
// profileHomes are normalized configured Copilot home directories; defaultHome
// is harness.DefaultHomeDir(harness.Copilot). Session IDs containing path
// separators or traversal are rejected before any path is built.
func ResolveCopilotTranscript(sessionID string, profileHomes []string, defaultHome string) (string, error) {
	if err := validateCopilotSessionID(sessionID); err != nil {
		return "", err
	}
	for _, home := range profileHomes {
		if home == "" {
			continue
		}
		p := copilotTranscriptPath(home, sessionID)
		if copilotTranscriptExists(p) {
			return p, nil
		}
	}
	if defaultHome == "" {
		return "", errors.New("transcript: no copilot home to resolve session")
	}
	// No configured profile holds it — return the default-home path so the
	// caller's reader fails closed on a genuinely missing transcript.
	return copilotTranscriptPath(defaultHome, sessionID), nil
}

func copilotTranscriptPath(home, sessionID string) string {
	root := harness.Lookup(harness.Copilot).TranscriptsDir(home)
	return filepath.Join(root, sessionID, "events.jsonl")
}

func copilotTranscriptExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// validateCopilotSessionID rejects any session ID that is empty, carries a path
// separator, or contains a traversal segment. A legitimate Copilot session UUID
// never contains these, so the check is conservative without false rejections.
func validateCopilotSessionID(id string) error {
	if id == "" {
		return errInvalidCopilotSessionID
	}
	if strings.ContainsAny(id, `/\`) || strings.ContainsRune(id, 0) {
		return errInvalidCopilotSessionID
	}
	if strings.ContainsRune(id, os.PathSeparator) || strings.ContainsRune(id, os.PathListSeparator) {
		return errInvalidCopilotSessionID
	}
	if id == "." || id == ".." || strings.Contains(id, "..") {
		return errInvalidCopilotSessionID
	}
	return nil
}
