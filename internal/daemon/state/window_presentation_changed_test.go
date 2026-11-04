package state

import (
	"strings"
	"testing"
	"time"
)

func TestPresentationChangedAtRoundTripsThroughStatePayloadInMilliseconds(t *testing.T) {
	changedAt := time.Unix(1_710_000_100, 123_456_789)
	encoded, err := (WindowState{PresentationChangedAt: changedAt}).MarshalStatePayload()
	if err != nil {
		t.Fatalf("MarshalStatePayload() error: %v", err)
	}
	wantMillis := changedAt.UnixMilli()
	if !strings.Contains(encoded, `"pc":1710000100123`) {
		t.Fatalf("MarshalStatePayload() missing compact millisecond timestamp: %s", encoded)
	}

	var got WindowState
	if err := got.UnmarshalStatePayload(encoded); err != nil {
		t.Fatalf("UnmarshalStatePayload() error: %v", err)
	}
	if got.PresentationChangedAt.UnixMilli() != wantMillis {
		t.Fatalf("PresentationChangedAt = %v; want unix milliseconds %d", got.PresentationChangedAt, wantMillis)
	}
}

func TestPresentationChangedAtAbsentKeyRestoresZeroAndIsOmitted(t *testing.T) {
	encoded, err := (WindowState{}).MarshalStatePayload()
	if err != nil {
		t.Fatalf("MarshalStatePayload() error: %v", err)
	}
	if strings.Contains(encoded, `"pc"`) {
		t.Fatalf("zero presentation timestamp was persisted: %s", encoded)
	}

	var got WindowState
	if err := got.UnmarshalStatePayload(`{"v":4,"ei":"stop"}`); err != nil {
		t.Fatalf("UnmarshalStatePayload() error: %v", err)
	}
	if !got.PresentationChangedAt.IsZero() {
		t.Fatalf("missing pc restored as %v; want zero", got.PresentationChangedAt)
	}

	if err := got.UnmarshalStatePayload(`{"a":"stop","pc":1710000100123}`); err != nil {
		t.Fatalf("UnmarshalStatePayload(v1) error: %v", err)
	}
	if !got.PresentationChangedAt.IsZero() {
		t.Fatalf("schema-v1 payload fabricated presentation recency %v", got.PresentationChangedAt)
	}
}
