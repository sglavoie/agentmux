// Package datastorerecovery coordinates daemon teardown with destructive
// recovery of the shared workbench datastore.
package datastorerecovery

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/datastore"
)

type daemonLifecycle interface {
	Health() daemon.Health
	ShutdownAndWait(context.Context) error
}

type dependencies struct {
	newDaemon  func() daemonLifecycle
	recover    func(context.Context, error) (datastore.RecoveryResult, error)
	executable func() (string, error)
	restart    func(string) error
	beginGate  func(context.Context) (rebuildGate, error)
}

type rebuildGate interface {
	SetRestartDaemon(bool) error
	MarkRebuildPending(datastore.RecoveryResult) error
	Release() error
}

type noopRebuildGate struct{}

func (noopRebuildGate) SetRestartDaemon(bool) error                       { return nil }
func (noopRebuildGate) MarkRebuildPending(datastore.RecoveryResult) error { return nil }
func (noopRebuildGate) Release() error                                    { return nil }

func defaultDependencies() dependencies {
	return dependencies{
		newDaemon:  func() daemonLifecycle { return daemon.NewClient() },
		recover:    datastore.RecoverCorruptDefault,
		executable: os.Executable,
		restart:    daemon.SpawnDetached,
		beginGate: func(ctx context.Context) (rebuildGate, error) {
			return datastore.BeginDefaultRebuildGateContext(ctx)
		},
	}
}

// Recover shuts down a live daemon before requesting exclusive datastore
// recovery, then immediately restores its prior liveness. Callers that must
// rebuild compatibility state before the daemon reopens the fresh database use
// RecoverForRebuild instead.
func Recover(ctx context.Context, cause error) (datastore.RecoveryResult, error) {
	return coordinate(ctx, cause, defaultDependencies())
}

func coordinate(ctx context.Context, cause error, deps dependencies) (datastore.RecoveryResult, error) {
	result, finish, err := coordinateForRebuild(ctx, cause, deps)
	if finish != nil {
		err = errors.Join(err, finish())
	}
	return result, err
}

// RecoverForRebuild blocks every daemon start, stops a live daemon, and replaces
// the corrupt datastore. The caller must invoke the returned completion after
// compatibility import, workspace synchronization, and a durable checkpoint.
// After replacement, early exits leave that completion pending and fail closed.
// Completion reopens daemon startup and restores prior liveness only when the
// daemon was originally live. Recovery failures complete cleanup before
// returning and therefore return no callback.
func RecoverForRebuild(ctx context.Context, cause error) (datastore.RecoveryResult, func() error, error) {
	return coordinateForRebuild(ctx, cause, defaultDependencies())
}

func coordinateForRebuild(ctx context.Context, cause error, deps dependencies) (datastore.RecoveryResult, func() error, error) {
	if ctx == nil {
		return datastore.RecoveryResult{}, nil, errors.New("datastore recovery: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return datastore.RecoveryResult{}, nil, err
	}
	gate := rebuildGate(noopRebuildGate{})
	if deps.beginGate != nil {
		var err error
		gate, err = deps.beginGate(ctx)
		if err != nil {
			return datastore.RecoveryResult{}, nil, fmt.Errorf("datastore recovery: blocking daemon startup: %w", err)
		}
	}
	releaseGate := func(primary error) error {
		return errors.Join(primary, gate.Release())
	}

	client := deps.newDaemon()
	wasAlive := client.Health().Alive
	if err := gate.SetRestartDaemon(wasAlive); err != nil {
		return datastore.RecoveryResult{}, nil, releaseGate(fmt.Errorf("datastore recovery: recording daemon restart intent: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return datastore.RecoveryResult{}, nil, releaseGate(err)
	}

	var executable string
	if wasAlive {
		var err error
		executable, err = deps.executable()
		if err != nil {
			return datastore.RecoveryResult{}, nil, releaseGate(fmt.Errorf("datastore recovery: resolving daemon executable: %w", err))
		}
		if err := client.ShutdownAndWait(ctx); err != nil {
			return datastore.RecoveryResult{}, nil, releaseGate(fmt.Errorf("datastore recovery: stopping daemon: %w", err))
		}
	}

	result, recoveryErr := deps.recover(ctx, cause)
	if recoveryErr != nil {
		recoveryErr = fmt.Errorf("datastore recovery: replacing corrupt datastore: %w", recoveryErr)
	}
	if recoveryErr == nil && result.Replaced {
		if err := gate.MarkRebuildPending(result); err != nil {
			// Replacement is already published. Do not release an incompletely
			// persisted gate and allow a daemon to open it before rebuild.
			return result, nil, fmt.Errorf("datastore recovery: preserving post-replacement rebuild gate: %w", err)
		}
	}

	gateReleased := false
	restartPending := wasAlive
	finish := func() error {
		if !gateReleased {
			if err := gate.Release(); err != nil {
				return fmt.Errorf("datastore recovery: reopening daemon startup: %w", err)
			}
			gateReleased = true
		}
		if restartPending {
			if err := deps.restart(executable); err != nil {
				return fmt.Errorf("datastore recovery: restarting daemon: %w", err)
			}
			restartPending = false
		}
		return nil
	}
	if recoveryErr != nil {
		return result, nil, errors.Join(recoveryErr, finish())
	}
	return result, finish, nil
}
