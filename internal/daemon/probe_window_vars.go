package daemon

import "github.com/sglavoie/agentmux/internal/harness"

// probeWindowVars is the small presentation context shared by screen probes.
// Runtime sessions supply it from reducer state.
type probeWindowVars struct {
	Profile string
	Harness string
	Account string
	Mute    string
}

func (v probeWindowVars) EffectiveProfile() string {
	if v.Profile != "" {
		return v.Profile
	}
	return v.Account
}

func (v probeWindowVars) EffectiveHarness() string {
	if v.Harness != "" {
		return harness.Normalize(v.Harness)
	}
	if v.Account != "" {
		return harness.Claude
	}
	return ""
}
