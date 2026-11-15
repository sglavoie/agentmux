package config

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/sglavoie/agentmux/cmd/hooks"
	"github.com/sglavoie/agentmux/internal/atomicfile"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/shellquote"
	"github.com/sglavoie/agentmux/internal/tmux"
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
must exist. Setup offers to sync this profile's hooks, checks launch requirements,
and summarizes effective destinations. Keep leaves the launch destination unset:
CLI launch commands and terminal dashboards use the private tmux server.

--detect reports CLI availability, default homes and up to eight recent existing
project directories without writing anything. With --harness and --directory,
setup runs without prompts. Other flags override wizard answers; hooks are only
synced when explicitly requested. --json emits the schema-v1 result on stdout
(prompts go to stderr).`,
	RunE: runConfigSetup,
}

// SetupCommand exposes the existing guided setup to the embedded workspace view.
func SetupCommand() *cobra.Command { return configSetupCmd }

var setupLookPath = exec.LookPath
var setupBackendStatus = func(cfg *config.Config) cli.BackendStatus {
	cli.ConfigureTmux(cfg)
	return cli.CollectBackendStatus(cfg, nil, cli.BackendProbe{})
}
var setupSyncHooks = func(cmd *cobra.Command, profile string) error {
	return hooks.SyncCommand().RunE(cmd, []string{profile})
}

func init() {
	addSetupFlags(configSetupCmd)
}

func addSetupFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("detect", false, "report installed harnesses and recent projects without changes")
	cmd.Flags().String("harness", "", "agent CLI: claude, copilot, codex")
	cmd.Flags().String("home", "", "profile home (defaults to the harness home)")
	cmd.Flags().String("directory", "", "existing project directory")
	cmd.Flags().String("title", "", "project title (defaults to directory name)")
	cmd.Flags().String("profile-name", "", "new profile name or existing profile to use")
	cmd.Flags().String("backend", "keep", "default backend: keep or tmux")
	cmd.Flags().Bool("sync-hooks", false, "sync the selected profile's hooks after saving")
	cli.AddJSONFlag(cmd, nil)
}

type setupResult struct {
	ConfigPath  string `json:"config_path"`
	Saved       bool   `json:"saved"`
	Profile     string `json:"profile"`
	Harness     string `json:"harness"`
	Home        string `json:"home"`
	Directory   string `json:"directory"`
	Title       string `json:"title"`
	Backend     string `json:"backend"`
	HooksStatus string `json:"hooks_status"`
	HooksError  string `json:"hooks_error,omitempty"`
	Summary     string `json:"summary"`
}

func runConfigSetup(cmd *cobra.Command, _ []string) error {
	jsonOutput, _ := cmd.Flags().GetBool("json")
	detect, _ := cmd.Flags().GetBool("detect")
	if detect {
		result := detectSetup()
		if jsonOutput {
			return cli.WriteJSON(cmd, result)
		}
		return result.writeText(cmd.OutOrStdout())
	}
	input := newSetupInput(cmd)
	if jsonOutput {
		input.out = cmd.ErrOrStderr()
	}
	result := setupResult{ConfigPath: config.DefaultConfigPath(), HooksStatus: "not_synced"}
	err := applyConfigSetup(cmd, input, &result)
	if jsonOutput {
		if err != nil {
			if result.Saved {
				return cli.WriteJSONPartial(cmd, result, err)
			}
			return cli.CommandFailure(cmd, true, "setup_failed", err)
		}
		return cli.WriteJSON(cmd, result)
	}
	return err
}

func applyConfigSetup(cmd *cobra.Command, input *setupInput, result *setupResult) error {
	path := result.ConfigPath
	previous, err := os.ReadFile(path)
	originalExists := err == nil
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

	out := input.out
	if err := input.validate(); err != nil {
		return err
	}
	detection := setupDetection{}
	if !input.noninteractive {
		detection = detectSetup()
	}
	var addition strings.Builder
	profile := ""
	switch len(current.Profiles) {
	case 0:
		chosen, err := input.answer("harness", "Agent CLI (claude, copilot, codex)", detection.defaultHarness(), harness.Names()...)
		if err != nil {
			return err
		}
		profile, err = input.answer("profile-name", "Profile name", chosen+"-local")
		if err != nil {
			return err
		}
		home, err := input.answer("home", "Profile home", "~/."+chosen)
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
	default:
		var err error
		profile, err = input.existingProfile(current.Profiles)
		if err != nil {
			return err
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("finding current directory: %w", err)
	}
	if !input.noninteractive && !input.has("directory") {
		for i, project := range detection.RecentProjects {
			if _, err := fmt.Fprintf(out, "  %d. %s (%s)\n", i+1, project.Directory, project.Harness); err != nil {
				return err
			}
		}
		if len(detection.RecentProjects) > 0 {
			if _, err := fmt.Fprintln(out, "Choose a recent project number or enter a path."); err != nil {
				return err
			}
		}
	}
	var directory string
	for {
		entered, err := input.answer("directory", "Project directory", cwd)
		if err != nil {
			return err
		}
		if !input.noninteractive && !input.has("directory") {
			if number, parseErr := strconv.Atoi(entered); parseErr == nil && number > 0 && number <= len(detection.RecentProjects) {
				entered = detection.RecentProjects[number-1].Directory
			}
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
		if input.noninteractive || input.has("directory") {
			return fmt.Errorf("project directory %q: %w", entered, err)
		}
		if _, writeErr := fmt.Fprintf(out, "Project directory %q: %v. Enter an existing directory.\n", entered, err); writeErr != nil {
			return writeErr
		}
	}
	title, err := input.answer("title", "Project title", filepath.Base(directory))
	if err != nil {
		return err
	}
	if strings.TrimSpace(current.Defaults.LaunchBackend) == "" || input.has("backend") {
		if _, err := fmt.Fprintln(out, "Keep uses the tmux default in every terminal."); err != nil {
			return err
		}
		backend, err := input.answer("backend", "Default backend (keep or tmux)", "keep", "keep", config.LaunchBackendTmux)
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
	syncChoice, err := input.hookAnswer(profile)
	if err != nil {
		return err
	}
	if before, readErr := os.ReadFile(path); (readErr == nil && (!originalExists || !bytes.Equal(before, original))) || (errors.Is(readErr, os.ErrNotExist) && originalExists) {
		return fmt.Errorf("config changed during setup; rerun `amx config setup` against the current file")
	} else if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("rechecking config: %w", readErr)
	}
	if err := atomicfile.Replace(path, updated, 0o644); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	result.Saved = true
	result.Profile, result.Directory, result.Title = profile, directory, title
	result.Backend = candidate.LaunchBackendOrDefault()
	for _, account := range candidate.Profiles {
		if account.Name == profile {
			result.Harness, result.Home = account.EffectiveHarness(), config.ExpandTilde(account.EffectiveHomeDir())
		}
	}
	if _, err := fmt.Fprintf(out, "Configured %s with profile %s in %s\n", title, profile, path); err != nil {
		return err
	}
	var syncErr error
	if syncChoice == "yes" {
		// Keep hook progress off the JSON result stream, including partial failures.
		hookCmd := &cobra.Command{}
		hookCmd.SetOut(out)
		hookCmd.SetErr(cmd.ErrOrStderr())
		syncErr = setupSyncHooks(hookCmd, profile)
		result.HooksStatus = "synced"
		if syncErr != nil {
			result.HooksStatus, result.HooksError = "failed", syncErr.Error()
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("configuration saved, but readiness checks could not load it: %w", err)
	}
	var summary strings.Builder
	if err := setupSummary(&summary, cfg, profile, syncChoice == "yes", syncErr); err != nil {
		return err
	}
	result.Summary = summary.String()
	if _, err := io.WriteString(out, result.Summary); err != nil {
		return err
	}
	if syncErr != nil {
		return fmt.Errorf("configuration saved; hook setup failed: %w", syncErr)
	}
	return nil
}

func setupSummary(out io.Writer, cfg *config.Config, profile string, synced bool, syncErr error) error {
	var summary strings.Builder
	cliBackend := cfg.LaunchBackendOrDefault()
	dashboardBackend := cliBackend
	source := "config.toml"
	if strings.TrimSpace(cfg.Defaults.LaunchBackend) == "" {
		source = "default"
	}
	fmt.Fprintf(&summary, "\nLaunch destinations\n  CLI: %s (%s; --backend overrides)\n  Dashboard in this terminal: %s\n", cliBackend, source, dashboardBackend)
	if strings.TrimSpace(cfg.Defaults.LaunchBackend) == "" {
		summary.WriteString("  Dashboard elsewhere: tmux in every terminal.\n")
	}
	summary.WriteString("  macOS app: its Launch Project preference applies; check Settings → Launch destination.\n\nLaunch checks\n  Configuration: valid; project directory exists.\n")
	for _, account := range cfg.Profiles {
		if account.Name != profile {
			continue
		}
		executable := harness.Executable(account.EffectiveHarness())
		if path, err := setupLookPath(executable); err == nil {
			fmt.Fprintf(&summary, "  Agent CLI: %s\n", path)
		} else {
			fmt.Fprintf(&summary, "  Agent CLI missing: install %s and put it on PATH before launching.\n", executable)
		}
		fmt.Fprintf(&summary, "  Profile home: %s\n  Authentication: not checked; authenticate the agent CLI with this profile home before launching.\n", account.EffectiveHomeDir())
	}
	status := setupBackendStatus(cfg)
	if status.Tmux.Available {
		fmt.Fprintf(&summary, "  tmux: %s (%s)\n", status.Tmux.Version, status.Tmux.Path)
	}
	for _, problem := range status.Problems {
		fmt.Fprintf(&summary, "  tmux %s: %s %s\n", problem.Severity, problem.Message, problem.Hint)
	}
	if dashboardBackend == config.LaunchBackendTmux && !status.Usable() {
		summary.WriteString("  Dashboard launch blocked: resolve the tmux problems above before launching here.\n")
	}
	switch {
	case syncErr != nil:
		fmt.Fprintf(&summary, "  Hooks: failed — %v\n  Retry: %s\n", syncErr, setupHookCommand(profile))
	case synced:
		summary.WriteString("  Hooks: synced for this profile; event delivery still needs verification in a live session.\n")
	default:
		fmt.Fprintf(&summary, "  Hooks: not synced during setup. Next: %s\n", setupHookCommand(profile))
	}
	if os.Getenv(tmux.WorkspaceHostEnv) == "1" {
		summary.WriteString("\nReturn to the workspace, choose Start Daemon if offered, then choose Launch project… and select your project.\nThe app includes amx and tmux; no separate installation is needed.\n")
	} else {
		summary.WriteString("\nAfter resolving any missing requirements, run amx, press l, select your project, and press Enter.\nFor a full installation check: amx doctor --strict\n")
	}
	_, err := io.WriteString(out, summary.String())
	return err
}

func setupHookCommand(profile string) string {
	bin := "amx"
	if os.Getenv(tmux.WorkspaceHostEnv) == "1" {
		if current, err := cli.AmxBinary(); err == nil {
			bin = current
		}
	}
	return shellquote.Quote(bin) + " config hooks-sync -- " + shellquote.Quote(profile)
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
