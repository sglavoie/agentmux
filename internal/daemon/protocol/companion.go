package protocol

import (
	"encoding/json"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/conversation"
)

// This file holds the companion wire vocabulary: what the native menu bar app
// and any other long-lived observer of the daemon say to it and hear back. It
// is shapes only. The subscriber registry and stream writer live in
// internal/daemon/ipc, the projection that fills a CompanionSnapshot lives in
// internal/daemon/companion, and the handlers that answer these requests stay
// in the daemon root.
//
// Every shape here is additive. A field a peer does not know is ignored on
// decode and a field a peer omits decodes to its zero value, so the protocol
// version moves only for a change a reader could not survive by ignoring it.

// CompanionProtocolVersion is the companion contract a daemon built from this
// tree speaks. A subscriber names the version it was written against and the
// daemon answers with the newest it supports, so both sides can tell a peer
// that is merely newer from one that is incompatible.
const CompanionProtocolVersion = 1

// CompanionSnapshotVersion versions the CompanionSnapshot payload on its own,
// independently of the protocol version that frames it, so the snapshot can
// grow a breaking shape without renaming every other companion message.
const CompanionSnapshotVersion = 1

// CompanionSnapshotVersionRuntime is the mixed-backend snapshot: every v1
// field, plus a runtime block on each session an AgentMux runtime session
// owns, plus the sessions that run on a backend other than Kitty. Those carry
// no window, tab, OS-window, or socket field at all — a tmux pane has no Kitty
// identity, and inventing one would let an old client focus the wrong window.
// A daemon sends it only to a subscriber that asked for
// CompanionFeatureRuntimeSessions; every other subscriber keeps receiving v1,
// which lists Kitty sessions only.
const CompanionSnapshotVersionRuntime = 2

// CompanionFeatureRuntimeSessions is the subscribe feature that negotiates
// CompanionSnapshotVersionRuntime. The ack echoes it only when the daemon
// granted it, so a client knows which snapshot shape the stream carries.
const CompanionFeatureRuntimeSessions = "runtime_sessions"

// CompanionSessionCap bounds how many sessions one snapshot carries. Every
// field on a session is clamped, so this cap is what keeps a single snapshot
// frame under CompanionFrameMaxBytes however many windows Kitty holds.
const CompanionSessionCap = 512

// CompanionWaitingCap bounds the ASL waiting list for the same reason.
const CompanionWaitingCap = 64

// CompanionFrameMaxBytes is the ceiling on one encoded stream frame. The
// transport drops and counts a frame above it rather than writing it.
const CompanionFrameMaxBytes = 1 << 20

// Companion request types. None of them is serialized behind the hook ingress
// queue and none is deduplicated by request ID: subscribe is a stream rather
// than an event, focus_window acts on Kitty rather than on reducer state, and
// snooze and disposition are idempotent by construction, so the at-most-once
// machinery the hook path needs would only add latency to them.
const (
	TypeSubscribe               MessageType = "subscribe"
	TypeFocusWindow             MessageType = "focus_window"
	TypeNotificationSnooze      MessageType = "notification_snooze"
	TypeNotificationDisposition MessageType = "notification_disposition"
	TypeNotificationTest        MessageType = "notification_test"
)

// CompanionMessageTypes lists every companion request type, so a transport
// test can assert all of them stay off the serialized path in one place.
var CompanionMessageTypes = []MessageType{
	TypeSubscribe,
	TypeFocusWindow,
	TypeNotificationSnooze,
	TypeNotificationDisposition,
	TypeNotificationTest,
	TypeWorkspaceView,
}

// AttentionStates is the set of icon states that mean a human is being waited
// on: a finished turn, a permission prompt, a failure, and an ASL supervisor
// question. It is the one definition `amx focus-next` and the companion
// snapshot both read, so "needs attention" cannot mean two things. Warning is
// deliberately outside it: it decorates a running session rather than
// blocking one.
var AttentionStates = map[string]bool{
	"stop":     true,
	"key":      true,
	"error":    true,
	"question": true,
}

// SubscribeRequest opens a companion stream. Client, ClientVersion, and
// AMXBuild identify the subscriber for health and doctor; they are reported,
// never trusted for any decision. NotificationBackend claims the notification
// delivery role, and NotificationsAuthorized says the operating system lets the
// client actually show one — a claim without authorization is not a backend.
type SubscribeRequest struct {
	Protocol      int             `json:"protocol"`
	Client        string          `json:"client,omitempty"`
	ClientVersion string          `json:"client_version,omitempty"`
	AMXBuild      *buildinfo.Info `json:"amx_build,omitempty"`
	// Features names optional behaviors the client understands:
	// CompanionFeatureRuntimeSessions selects the snapshot shape, and
	// CompanionFeatureWorkspaceViews asks for workspace_view frames.
	Features                []string `json:"features,omitempty"`
	NotificationBackend     bool     `json:"notification_backend,omitempty"`
	NotificationsAuthorized bool     `json:"notifications_authorized,omitempty"`
}

// SubscriptionAck is the response line that opens a stream. Protocol is the
// version the daemon will speak on this stream; ServerProtocolMax the newest it
// could have. Frames follow on the same connection until a bye or EOF.
type SubscriptionAck struct {
	Protocol          int      `json:"protocol"`
	ServerProtocolMax int      `json:"server_protocol_max"`
	SubscriberID      string   `json:"subscriber_id"`
	HeartbeatMS       int64    `json:"heartbeat_ms"`
	Features          []string `json:"features,omitempty"`
}

// FocusWindowRequest asks the daemon to focus one window in one Kitty
// instance. KittySocket must be an endpoint the daemon already knows; the
// daemon never dials an address a client supplied.
type FocusWindowRequest struct {
	KittySocket string `json:"kitty_socket"`
	WindowID    int    `json:"window_id"`
}

// Focus refusal codes. They travel in Response.Error so a client can branch on
// them without parsing prose.
const (
	FocusErrorStaleTarget   = "stale_target"
	FocusErrorUnknownSocket = "unknown_socket"
)

// FocusWindowResult reports where the focused window lives, so a client can
// bring the right OS window forward.
type FocusWindowResult struct {
	WindowID   int `json:"window_id"`
	TabID      int `json:"tab_id"`
	OSWindowID int `json:"os_window_id"`
}

// Snooze modes for SnoozeRequest.Mode.
const (
	SnoozeModeUntil      = "until"
	SnoozeModeIndefinite = "indefinite"
	SnoozeModeClear      = "clear"
)

// SnoozeRequest sets or clears the global notification snooze. Until is a Unix
// second and is read only for SnoozeModeUntil.
type SnoozeRequest struct {
	Mode  string `json:"mode"`
	Until int64  `json:"until,omitempty"`
}

// SnoozeState is the snooze in force. SnoozedUntil is a Unix second, zero when
// no timed snooze is set; Indefinite outranks it.
type SnoozeState struct {
	SnoozedUntil int64 `json:"snoozed_until,omitempty"`
	Indefinite   bool  `json:"indefinite,omitempty"`
}

// DispositionRequest reports what became of one notification the companion
// delivered. Disposition is the daemon's disposition vocabulary; Error carries
// a failure's reason and is ignored otherwise.
type DispositionRequest struct {
	NotificationID string `json:"notification_id"`
	Disposition    string `json:"disposition"`
	Error          string `json:"error,omitempty"`
}

// Notification dispositions a companion reports in DispositionRequest. Delivered
// is the only non-terminal one: it says the operating system accepted the
// notification, and the daemon waits for it before treating the companion as
// the backend that showed it. Every other value is terminal, and the first
// terminal value the daemon receives for a notification wins. Failed before
// delivered sends the notification through Alerter instead.
const (
	DispositionDelivered    = "delivered"
	DispositionClicked      = "clicked"
	DispositionDismissed    = "dismissed"
	DispositionActionMute   = "action_mute"
	DispositionActionSnooze = "action_snooze"
	DispositionActionAnswer = "action_answer"
	// DispositionActionRespond says the operator asked to answer the prompt a
	// session is showing; see NotificationActionRespond.
	DispositionActionRespond = "action_respond"
	DispositionFailed        = "failed"
)

// CompanionDispositions lists every disposition a companion may report.
var CompanionDispositions = []string{
	DispositionDelivered,
	DispositionClicked,
	DispositionDismissed,
	DispositionActionMute,
	DispositionActionSnooze,
	DispositionActionAnswer,
	DispositionActionRespond,
	DispositionFailed,
}

// Notification actions a notification frame may offer. The daemon only offers
// what the companion can carry out itself through requests it already has:
// muting goes through `amx mute-toggle`, snoozing through notification_snooze,
// answering through the ASL question surface, and responding to a harness
// prompt through `amx send`, with the prompt in front of the operator. The
// daemon records which one was chosen and performs none of them.
const (
	NotificationActionMute   = "mute"
	NotificationActionSnooze = "snooze"
	NotificationActionAnswer = "answer"
	// NotificationActionRespond is offered on a permission prompt of a session
	// `amx send` can write to. It is not an approval: the companion shows the
	// session's screen and the operator presses the key that answers it.
	NotificationActionRespond = "respond"
)

// NotificationRetract is the payload of a notification_retract frame: the
// daemon no longer wants the named notification shown, because something newer
// replaced it, the session moved on, or its window closed. Reason is
// informational. A companion removes the notification if it still has it and
// reports nothing back.
type NotificationRetract struct {
	NotificationID string `json:"notification_id"`
	Reason         string `json:"reason,omitempty"`
}

// NotificationTestResult answers TypeNotificationTest. Backend names what
// confirmed delivery — "app" when the connected companion did. Delivered false
// with a Reason means no companion could confirm one, and the caller is
// expected to test Alerter itself.
type NotificationTestResult struct {
	Backend   string `json:"backend,omitempty"`
	Delivered bool   `json:"delivered"`
	Reason    string `json:"reason,omitempty"`
}

// CompanionHealth is the health-response view of companion subscribers.
type CompanionHealth struct {
	Subscribers                  int               `json:"subscribers"`
	NotificationBackendConnected bool              `json:"notification_backend_connected"`
	Clients                      []CompanionClient `json:"clients,omitempty"`
}

// CompanionClient is one connected subscriber as it identified itself.
type CompanionClient struct {
	SubscriberID        string          `json:"subscriber_id"`
	Client              string          `json:"client,omitempty"`
	ClientVersion       string          `json:"client_version,omitempty"`
	AMXBuild            *buildinfo.Info `json:"amx_build,omitempty"`
	NotificationBackend bool            `json:"notification_backend,omitempty"`
	ConnectedAt         int64           `json:"connected_at,omitempty"`
	// SnapshotVersion is the snapshot shape this subscriber negotiated.
	SnapshotVersion int `json:"snapshot_version,omitempty"`
	// WorkspaceViews reports a subscriber that hosts workspace views.
	WorkspaceViews bool `json:"workspace_views,omitempty"`
}

// StreamFrameType names the payload a StreamFrame carries.
type StreamFrameType string

const (
	FrameSnapshot            StreamFrameType = "snapshot"
	FrameNotification        StreamFrameType = "notification"
	FrameNotificationRetract StreamFrameType = "notification_retract"
	FrameHeartbeat           StreamFrameType = "heartbeat"
	FrameBye                 StreamFrameType = "bye"
)

// StreamFrame is one server-to-client line on a subscription. V is the
// protocol version, Seq advances by one per frame on one stream, and Data is
// the type-specific payload, decoded by the reader once it has read Type. A
// reader skips a frame type it does not know.
type StreamFrame struct {
	V    int             `json:"v"`
	Seq  uint64          `json:"seq"`
	Type StreamFrameType `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Bye reasons.
const (
	ByeShutdown     = "shutdown"
	ByeSlowConsumer = "slow_consumer"
)

// Bye is the payload of the last frame the daemon writes before it closes a
// stream on purpose. A stream that ends without one ended by accident.
type Bye struct {
	Reason string `json:"reason"`
}

// CompanionSnapshot is the whole state a companion renders, replaced wholesale
// on every snapshot frame. It deliberately carries timestamps rather than ages:
// ages are left to the client, so two projections of an unchanged world encode
// to the same bytes and the daemon can skip publishing one.
type CompanionSnapshot struct {
	V            int                   `json:"v"`
	Daemon       CompanionDaemon       `json:"daemon"`
	Kitty        CompanionKitty        `json:"kitty"`
	Notification CompanionNotification `json:"notification"`
	Dashboard    CompanionDashboard    `json:"dashboard"`
	Attention    CompanionAttention    `json:"attention"`
	ASL          CompanionASL          `json:"asl"`
	Sessions     []CompanionSession    `json:"sessions"`
	// Truncated reports that Sessions was capped at CompanionSessionCap. The
	// attention counts still cover every session.
	Truncated bool `json:"truncated,omitempty"`
}

// CompanionDaemon identifies the daemon that produced the snapshot.
type CompanionDaemon struct {
	PID            int             `json:"pid"`
	StartedAt      int64           `json:"started_at,omitempty"`
	Build          *buildinfo.Info `json:"build,omitempty"`
	Ready          bool            `json:"ready"`
	DegradedReason string          `json:"degraded_reason,omitempty"`
}

// CompanionKitty describes the window tree the sessions were read from. Socket
// is the instance that tree belongs to; Sockets lists every instance a session
// in this snapshot lives in, which is how a client knows it is looking at more
// than one Kitty.
type CompanionKitty struct {
	Ready   bool     `json:"ready"`
	Stale   bool     `json:"stale,omitempty"`
	Socket  string   `json:"socket,omitempty"`
	Sockets []string `json:"sockets,omitempty"`
}

// CompanionNotification is the delivery posture a companion shows read-only.
// QuietHours is the configured "HH:MM-HH:MM" window verbatim, and QuietNow
// whether it covers the moment of projection.
type CompanionNotification struct {
	Backend      string `json:"backend,omitempty"`
	AppConnected bool   `json:"app_connected,omitempty"`
	SnoozedUntil int64  `json:"snoozed_until,omitempty"`
	Indefinite   bool   `json:"indefinite,omitempty"`
	QuietHours   string `json:"quiet_hours,omitempty"`
	QuietNow     bool   `json:"quiet_now,omitempty"`
}

// CompanionDashboard locates a running amx dashboard, if any.
type CompanionDashboard struct {
	Open        bool   `json:"open"`
	KittySocket string `json:"kitty_socket,omitempty"`
	OSWindowID  int    `json:"os_window_id,omitempty"`
	TabID       int    `json:"tab_id,omitempty"`
	WindowID    int    `json:"window_id,omitempty"`
}

// CompanionAttention counts sessions by what they ask of the operator. Total is
// the number of sessions whose Attention bit is set; Working counts the
// active-class icons, which never need attention.
type CompanionAttention struct {
	Total    int `json:"total"`
	Stop     int `json:"stop,omitempty"`
	Key      int `json:"key,omitempty"`
	Error    int `json:"error,omitempty"`
	Question int `json:"question,omitempty"`
	Working  int `json:"working,omitempty"`
	Unseen   int `json:"unseen,omitempty"`
}

// CompanionASL lists ASL runs waiting on a supervisor answer.
type CompanionASL struct {
	Waiting []CompanionASLWaiting `json:"waiting,omitempty"`
}

// CompanionASLWaiting is one run with unanswered supervisor questions. It names
// the question and never carries its body or an action token: a companion
// fetches the body through asl_question when the operator opens it, so the
// stream never holds prose a subscriber was not asked to show.
type CompanionASLWaiting struct {
	RunID   string `json:"run_id"`
	PlanDir string `json:"plan_dir,omitempty"`
	// WindowID is the Kitty window driving the run. A v2 run driven from a
	// non-Kitty runtime session has none and names RuntimeSessionID instead.
	WindowID                int    `json:"window_id,omitempty"`
	RuntimeSessionID        string `json:"runtime_session_id,omitempty"`
	Unanswered              int    `json:"unanswered"`
	LatestQuestionID        string `json:"latest_question_id,omitempty"`
	LatestQuestionCreatedAt int64  `json:"latest_question_created_at,omitempty"`
}

// CompanionSession is one live managed session. Timestamps are Unix seconds
// except PresentationChangedAtMS, which mirrors WindowStateSnapshot. The Kitty
// fields are set exactly when the session lives in Kitty; in a v2 snapshot a
// session on another backend has only Runtime to be addressed by.
type CompanionSession struct {
	WindowID                int                    `json:"window_id,omitempty"`
	TabID                   int                    `json:"tab_id,omitempty"`
	OSWindowID              int                    `json:"os_window_id,omitempty"`
	KittySocket             string                 `json:"kitty_socket,omitempty"`
	Harness                 string                 `json:"harness,omitempty"`
	Profile                 string                 `json:"profile,omitempty"`
	Project                 string                 `json:"project,omitempty"`
	BaseTitle               string                 `json:"base_title,omitempty"`
	Conversation            *conversation.Identity `json:"conversation,omitempty"`
	ASLRunID                string                 `json:"asl_run_id,omitempty"`
	IconState               string                 `json:"icon_state,omitempty"`
	Reason                  string                 `json:"reason,omitempty"`
	Unseen                  bool                   `json:"unseen,omitempty"`
	Attention               bool                   `json:"attention,omitempty"`
	Muted                   bool                   `json:"muted,omitempty"`
	Locked                  bool                   `json:"locked,omitempty"`
	Looping                 bool                   `json:"looping,omitempty"`
	BusySinceAt             int64                  `json:"busy_since_at,omitempty"`
	LastEventAt             int64                  `json:"last_event_at,omitempty"`
	PresentationChangedAtMS int64                  `json:"presentation_changed_at_ms,omitempty"`
	Branch                  string                 `json:"branch,omitempty"`
	Dirty                   bool                   `json:"dirty,omitempty"`
	// TabColor is the background a session with no Kitty tab is listed on,
	// as "#rrggbb": its palette color's bright background, which black text
	// reads on. Only a v2 snapshot carries it, and the daemon leaves it empty
	// for a Kitty session, whose tab Kitty itself paints.
	TabColor string `json:"tab_color,omitempty"`
	// Name is the name the operator gave a session with no Kitty tab
	// (`amx name-session`), empty for none. Only a v2 snapshot carries it.
	Name string `json:"name,omitempty"`
	// HarnessIcon is the harness badge a session with no Kitty tab is listed
	// with: its harness's configured emoji, or the one the icon picker put
	// in its title, empty for none. Only a v2 snapshot carries it.
	HarnessIcon string `json:"harness_icon,omitempty"`
	// Runtime names the AgentMux runtime session that owns this session. Only
	// a v2 snapshot carries it.
	Runtime *CompanionRuntime `json:"runtime,omitempty"`
}

// CompanionRuntime is a session's AgentMux runtime identity and the backend
// target it runs at. Pane is the backend's own reference (a Kitty window ID in
// decimal, a tmux pane ID), opaque to the client.
type CompanionRuntime struct {
	SessionID  string `json:"session_id"`
	Backend    string `json:"backend"`
	Instance   string `json:"instance,omitempty"`
	Generation string `json:"generation,omitempty"`
	Pane       string `json:"pane,omitempty"`
}

// TypeWorkspaceView asks the desktop workspace to show something: the native
// view of a session, its Dashboard or Overview, or a utility terminal. It is
// how a surface the workspace embeds (the dashboard, the overview) hands a
// launch or a focus to the workspace instead of taking over its own terminal.
// The daemon validates the request and forwards it as a FrameWorkspaceView to
// every subscriber that asked for CompanionFeatureWorkspaceViews; it shows
// nothing itself. Like the other companion requests it stays off the hook
// ingress queue.
const TypeWorkspaceView MessageType = "workspace_view"

// CompanionFeatureWorkspaceViews is the subscribe feature a client that hosts
// workspace views names. Only such a subscriber is sent workspace_view frames,
// and a workspace_view request is refused while none is connected.
const CompanionFeatureWorkspaceViews = "workspace_views"

// FrameWorkspaceView carries one WorkspaceViewRequest to a workspace.
const FrameWorkspaceView StreamFrameType = "workspace_view"

// Workspace view kinds. A session view shows a managed agent and is the only
// kind tied to a runtime session's identity. The others are utility views:
// terminals the workspace runs beside the agents, which are never sessions,
// never counted as waiting, and never carry a runtime session of their own.
// A pager names the session whose scrollback it reads, nothing more.
const (
	WorkspaceViewSession   = "session"
	WorkspaceViewDashboard = "dashboard"
	WorkspaceViewOverview  = "overview"
	WorkspaceViewShell     = "shell"
	WorkspaceViewEditor    = "editor"
	WorkspaceViewPager     = "pager"
)

// WorkspaceViewKinds lists every kind a workspace_view request may name.
var WorkspaceViewKinds = []string{
	WorkspaceViewSession,
	WorkspaceViewDashboard,
	WorkspaceViewOverview,
	WorkspaceViewShell,
	WorkspaceViewEditor,
	WorkspaceViewPager,
}

// Workspace view refusal codes, in Response.Error.
const (
	// WorkspaceViewErrorUnavailable: no workspace is connected to show it.
	WorkspaceViewErrorUnavailable = "workspace_unavailable"
	// WorkspaceViewErrorInvalid: the request names no known kind or lacks
	// what its kind needs.
	WorkspaceViewErrorInvalid = "invalid_view"
	// WorkspaceViewErrorSessionUnknown: the runtime session is not a live
	// session a workspace can embed.
	WorkspaceViewErrorSessionUnknown = "runtime_session_unknown"
	// WorkspaceViewErrorSessionNotListed: the session is live but the daemon
	// has not heard from it, so no workspace lists it yet.
	WorkspaceViewErrorSessionNotListed = "session_not_listed"
)

// WorkspaceViewRequest names one view. It never carries a command line: the
// workspace decides what each kind runs, so a request can open a shell or an
// editor where the operator already could and nothing else.
type WorkspaceViewRequest struct {
	Kind string `json:"kind"`
	// RuntimeSessionID is the session a session view shows or a pager reads.
	RuntimeSessionID string `json:"runtime_session_id,omitempty"`
	// Await marks a session that was just launched: a workspace that does not
	// list it yet shows it once it does, instead of the request being refused.
	Await bool `json:"await,omitempty"`
	// Directory is where a shell starts. Absolute.
	Directory string `json:"directory,omitempty"`
	// Path is what an editor opens. Absolute.
	Path string `json:"path,omitempty"`
	// Title labels a utility view in the workspace.
	Title string `json:"title,omitempty"`
}

// WorkspaceViewResult answers TypeWorkspaceView: how many workspaces were
// handed the request.
type WorkspaceViewResult struct {
	Delivered int `json:"delivered"`
}
