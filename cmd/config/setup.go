package config

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/sglavoie/agentmux/internal/atomicfile"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/spf13/cobra"
)

// setup adds one launchable project to a new or profile-only configuration.
// It deliberately leaves the user's optional settings and comments intact.
var configSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Set up the first agent profile and project",
	Args:  cobra.NoArgs,
	Long: `Guide the first launch by adding a profile (if needed) and one project.

Existing comments and optional settings are preserved. Setup refuses a config
that already has projects; use amx config edit to add more. The project directory
must exist. After setup, sync the harness hooks and check the backend before
launching from the dashboard. Keep leaves the launch destination unset: CLI
launch commands retain their Kitty default, while the dashboard chooses Kitty
inside Kitty and tmux in other terminals.`,
	RunE: runConfigSetup,
}

func runConfigSetup(cmd *cobra.Command, _ []string) error {
	path := config.DefaultConfigPath()
	previous, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		previous = []byte(config.ExampleConfig)
	} else if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}
	original := bytes.Clone(previous)
	var current config.Config
	if _, err := toml.Decode(string(previous), &current); err != nil {
		return fmt.Errorf("existing config is invalid: %w", err)
	}
	if err := config.Validate(&current); err != nil {
		return fmt.Errorf("existing config is invalid: %w", err)
	}
	if len(current.Projects) > 0 || len(current.Sessions) > 0 {
		return fmt.Errorf("projects already exist; use `amx config edit` to add another")
	}

	reader := bufio.NewReader(cmd.InOrStdin())
	out := cmd.OutOrStdout()
	var addition strings.Builder
	profile := ""
	switch len(current.Profiles) {
	case 0:
		chosen, err := setupChoice(reader, out, "Agent CLI (claude, copilot, codex)", "claude", harness.Names()...)
		if err != nil {
			return err
		}
		profile, err = setupPrompt(reader, out, "Profile name", chosen+"-local")
		if err != nil {
			return err
		}
		home, err := setupPrompt(reader, out, "Profile home", "~/."+chosen)
		if err != nil {
			return err
		}
		color := map[string]string{"claude": "yellow", "copilot": "green", "codex": "purple"}[chosen]
		if _, ok := current.HarnessEmojis[chosen]; !ok {
			emoji := map[string]string{"claude": "🦊", "copilot": "🚁", "codex": "📟"}[chosen]
			previous = setupEnsureTableEntry(previous, "harness_emojis", chosen, emoji)
		}
		if len(current.HarnessColors) > 0 {
			if _, ok := current.HarnessColors[chosen]; !ok {
				previous = setupEnsureTableEntry(previous, "harness_colors", chosen, color)
			}
		}
		addition.WriteString("\n# First profile and project added by amx config setup.\n[[profiles]]\n")
		setupField(&addition, "name", profile)
		setupField(&addition, "harness", chosen)
		setupField(&addition, "home_dir", home)
		setupField(&addition, "color", color)
	case 1:
		profile = current.Profiles[0].Name
		if _, err := fmt.Fprintf(out, "Using existing profile %s\n", profile); err != nil {
			return err
		}
	default:
		for {
			chosen, err := setupPrompt(reader, out, "Existing profile name", current.Profiles[0].Name)
			if err != nil {
				return err
			}
			for _, candidate := range current.Profiles {
				if strings.EqualFold(candidate.Name, chosen) {
					profile = candidate.Name
					break
				}
			}
			if profile != "" {
				break
			}
			if _, err := fmt.Fprintf(out, "Unknown profile %q. Choose %s.\n", chosen, setupProfileNames(current.Profiles)); err != nil {
				return err
			}
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("finding current directory: %w", err)
	}
	var directory string
	for {
		entered, err := setupPrompt(reader, out, "Project directory", cwd)
		if err != nil {
			return err
		}
		directory, err = filepath.Abs(config.ExpandTilde(entered))
		if err == nil {
			var info os.FileInfo
			info, err = os.Stat(directory)
			if err == nil && info.IsDir() {
				break
			}
			if err == nil {
				err = fmt.Errorf("not a directory")
			}
		}
		if _, writeErr := fmt.Fprintf(out, "Project directory %q: %v. Enter an existing directory.\n", entered, err); writeErr != nil {
			return writeErr
		}
	}
	title, err := setupPrompt(reader, out, "Project title", filepath.Base(directory))
	if err != nil {
		return err
	}
	if current.Defaults.LaunchBackend == "" {
		backend, err := setupChoice(reader, out, "Default backend (keep, tmux, or kitty)", "keep", "keep", config.LaunchBackendTmux, config.LaunchBackendKitty)
		if err != nil {
			return err
		}
		if backend != "keep" {
			previous = setupEnsureTableEntry(previous, "defaults", "launch_backend", backend)
		}
	}
	addition.WriteString("\n[[projects]]\n")
	setupField(&addition, "title", title)
	setupField(&addition, "directory", directory)
	setupField(&addition, "profile", profile)

	updated := append(bytes.TrimRight(previous, "\n"), '\n')
	updated = append(updated, addition.String()...)
	var candidate config.Config
	if _, err := toml.Decode(string(updated), &candidate); err != nil {
		return fmt.Errorf("setup would create invalid TOML: %w", err)
	}
	if err := config.Validate(&candidate); err != nil {
		return fmt.Errorf("setup would create invalid config: %w", err)
	}
	if before, readErr := os.ReadFile(path); readErr == nil && !bytes.Equal(before, original) {
		return fmt.Errorf("config changed during setup; rerun `amx config setup` against the current file")
	} else if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("rechecking config: %w", readErr)
	}
	if err := atomicfile.Replace(path, updated, 0o644); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	_, err = fmt.Fprintf(out, "Configured %s with profile %s in %s\nNext: amx config hooks-sync; amx backend status; amx\nIf launching on tmux, run amx backend status --check first.\n", title, profile, path)
	return err
}

func setupPrompt(reader *bufio.Reader, out io.Writer, label, fallback string) (string, error) {
	if _, err := fmt.Fprintf(out, "%s [%s]: ", label, fallback); err != nil {
		return "", err
	}
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	value := strings.TrimSpace(line)
	if value == "" && errors.Is(err, io.EOF) {
		return "", fmt.Errorf("%s is required; run setup in a terminal or provide an answer", label)
	}
	if value == "" {
		value = fallback
	}
	if strings.ContainsAny(value, "\x00\n\r") {
		return "", fmt.Errorf("%s contains a control character", label)
	}
	return value, nil
}

func setupProfileNames(profiles []config.Account) string {
	names := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		names = append(names, profile.Name)
	}
	return strings.Join(names, ", ")
}

// setupChoice gives a first-time operator room to correct a mistyped choice
// without repeating the whole setup. EOF still stops a scripted or closed
// input instead of silently choosing a default.
func setupChoice(reader *bufio.Reader, out io.Writer, label, fallback string, choices ...string) (string, error) {
	for {
		value, err := setupPrompt(reader, out, label, fallback)
		if err != nil {
			return "", err
		}
		value = strings.ToLower(value)
		if slices.Contains(choices, value) {
			return value, nil
		}
		if _, err := fmt.Fprintf(out, "Choose %s.\n", strings.Join(choices, ", ")); err != nil {
			return "", err
		}
	}
}

func setupField(out *strings.Builder, key, value string) {
	out.WriteString(key)
	out.WriteString(" = ")
	out.WriteString(strconv.Quote(value))
	out.WriteByte('\n')
}

// setupEnsureTableEntry writes one key into its existing table, or appends a
// new table. An existing empty launch_backend value is replaced in place;
// surrounding comments and optional settings are left intact.
func setupEnsureTableEntry(data []byte, table, key, value string) []byte {
	entry := []byte(key + " = " + strconv.Quote(value) + "\n")
	lines := bytes.SplitAfter(data, []byte("\n"))
	header := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(string(line))
		if header < 0 {
			if setupTableHeader(trimmed, table) {
				header = i
			}
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			break
		}
		before, _, found := strings.Cut(trimmed, "=")
		if found && strings.TrimSpace(before) == key {
			lines[i] = setupReplaceTableEntry(line, value)
			return bytes.Join(lines, nil)
		}
	}
	if header >= 0 {
		var updated []byte
		for j, part := range lines {
			updated = append(updated, part...)
			if header == j {
				if len(part) == 0 || part[len(part)-1] != '\n' {
					updated = append(updated, '\n')
				}
				updated = append(updated, entry...)
			}
		}
		return updated
	}
	updated := append(bytes.TrimRight(data, "\n"), '\n', '\n')
	updated = append(updated, []byte("["+table+"]\n")...)
	return append(updated, entry...)
}

func setupTableHeader(trimmed, table string) bool {
	prefix := "[" + table + "]"
	if !strings.HasPrefix(trimmed, prefix) {
		return false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
	return rest == "" || strings.HasPrefix(rest, "#")
}

func setupReplaceTableEntry(line []byte, value string) []byte {
	body := strings.TrimSuffix(string(line), "\n")
	newline := "\n"
	if strings.HasSuffix(body, "\r") {
		body = strings.TrimSuffix(body, "\r")
		newline = "\r\n"
	}
	equals := strings.IndexByte(body, '=')
	before, after := body[:equals+1], body[equals+1:]
	leading := after[:len(after)-len(strings.TrimLeft(after, " \t"))]
	comment := ""
	if at := strings.IndexByte(after, '#'); at >= 0 {
		comment = after[len(strings.TrimRight(after[:at], " \t")):]
	}
	return []byte(before + leading + strconv.Quote(value) + comment + newline)
}
