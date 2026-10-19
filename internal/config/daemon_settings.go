package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

// DaemonSettings fingerprints only configuration consumed at daemon startup.
// Projects, launch arguments, editor and reader preferences are deliberately
// excluded. Health exposes these digests, never profile paths or setting values.
// The version prefix makes an incompatible projection explicitly unverifiable.
func (c *Config) DaemonSettings() map[string]string {
	d := c.Defaults
	n := d.Notifications
	elicitation := n.ElicitationEnabled()
	n.Elicitation = &elicitation
	n.QuietHours = strings.TrimSpace(n.QuietHours)
	n.AlerterPath = ExpandTilde(n.AlerterPath)
	colors := map[string]string{}
	watchdogs := map[string][4]int{}
	homes := []string{}
	for _, p := range c.Profiles {
		colors[p.Name] = p.Color
		w := p.Watchdog
		values := [4]int{w.Active, w.Subagent, w.Compact, w.HookSilence}
		defaults := [4]int{d.Watchdog.Active, d.Watchdog.Subagent, d.Watchdog.Compact, d.Watchdog.HookSilence}
		for i := range values {
			if values[i] == 0 {
				values[i] = defaults[i]
			}
		}
		if values != defaults {
			watchdogs[p.Name] = values
		}
		if p.Harness == "copilot" {
			homes = append(homes, ExpandTilde(p.HomeDir))
		}
	}
	sort.Strings(homes)
	ttl, timeout := d.GitProbeTTL, d.GitProbeTimeout
	if ttl == 0 {
		ttl = 10
	}
	if timeout == 0 {
		timeout = 2
	}
	groups := map[string]any{
		"Notifications":        n,
		"Activity detection":   []any{d.GuardDuration, d.Watchdog, watchdogs, d.StopConfirm, homes},
		"Git status":           []any{d.GitProbeValue(), ttl, timeout},
		"Session presentation": []any{d.Sort, colors, c.HarnessIcons()},
		"ASL integration":      []any{d.ASL.ASLModeValue(), d.ASL.Command, ExpandTilde(d.ASL.StateRoot)},
		"Daemon journal":       []any{d.Diagnostics.JournalEnabledValue(), d.Diagnostics.JournalKeepDays, d.Diagnostics.JournalMaxMiB, d.Diagnostics.JournalDetailFullValue()},
		"tmux executable":      c.TmuxPath(),
	}
	out := make(map[string]string, len(groups))
	for name, value := range groups {
		data, _ := json.Marshal(value) // fixed, JSON-safe projection above
		sum := sha256.Sum256(data)
		out[name] = "v1:" + hex.EncodeToString(sum[:])
	}
	return out
}

// ChangedDaemonSettings refuses an absent or incompatible startup projection.
// That is unknown, never evidence that the file has already been applied.
func ChangedDaemonSettings(running, desired map[string]string) ([]string, bool) {
	if len(running) == 0 || len(running) != len(desired) {
		return nil, false
	}
	changed := []string{}
	for name, want := range desired {
		got, ok := running[name]
		if !ok || !strings.HasPrefix(got, "v1:") || !strings.HasPrefix(want, "v1:") {
			return nil, false
		}
		if got != want {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	return changed, true
}
