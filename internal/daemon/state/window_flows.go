package state

// This file holds the small per-window flow enums carried by WindowState and
// persisted inside the amx_state user var. Their transition rules stay with the
// handlers that own them (Copilot permission handling, the reset flow); only the
// durable vocabulary lives here, because the persisted payload is this package's
// contract.

// CopilotToolPermissionState tracks Copilot's two-step tool-permission dance for
// one window. It is persisted as a small integer, so the ordinals are wire
// values and must not be reordered.
type CopilotToolPermissionState uint8

const (
	// CopilotToolPermissionNone is the resting state: no permission prompt is
	// outstanding and none was auto-approved.
	CopilotToolPermissionNone CopilotToolPermissionState = iota
	// CopilotToolPermissionPending marks a permission prompt awaiting the user.
	CopilotToolPermissionPending
	// CopilotToolPermissionAutoApproved marks a prompt Copilot resolved on its
	// own, so its trailing confirmation must not repaint the key icon.
	CopilotToolPermissionAutoApproved
)

// ResetFlowState is the short-lived per-window marker for a /clear, /reset, or
// /new flow. Like the Copilot state it is persisted as a small integer, so the
// ordinals are wire values.
type ResetFlowState uint8

const (
	// ResetFlowNone means no reset is in flight.
	ResetFlowNone ResetFlowState = iota
	// ResetFlowRequested marks a reset the user asked for but whose cleanup
	// events have not started arriving.
	ResetFlowRequested
	// ResetFlowClearing marks a reset whose cleanup events are being rewritten
	// into user_clear.
	ResetFlowClearing
)
