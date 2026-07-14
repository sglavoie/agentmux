package datastorerecovery

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/datastore"
)

type fakeDaemon struct {
	health   daemon.Health
	shutdown func(context.Context) error
	calls    *[]string
}

type fakeRebuildGate struct {
	calls       *[]string
	marked      []datastore.RecoveryResult
	markFailure error
}

type startupRaceDaemon struct {
	healthCalled   chan struct{}
	shutdownCalled chan struct{}
}

func (d *startupRaceDaemon) Health() daemon.Health {
	close(d.healthCalled)
	return daemon.Health{Alive: true}
}

func (d *startupRaceDaemon) ShutdownAndWait(context.Context) error {
	close(d.shutdownCalled)
	return nil
}

func (g *fakeRebuildGate) SetRestartDaemon(restart bool) error {
	*g.calls = append(*g.calls, fmt.Sprintf("gate-intent:%t", restart))
	return nil
}

func (g *fakeRebuildGate) MarkRebuildPending(result datastore.RecoveryResult) error {
	g.marked = append(g.marked, result)
	return g.markFailure
}

func (g *fakeRebuildGate) Release() error {
	*g.calls = append(*g.calls, "gate-release")
	return nil
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

func TestCoordinateForRebuildKeepsDaemonGateThroughDeferredRebuild(t *testing.T) {
	calls := []string{}
	client := &fakeDaemon{health: daemon.Health{Alive: true}, calls: &calls}
	_, finish, err := coordinateForRebuild(context.Background(), errors.New("corrupt"), dependencies{
		beginGate: func(context.Context) (rebuildGate, error) {
			calls = append(calls, "gate-begin")
			return &fakeRebuildGate{calls: &calls}, nil
		},
		newDaemon: func() daemonLifecycle { return client },
		recover: func(context.Context, error) (datastore.RecoveryResult, error) {
			calls = append(calls, "recover")
			return datastore.RecoveryResult{Replaced: true}, nil
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
	if err != nil || finish == nil {
		t.Fatalf("coordinate for rebuild = finish %v, error %v", finish != nil, err)
	}
	if want := []string{"gate-begin", "health", "gate-intent:true", "executable", "shutdown", "recover"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls before rebuild = %v, want %v", calls, want)
	}
	if err := finish(); err != nil {
		t.Fatalf("finish rebuild: %v", err)
	}
	if want := []string{"gate-begin", "health", "gate-intent:true", "executable", "shutdown", "recover", "gate-release", "restart"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls after rebuild = %v, want %v", calls, want)
	}
}

func TestCoordinateForRebuildMarksReplacementDurableBeforeHandoff(t *testing.T) {
	calls := []string{}
	gate := &fakeRebuildGate{calls: &calls}
	wantResult := datastore.RecoveryResult{Replaced: true, DatabasePath: "/tmp/agent-mux.db", QuarantineDir: "/tmp/recovery/now"}
	got, finish, err := coordinateForRebuild(context.Background(), errors.New("corrupt"), dependencies{
		beginGate: func(context.Context) (rebuildGate, error) { return gate, nil },
		newDaemon: func() daemonLifecycle { return &fakeDaemon{calls: &calls} },
		recover:   func(context.Context, error) (datastore.RecoveryResult, error) { return wantResult, nil },
	})
	if err != nil || finish == nil || got != wantResult {
		t.Fatalf("coordinate for rebuild = result %+v finish %v error %v", got, finish != nil, err)
	}
	if len(gate.marked) != 1 || gate.marked[0] != wantResult {
		t.Fatalf("durable replacements = %+v, want [%+v]", gate.marked, wantResult)
	}
	if err := finish(); err != nil {
		t.Fatalf("finish rebuild: %v", err)
	}
}

func TestCoordinateForRebuildDurabilityFailureDoesNotReleaseGate(t *testing.T) {
	calls := []string{}
	markFailure := errors.New("marker sync failed")
	gate := &fakeRebuildGate{calls: &calls, markFailure: markFailure}
	result, finish, err := coordinateForRebuild(context.Background(), errors.New("corrupt"), dependencies{
		beginGate: func(context.Context) (rebuildGate, error) { return gate, nil },
		newDaemon: func() daemonLifecycle { return &fakeDaemon{calls: &calls} },
		recover: func(context.Context, error) (datastore.RecoveryResult, error) {
			return datastore.RecoveryResult{Replaced: true}, nil
		},
	})
	if !result.Replaced || finish != nil || !errors.Is(err, markFailure) {
		t.Fatalf("durability failure = result %+v finish %v error %v", result, finish != nil, err)
	}
	if strings.Contains(strings.Join(calls, ","), "gate-release") {
		t.Fatalf("post-replacement durability failure released gate: %v", calls)
	}
}

func TestCoordinateForRebuildFinishRetriesRestartWithoutReleasingGateAgain(t *testing.T) {
	calls := []string{}
	restartFailure := errors.New("spawn failed")
	restartCalls := 0
	client := &fakeDaemon{health: daemon.Health{Alive: true}, calls: &calls}
	_, finish, err := coordinateForRebuild(context.Background(), errors.New("corrupt"), dependencies{
		beginGate: func(context.Context) (rebuildGate, error) {
			calls = append(calls, "gate-begin")
			return &fakeRebuildGate{calls: &calls}, nil
		},
		newDaemon: func() daemonLifecycle { return client },
		recover: func(context.Context, error) (datastore.RecoveryResult, error) {
			calls = append(calls, "recover")
			return datastore.RecoveryResult{Replaced: true}, nil
		},
		executable: func() (string, error) {
			calls = append(calls, "executable")
			return "/installed/amx", nil
		},
		restart: func(string) error {
			restartCalls++
			calls = append(calls, fmt.Sprintf("restart:%d", restartCalls))
			if restartCalls == 1 {
				return restartFailure
			}
			return nil
		},
	})
	if err != nil || finish == nil {
		t.Fatalf("coordinate for rebuild = finish %v, error %v", finish != nil, err)
	}
	if err := finish(); !errors.Is(err, restartFailure) {
		t.Fatalf("first finish = %v, want restart failure", err)
	}
	if err := finish(); err != nil {
		t.Fatalf("retry finish: %v", err)
	}
	if err := finish(); err != nil {
		t.Fatalf("completed finish retry: %v", err)
	}
	if want := []string{"gate-begin", "health", "gate-intent:true", "executable", "shutdown", "recover", "gate-release", "restart:1", "restart:2"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestCoordinateForRebuildStillReturnsGateFinishWhenDaemonWasStopped(t *testing.T) {
	calls := []string{}
	client := &fakeDaemon{calls: &calls}
	_, finish, err := coordinateForRebuild(context.Background(), errors.New("corrupt"), dependencies{
		beginGate: func(context.Context) (rebuildGate, error) {
			calls = append(calls, "gate-begin")
			return &fakeRebuildGate{calls: &calls}, nil
		},
		newDaemon: func() daemonLifecycle { return client },
		recover: func(context.Context, error) (datastore.RecoveryResult, error) {
			calls = append(calls, "recover")
			return datastore.RecoveryResult{Replaced: true}, nil
		},
	})
	if err != nil || finish == nil {
		t.Fatalf("stopped-daemon rebuild = finish %v, error %v", finish != nil, err)
	}
	if err := finish(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"gate-begin", "health", "gate-intent:false", "recover", "gate-release"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestCoordinateForRebuildWaitsForInflightDaemonStartupBeforeHealth(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	startup, err := datastore.BeginDefaultDaemonStartup(t.Context())
	if err != nil {
		t.Fatalf("begin daemon startup: %v", err)
	}
	t.Cleanup(func() { _ = startup.Release() })

	client := &startupRaceDaemon{
		healthCalled:   make(chan struct{}),
		shutdownCalled: make(chan struct{}),
	}
	recovered := make(chan struct{})
	type result struct {
		finish func() error
		err    error
	}
	resultCh := make(chan result, 1)
	go func() {
		_, finish, err := coordinateForRebuild(t.Context(), errors.New("corrupt"), dependencies{
			beginGate: func(ctx context.Context) (rebuildGate, error) {
				return datastore.BeginDefaultRebuildGateContext(ctx)
			},
			newDaemon: func() daemonLifecycle { return client },
			recover: func(context.Context, error) (datastore.RecoveryResult, error) {
				close(recovered)
				return datastore.RecoveryResult{Replaced: true}, nil
			},
			executable: func() (string, error) { return "/installed/amx", nil },
			restart:    func(string) error { return nil },
		})
		resultCh <- result{finish: finish, err: err}
	}()

	select {
	case <-client.healthCalled:
		t.Fatal("recovery checked daemon health before in-flight startup released serialization")
	case <-time.After(75 * time.Millisecond):
	}

	if err := startup.Release(); err != nil {
		t.Fatalf("release daemon startup: %v", err)
	}
	select {
	case <-client.healthCalled:
	case <-time.After(3 * time.Second):
		t.Fatal("recovery did not check the daemon that won startup serialization")
	}
	select {
	case <-client.shutdownCalled:
	case <-time.After(3 * time.Second):
		t.Fatal("recovery did not stop the daemon that completed startup")
	}
	select {
	case <-recovered:
	case <-time.After(3 * time.Second):
		t.Fatal("replacement did not proceed after the in-flight daemon stopped")
	}

	var got result
	select {
	case got = <-resultCh:
	case <-time.After(3 * time.Second):
		t.Fatal("recovery coordination did not return")
	}
	if got.err != nil || got.finish == nil {
		t.Fatalf("coordinate for rebuild = finish %v, error %v", got.finish != nil, got.err)
	}
	if active, err := datastore.DefaultRebuildGateActive(); err != nil || !active {
		t.Fatalf("rebuild gate before completion = %v, %v; want active", active, err)
	}
	if err := got.finish(); err != nil {
		t.Fatalf("finish rebuild: %v", err)
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
