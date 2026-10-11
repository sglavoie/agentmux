package tmux

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// The registry says which agents are live and who owns each one's terminal,
// but it only learns of a change from whoever makes it. An agent that exits on
// its own, a server that dies, and a viewer that is killed before it can
// release its ownership tell nobody. Reconciling compares the registry with
// what the private servers actually hold and records the difference. It never
// starts a server and never launches anything: an agent that is gone is
// recorded as gone.

// attachmentGrace is how long a recorded owner may have no terminal attached
// before its ownership is released. `amx attach` records ownership a moment
// before its client connects, so an owner without a terminal is only stale
// once that moment has clearly passed.
const attachmentGrace = 10 * time.Second

// Reconciler reconciles the tmux runtime sessions of one registry.
type Reconciler struct {
	store  *runtimesession.Store
	reader *runtimesession.Reader
	// open returns the backend for a server address; replaceable in tests.
	open func(address string) (*Backend, error)
}

// NewReconciler reconciles the registry behind store. reader may be shared
// with other readers of the same registry; nil makes one.
func NewReconciler(store *runtimesession.Store, reader *runtimesession.Reader) *Reconciler {
	if reader == nil {
		reader = runtimesession.NewReader(store)
	}
	return &Reconciler{store: store, reader: reader, open: func(address string) (*Backend, error) {
		server, err := OpenExisting(address)
		if err != nil {
			return nil, err
		}
		return NewBackend(server), nil
	}}
}

// Ended is one session a pass found no longer running.
type Ended struct {
	ID      runtimesession.ID
	Outcome runtimesession.Outcome
	Reason  string
}

// Viewed is one live session with a terminal attached, and the last moment
// that terminal was seen to be in use: when it attached, or when the operator
// last typed or pointed in it, whichever is later. tmux keeps both to the
// second.
type Viewed struct {
	ID runtimesession.ID
	At time.Time
}

// Muted is one live session and whether its pane carries the operator's mute
// (cc_mute). A session is listed only when the pass read its pane, so one
// left out says nothing either way.
type Muted struct {
	ID    runtimesession.ID
	Muted bool
}

// Report is what one pass changed, and who it found looking.
type Report struct {
	// Ended lists the sessions marked stale, with whether the agent was seen
	// to exit or could not be found.
	Ended []Ended
	// Released lists the sessions whose recorded owner had no terminal left.
	Released []runtimesession.ID
	// Detached lists the terminals removed because a later one owns the
	// session they were attached to.
	Detached []Client
	// Viewed lists the live sessions a terminal is attached to. It is an
	// observation, not a change: Empty ignores it.
	Viewed []Viewed
	// Mutes says, for every live session the pass read, whether it is muted.
	// Like Viewed it is an observation.
	Mutes []Muted
}

// Empty reports whether the pass changed nothing.
func (r Report) Empty() bool {
	return len(r.Ended) == 0 && len(r.Released) == 0 && len(r.Detached) == 0
}

// Reconcile runs one pass over every live tmux session in the registry. A
// server that cannot be read (as opposed to one that is not running) is left
// alone and reported in the error, after every other server was handled:
// missing evidence never ends a session.
func (r *Reconciler) Reconcile(now time.Time) (Report, error) {
	live, err := r.reader.Live()
	if err != nil {
		return Report{}, fmt.Errorf("tmux: reading runtime sessions: %w", err)
	}
	byServer := map[string][]runtimesession.Record{}
	for _, record := range live {
		if record.Target != nil && record.Target.Instance.Kind == runtimesession.BackendTmux {
			byServer[record.Target.Instance.Address] = append(byServer[record.Target.Instance.Address], record)
		}
	}
	addresses := make([]string, 0, len(byServer))
	for address := range byServer {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)

	var report Report
	var failures []error
	for _, address := range addresses {
		if err := r.reconcileServer(address, byServer[address], now, &report); err != nil {
			failures = append(failures, err)
		}
	}
	return report, errors.Join(failures...)
}

func (r *Reconciler) reconcileServer(address string, records []runtimesession.Record, now time.Time, report *Report) error {
	survey := Survey{Instance: runtimesession.BackendInstance{Kind: runtimesession.BackendTmux, Address: address}}
	server, err := r.open(address)
	switch {
	case errors.Is(err, ErrServerNotRunning):
	case err != nil:
		return err
	default:
		if survey, err = server.Survey(); err != nil {
			return err
		}
	}

	// A terminal that attached without detaching the others shares the
	// keyboard and the size with them. The last one to attach is the owner.
	for _, client := range survey.Competing() {
		if err := server.DetachClient(client); err != nil {
			return err
		}
		report.Detached = append(report.Detached, client)
	}

	type change struct {
		observation runtimesession.Observation
		release     string
	}
	changes := map[runtimesession.ID]change{}
	for _, record := range records {
		observation := survey.Observation(record, nil)
		if !unchanged(record, observation) {
			changes[record.ID] = change{observation: observation}
			continue
		}
		if pane, ok := backend.FindPane(survey.Panes, recordPane(record)); ok {
			report.Mutes = append(report.Mutes, Muted{ID: record.ID, Muted: pane.Vars[muteVar] != ""})
		}
		if at, ok := lastViewed(survey.ClientsOf(recordPane(record))); ok {
			report.Viewed = append(report.Viewed, Viewed{ID: record.ID, At: at})
		}
		if a := record.Attachment; a != nil && now.Sub(record.UpdatedAt) >= attachmentGrace &&
			len(survey.ClientsOf(recordPane(record))) == 0 {
			changes[record.ID] = change{release: a.ID}
		}
	}
	if len(changes) == 0 {
		return nil
	}
	// The registry is re-read under its lock: a session that was ended,
	// re-attached, or replaced since the listing is not touched on old news.
	return r.store.Update(func(registry *runtimesession.Registry) error {
		for _, record := range records {
			c, ok := changes[record.ID]
			if !ok {
				continue
			}
			current, err := registry.Get(record.ID)
			if err != nil || current.State != runtimesession.StateLive || current.Target == nil || *current.Target != *record.Target {
				continue
			}
			if c.release != "" {
				if current.Attachment == nil || current.Attachment.ID != c.release || !current.UpdatedAt.Equal(record.UpdatedAt) {
					continue
				}
				if _, err := registry.Detach(record.ID, c.release, now); err == nil {
					report.Released = append(report.Released, record.ID)
				}
				continue
			}
			stale, err := registry.Validate(record.ID, c.observation, now)
			if errors.Is(err, runtimesession.ErrStaleTarget) {
				report.Ended = append(report.Ended, Ended{ID: record.ID, Outcome: stale.Outcome(), Reason: stale.StaleReason})
			}
		}
		return nil
	})
}

// muteVar is the pane variable that mutes a session's notifications
// (session.Mute writes it; this package cannot import that one).
const muteVar = "cc_mute"

// lastViewed is the latest moment any of a session's terminals attached or
// was used.
func lastViewed(clients []Client) (time.Time, bool) {
	var latest int64
	for _, client := range clients {
		latest = max(latest, client.Created, client.Activity)
	}
	if latest == 0 {
		return time.Time{}, false
	}
	return time.Unix(latest, 0), true
}

// unchanged reports whether an observation agrees with a live record, which
// is exactly when Registry.Validate would leave it live.
func unchanged(record runtimesession.Record, observed runtimesession.Observation) bool {
	return observed.Instance.Generation == record.Target.Instance.Generation && observed.Present && !observed.Exited &&
		observed.Process != nil && record.Process != nil && *observed.Process == *record.Process
}
