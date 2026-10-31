package gitstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// gitBinary avoids Apple's /usr/bin/git shim, which offers to install developer
// tools when none are configured. These optional status and review reads must
// never open an installer. An independently installed Git keeps PATH precedence.
func gitBinary(ctx context.Context) (string, error) {
	path, err := exec.LookPath("git")
	if err != nil {
		return "", err
	}
	return resolveGitBinary(runtime.GOOS, path, func() (string, error) {
		if directory := os.Getenv("DEVELOPER_DIR"); directory != "" {
			directory = filepath.Clean(directory)
			if strings.HasSuffix(directory, ".app") {
				directory = filepath.Join(directory, "Contents", "Developer")
			}
			return directory, nil
		}
		// Unlike invoking a tool shim, printing the selected directory does
		// not request installation. It shares the caller's command deadline.
		output, err := exec.CommandContext(ctx, "/usr/bin/xcode-select", "--print-path").Output()
		return strings.TrimSpace(string(output)), err
	})
}

func resolveGitBinary(platform, candidate string, developerDirectory func() (string, error)) (string, error) {
	canonical := func(path string) string {
		if real, err := filepath.EvalSymlinks(path); err == nil {
			return real
		}
		return filepath.Clean(path)
	}
	if platform != "darwin" || canonical(candidate) != "/usr/bin/git" {
		return candidate, nil
	}
	directory, err := developerDirectory()
	if err != nil || !filepath.IsAbs(directory) {
		return "", errors.New("git is unavailable: Apple developer tools are not configured; install Git to use checkout features")
	}
	path, err := exec.LookPath(filepath.Join(directory, "usr", "bin", "git"))
	if err != nil || canonical(path) == "/usr/bin/git" {
		return "", fmt.Errorf("git is unavailable in the selected developer directory %s", directory)
	}
	return path, nil
}
