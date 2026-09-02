package daemon

import (
	"log/slog"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
)

// SweepHookSilence detects accounts whose most recent event predates the
// configured hook-silence threshold and fires a one-shot notification per
// silence stretch. Unlike SweepStale, which is per-window, this sweep is
// account-level: it iterates running Kitty windows (cc_account user var) so
// notifications are not produced for accounts the user has not launched.
//
// Behavior:
//   - No-op when h.kittyClient is nil (test environments without Kitty).
//   - No-op when ListWindows fails — logged at debug and swallowed so other
//     ticks can retry.
//   - The "hook_silence" action is daemon-internal: not in
//     protocol.ActionPriority or protocol.IconState; only the notifier
//     recognizes it as an action name.
//   - The notifier's per-(windowID, action) cooldown is keyed by a synthetic
//     negative ID derived from the account name so it cannot collide with
//     real positive Kitty window IDs (see hookSilenceWindowID).
func (h *Handler) SweepHookSilence(now time.Time) {
	if h.kittyClient == nil {
		return
	}
	windows, err := h.kittyClient.ListWindows()
	if err != nil {
		slog.Debug("hook-silence sweep: list windows failed", "err", err)
		return
	}
	h.SweepHookSilenceWithSnapshot(now, windows)
}

func (h *Handler) SweepHookSilenceWithSnapshot(now time.Time, windows []kitty.OSWindow) {
	h.sweepHookSilence(now, windows, 0, false)
}

// sweepHookSilence evaluates account-level hook silence on the same monotonic
// accumulation the stale sweep uses: an account's silence grows only by
// in-process observed elapsed time (seeded from wall-clock age on first
// observation), so a system sleep cannot fire a false "hooks silent"
// notification the moment the daemon wakes.
func (h *Handler) sweepHookSilence(now time.Time, windows []kitty.OSWindow, credit time.Duration, gapTick bool) {
	running := make(map[string]bool)
	for _, ow := range windows {
		for _, tab := range ow.Tabs {
			for _, w := range tab.Windows {
				if acc := w.UserVars.Account; acc != "" {
					running[acc] = true
				}
			}
		}
	}
	if len(running) == 0 {
		return
	}

	type silentEntry struct {
		account string
	}
	var silent []silentEntry

	h.stateMu.Lock()
	for account := range running {
		threshold := h.resolveHookSilenceThreshold(account)
		if threshold <= 0 {
			continue
		}
		accountKey := strings.ToLower(account)
		last, ok := h.lastSeenEventAt[accountKey]
		if !ok {
			// No event has ever been seen for this account during the
			// daemon's lifetime — skip. The first event from this account
			// will seed lastSeenEventAt and arm the detector.
			continue
		}
		entry := h.hookSilenceIdle[accountKey]
		if entry == nil || !entry.anchor.Equal(last) {
			entry = &accountIdle{anchor: last}
			if !gapTick {
				if age := now.Sub(last); age > 0 {
					entry.idle = age
				}
			}
			h.hookSilenceIdle[accountKey] = entry
		} else {
			entry.idle += credit
		}
		if entry.idle < threshold {
			continue
		}
		if h.notifiedHookSilence[accountKey] {
			continue
		}
		h.notifiedHookSilence[accountKey] = true
		silent = append(silent, silentEntry{account: account})
	}
	h.stateMu.Unlock()

	for _, s := range silent {
		if h.notifier != nil {
			h.notifier.MaybeNotifyTarget(NotificationTarget{
				WindowID: hookSilenceWindowID(s.account), Account: s.account,
			}, s.account, "hook_silence", false)
		}
	}
}
