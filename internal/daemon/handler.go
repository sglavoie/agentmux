package daemon

import (
	"sync"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/asl"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// kittyTitleClient is the narrow subset of kitty.KittyClient that the daemon
// sweeps need. Declaring it locally lets tests provide a focused fake instead
// of all methods on kitty.KittyClient. kitty.SubprocessClient satisfies this
// interface implicitly.
type kittyTitleClient interface {
	ListWindows() ([]kitty.OSWindow, error)
	GetText(matchExpr, extent string) (string, error)
	SetTabTitle(matchExpr, title string) error
	SetUserVars(matchExpr string, vars map[string]string) error
}

// Handler processes daemon requests.
type Handler struct {
	guard    *activity.StateGuard
	notifier *notification.Notifier
	// decisionJournal receives complete sanitized core decision records after
	// the in-memory SessionEvent has been finalized. Nil keeps diagnostics
	// disabled and every write remains fail-open.
	decisionJournal *diagnostics.Journal
	// captureStore is consulted only for explicitly authorized temporary raw
	// captures. Nil and every write error are fail-open for hook delivery.
	captureStore             *diagnostics.CaptureStore
	captureMaintenanceMu     sync.Mutex
	lastCaptureMaintenanceAt time.Time
	asyncMu                  sync.Mutex
	notificationEffects      map[string]notification.NotificationDisposition
	notificationEffectOrder  []string

	// startedAt records the daemon start time so health queries can report
	// uptime. Zero value means uptime should be reported as 0.
	startedAt time.Time

	// buildInfo identifies the artifact this daemon is running. Stamped once
	// by daemon.Run at startup (before the on-disk binary can be replaced)
	// and reported from TypeHealth so clients can detect a CLI/daemon build
	// mismatch. Nil in unit tests that never set it.
	buildInfo *buildinfo.Info

	// metricsFn returns the owning server's request-outcome counters (queue
	// saturation, queue latency) for TypeHealth and export surfaces. Installed
	// by NewServer; nil when the handler runs without a server.
	metricsFn func() protocol.MetricsSnapshot

	// aslBridge is the optional agent-session-loop integration. Unset means the
	// daemon was started without it, in which case every ASL request is refused
	// and the health capability block is omitted. It is installed after the
	// socket is already serving, so it publishes atomically rather than under
	// stateMu: connections are being answered before it exists.
	aslBridge asl.BridgeHolder

	// eventSeq is the daemon's global monotonic receipt sequence, stamped onto
	// every event appended to a per-window ring so exports can totally order
	// receipts across windows. Guarded by stateMu; restarts from zero with the
	// daemon (receipt order is a process-lifetime notion).
	eventSeq uint64

	// kittyClient is used by the stale sweep to clear stranded tab-title icons.
	// Injected by daemon.Run; nil in unit tests that don't exercise the
	// title-clear path.
	kittyClient kittyTitleClient

	// renderQueue is the latest-generation per-window render queue consumed by
	// the render worker (renderqueue.go), the daemon's single Kitty writer.
	renderQueue *rendering.Queue

	// processInspector observes the validated PID/start-token fingerprints
	// retained by reducer facts. The lifecycle loop always calls it off stateMu;
	// tests replace it with a controllable inspector to exercise stale-observation
	// rejection and shutdown. The dedicated mutex permits live replacement without
	// coupling a potentially slow process-table read to stateMu.
	processInspectorMu sync.RWMutex
	processInspector   probes.HarnessProcessInspector
	// processDiagnosticsMu rate-limits lifecycle inspection-failure journal
	// records independently of stateMu. Healthy one-second polls are never
	// journaled; committed settlements are recorded separately without rate
	// limiting.
	processDiagnosticsMu           sync.Mutex
	lastProcessInspectionFailureAt time.Time

	// staleThresholds is the per-action stale-detection policy the sweep
	// resolves an (account, action) pair against. Its zero value disables the
	// sweep, which is what a Handler behaves like until SetStaleThresholds runs.
	staleThresholds activity.StaleThresholds

	// hookSilence is the account-level hook-silence policy: when an account's
	// most recent event is older than the duration it resolves, the sweep fires
	// a one-shot notification. Its zero value disables the sweep.
	hookSilence activity.HookSilenceThresholds

	// stateMu protects store, the account-level hook-silence maps, and
	// windowMetadata. Per-window state lives under store so handlers mutate a
	// single WindowState entry instead of a set of parallel maps.
	stateMu sync.Mutex
	store   map[int]*state.WindowState

	// resetStopCommitMu linearizes a claimed held Stop's final state
	// application with events that supersede it. Callers acquire it before
	// stateMu; the callback does not hold either lock while invoking the test
	// seam below.
	resetStopCommitMu             sync.Mutex
	pendingStopHolds              map[int]pendingStopHold
	nextStopHoldSeq               uint64
	afterDeferredStopClaimForTest func(windowID int, seq uint64)

	kittySockets      map[int]string
	lastKittyListenOn string

	// snapshots holds the immutable Kitty snapshot cache. The render/restore
	// loop is the single lister and publishes into it; hook routing reads it
	// without any Kitty I/O so a slow or wedged ListWindows can never delay a
	// hook acknowledgement. Its own RWMutex keeps snapshot reads off stateMu.
	snapshots probes.SnapshotStore

	// readiness tracks whether the daemon completed its bounded initial
	// snapshot+restore before binding the public socket, and, if not, why it
	// started degraded. Exposed via TypeHealth. Guarded by its own mutex.
	readiness readinessState

	// probeRunner runs an asynchronous context-limit screen probe off the hook
	// goroutine. Nil means spawn a goroutine (production); tests inject a
	// synchronous runner for deterministic assertions.
	probeRunner func(func())

	// activeLoopCache memoizes agent-session-loop discovery for the ASL
	// sibling-pane routing exception (handler_asl_routing.go). Discovery scans
	// on-disk loop state, so a hook burst from the same pane must not rescan on
	// every event. Entries are valid for at most rendering.Interval and are
	// invalidated whenever a loop lifecycle event (started, turn started,
	// finished) changes the active set. The cache is rebuilt lazily, including
	// after a daemon restart, and is never persisted in amx_state. Guarded by
	// stateMu.
	activeLoopCache      []agentloop.Candidate
	activeLoopCacheAt    time.Time
	activeLoopCacheValid bool
	// activeLoopCacheGen counts lifecycle invalidations. A scan captures the
	// generation before releasing stateMu to discover, and only publishes its
	// result if the generation still matches on re-lock. This prevents a slow
	// scan that started before invalidateActiveLoopCache from overwriting the
	// newer (empty) state with a stale candidate set.
	activeLoopCacheGen uint64

	// aslMissStreak counts, per window lease, how many consecutive discoveries
	// positively confirmed the lease's run absent. Reconciliation releases the
	// lease at asl.MissingRunThreshold; indeterminate evidence neither advances
	// nor resets a counter, and a discovery failure leaves the counters (and
	// the leases) untouched. lastASLReconcileAt gates the periodic
	// reconciliation cadence. Both guarded by stateMu.
	aslMissStreak      asl.MissStreaks
	lastASLReconcileAt time.Time
	// aslRunPosition records, per run ID, the furthest projection generation
	// and lifecycle source sequence reconciliation has folded in. Both counters
	// are monotonic at the source, so an observation that goes backwards on
	// either is a stale read and is rejected rather than applied. Guarded by
	// stateMu.
	aslRunPosition asl.RunPositions
	// aslStateRootValue is the canonical agent-session-loop state root, and
	// aslMetrics the reconciliation counters surfaced in health. Both guarded
	// by stateMu.
	aslStateRootValue string
	aslMetrics        asl.ReconcileMetrics
	// aslDelivery counts lifecycle-hook ingestion outcomes. It is the inbound
	// counterpart to aslMetrics: reconciliation says what the daemon can read
	// from the state root, delivery says what ASL managed to push. Guarded by
	// stateMu.
	aslDelivery asl.HookDeliveryMetrics
	// aslProjectionCache lets the repeating sweep skip re-parsing state
	// documents that have not changed since the last one. It is internally
	// synchronized and is deliberately not guarded by stateMu, because
	// discovery runs outside it.
	aslProjectionCache *agentloop.ProjectionCache
	// aslSupervisors retains the small fixed-size lease path for each
	// discovered live ASL run. The two-second maintenance pass refreshes only
	// these registered targets instead of rescanning the complete ASL state
	// root. Projections are cached alongside them so a Stop arriving after the
	// mailbox observation can correlate without filesystem I/O on the hook
	// path, and closure cutoffs keep an in-flight stale projection from
	// reasserting a question after an authoritative mailbox reply or
	// resolution. Guarded by stateMu.
	aslSupervisors asl.SupervisorRegistry

	// idleSamples counts, per window probe source, how many consecutive
	// stabilized idle screen samples an adapter has observed before an
	// AuthoritativeIdle may settle stale interactive work, together with the
	// evidence generation each streak is pinned to. Screen adapters that require
	// stabilized evidence (Codex/Claude reads) accumulate here; a streak is
	// cleared after activity, a classifier failure, a protected-state change, or
	// window closure. The tracker is caller-locked: guarded by stateMu.
	idleSamples activity.Samples

	// declines counts, per window probe source, how many consecutive screen
	// frames the classifier refused to grade as a settleable terminal, the
	// fingerprint each streak is accumulating against, and the diagnostic
	// context of the frame that last extended it. A pane that is genuinely
	// working declines on every tick, so the count on its own is not a health
	// signal — the fingerprint is what turns it into one. Cleared by a
	// successful settle and dropped when the window closes. Caller-locked:
	// guarded by stateMu.
	declines activity.Declines

	// reducerCfg holds the deterministic thresholds handed to each window's
	// shadow reducer. Set once at startup from [defaults.watchdog] and
	// guard_duration (SetReducerConfig); the zero value disables the reducer's
	// staleness self-heals and legacy grace, which is what unit tests want.
	// Guarded by stateMu (per-window reducers are only touched under it).
	reducerCfg state.ReducerConfig

	// claudeContextLimitProbeEnabled gates ProbeClaudeContextLimit. Set from
	// [defaults.watchdog].claude_context_limit_probe at daemon startup.
	// Defaults to true; an explicit `false` skips the probe entirely.
	claudeContextLimitProbeEnabled bool

	// stopConfirmDelay, stopConfirmTimeout and stopConfirmEnabled configure the
	// confirmed-idle gate a native stop passes through before it paints the
	// terminal icon: the quiet period a held stop must observe, the hard
	// deadline after which it commits unconfirmed, and whether the gate runs at
	// all. Set from [defaults.stop_confirm] at daemon startup
	// (SetStopConfirm) before the hook path can read them.
	stopConfirmDelay   time.Duration
	stopConfirmTimeout time.Duration
	stopConfirmEnabled bool

	// copilotTranscriptHomes and copilotDefaultHome resolve a managed Copilot
	// tab's interactive session transcript for the transcript stop probe.
	// copilotTranscriptHomes is the ordered set of configured Copilot profile
	// home directories (already ~-expanded); copilotDefaultHome is the default
	// Copilot home used when no configured profile holds the transcript. Both
	// are set once at startup (SetCopilotTranscriptHomes) before the probe loop
	// starts and are never mutated afterward, so the probe reads them without a
	// lock — matching claudeContextLimitProbeEnabled.
	copilotTranscriptHomes []string
	copilotDefaultHome     string

	// screenPatterns records what screenstate.Load did at startup: which source
	// each pattern set came from and every warning the load produced. Set once
	// on the startup goroutine (SetScreenPatterns) before the probe loop starts
	// and never mutated afterward, so handleHealth reads it without a lock —
	// the same convention as copilotTranscriptHomes above. Nil means the load
	// never ran (a hand-built Handler in a test), which health reports as an
	// absent block rather than as an empty one.
	screenPatterns *protocol.ScreenPatternsSnapshot

	// copilotLifecycleReader reads a resolved Copilot transcript's terminal
	// evidence for the transcript stop probe. Defaults to
	// transcript.ReadCopilotLifecycle (NewHandler); tests inject a reader to
	// model a slow read racing a concurrent prompt, or to drive specific
	// evidence, without threading fixture timing through the resolver.
	copilotLifecycleReader func(path string) (transcript.CopilotLifecycleEvidence, error)

	// lastSeenEventAt records the most recent event timestamp per account
	// (any action, any window). The hook-silence sweep compares this against
	// the configured threshold to detect accounts whose hooks have stopped
	// reaching the daemon entirely. Keys are lowercased account names so that
	// hook payloads and cc_account user vars with differing case still match.
	lastSeenEventAt map[string]time.Time

	// notifiedHookSilence is a one-shot latch per account: once the
	// hook-silence sweep fires for an account, this prevents re-notification
	// until a fresh event for that account re-arms the entry by deleting it
	// in handleStateUpdate. Keys are lowercased account names — see
	// lastSeenEventAt for rationale.
	notifiedHookSilence map[string]bool

	// hookSilenceIdle accumulates in-process observed idle time per account
	// (lowercased), anchored to that account's lastSeenEventAt value. The
	// hook-silence sweep expires on the accumulated duration rather than a
	// wall-clock difference so a system sleep cannot fire a false silence
	// notification on wake. Guarded by stateMu.
	hookSilenceIdle map[string]*activity.AccountIdle

	// windowMetadata stores the durable per-window export snapshot. Terminal
	// cleanup intentionally does not clear this so post-mortem exports still
	// have identity/context even after the original Kitty tab is gone;
	// closed-window GC bounds it via windowTombstones below.
	windowMetadata map[int]protocol.WindowDebugMetadata

	// windowTombstones records when each closed window was garbage-collected.
	// The tombstone is the retention lease for the window's durable metadata:
	// entries expire after windowTombstoneRetention or once
	// maxWindowTombstones is exceeded (oldest first, tracked by
	// tombstoneOrder), and eviction deletes the windowMetadata entry too. A
	// tombstoned window never counts as renderer drift. Guarded by stateMu.
	windowTombstones map[int]time.Time
	tombstoneOrder   []int

	// onShutdown is called when a TypeShutdown request is received.
	onShutdown func()

	// shutdownOnce gates onShutdown so back-to-back shutdown requests cannot
	// race the daemon's cancel func. context.CancelFunc is already idempotent,
	// so this is a defence-in-depth guard rather than a correctness fix.
	shutdownOnce sync.Once
}

// maxEventsPerWindow caps the per-window ring buffer used by handleSessionDetail.
// At ~50 windows × ~200 B/event, the upper bound stays around ~5 MB worst case.
const maxEventsPerWindow = 5000

// NewHandler creates a Handler with the given guard and notifier. The
// reducer's trailing-event grace defaults to the guard duration so legacy
// guard semantics and reducer suppression agree; daemon.Run overrides the
// full threshold set via SetReducerConfig.
func NewHandler(guard *activity.StateGuard, notifier *notification.Notifier) *Handler {
	var cfg state.ReducerConfig
	if guard != nil {
		cfg.GuardDuration = int64(guard.Duration() / time.Second)
	}
	h := &Handler{
		guard:                          guard,
		notifier:                       notifier,
		reducerCfg:                     cfg,
		startedAt:                      time.Now(),
		store:                          make(map[int]*state.WindowState),
		pendingStopHolds:               make(map[int]pendingStopHold),
		renderQueue:                    rendering.NewQueue(),
		processInspector:               probes.NewPSHarnessProcessInspector(),
		kittySockets:                   make(map[int]string),
		claudeContextLimitProbeEnabled: true,
		stopConfirmDelay:               defaultStopConfirmDelay,
		stopConfirmTimeout:             defaultStopConfirmTimeout,
		stopConfirmEnabled:             true,
		copilotLifecycleReader:         transcript.ReadCopilotLifecycle,
		aslProjectionCache:             agentloop.NewProjectionCache(),
		lastSeenEventAt:                make(map[string]time.Time),
		notifiedHookSilence:            make(map[string]bool),
		hookSilenceIdle:                make(map[string]*activity.AccountIdle),
		windowMetadata:                 make(map[int]protocol.WindowDebugMetadata),
		windowTombstones:               make(map[int]time.Time),
		notificationEffects:            make(map[string]notification.NotificationDisposition),
	}
	if notifier != nil {
		notifier.SetObserver(h.recordNotificationDispositionBounded)
	}
	return h
}
