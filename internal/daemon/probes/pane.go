package probes

import (
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// Pane evidence for backends other than Kitty. The Kitty snapshot cache in
// snapshot.go is keyed by Kitty's window tree; a backend that reports flat
// panes through backend.Discoverer publishes here instead, and gets the same
// two guarantees: a probe-relevant change advances a pane's epoch, and churn
// probes do not consume leaves it alone.

// Metadata keys a pane's probe signature reads. They are the pane-metadata
// spellings of the Kitty user vars the tab signature reads.
const (
	paneVarProfile = "amx_profile"
	paneVarHarness = "amx_harness"
	paneVarMute    = "cc_mute"
)

// PaneKey names one pane of one backend instance generation. The generation is
// part of the key because a restarted tmux server hands out the same pane IDs
// again: the same ID under a new generation is a different pane.
type PaneKey struct {
	Instance runtimesession.BackendInstance
	Pane     backend.Ref
}

// paneSignature is the probe-relevant projection of one discovered pane. Like
// windowSignature it keeps derived facts only — which harness, whether the
// program has ended, which process owns the pane, Claude's busy-title class —
// and never the title text or working directory, which change continuously.
type paneSignature struct {
	group             backend.Ref
	exited            bool
	exitStatus        int
	exitSignal        string
	foregroundPIDs    []int
	foregroundHarness string
	foregroundPipe    bool
	profile           string
	harness           string
	mute              string
	claudeBusyTitle   bool
}

func paneSignatureFor(pane backend.Pane) paneSignature {
	signature := paneSignature{
		group:           pane.Group,
		profile:         pane.Vars[paneVarProfile],
		harness:         harness.NormalizeKnown(pane.Vars[paneVarHarness]),
		mute:            pane.Vars[paneVarMute],
		claudeBusyTitle: activity.ClaudeWindowTitleHasDescriptionMarker(pane.Title),
	}
	if pane.Exit != nil {
		signature.exited = true
		signature.exitStatus = pane.Exit.Status
		signature.exitSignal = pane.Exit.Signal
	}
	for _, process := range pane.Foreground {
		signature.foregroundPIDs = append(signature.foregroundPIDs, process.PID)
	}
	slices.Sort(signature.foregroundPIDs)
	signature.foregroundHarness, signature.foregroundPipe = activity.PaneForegroundHarness(pane)
	return signature
}

type paneState struct {
	epoch     uint64
	signature paneSignature
	pane      backend.Pane
}

// PaneStore holds the last published pane discovery of each backend instance
// address behind a reader/writer lock. The zero value is a cold store.
type PaneStore struct {
	mu        sync.RWMutex
	instances map[paneInstanceKey]paneDiscovery
	nextEpoch uint64
}

// paneInstanceKey is a backend instance without its generation: one tmux
// socket, across restarts.
type paneInstanceKey struct {
	kind    runtimesession.BackendKind
	address string
}

type paneDiscovery struct {
	generation string
	at         time.Time
	panes      map[backend.Ref]paneState
}

// PanePublication is one successful discovery of one backend instance.
type PanePublication struct {
	// Instance is the instance that answered, with its current generation.
	Instance runtimesession.BackendInstance
	// Panes is everything it reported. It must not be mutated afterwards.
	Panes []backend.Pane
	// CapturedAt is the instant the discovery completed.
	CapturedAt time.Time
}

// Publish records one discovery. A pane keeps its epoch when the instance
// generation and its signature are unchanged; a new generation gives every
// pane a new epoch, because none of them is the pane the old epoch named. A
// failed discovery is simply not published, which leaves the last success —
// and its capture time — in place.
func (s *PaneStore) Publish(p PanePublication) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.instances == nil {
		s.instances = make(map[paneInstanceKey]paneDiscovery)
	}
	key := paneInstanceKey{kind: p.Instance.Kind, address: p.Instance.Address}
	previous := s.instances[key]
	sameGeneration := previous.generation == p.Instance.Generation
	next := paneDiscovery{
		generation: p.Instance.Generation,
		at:         p.CapturedAt,
		panes:      make(map[backend.Ref]paneState, len(p.Panes)),
	}
	for _, pane := range p.Panes {
		state := paneState{signature: paneSignatureFor(pane), pane: pane}
		if prior, ok := previous.panes[pane.Ref]; ok && sameGeneration &&
			reflect.DeepEqual(prior.signature, state.signature) {
			state.epoch = prior.epoch
		} else {
			s.nextEpoch++
			state.epoch = s.nextEpoch
		}
		next.panes[pane.Ref] = state
	}
	s.instances[key] = next
}

// PaneObservation is what the last discovery said about one pane.
type PaneObservation struct {
	Key PaneKey
	// Pane is the discovered pane. It is read-only.
	Pane backend.Pane
	// Epoch is the pane's freshness epoch; it is never zero for a found pane.
	Epoch uint64
	// CapturedAt is when the discovery that reported the pane completed.
	CapturedAt time.Time
}

// Observe returns the pane as last discovered, and false when the instance has
// not been published, has since restarted under another generation, or did not
// report the pane.
func (s *PaneStore) Observe(key PaneKey) (PaneObservation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	discovery, ok := s.instances[paneInstanceKey{kind: key.Instance.Kind, address: key.Instance.Address}]
	if !ok || discovery.generation != key.Instance.Generation {
		return PaneObservation{}, false
	}
	state, ok := discovery.panes[key.Pane]
	if !ok {
		return PaneObservation{}, false
	}
	return PaneObservation{Key: key, Pane: state.pane, Epoch: state.epoch, CapturedAt: discovery.at}, true
}

// Fresh reports whether observation still describes the pane as currently
// published: same generation, still present, same epoch. It is the pane
// counterpart of BindingFresh, and like it a cold store is never fresh.
func (s *PaneStore) Fresh(observation PaneObservation) bool {
	if observation.Epoch == 0 {
		return false
	}
	current, ok := s.Observe(observation.Key)
	return ok && current.Epoch == observation.Epoch
}

// ScreenExpectation is what a screen read made for this pane observation must
// describe: that pane, in that instance generation, captured no earlier than
// the discovery that made it eligible.
func (o PaneObservation) ScreenExpectation() ScreenExpectation {
	return ScreenExpectation{Instance: o.Key.Instance, Pane: o.Key.Pane, NotBefore: o.CapturedAt}
}

// PaneHarnessProcess judges a tracked harness process using what a backend's
// discovery says about the pane it was launched in, together with the process
// table's answer for the same identity.
//
// The pane settles the question only when it can: a pane that a successful
// discovery no longer reports, or that the backend retains after its program
// ended, has lost that instance whatever the process table says — a reused PID
// cannot revive it. Otherwise the table's answer stands, including Unknown,
// which keeps every fact and retries later. The pane's foreground PIDs are
// deliberately not compared: a harness running a tool is not the foreground
// process of its own pane, and that must not read as a replaced instance.
//
// found is false when the discovery succeeded but did not report the pane. A
// failed discovery must not be passed here as "not found"; use the table's
// answer alone.
func PaneHarnessProcess(pane backend.Pane, found bool, table HarnessProcessInspection) HarnessProcessInspection {
	if !found || pane.Exit != nil {
		return HarnessProcessMissing
	}
	if table == "" {
		return HarnessProcessUnknown
	}
	return table
}
