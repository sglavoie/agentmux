package flightlog

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/sglavoie/agentmux/internal/diagnostics"
)

// Collector keeps disk I/O off the reducer lock and hook acknowledgement.
// Overflow and failed writes produce a durable gap marker on recovery.
type Collector struct {
	store    Store
	Epoch    string
	queue    chan Event
	stop     chan struct{}
	done     chan struct{}
	sequence atomic.Uint64
	lost     atomic.Uint64
}

func NewCollector(store Store) (*Collector, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := store.Initialize(ctx, time.Now()); err != nil {
		return nil, err
	}
	c := &Collector{store: store, Epoch: diagnostics.NewTraceID(), queue: make(chan Event, 2048), stop: make(chan struct{}), done: make(chan struct{})}
	go c.run()
	return c, nil
}

func (c *Collector) Record(event Event) {
	if c == nil {
		return
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	if event.ID == "" {
		event.ID = fmt.Sprintf("%s:%d", c.Epoch, c.sequence.Add(1))
	}
	event.Epoch = c.Epoch
	select {
	case c.queue <- event:
	default:
		c.lost.Add(1)
	}
}

func (c *Collector) Close() { close(c.stop); <-c.done }

func (c *Collector) run() {
	defer close(c.done)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	compactTicker := time.NewTicker(24 * time.Hour)
	defer compactTicker.Stop()
	compact := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.store.Compact(ctx); err != nil {
			slog.Warn("flight log: compaction deferred", "error", err)
		}
	}
	compact()
	batch := []Event{}
	flush := func() {
		lost := c.lost.Swap(0)
		if lost > 0 {
			batch = append(batch, Event{ID: diagnostics.NewTraceID(), At: time.Now().UTC(), Kind: "gap", Name: fmt.Sprintf("%d events not persisted", lost)})
		}
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		err := c.store.Append(ctx, batch...)
		cancel()
		if err != nil {
			c.lost.Add(uint64(len(batch)))
			slog.Warn("flight log: recording failed; observation gap", "error", err)
		}
		batch = nil
	}
	for {
		select {
		case e := <-c.queue:
			batch = append(batch, e)
			if len(batch) >= 128 {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-compactTicker.C:
			flush()
			compact()
		case <-c.stop:
			for {
				select {
				case e := <-c.queue:
					batch = append(batch, e)
				default:
					flush()
					return
				}
			}
		}
	}
}
