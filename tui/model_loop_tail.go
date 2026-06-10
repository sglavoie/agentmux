package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/agentloop"
)

const agentSessionLoopBinary = "agent-session-loop"

var (
	loopTailDiscover      = agentloop.Discover
	loopTailExecProcess   = tea.ExecProcess
	loopTailLookPath      = exec.LookPath
	loopTailFallbackPaths = defaultLoopTailFallbackPaths
	loopTailStat          = os.Stat

	errNoActiveLoop = errors.New("no active agent-session-loop runs found")
)

type loopTailSelectionMsg struct {
	selection agentloop.Selection
	err       error
}

type loopTailDoneMsg struct{ err error }

func activeLoopRefreshCmd(seq int) tea.Cmd {
	return func() tea.Msg {
		candidates, err := loopTailDiscover(agentloop.DiscoverOptions{})
		if err != nil {
			return activeLoopsMsg{seq: seq, err: err}
		}
		return activeLoopsMsg{seq: seq, candidates: candidates}
	}
}

// selectedLoopTailDirectory returns the highlighted dashboard row directory,
// or an empty string when the list is empty or the cursor is invalid.
func (m Model) selectedLoopTailDirectory() string {
	if len(m.filtered) == 0 || m.cursor < 0 || m.cursor >= len(m.filtered) {
		return ""
	}
	return m.filtered[m.cursor].Directory
}

func (m Model) activeLoopsForDirectory(directory string) []agentloop.Candidate {
	matches, err := agentloop.MatchingCWD(m.activeLoopCandidates, directory)
	if err != nil {
		return nil
	}
	return matches
}

func (m Model) activeLoopsForSelectedRow() []agentloop.Candidate {
	return m.activeLoopsForDirectory(m.selectedLoopTailDirectory())
}

// loopTailSelectionCmd discovers active loop runs and applies the dashboard
// selection rule against the highlighted row directory.
func (m Model) loopTailSelectionCmd() tea.Cmd {
	selectedDir := m.selectedLoopTailDirectory()
	return func() tea.Msg {
		candidates, err := loopTailDiscover(agentloop.DiscoverOptions{})
		if err != nil {
			return loopTailSelectionMsg{err: err}
		}
		selection, err := selectLoopTailCandidates(candidates, selectedDir)
		return loopTailSelectionMsg{selection: selection, err: err}
	}
}

func selectLoopTailCandidates(candidates []agentloop.Candidate, selectedDir string) (agentloop.Selection, error) {
	return agentloop.SelectForDirectory(candidates, selectedDir)
}

func (m Model) handleLoopTailSelection(msg loopTailSelectionMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.loopTailCandidates = nil
		m.loopTailCursor = 0
		m.loopTailOffset = 0
		m.actionErr = msg.err
		return m, nil
	}

	switch msg.selection.Kind {
	case agentloop.SelectionLaunch:
		candidate, ok := msg.selection.LaunchCandidate()
		if !ok {
			m.loopTailCandidates = nil
			m.loopTailCursor = 0
			m.loopTailOffset = 0
			m.actionErr = errors.New("agent-session-loop selection did not contain exactly one candidate")
			return m, nil
		}
		m.loopTailCandidates = nil
		m.loopTailCursor = 0
		m.loopTailOffset = 0
		m.actionErr = nil
		return m, loopTailLaunchCmd(candidate)
	case agentloop.SelectionPicker:
		m.loopTailCandidates = append([]agentloop.Candidate(nil), msg.selection.Candidates...)
		m.loopTailCursor = 0
		m.loopTailOffset = 0
		m.actionErr = nil
		m.clearJumpChord()
		m.mode = ModeLoopTailPicker
		scrollLoopTailPickerToCursor(&m)
		return m, nil
	default:
		m.loopTailCandidates = nil
		m.loopTailCursor = 0
		m.loopTailOffset = 0
		m.actionErr = errNoActiveLoop
		return m, nil
	}
}

// loopTailLaunchCmd runs the existing Python tail command for a selected
// active run in the dashboard terminal. It resolves the executable when the
// user launches the tail so PATH changes are honored between dashboard startup
// and user action. While tea.ExecProcess owns the terminal, Ctrl+C is handled
// by the child tail process; when the child exits, the dashboard resumes via
// loopTailDoneMsg.
func loopTailLaunchCmd(candidate agentloop.Candidate) tea.Cmd {
	cmd, err := loopTailExecCmd(candidate)
	if err != nil {
		return func() tea.Msg {
			return loopTailDoneMsg{err: err}
		}
	}
	return loopTailExecProcess(cmd, func(err error) tea.Msg {
		return loopTailDoneMsg{err: err}
	})
}

func loopTailExecCmd(candidate agentloop.Candidate) (*exec.Cmd, error) {
	if candidate.PlanDir == "" {
		return nil, errors.New("agent-session-loop candidate missing plan directory")
	}

	binary, err := resolveLoopTailBinary()
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(binary, "tail", candidate.PlanDir)
	cmd.Dir = candidate.OverlayCWD()
	cmd.Env = loopTailExecEnv(binary, os.Environ())
	return cmd, nil
}

func resolveLoopTailBinary() (string, error) {
	binary, err := loopTailLookPath(agentSessionLoopBinary)
	if err == nil {
		if abs, absErr := filepath.Abs(binary); absErr == nil {
			return abs, nil
		}
		return binary, nil
	}

	fallbackPaths := loopTailFallbackPaths()
	checked := make([]string, 0, len(fallbackPaths))
	for _, path := range fallbackPaths {
		if path == "" {
			continue
		}
		checked = append(checked, path)
		if !isExecutableFile(path) {
			continue
		}
		return path, nil
	}

	if len(checked) == 0 {
		return "", fmt.Errorf("%s not found in PATH: %w", agentSessionLoopBinary, err)
	}
	return "", fmt.Errorf("%s not found in PATH or fallback locations (%s): %w", agentSessionLoopBinary, strings.Join(checked, ", "), err)
}

func defaultLoopTailFallbackPaths() []string {
	paths := make([]string, 0, 3)
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths, filepath.Join(home, ".local", "bin", agentSessionLoopBinary))
	}
	paths = append(paths,
		filepath.Join("/opt/homebrew/bin", agentSessionLoopBinary),
		filepath.Join("/usr/local/bin", agentSessionLoopBinary),
	)
	return paths
}

func isExecutableFile(path string) bool {
	info, err := loopTailStat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

func loopTailExecEnv(binary string, environ []string) []string {
	pathSet := false
	out := make([]string, 0, len(environ)+1)
	for _, entry := range environ {
		if strings.HasPrefix(entry, "PATH=") {
			if !pathSet {
				out = append(out, "PATH="+loopTailExecPath(binary, strings.TrimPrefix(entry, "PATH=")))
				pathSet = true
			}
			continue
		}
		out = append(out, entry)
	}
	if !pathSet {
		out = append(out, "PATH="+loopTailExecPath(binary, ""))
	}
	return out
}

func loopTailExecPath(binary, currentPath string) string {
	dirs := make([]string, 0, 8)
	if binaryDir := filepath.Dir(binary); binaryDir != "." && binaryDir != string(filepath.Separator) {
		dirs = append(dirs, binaryDir)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"))
	}
	dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin")
	dirs = append(dirs, filepath.SplitList(currentPath)...)
	dirs = append(dirs, "/usr/bin", "/bin", "/usr/sbin", "/sbin")
	return joinUniquePath(dirs)
}

func joinUniquePath(dirs []string) string {
	seen := make(map[string]bool, len(dirs))
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	return strings.Join(out, string(os.PathListSeparator))
}
