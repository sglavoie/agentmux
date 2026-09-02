package activity

import (
	"hash/fnv"
	"strings"
	"time"
)

// Hook silence: the account-level counterpart to the per-window stale sweep. A
// running harness that has stopped delivering hooks entirely produces no
// per-window evidence to expire, so the daemon accumulates idleness against the
// account's last seen event instead.

// HookSilenceThresholds resolves the silence duration for an account. A
// positive per-account override wins over the global default; a zero value at
// both levels means the sweep is disabled for that account.
//
// The zero HookSilenceThresholds is usable and disables the sweep.
type HookSilenceThresholds struct {
	global     time.Duration
	perAccount map[string]time.Duration
}

// NewHookSilenceThresholds copies the configured thresholds. The global value
// applies whenever a per-account override is missing or zero; non-positive
// per-account values are dropped rather than stored, so they can never shadow a
// positive global. Per-account keys are lowercased here, so lookups are
// case-insensitive.
func NewHookSilenceThresholds(global time.Duration, perAccount map[string]time.Duration) HookSilenceThresholds {
	pa := make(map[string]time.Duration, len(perAccount))
	for account, d := range perAccount {
		if d <= 0 {
			continue
		}
		pa[strings.ToLower(account)] = d
	}
	return HookSilenceThresholds{global: global, perAccount: pa}
}

// Resolve returns the hook-silence duration to use for the given account.
func (t HookSilenceThresholds) Resolve(account string) time.Duration {
	if account != "" {
		if d, ok := t.perAccount[strings.ToLower(account)]; ok && d > 0 {
			return d
		}
	}
	return t.global
}

// HookSilenceWindowID maps an account name to a stable synthetic window ID used
// as the notifier dedup key for hook-silence dispatches. Real Kitty window IDs
// are positive ints; negating the FNV-32 hash places the synthetic IDs in a
// non-overlapping negative range. The -1 offset prevents collision with the
// zero value (uint32 hashes are 32-bit, so a 64-bit int can hold the full
// negation without overflow).
func HookSilenceWindowID(account string) int {
	hh := fnv.New32a()
	_, _ = hh.Write([]byte(strings.ToLower(account)))
	return -int(hh.Sum32()) - 1
}

// AccountIdle is the hook-silence sweep's per-account idle accumulation entry,
// anchored to the account's last seen event time. Anchor is what makes the
// accumulation restartable: a fresh event moves the anchor, and an entry whose
// anchor no longer matches is rebuilt rather than extended.
type AccountIdle struct {
	Anchor time.Time
	Idle   time.Duration
}
