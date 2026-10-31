package flightlog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/sglavoie/agentmux/internal/atomicfile"
)

// Compact replaces sample pairs with merged observation spans. Every launch,
// transition, run event, gap, and earning-session identity survives. A final
// observation per session anchors the next sample after compaction. Corrupt
// ledgers are preserved for inspection rather than rewritten automatically.
func (s Store) Compact(ctx context.Context) error {
	return s.withLock(ctx, func() error {
		snapshot, err := s.load(ctx)
		if err != nil {
			return err
		}
		if snapshot.TrackingSince.IsZero() {
			return nil
		}
		if snapshot.CorruptLines > 0 {
			return fmt.Errorf("flight log: compaction refused with %d unreadable records; data preserved", snapshot.CorruptLines)
		}
		events := uniqueEvents(snapshot.Events, time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC))
		var out []Event
		for _, e := range events {
			if e.Kind != "sample" && e.Kind != "span" {
				out = append(out, e)
			}
		}
		for _, session := range sessions(events) {
			if err := ctx.Err(); err != nil {
				return err
			}
			merged := []Segment{}
			for _, segment := range session.Timeline {
				if n := len(merged); n > 0 && merged[n-1].To.Equal(segment.From) && merged[n-1].Icon == segment.Icon && merged[n-1].Looping == segment.Looping && merged[n-1].Observed == segment.Observed && merged[n-1].ConversationID == segment.ConversationID {
					merged[n-1].To = segment.To
				} else {
					merged = append(merged, segment)
				}
			}
			for _, segment := range merged {
				from := segment.From
				out = append(out, Event{ID: fmt.Sprintf("span:%s:%d:%d", session.Key, from.UnixNano(), segment.To.UnixNano()), Key: session.Key, At: segment.To, Kind: "span", From: &from, Icon: segment.Icon, Looping: segment.Looping, Observed: segment.Observed, ConversationID: segment.ConversationID})
			}
			for _, e := range session.Events {
				if e.Kind != "sample" {
					continue
				}
				i := sort.Search(len(session.Timeline), func(i int) bool { return session.Timeline[i].From.After(e.At) }) - 1
				covered := i >= 0 && session.Timeline[i].Observed && e.At.Before(session.Timeline[i].To) && session.Timeline[i].ConversationID == e.ConversationID
				// Standalone observations still prove a conversation was known
				// at that instant, even when neither adjacent interval is known.
				// Retaining them also keeps the final sample as the next anchor.
				if !covered {
					out = append(out, e)
				}
			}
		}
		out = uniqueEvents(out, time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC))
		var data bytes.Buffer
		encoder := json.NewEncoder(&data)
		if err := encoder.Encode(header{SchemaVersion: snapshot.SchemaVersion, TrackingSince: snapshot.TrackingSince, Settings: snapshot.Settings}); err != nil {
			return err
		}
		for _, e := range out {
			if err := encoder.Encode(e); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return atomicfile.Write(s.Path, data.Bytes(), 0o600)
	})
}
