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
}

func defaultDependencies() dependencies {
	return dependencies{
		newDaemon:  func() daemonLifecycle { return daemon.NewClient() },
		recover:    datastore.RecoverCorruptDefault,
		executable: os.Executable,
		restart:    daemon.SpawnDetached,
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
	result, restart, err := coordinateForRebuild(ctx, cause, deps)
	if restart != nil {
		err = errors.Join(err, restart())
	}
	return result, err
}

// RecoverForRebuild stops a live daemon and replaces the corrupt datastore but
// defers a successful recovery's daemon restart. The caller must invoke the
// returned function after compatibility import and workspace synchronization,
// or on every early-exit path. Recovery failures restore daemon liveness before
// returning and therefore never return a restart function.
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

	client := deps.newDaemon()
	wasAlive := client.Health().Alive
	if err := ctx.Err(); err != nil {
		return datastore.RecoveryResult{}, nil, err
	}

	var executable string
	if wasAlive {
		var err error
		executable, err = deps.executable()
		if err != nil {
			return datastore.RecoveryResult{}, nil, fmt.Errorf("datastore recovery: resolving daemon executable: %w", err)
		}
		if err := client.ShutdownAndWait(ctx); err != nil {
			return datastore.RecoveryResult{}, nil, fmt.Errorf("datastore recovery: stopping daemon: %w", err)
		}
	}

	result, recoveryErr := deps.recover(ctx, cause)
	if recoveryErr != nil {
		recoveryErr = fmt.Errorf("datastore recovery: replacing corrupt datastore: %w", recoveryErr)
	}

	if !wasAlive {
		return result, nil, recoveryErr
	}
	restart := func() error {
		if err := deps.restart(executable); err != nil {
			return fmt.Errorf("datastore recovery: restarting daemon: %w", err)
		}
		return nil
	}
	if recoveryErr != nil {
		return result, nil, errors.Join(recoveryErr, restart())
	}
	return result, restart, nil
}
