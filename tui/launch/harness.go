package launch

import "github.com/sglavoie/agentmux/internal/config"

// harnessCellText renders the launcher's harness cell text for a single
// account: "<emoji> <profile>/<harness>", or just "<profile>/<harness>" when
// no emoji is configured for the harness, so unconfigured users do not see a
// stray leading space.
func harnessCellText(cfg *config.Config, acc config.Account) string {
	label := acc.Name + "/" + acc.EffectiveHarness()
	emoji := ""
	if cfg != nil {
		emoji = cfg.HarnessEmoji(acc.EffectiveHarness())
	}
	if emoji == "" {
		return label
	}
	return emoji + " " + label
}

// harnessDisplayText renders a harness name with its configured emoji prefix,
// falling back to the bare harness name when no emoji is configured.
func harnessDisplayText(cfg *config.Config, harness string) string {
	emoji := ""
	if cfg != nil {
		emoji = cfg.HarnessEmoji(harness)
	}
	if emoji == "" {
		return harness
	}
	return emoji + " " + harness
}
