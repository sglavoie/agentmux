package state

import (
	"strings"
	"testing"
	"time"
)

func TestPresentationChangedAtRoundTripsThroughUserVarInMilliseconds(t *testing.T) {
	changedAt := time.Unix(1_710_000_100, 123_456_789)
	encoded, err := (WindowState{PresentationChangedAt: changedAt}).MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar() error: %v", err)
	}
	wantMillis := changedAt.UnixMilli()
	if !strings.Contains(encoded, `"pc":1710000100123`) {
		t.Fatalf("MarshalUserVar() missing compact millisecond timestamp: %s", encoded)
	}

	var got WindowState
	if err := got.UnmarshalUserVar(encoded); err != nil {
		t.Fatalf("UnmarshalUserVar() error: %v", err)
	}
	if got.PresentationChangedAt.UnixMilli() != wantMillis {
		t.Fatalf("PresentationChangedAt = %v; want unix milliseconds %d", got.PresentationChangedAt, wantMillis)
	}
}

func TestPresentationChangedAtAbsentKeyRestoresZeroAndIsOmitted(t *testing.T) {
	encoded, err := (WindowState{}).MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar() error: %v", err)
	}
	if strings.Contains(encoded, `"pc"`) {
		t.Fatalf("zero presentation timestamp was persisted: %s", encoded)
	}

	var got WindowState
	if err := got.UnmarshalUserVar(`{"v":4,"ei":"stop"}`); err != nil {
		t.Fatalf("UnmarshalUserVar() error: %v", err)
	}
	if !got.PresentationChangedAt.IsZero() {
		t.Fatalf("missing pc restored as %v; want zero", got.PresentationChangedAt)
	}

	if err := got.UnmarshalUserVar(`{"a":"stop","pc":1710000100123}`); err != nil {
		t.Fatalf("UnmarshalUserVar(v1) error: %v", err)
	}
	if !got.PresentationChangedAt.IsZero() {
		t.Fatalf("schema-v1 payload fabricated presentation recency %v", got.PresentationChangedAt)
	}
}
