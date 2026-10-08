package process

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/diagnostics"
)

// SpawnDetached starts `amxBin daemon start` in a new session. Stdio is
// detached from the caller and inherited by a private bootstrap log when the
// diagnostics directory is available. Diagnostics setup is fail-open: if the
// private log cannot be created, the daemon is still started with nil stdio.
func SpawnDetached(amxBin string) error {
	cmd := exec.Command(amxBin, "daemon", "start")
	cmd.SysProcAttr = detachSysProcAttr()

	var bootstrap *os.File
	if paths, err := diagnostics.DefaultPaths(); err == nil {
		launchID := strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
		if file, bootstrapPath, openErr := diagnostics.OpenBootstrapLog(paths, launchID); openErr == nil {
			bootstrap = file
			cmd.Stdout = bootstrap
			cmd.Stderr = bootstrap
			cmd.Env = environmentWithOverrides(os.Environ(),
				DetachedEnv+"=1",
				LaunchIDEnv+"="+launchID,
				BootstrapEnv+"="+bootstrapPath,
			)
		}
	}
	if err := cmd.Start(); err != nil {
		if bootstrap != nil {
			return errors.Join(err, bootstrap.Close())
		}
		return err
	}
	_ = cmd.Process.Release()
	if bootstrap != nil {
		_ = bootstrap.Close()
	}
	return nil
}

func environmentWithOverrides(base []string, overrides ...string) []string {
	keys := make(map[string]bool, len(overrides))
	for _, override := range overrides {
		key, _, _ := strings.Cut(override, "=")
		keys[key] = true
	}
	result := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if !keys[key] {
			result = append(result, entry)
		}
	}
	return append(result, overrides...)
}
