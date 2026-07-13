package datastorerecovery

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/datastore"
)

type fakeDaemon struct {
	health   daemon.Health
	shutdown func(context.Context) error
	calls    *[]string
}

func (f *fakeDaemon) Health() daemon.Health {
	*f.calls = append(*f.calls, "health")
	return f.health
}

func (f *fakeDaemon) ShutdownAndWait(ctx context.Context) error {
	*f.calls = append(*f.calls, "shutdown")
	if f.shutdown != nil {
		return f.shutdown(ctx)
	}
	return nil
}

func TestCoordinateRecoversAndRestoresPriorDaemonLiveness(t *testing.T) {
	for _, test := range []struct {
		name      string
		alive     bool
		wantCalls []string
	}{
		{name: "live daemon", alive: true, wantCalls: []string{"health", "executable", "shutdown", "recover", "restart"}},
		{name: "stopped daemon", alive: false, wantCalls: []string{"health", "recover"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := []string{}
			client := &fakeDaemon{health: daemon.Health{Alive: test.alive}, calls: &calls}
			wantResult := datastore.RecoveryResult{Replaced: true, QuarantineDir: "/recovery/now"}
			deps := dependencies{
				newDaemon: func() daemonLifecycle { return client },
				recover: func(context.Context, error) (datastore.RecoveryResult, error) {
					calls = append(calls, "recover")
					return wantResult, nil
				},
				executable: func() (string, error) {
					calls = append(calls, "executable")
					return "/installed/amx", nil
				},
				restart: func(path string) error {
					calls = append(calls, "restart")
					if path != "/installed/amx" {
						t.Fatalf("restart path = %q, want /installed/amx", path)
					}
					return nil
				},
			}

			got, err := coordinate(context.Background(), errors.New("corrupt"), deps)
			if err != nil {
				t.Fatalf("coordinate: %v", err)
			}
			if !reflect.DeepEqual(got, wantResult) {
				t.Fatalf("result = %+v, want %+v", got, wantResult)
			}
			if !reflect.DeepEqual(calls, test.wantCalls) {
				t.Fatalf("calls = %v, want %v", calls, test.wantCalls)
			}
		})
	}
}

func TestCoordinateForRebuildDefersSuccessfulRecoveryRestart(t *testing.T) {
	calls := []string{}
	client := &fakeDaemon{health: daemon.Health{Alive: true}, calls: &calls}
	wantResult := datastore.RecoveryResult{Replaced: true, QuarantineDir: "/recovery/now"}
	got, restart, err := coordinateForRebuild(context.Background(), errors.New("corrupt"), dependencies{
		newDaemon: func() daemonLifecycle { return client },
		recover: func(context.Context, error) (datastore.RecoveryResult, error) {
			calls = append(calls, "recover")
			return wantResult, nil
		},
		executable: func() (string, error) {
			calls = append(calls, "executable")
			return "/installed/amx", nil
		},
		restart: func(string) error {
			calls = append(calls, "restart")
			return nil
		},
	})
	if err != nil || !reflect.DeepEqual(got, wantResult) || restart == nil {
		t.Fatalf("deferred recovery = result %+v restart %v error %v", got, restart != nil, err)
	}
	if want := []string{"health", "executable", "shutdown", "recover"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls before rebuild = %v, want %v", calls, want)
	}
	if err := restart(); err != nil {
		t.Fatalf("deferred restart: %v", err)
	}
	if want := []string{"health", "executable", "shutdown", "recover", "restart"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls after rebuild = %v, want %v", calls, want)
	}
}

func TestCoordinateRestartsAfterRecoveryFailureAndJoinsFailures(t *testing.T) {
	recoveryFailure := errors.New("replacement failed")
	restartFailure := errors.New("spawn failed")
	calls := []string{}
	client := &fakeDaemon{health: daemon.Health{Alive: true}, calls: &calls}
	result := datastore.RecoveryResult{QuarantineDir: "/recovery/partial"}
	got, err := coordinate(context.Background(), errors.New("corrupt"), dependencies{
		newDaemon: func() daemonLifecycle { return client },
		recover: func(context.Context, error) (datastore.RecoveryResult, error) {
			calls = append(calls, "recover")
			return result, recoveryFailure
		},
		executable: func() (string, error) {
			calls = append(calls, "executable")
			return "/installed/amx", nil
		},
		restart: func(string) error {
			calls = append(calls, "restart")
			return restartFailure
		},
	})
	if !reflect.DeepEqual(got, result) {
		t.Fatalf("result = %+v, want %+v", got, result)
	}
	if !errors.Is(err, recoveryFailure) || !errors.Is(err, restartFailure) {
		t.Fatalf("error = %v, want joined recovery and restart failures", err)
	}
	if want := []string{"health", "executable", "shutdown", "recover", "restart"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestCoordinateRestartsAfterRecoveryFailure(t *testing.T) {
	recoveryFailure := errors.New("replacement failed")
	calls := []string{}
	client := &fakeDaemon{health: daemon.Health{Alive: true}, calls: &calls}
	_, err := coordinate(context.Background(), errors.New("corrupt"), dependencies{
		newDaemon: func() daemonLifecycle { return client },
		recover: func(context.Context, error) (datastore.RecoveryResult, error) {
			calls = append(calls, "recover")
			return datastore.RecoveryResult{}, recoveryFailure
		},
		executable: func() (string, error) {
			calls = append(calls, "executable")
			return "/installed/amx", nil
		},
		restart: func(string) error {
			calls = append(calls, "restart")
			return nil
		},
	})
	if !errors.Is(err, recoveryFailure) {
		t.Fatalf("error = %v, want recovery failure", err)
	}
	if want := []string{"health", "executable", "shutdown", "recover", "restart"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestCoordinateReturnsRestartFailureAfterSuccessfulRecovery(t *testing.T) {
	restartFailure := errors.New("spawn failed")
	calls := []string{}
	client := &fakeDaemon{health: daemon.Health{Alive: true}, calls: &calls}
	result := datastore.RecoveryResult{Replaced: true}
	got, err := coordinate(context.Background(), errors.New("corrupt"), dependencies{
		newDaemon: func() daemonLifecycle { return client },
		recover: func(context.Context, error) (datastore.RecoveryResult, error) {
			calls = append(calls, "recover")
			return result, nil
		},
		executable: func() (string, error) {
			calls = append(calls, "executable")
			return "/installed/amx", nil
		},
		restart: func(string) error {
			calls = append(calls, "restart")
			return restartFailure
		},
	})
	if got != result {
		t.Fatalf("result = %+v, want %+v", got, result)
	}
	if !errors.Is(err, restartFailure) {
		t.Fatalf("error = %v, want restart failure", err)
	}
}

func TestCoordinateRestartsAfterRecoveryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := []string{}
	client := &fakeDaemon{
		health: daemon.Health{Alive: true},
		calls:  &calls,
		shutdown: func(context.Context) error {
			cancel()
			return nil
		},
	}
	_, err := coordinate(ctx, errors.New("corrupt"), dependencies{
		newDaemon: func() daemonLifecycle { return client },
		recover: func(ctx context.Context, _ error) (datastore.RecoveryResult, error) {
			calls = append(calls, "recover")
			return datastore.RecoveryResult{}, ctx.Err()
		},
		executable: func() (string, error) {
			calls = append(calls, "executable")
			return "/installed/amx", nil
		},
		restart: func(string) error {
			calls = append(calls, "restart")
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if want := []string{"health", "executable", "shutdown", "recover", "restart"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestCoordinateStopsBeforeMutationOnPreRecoveryFailures(t *testing.T) {
	shutdownFailure := errors.New("shutdown rejected")
	for _, test := range []struct {
		name       string
		ctx        func() context.Context
		shutdown   error
		executable error
		want       error
		wantCalls  []string
	}{
		{
			name: "already canceled",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			want:      context.Canceled,
			wantCalls: []string{},
		},
		{
			name:       "executable resolution",
			ctx:        context.Background,
			executable: errors.New("no executable"),
			want:       errors.New("no executable"),
			wantCalls:  []string{"health", "executable"},
		},
		{
			name:      "shutdown failure",
			ctx:       context.Background,
			shutdown:  shutdownFailure,
			want:      shutdownFailure,
			wantCalls: []string{"health", "executable", "shutdown"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := []string{}
			client := &fakeDaemon{health: daemon.Health{Alive: true}, calls: &calls, shutdown: func(context.Context) error { return test.shutdown }}
			executableFailure := test.executable
			_, err := coordinate(test.ctx(), errors.New("corrupt"), dependencies{
				newDaemon: func() daemonLifecycle { return client },
				recover: func(context.Context, error) (datastore.RecoveryResult, error) {
					calls = append(calls, "recover")
					return datastore.RecoveryResult{}, nil
				},
				executable: func() (string, error) {
					calls = append(calls, "executable")
					return "/installed/amx", executableFailure
				},
				restart: func(string) error {
					calls = append(calls, "restart")
					return nil
				},
			})
			if test.name == "executable resolution" {
				if err == nil || !errors.Is(err, executableFailure) {
					t.Fatalf("error = %v, want executable failure", err)
				}
			} else if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if !reflect.DeepEqual(calls, test.wantCalls) {
				t.Fatalf("calls = %v, want %v", calls, test.wantCalls)
			}
		})
	}
}
