package tui

import (
	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/tui/asl"
)

// modelASLState holds run snapshots, response drafts, and operation receipts.
// It is embedded by value in the root model.
type modelASLState struct {
	// agent-session-loop runs view (ModeASLRuns). aslRows is the last snapshot
	// projection sorted by urgency; refreshes keep the cached rows and the
	// selected row_id so a poll landing under the cursor cannot move it.
	// aslLoaded distinguishes "no snapshot yet" from "a snapshot with no rows",
	// and aslStatus carries the daemon's availability verdict so the header can
	// say unavailable instead of rendering a false empty state.
	aslRows       []aslbridge.Row
	aslMeta       aslbridge.SnapshotMeta
	aslStatus     aslbridge.Status
	aslCursor     int
	aslOffset     int
	aslSelectedID string
	aslLoading    bool
	aslLoaded     bool
	aslErr        error
	aslRefreshSeq int
	// aslFocusTarget retains the row a focus detour was raised for, so an
	// ambiguity choice cannot be retargeted by a refresh landing mid-detour.
	aslFocusTarget string
	// aslResponse is the answer form (ModeASLResponse). It owns the fetched
	// question body and is zeroed wholesale on close, success, and mode exit.
	// The drafts typed into it outlive the form in aslReplyDrafts, keyed by plan
	// and question, the way unsent send drafts do: esc keeps a reply and the next
	// open of the same question restores it. An accepted answer drops its draft.
	aslResponse    asl.ResponseState
	aslReplyDrafts aslDraftStore
	// aslOperations is the bounded body-free receipt window shown next to the
	// runs list, so an accepted submission can reach consumed after its form has
	// already closed.
	aslOperations []asl.OperationView
	// aslActionTarget retains the run a stop/abort confirmation was raised for,
	// so a refresh landing under the overlay cannot retarget it.
	aslActionTarget    *asl.ActionTarget
	aslControlInFlight bool
	aslControlSeq      int
	// aslNotice explains the outcome of a deliberate action: a refusal, an
	// invalidated target, or an accepted control command. It is kept apart from
	// aslErr because aslErr also decides whether the held snapshot is
	// trustworthy, and an action refusal says nothing about the snapshot.
	aslNotice string
}
