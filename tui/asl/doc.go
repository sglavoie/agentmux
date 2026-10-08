// Package asl owns the TUI's agent-session-loop workflow, the ASL Runs
// surface: the Runs list with its run rows, their sort, and its availability
// verdict; the exact and fallback binding of a run to a live session; the
// per-run detail and supervision status display; the response modal that
// presents one question and carries an operator's answer or closure back; the
// run-level control confirmations; the bounded, body-free operation-receipt
// history; and the fixed refusal sentences every declined action is reported
// through, mapped from bridge codes.
//
// This is the operator-facing half of the agent-session-loop integration. The
// daemon's half — reading what an ASL run publishes, the lease and mailbox
// evidence, and the bridge subprocess itself — stays in internal/daemon/asl,
// which this package never imports; it reads an already-projected snapshot
// through internal/aslbridge instead.
//
// The leaf reads its facts from explicit input values rather than from the root
// Bubble Tea model, so it never imports the root tui package. Deciding what a
// keypress DOES — mutating model state, dispatching tea.Cmds, driving the
// toast list and the mode stack — stays in the root, which owns the program
// model.
//
// One rule governs everything here: no bridge-authored text and no operator
// answer may reach a status line, a toast, an error string, or a log. A
// question body and a draft are revealed only by the renderer that paints them,
// and a refusal is mapped from a stable code to a sentence this package wrote.
// The response form's String/GoString redaction contract is load-bearing for
// the same reason: the form's fields are exported and fmt reaches an exported
// field directly, so the state that holds the body and the drafts has to
// redact itself.
package asl
