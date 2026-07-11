package conversation

import "testing"

func TestIdentityNewAndEqual(t *testing.T) {
	id, ok := New(" CODEX ", "thread-1")
	if !ok || id != (Identity{Harness: "codex", SessionID: "thread-1"}) {
		t.Fatalf("New() = %+v, %v", id, ok)
	}
	if !id.Valid() {
		t.Fatal("canonical identity should be valid")
	}
	if !id.Equal(Identity{Harness: "Codex", SessionID: "thread-1"}) {
		t.Fatal("canonical harness variants should compare equal")
	}
}

func TestIdentityRejectsMissingAuthoritativeParts(t *testing.T) {
	for _, tc := range []Identity{
		{},
		{Harness: "claude"},
		{SessionID: "session-1"},
	} {
		if tc.Valid() {
			t.Fatalf("%+v should be invalid", tc)
		}
	}
}
