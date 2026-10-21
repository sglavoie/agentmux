package session

import (
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

// TestTitleFallbackBases_DeterministicOrderWithOverlappingEmojis pins that
// overlapping harness emoji values (one a byte-prefix of another) always
// yield the same candidate order: longest emoji stripped first, ties broken
// lexicographically. Before the fix the candidates followed Go map iteration
// order, so which configured session a ghost tab resolved to could change
// from one run to the next. The loop hammers the call because a single
// invocation could pass by luck under randomized map iteration.
func TestTitleFallbackBases_DeterministicOrderWithOverlappingEmojis(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		HarnessEmojis: map[string]string{
			"claude":  "⚡",  // 3 bytes, byte-prefix of the codex emoji
			"codex":   "⚡️", // 6 bytes: bolt + variation selector
			"copilot": "🦀",  // 4 bytes, never matches the base below
		},
	}
	base := "⚡️api"
	// Stripping the 6-byte "⚡️" first yields "api"; stripping the
	// 3-byte "⚡" afterwards leaves the bare variation selector behind.
	want := []string{base, "api", "\ufe0fapi"}
	for i := 0; i < 100; i++ {
		got := titleFallbackBases(cfg, base)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("iteration %d: titleFallbackBases returned %q, want %q", i, got, want)
		}
	}
}
