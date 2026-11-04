package state

// The durable unseen bit. Its polarity is inverted on purpose — zero means
// *seen* — so the interesting cases are the ones where nothing was written: a
// pre-field payload, a schema-v1 payload, and an explicitly seen window all have
// to restore quietly rather than re-raise a stop the user already read.

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// unseenReducer builds a reducer holding one resting harness fact, which is what
// a real unseen window looks like on disk.
func unseenReducer() *Reducer {
	r := NewReducer(ReducerConfig{})
	r.Apply(protocol.ReducerEvent{
		Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"},
		Action: "stop",
		Seq:    1,
		At:     1710000100,
	})
	return r
}

func TestUnseenRoundTripsThroughStatePayload(t *testing.T) {
	encoded, err := WindowState{Reducer: unseenReducer(), Unseen: true}.MarshalStatePayload()
	if err != nil {
		t.Fatalf("MarshalStatePayload() error: %v", err)
	}
	if !strings.Contains(encoded, `"us":true`) {
		t.Fatalf("MarshalStatePayload() missing the unseen bit: %s", encoded)
	}

	var got WindowState
	if err := got.UnmarshalStatePayload(encoded); err != nil {
		t.Fatalf("UnmarshalStatePayload() error: %v", err)
	}
	if !got.Unseen {
		t.Fatalf("Unseen = false after round trip, want true")
	}
}

// TestSeenIsOmittedFromStatePayload pins the omitempty half of the inversion: a seen
// window writes no key at all, which is what makes a pre-field payload and an
// explicitly-seen payload indistinguishable — and therefore both safe.
func TestSeenIsOmittedFromStatePayload(t *testing.T) {
	encoded, err := WindowState{Reducer: unseenReducer()}.MarshalStatePayload()
	if err != nil {
		t.Fatalf("MarshalStatePayload() error: %v", err)
	}
	if strings.Contains(encoded, `"us"`) {
		t.Fatalf("MarshalStatePayload() wrote an unseen key for a seen window: %s", encoded)
	}
}

// TestStatePayloadWithoutUnseenKeyRestoresAsSeen is the restart guarantee. A schema-v4
// payload written before the field existed carries no `us` key, and it must
// restore as seen — a daemon restart that demoted every row would be a nuisance,
// one that raised every already-read stop into Needs Attention would make the
// group useless.
func TestStatePayloadWithoutUnseenKeyRestoresAsSeen(t *testing.T) {
	raw := `{"v":4,"a":"stop","f":[{"k":"harness","y":"claude:s1","a":"stop","i":"stop","q":1,"t":1710000100,"tm":true,"ta":1710000100}],"ei":"stop","sq":1}`
	var got WindowState
	if err := got.UnmarshalStatePayload(raw); err != nil {
		t.Fatalf("UnmarshalStatePayload() error: %v", err)
	}
	if got.Unseen {
		t.Fatalf("Unseen = true for a payload with no us key, want false (seen)")
	}
}

// TestSchemaV1PayloadRestoresAsSeen covers the writer that predates the field
// entirely: v1 has no discriminator, migrates through migrateReducerV1, and never
// reaches the v2+ restore path where the bit is read.
func TestSchemaV1PayloadRestoresAsSeen(t *testing.T) {
	for _, raw := range []string{
		`{"a":"stop","t":1710000100}`,
		// Even a forged bit on a v1 payload fails closed: v1 predates the field,
		// so the value is not evidence of anything.
		`{"a":"stop","t":1710000100,"us":true}`,
	} {
		var got WindowState
		if err := got.UnmarshalStatePayload(raw); err != nil {
			t.Fatalf("UnmarshalStatePayload(%s) error: %v", raw, err)
		}
		if got.Unseen {
			t.Fatalf("Unseen = true for v1 payload %s, want false (seen)", raw)
		}
	}
}

// TestEmptyStatePayloadRestoresAsSeen covers the remaining writer with nothing to say:
// a blank amx_state resets to the zero WindowState, which must mean seen.
func TestEmptyStatePayloadRestoresAsSeen(t *testing.T) {
	got := WindowState{Unseen: true}
	if err := got.UnmarshalStatePayload("   "); err != nil {
		t.Fatalf("UnmarshalStatePayload() error: %v", err)
	}
	if got.Unseen {
		t.Fatalf("Unseen = true after restoring an empty payload, want false (seen)")
	}
}
