package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
)

type setupHarness struct {
	Harness      string `json:"harness"`
	CLIAvailable bool   `json:"cli_available"`
	CLIPath      string `json:"cli_path,omitempty"`
	Home         string `json:"home"`
	HomeExists   bool   `json:"home_exists"`
}

type setupProject struct {
	Directory string    `json:"directory"`
	Harness   string    `json:"harness"`
	LastUsed  time.Time `json:"last_used"`
}

type setupDetection struct {
	Harnesses      []setupHarness `json:"harnesses"`
	RecentProjects []setupProject `json:"recent_projects"`
	Warnings       []string       `json:"warnings"`
}

// Detection never loads configuration, a transcript index, or message content.
// File mtime is the metadata readers' recency key; identity timestamps may be
// absent or record only the beginning of a conversation.
func detectSetup() setupDetection {
	result := setupDetection{Harnesses: []setupHarness{}, RecentProjects: []setupProject{}, Warnings: []string{}}
	readers := map[string]transcript.MetadataDiscoverer{
		harness.Claude:  transcript.ClaudeReader{},
		harness.Codex:   transcript.CodexReader{},
		harness.Copilot: transcript.CopilotReader{},
	}
	recent := map[string]setupProject{}
	for _, name := range harness.Names() {
		home := harness.DefaultHomeDir(name)
		path, cliErr := setupLookPath(harness.Executable(name))
		info, statErr := os.Stat(home)
		exists := statErr == nil && info.IsDir()
		result.Harnesses = append(result.Harnesses, setupHarness{name, cliErr == nil, path, home, exists})
		if statErr != nil && !os.IsNotExist(statErr) {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s home: %v", name, statErr))
		}
		if !exists {
			continue
		}
		candidates, err := readers[name].DiscoverMetadata(home, transcript.MetadataOptions{})
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s metadata: %v", name, err))
		}
		for _, candidate := range candidates {
			for _, directory := range candidate.Directories {
				// A relative identity must not resolve against amx's own cwd.
				if !filepath.IsAbs(directory) {
					continue
				}
				directory = filepath.Clean(directory)
				info, err := os.Stat(directory)
				if err != nil || !info.IsDir() {
					continue
				}
				if previous, ok := recent[directory]; !ok || candidate.FileModTime.After(previous.LastUsed) {
					recent[directory] = setupProject{directory, name, candidate.FileModTime.UTC()}
				}
			}
		}
	}
	for _, project := range recent {
		result.RecentProjects = append(result.RecentProjects, project)
	}
	sort.Slice(result.RecentProjects, func(i, j int) bool {
		a, b := result.RecentProjects[i], result.RecentProjects[j]
		if a.LastUsed.Equal(b.LastUsed) {
			return a.Directory < b.Directory
		}
		return a.LastUsed.After(b.LastUsed)
	})
	if len(result.RecentProjects) > 8 {
		result.RecentProjects = result.RecentProjects[:8]
	}
	return result
}

func (d setupDetection) defaultHarness() string {
	chosen := ""
	for _, h := range d.Harnesses {
		if !h.CLIAvailable && !h.HomeExists {
			continue
		}
		if chosen != "" {
			return harness.Claude
		}
		chosen = h.Harness
	}
	if chosen == "" {
		return harness.Claude
	}
	return chosen
}

func (d setupDetection) writeText(out io.Writer) error {
	for _, h := range d.Harnesses {
		if _, err := fmt.Fprintf(out, "%s: CLI on PATH=%t, default home exists=%t (%s)\n", h.Harness, h.CLIAvailable, h.HomeExists, h.Home); err != nil {
			return err
		}
	}
	for _, p := range d.RecentProjects {
		if _, err := fmt.Fprintf(out, "%s (%s, last used %s)\n", p.Directory, p.Harness, p.LastUsed.Format(time.RFC3339)); err != nil {
			return err
		}
	}
	for _, warning := range d.Warnings {
		if _, err := fmt.Fprintln(out, "Warning:", warning); err != nil {
			return err
		}
	}
	return nil
}
