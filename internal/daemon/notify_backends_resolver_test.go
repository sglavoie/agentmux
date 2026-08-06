package daemon

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

type resolverFileInfo struct {
	name string
	mode fs.FileMode
}

func (i resolverFileInfo) Name() string       { return i.name }
func (i resolverFileInfo) Size() int64        { return 0 }
func (i resolverFileInfo) Mode() fs.FileMode  { return i.mode }
func (i resolverFileInfo) ModTime() time.Time { return time.Time{} }
func (i resolverFileInfo) IsDir() bool        { return i.mode.IsDir() }
func (i resolverFileInfo) Sys() any           { return nil }

func executableInfo(path string) (os.FileInfo, error) {
	return resolverFileInfo{name: path, mode: 0o755}, nil
}

func TestResolveAlerterFindsStandardHomebrewPathOutsidePATH(t *testing.T) {
	resolver := alerterResolver{
		goos:     "darwin",
		lookPath: func(string) (string, error) { return "", errors.New("not on PATH") },
		stat: func(path string) (os.FileInfo, error) {
			if path == "/opt/homebrew/bin/alerter" {
				return executableInfo(path)
			}
			return nil, os.ErrNotExist
		},
		versionOf: func(context.Context, string) ([]byte, error) { return []byte("26.5\n"), nil },
	}

	got := resolveAlerter(&config.NotificationConfig{Stop: true}, resolver)
	if !got.Available || !got.Interactive || got.Executable != "/opt/homebrew/bin/alerter" || got.Version != "26.5" {
		t.Fatalf("capability = %+v, want interactive Homebrew Alerter", got)
	}
}

func TestResolveAlerterOverrideIsAuthoritative(t *testing.T) {
	lookups := 0
	resolver := alerterResolver{
		goos: "darwin",
		lookPath: func(string) (string, error) {
			lookups++
			return "/opt/homebrew/bin/alerter", nil
		},
		stat:      func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
		versionOf: func(context.Context, string) ([]byte, error) { return []byte("26.5"), nil },
	}

	got := resolveAlerter(&config.NotificationConfig{Stop: true, AlerterPath: "/missing/alerter"}, resolver)
	if got.Available || got.Reason != "invalid_override" || got.Executable != "/missing/alerter" {
		t.Fatalf("capability = %+v, want authoritative invalid override", got)
	}
	if lookups != 0 {
		t.Fatalf("PATH lookups = %d, want none after explicit override", lookups)
	}
}

func TestResolveAlerterRejectsLegacyVersion(t *testing.T) {
	resolver := alerterResolver{
		goos:     "darwin",
		lookPath: func(string) (string, error) { return "/custom/alerter", nil },
		stat: func(path string) (os.FileInfo, error) {
			if path == "/custom/alerter" {
				return executableInfo(path)
			}
			return nil, os.ErrNotExist
		},
		versionOf: func(context.Context, string) ([]byte, error) { return []byte("2.0"), nil },
	}

	got := resolveAlerter(&config.NotificationConfig{Stop: true}, resolver)
	if got.Available || got.Reason != "version_unsupported" || got.Version != "2.0" {
		t.Fatalf("capability = %+v, want unsupported legacy version", got)
	}
}

func TestResolveAlerterDisabledSkipsDiscovery(t *testing.T) {
	disabled := false
	resolver := alerterResolver{
		goos: "darwin",
		lookPath: func(string) (string, error) {
			t.Fatal("disabled notifications must not discover Alerter")
			return "", nil
		},
	}
	cfg := &config.NotificationConfig{
		StopFailure: &disabled, StaleActive: &disabled, HookSilence: &disabled,
	}

	got := resolveAlerter(cfg, resolver)
	if got.Enabled || got.Available || got.Backend != "none" || got.Reason != "disabled" {
		t.Fatalf("capability = %+v, want disabled", got)
	}
}

func TestResolveAlerterRejectsUnsupportedPlatform(t *testing.T) {
	got := resolveAlerter(&config.NotificationConfig{Stop: true}, alerterResolver{goos: "linux"})
	if got.Available || got.Reason != "unsupported_platform" {
		t.Fatalf("capability = %+v, want unsupported platform", got)
	}
}

func TestResolveAlerterProbeFailureIsReported(t *testing.T) {
	resolver := alerterResolver{
		goos:     "darwin",
		lookPath: func(string) (string, error) { return "/custom/alerter", nil },
		stat: func(path string) (os.FileInfo, error) {
			if path == "/custom/alerter" {
				return executableInfo(path)
			}
			return nil, os.ErrNotExist
		},
		versionOf: func(context.Context, string) ([]byte, error) { return nil, context.DeadlineExceeded },
	}

	got := resolveAlerter(&config.NotificationConfig{Stop: true}, resolver)
	if got.Available || got.Reason != "probe_failed" || got.Executable != "/custom/alerter" {
		t.Fatalf("capability = %+v, want probe failure", got)
	}
}
