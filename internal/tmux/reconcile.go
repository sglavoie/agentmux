package tmux

import (
	"errors"
	"fmt"
	"sort"
	"time"

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

// Report is what one pass changed.
type Report struct {
	// Ended lists the sessions marked stale, with whether the agent was seen
	// to exit or could not be found.
	Ended []Ended
	// Released lists the sessions whose recorded owner had no terminal left.
	Released []runtimesession.ID
	// Detached lists the terminals removed because a later one owns the
	// session they were attached to.
	Detached []Client
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

// unchanged reports whether an observation agrees with a live record, which
// is exactly when Registry.Validate would leave it live.
func unchanged(record runtimesession.Record, observed runtimesession.Observation) bool {
	return observed.Instance.Generation == record.Target.Instance.Generation && observed.Present && !observed.Exited &&
		observed.Process != nil && record.Process != nil && *observed.Process == *record.Process
}
