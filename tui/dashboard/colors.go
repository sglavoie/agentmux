package dashboard

import (
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
)

// ProfileColorMap maps profile name → palette color name resolved from the
// profile's required Color field. Used for elements that should track the
// profile (project title, [X] account badge, account chips, detail/preview
// header).
func ProfileColorMap(cfg *config.Config) map[string]string {
	if cfg == nil {
		return map[string]string{}
	}
	result := make(map[string]string, len(cfg.Accounts))
	for _, acc := range cfg.Accounts {
		result[acc.Name] = acc.Color
	}
	return result
}

// HarnessColorMap maps harness name → palette color name from the
// [harness_colors] config table. It is keyed by harness rather than by profile
// so the color stays stable across profile overrides.
//
// The queue row is no longer one of its readers — a session row carries no
// harness cell to color, and the selected row's harness is named on the
// inspector's Agent line, which is uncolored alongside the rest of the metadata
// block. What is left is the launcher, which still tells harnesses apart by
// color while choosing one.
func HarnessColorMap(cfg *config.Config) map[string]string {
	if cfg == nil {
		return map[string]string{}
	}
	result := make(map[string]string, len(cfg.HarnessColors))
	for name, color := range cfg.HarnessColors {
		result[name] = color
	}
	return result
}

// ProjectIdentityColor tracks the project's configured default profile so
// override-launched rows keep the project's visual identity on the title and
// badge. Falls back to the running account color when the project is not
// configured (orphan tab).
//
// A color the operator picked for the session itself comes first: it is the
// one thing here that was chosen for this row and not derived for it.
func ProjectIdentityColor(s session.RunningSession, acctColor map[string]string, cfg *config.Config) string {
	if s.Color != "" {
		if _, err := palette.Lookup(s.Color); err == nil {
			return s.Color
		}
	}
	colorName := acctColor[s.EffectiveProfile()]
	if cfg == nil {
		return colorName
	}
	if def := cfg.DefaultProfileFor(s.Project); def != "" {
		if c := acctColor[def]; c != "" {
			return c
		}
	}
	return colorName
}
