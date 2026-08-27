package cmd

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/aslcontract"
	"github.com/sglavoie/agentmux/internal/aslexport"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/debugger"
)

// stubASLExportSeams replaces the resolution and fetch seams for one test.
// Nothing here may start a real subprocess: a bundle has to be creatable on a
// machine with no ASL installed, and that is precisely what is under test.
func stubASLExportSeams(
	t *testing.T,
	resolve func(string) (string, error),
	fetch func(context.Context, aslexport.Options) (aslexport.Result, error),
) *aslexport.Options {
	t.Helper()
	previousResolve, previousFetch, previousInclude := debugBundleASLResolve, debugBundleASLFetch, debugBundleIncludeASL
	seen := &aslexport.Options{}
	debugBundleASLResolve = resolve
	debugBundleASLFetch = func(ctx context.Context, options aslexport.Options) (aslexport.Result, error) {
		*seen = options
		return fetch(ctx, options)
	}
	t.Cleanup(func() {
		debugBundleASLResolve, debugBundleASLFetch, debugBundleIncludeASL = previousResolve, previousFetch, previousInclude
	})
	return seen
}

func okFetch(context.Context, aslexport.Options) (aslexport.Result, error) {
	return aslexport.Result{Availability: aslexport.Availability{Available: true, Reason: aslexport.ReasonOK}}, nil
}

func failResolve(string) (string, error) { return "", errors.New("asl not found in $PATH") }

func TestBundleASLFetchIsSkippedUnlessRequested(t *testing.T) {
	stubASLExportSeams(t, func(string) (string, error) {
		t.Fatal("resolution must not run without --include-asl")
		return "", nil
	}, okFetch)
	debugBundleIncludeASL = false
	result := fetchASLExportForBundle(t.Context(), nil, debugger.Selector{})
	if result.Availability.Reason != aslexport.ReasonNotRequested || result.Document != nil {
		t.Fatalf("result = %+v", result.Availability)
	}
}

func TestBundleASLFetchHonoursDisabledConfiguration(t *testing.T) {
	stubASLExportSeams(t, func(string) (string, error) {
		t.Fatal("a disabled integration must not start a subprocess")
		return "", nil
	}, okFetch)
	debugBundleIncludeASL = true
	disabled := false
	cfg := &config.Config{Defaults: config.Defaults{ASL: config.ASLConfig{Enabled: &disabled}}}
	result := fetchASLExportForBundle(t.Context(), cfg, debugger.Selector{})
	if result.Availability.Reason != aslexport.ReasonDisabled {
		t.Fatalf("result = %+v", result.Availability)
	}
}

func TestBundleASLFetchReportsUnresolvableExecutable(t *testing.T) {
	stubASLExportSeams(t, failResolve, func(context.Context, aslexport.Options) (aslexport.Result, error) {
		t.Fatal("an unresolvable executable must not be fetched")
		return aslexport.Result{}, nil
	})
	debugBundleIncludeASL = true
	result := fetchASLExportForBundle(t.Context(), nil, debugger.Selector{})
	if result.Availability.Reason != aslexport.ReasonNotFound {
		t.Fatalf("result = %+v", result.Availability)
	}
	if result.Availability.Detail == "" {
		t.Fatal("the resolution failure must be carried into the bundle")
	}
}

func TestBundleASLFetchPassesConfiguredIdentityAndSelector(t *testing.T) {
	seen := stubASLExportSeams(t,
		func(command string) (string, error) { return "/opt/" + command, nil }, okFetch)
	debugBundleIncludeASL = true
	cfg := &config.Config{Defaults: config.Defaults{ASL: config.ASLConfig{
		Command: "asl-nightly", StateRoot: "/srv/plans",
	}}}
	result := fetchASLExportForBundle(t.Context(), cfg, debugger.Selector{ASLRunID: "run-alpha"})
	if !result.Availability.Available {
		t.Fatalf("result = %+v", result.Availability)
	}
	if seen.Executable != "/opt/asl-nightly" || seen.StateRoot != "/srv/plans" ||
		seen.RunID != "run-alpha" || seen.ContractSetID != aslcontract.ContractSetID {
		t.Fatalf("options = %+v", *seen)
	}
}

func TestBundleASLFetchDefaultsTheStateRootAndCommand(t *testing.T) {
	seen := stubASLExportSeams(t,
		func(command string) (string, error) { return "/opt/" + command, nil }, okFetch)
	debugBundleIncludeASL = true
	fetchASLExportForBundle(t.Context(), &config.Config{}, debugger.Selector{})
	if seen.Executable != "/opt/asl" {
		t.Fatalf("executable = %q", seen.Executable)
	}
	if seen.StateRoot == "" {
		t.Fatal("the state root must be named explicitly so both halves agree on it")
	}
}

func TestBundleASLFetchTurnsAFetchErrorIntoAnAvailability(t *testing.T) {
	stubASLExportSeams(t,
		func(command string) (string, error) { return "/opt/" + command, nil },
		func(context.Context, aslexport.Options) (aslexport.Result, error) {
			return aslexport.Result{}, errors.New("boom")
		})
	debugBundleIncludeASL = true
	result := fetchASLExportForBundle(t.Context(), nil, debugger.Selector{})
	if result.Availability.Reason != aslexport.ReasonFailed || result.Availability.Detail != "boom" {
		t.Fatalf("result = %+v", result.Availability)
	}
}

func TestBundleASLFetchToleratesASlowFetchWithoutHangingTheCaller(t *testing.T) {
	// The bound belongs to the export package; this only asserts the command
	// hands the caller's context down so a cancelled bundle stops the child.
	seen := stubASLExportSeams(t,
		func(command string) (string, error) { return "/opt/" + command, nil },
		func(ctx context.Context, _ aslexport.Options) (aslexport.Result, error) {
			select {
			case <-ctx.Done():
				return aslexport.Result{}, ctx.Err()
			case <-time.After(time.Second):
				return aslexport.Result{}, errors.New("context was not propagated")
			}
		})
	debugBundleIncludeASL = true
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result := fetchASLExportForBundle(ctx, nil, debugger.Selector{})
	if result.Availability.Reason != aslexport.ReasonFailed {
		t.Fatalf("result = %+v", result.Availability)
	}
	if seen.ContractSetID != aslcontract.ContractSetID {
		t.Fatalf("options = %+v", *seen)
	}
}
