package config

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/spf13/cobra"
)

type setupInput struct {
	cmd            *cobra.Command
	reader         *bufio.Reader
	out            io.Writer
	noninteractive bool
}

func newSetupInput(cmd *cobra.Command) *setupInput {
	i := &setupInput{cmd: cmd, reader: bufio.NewReader(cmd.InOrStdin()), out: cmd.OutOrStdout()}
	i.noninteractive = i.has("harness") && i.has("directory")
	return i
}

func (i *setupInput) has(flag string) bool { return i.cmd.Flags().Changed(flag) }

func (i *setupInput) value(flag string) string {
	value, _ := i.cmd.Flags().GetString(flag)
	return strings.TrimSpace(value)
}

func (i *setupInput) validate() error {
	for _, flag := range []string{"harness", "directory", "home", "title", "profile-name", "backend"} {
		if !i.has(flag) {
			continue
		}
		value, _ := i.cmd.Flags().GetString(flag)
		if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\x00\n\r") {
			return fmt.Errorf("--%s must be nonempty and contain no control characters", flag)
		}
	}
	if i.has("harness") && harness.NormalizeKnown(i.value("harness")) == "" {
		return fmt.Errorf("--harness must be claude, copilot or codex")
	}
	if i.has("backend") && !slices.Contains([]string{"keep", "tmux"}, strings.ToLower(i.value("backend"))) {
		return fmt.Errorf("--backend must be keep or tmux")
	}
	return nil
}

func (i *setupInput) answer(flag, label, fallback string, choices ...string) (string, error) {
	if i.has(flag) || i.noninteractive {
		value := fallback
		if i.has(flag) {
			value = i.value(flag)
		}
		if len(choices) > 0 {
			value = strings.ToLower(value)
			if !slices.Contains(choices, value) {
				return "", fmt.Errorf("%s: choose %s", label, strings.Join(choices, ", "))
			}
		}
		return value, nil
	}
	if len(choices) > 0 {
		return setupChoice(i.reader, i.out, label, fallback, choices...)
	}
	return setupPrompt(i.reader, i.out, label, fallback)
}

func (i *setupInput) hookAnswer(profile string) (string, error) {
	if i.has("sync-hooks") || i.noninteractive {
		sync, _ := i.cmd.Flags().GetBool("sync-hooks")
		if sync {
			return "yes", nil
		}
		return "no", nil
	}
	return setupChoice(i.reader, i.out, "Sync Agent Mux hooks for "+profile+" now? (yes/no)", "no", "yes", "no")
}

func (i *setupInput) existingProfile(profiles []config.Account) (string, error) {
	var selected *config.Account
	if i.has("profile-name") {
		for n := range profiles {
			if strings.EqualFold(profiles[n].Name, i.value("profile-name")) {
				selected = &profiles[n]
				break
			}
		}
		if selected == nil {
			return "", fmt.Errorf("unknown profile %q; choose %s", i.value("profile-name"), setupProfileNames(profiles))
		}
	} else if i.noninteractive {
		for n := range profiles {
			if profiles[n].EffectiveHarness() == harness.NormalizeKnown(i.value("harness")) {
				if selected != nil {
					return "", fmt.Errorf("several profiles match --harness; choose --profile-name from %s", setupProfileNames(profiles))
				}
				selected = &profiles[n]
			}
		}
		if selected == nil {
			return "", fmt.Errorf("no existing profile matches --harness; use amx config edit to add one")
		}
	} else if len(profiles) == 1 {
		selected = &profiles[0]
	} else {
		for selected == nil {
			chosen, err := setupPrompt(i.reader, i.out, "Existing profile name", profiles[0].Name)
			if err != nil {
				return "", err
			}
			for n := range profiles {
				if strings.EqualFold(profiles[n].Name, chosen) {
					selected = &profiles[n]
					break
				}
			}
			if selected == nil {
				if _, err := fmt.Fprintf(i.out, "Unknown profile %q. Choose %s.\n", chosen, setupProfileNames(profiles)); err != nil {
					return "", err
				}
			}
		}
	}
	if i.has("harness") && selected.EffectiveHarness() != harness.NormalizeKnown(i.value("harness")) {
		return "", fmt.Errorf("profile %s does not match --harness", selected.Name)
	}
	if i.has("home") {
		requested, err := filepath.Abs(config.ExpandTilde(i.value("home")))
		if err != nil {
			return "", err
		}
		existing, err := filepath.Abs(config.ExpandTilde(selected.EffectiveHomeDir()))
		if err != nil {
			return "", err
		}
		if requested != existing {
			return "", fmt.Errorf("profile %s does not match --home; setup preserves existing profiles", selected.Name)
		}
	}
	_, err := fmt.Fprintf(i.out, "Using existing profile %s\n", selected.Name)
	return selected.Name, err
}
