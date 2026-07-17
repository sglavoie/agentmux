package hooks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

var errDiagnosticJournalDisabled = errors.New("diagnostic journal is disabled")

// deliveryFailureWindow bounds the trailing period in which dropped hook
// deliveries (typically around a daemon restart) surface as a doctor warning.
const deliveryFailureWindow = time.Hour

type statusEvidenceKey struct {
	harness string
	home    string
}

// deliveryTerminal is the accumulated terminal disposition of one hook delivery
// request. A request counts as a dropped delivery only when its terminal record
// reports failure; intermediate hook.ipc_attempt transport errors that a later
// attempt resolves never contribute. The authoritative terminal record is the
// always-written hook.delivery stage; a hook.failure error is a fallback for
// legacy records that carry no correlated hook.delivery.
type deliveryTerminal struct {
	observedAt  time.Time
	failed      bool
	hasDelivery bool
}

// StatusEvidence accumulates the newest hook record for each normalized
// harness/home-dir pair, using the producer-stamped config_dir evidence so
// events credit the producing harness home rather than the routed window's
// launch profile. Records without config_dir (older hook clients) accumulate
// in a legacy empty-home bucket per harness. Visit is suitable for
// diagnostics.SnapshotWithVisitor, which lets callers gather hook evidence
// without changing the snapshot's selector or record limit.
type StatusEvidence struct {
	lastSeen map[statusEvidenceKey]time.Time
	// earliestObserved/latestObserved span every visited record regardless of
	// stage, so callers can distinguish a genuinely silent hook from a journal
	// whose history was pruned before the verification window.
	earliestObserved time.Time
	latestObserved   time.Time
	observedAny      bool
	// deliveries accumulates one terminal disposition per delivery request,
	// keyed by request identity. Terminalization collapses a request's many
	// records into a single failed/succeeded verdict; the trailing-window filter
	// is applied at query time because Visit has no reference clock.
	deliveries map[string]*deliveryTerminal
}

// NewStatusEvidence returns an empty hook-status evidence accumulator.
func NewStatusEvidence() *StatusEvidence {
	return &StatusEvidence{
		lastSeen:   make(map[statusEvidenceKey]time.Time),
		deliveries: make(map[string]*deliveryTerminal),
	}
}

// Visit records hook evidence from one journal envelope. Non-hook stages only
// contribute to the observed horizon; per-home crediting remains hook-only.
func (e *StatusEvidence) Visit(envelope diagnostics.TraceEnvelope) error {
	if e == nil {
		return nil
	}
	if observedAt := envelope.ObservedAt; !observedAt.IsZero() {
		if !e.observedAny || observedAt.Before(e.earliestObserved) {
			e.earliestObserved = observedAt
		}
		if observedAt.After(e.latestObserved) {
			e.latestObserved = observedAt
		}
		e.observedAny = true
	}
	if !strings.HasPrefix(envelope.Stage, "hook.") {
		return nil
	}
	e.observeDelivery(envelope)
	if e.lastSeen == nil {
		e.lastSeen = make(map[statusEvidenceKey]time.Time)
	}
	key := statusEvidenceKey{
		harness: normalizeEvidenceHarness(envelope.Harness),
		home:    normalizeEvidenceHome(envelope.Evidence["config_dir"]),
	}
	if envelope.ObservedAt.After(e.lastSeen[key]) {
		e.lastSeen[key] = envelope.ObservedAt
	}
	return nil
}

// observeDelivery folds one terminal-relevant hook record into the per-request
// disposition. hook.delivery is the authoritative terminal record (always
// written exactly once per request); its outcome decides success or failure and
// overrides any hook.failure fallback. A hook.failure error only marks a request
// failed when no correlated hook.delivery is present, covering legacy records.
// Intermediate hook.ipc_attempt records are ignored so a transient transport
// error that a retry resolves never surfaces as a dropped delivery.
func (e *StatusEvidence) observeDelivery(envelope diagnostics.TraceEnvelope) {
	if envelope.ObservedAt.IsZero() {
		return
	}
	switch envelope.Stage {
	case "hook.delivery":
	case "hook.failure":
		if envelope.Outcome != "error" {
			return
		}
	default:
		return
	}
	if e.deliveries == nil {
		e.deliveries = make(map[string]*deliveryTerminal)
	}
	key := deliveryRequestKey(envelope)
	terminal := e.deliveries[key]
	if terminal == nil {
		terminal = &deliveryTerminal{}
		e.deliveries[key] = terminal
	}
	if envelope.Stage == "hook.delivery" {
		terminal.hasDelivery = true
		terminal.failed = !deliverySuccessOutcome(envelope.Outcome)
		if envelope.ObservedAt.After(terminal.observedAt) {
			terminal.observedAt = envelope.ObservedAt
		}
		return
	}
	// hook.failure error: fallback terminal only when no authoritative
	// hook.delivery record has been (or will be) folded in for this request.
	if terminal.hasDelivery {
		return
	}
	terminal.failed = true
	if envelope.ObservedAt.After(terminal.observedAt) {
		terminal.observedAt = envelope.ObservedAt
	}
}

// deliveryRequestKey is the terminal identity used to collapse a delivery's
// records. Real hook records always carry a request ID; the trace-ID and empty
// fallbacks keep legacy records deterministic without inflating a single
// request's terminal count.
func deliveryRequestKey(envelope diagnostics.TraceEnvelope) string {
	if id := strings.TrimSpace(envelope.RequestID); id != "" {
		return "request:" + id
	}
	if id := strings.TrimSpace(envelope.TraceID); id != "" {
		return "trace:" + id
	}
	return ""
}

// deliverySuccessOutcome reports whether a hook.delivery outcome is a
// success-class DeliveryStatus (accepted or duplicate); every other terminal
// status is a failed delivery.
func deliverySuccessOutcome(outcome string) bool {
	switch outcome {
	case "accepted", "duplicate":
		return true
	default:
		return false
	}
}

// latestForHome returns the newest hook evidence credited to the harness/home
// pair, falling back to the harness's legacy bucket of records without
// config_dir evidence. The fallback prevents a false "missing" verdict while
// mixed-build hook clients are still producing pre-upgrade records.
func (e *StatusEvidence) latestForHome(harnessName, home string) time.Time {
	if e == nil {
		return time.Time{}
	}
	harnessKey := normalizeEvidenceHarness(harnessName)
	exact := e.lastSeen[statusEvidenceKey{harness: harnessKey, home: normalizeEvidenceHome(home)}]
	legacy := e.lastSeen[statusEvidenceKey{harness: harnessKey}]
	if legacy.After(exact) {
		return legacy
	}
	return exact
}

// Horizon reports how far back the traversed journal reaches from now. The
// bool is false when no record of any stage was observed, letting callers
// distinguish an empty journal from one whose history is merely shallow.
func (e *StatusEvidence) Horizon(now time.Time) (time.Duration, bool) {
	if e == nil || !e.observedAny {
		return 0, false
	}
	horizon := now.Sub(e.earliestObserved)
	if horizon < 0 {
		horizon = 0
	}
	return horizon, true
}

// DeliveryFailures counts terminally failed hook deliveries observed within the
// trailing deliveryFailureWindow of now, one per request, and returns the newest
// such terminal observation. Each request contributes at most one count: a retry
// that later succeeds or duplicates resolves to zero, while an exhausted request
// with many failed attempts counts exactly once. hook.delivery and failure-class
// outcomes are exempt from standard-detail journal sampling, so the terminal
// verdict stays sound at any detail.
func (e *StatusEvidence) DeliveryFailures(now time.Time) (int, time.Time) {
	if e == nil {
		return 0, time.Time{}
	}
	cutoff := now.Add(-deliveryFailureWindow)
	count := 0
	var latest time.Time
	for _, terminal := range e.deliveries {
		if !terminal.failed || terminal.observedAt.Before(cutoff) {
			continue
		}
		count++
		if terminal.observedAt.After(latest) {
			latest = terminal.observedAt
		}
	}
	return count, latest
}

func normalizeEvidenceHarness(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// normalizeEvidenceHome compares home dirs with expansion plus filepath.Clean
// only; neither side resolves symlinks, matching the config loader.
func normalizeEvidenceHome(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(config.ExpandTilde(path))
}

var activationEvidenceSnapshot = func(ctx context.Context, journal *diagnostics.Journal, visitor diagnostics.SnapshotVisitor) error {
	// The selector intentionally matches no production stage. The visitor still
	// observes every valid deduplicated record without retaining the journal in
	// the returned snapshot.
	_, err := journal.SnapshotWithVisitor(ctx, diagnostics.Selector{
		Stages: []string{"hook.status.__evidence_only__"},
		Limit:  1,
	}, nil, visitor)
	return err
}

var activationEvidenceProbe = probeStatusEvidence

// ProbeStatusEvidence gathers hook status evidence with one bounded journal
// traversal. It is the exported counterpart of the probe StatusAll uses, for
// callers that need the evidence itself (horizon, delivery failures) alongside
// the per-profile statuses.
func ProbeStatusEvidence(ctx context.Context, cfg *config.Config) (*StatusEvidence, error) {
	return activationEvidenceProbe(ctx, cfg)
}

func probeStatusEvidence(ctx context.Context, cfg *config.Config) (*StatusEvidence, error) {
	evidence := NewStatusEvidence()
	if cfg != nil && !cfg.Defaults.Diagnostics.JournalEnabledValue() {
		return evidence, errDiagnosticJournalDisabled
	}
	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		return evidence, err
	}
	if _, err := os.Stat(paths.JournalDir); os.IsNotExist(err) {
		return evidence, nil
	} else if err != nil {
		return evidence, err
	}
	options := diagnostics.JournalOptions{}
	if cfg != nil {
		options.KeepDays = cfg.Defaults.Diagnostics.JournalKeepDays
		options.MaxBytes = int64(cfg.Defaults.Diagnostics.JournalMaxMiB) << 20
	}
	journal, err := diagnostics.NewJournal(paths, options)
	if err != nil {
		return evidence, err
	}
	if err := activationEvidenceSnapshot(ctx, journal, evidence.Visit); err != nil {
		return evidence, err
	}
	return evidence, nil
}
