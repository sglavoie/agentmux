package daemon

import (
	"reflect"
	"sort"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestDeduplicatedLifecycleIdentitiesStableOrder(t *testing.T) {
	identities := []protocol.HarnessProcessIdentity{
		{Harness: "codex", PID: 3, StartToken: "c"},
		{Harness: "claude", PID: 2, StartToken: "b"},
		{Harness: "claude", PID: 1, StartToken: "a"},
	}
	candidates := make([]processLifecycleCandidate, 0, 4)
	for _, identity := range append(identities, identities[0]) {
		candidates = append(candidates, processLifecycleCandidate{identity: identity})
	}
	got := deduplicatedLifecycleIdentities(candidates)
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].Harness != identities[j].Harness {
			return identities[i].Harness < identities[j].Harness
		}
		return identities[i].PID < identities[j].PID
	})
	if !reflect.DeepEqual(got, identities) {
		t.Fatalf("identities = %+v, want %+v", got, identities)
	}
}
