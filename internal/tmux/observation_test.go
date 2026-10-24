package tmux_test

import (
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux"
)

func observedRecord() (tmux.Survey, runtimesession.Record) {
	instance := runtimesession.BackendInstance{Kind: runtimesession.BackendTmux, Address: "/tmp/amx.sock", Generation: "g1"}
	process := runtimesession.ProcessFingerprint{PID: 42, StartToken: "Mon Oct 19 10:00:00 2026"}
	record := runtimesession.Record{
		Target:  &runtimesession.Target{Instance: instance, Ref: "%0"},
		Process: &process,
	}
	survey := tmux.Survey{
		Instance: instance,
		Running:  true,
		Panes:    []backend.Pane{{Ref: backend.PaneID("%0"), Foreground: []backend.Process{{PID: 42}}}},
	}
	return survey, record
}

func TestAFingerprintThatFailsIsNotEvidenceOfNoProcess(t *testing.T) {
	survey, record := observedRecord()
	failing := func(int) (runtimesession.ProcessFingerprint, error) {
		return runtimesession.ProcessFingerprint{}, errors.New("ps: timed out")
	}
	observation := survey.Observation(record, failing)
	if observation.Process == nil {
		t.Fatal("a failed fingerprint read reported no process; the registry would mark a running agent lost")
	}
	if *observation.Process != *record.Process {
		t.Fatalf("process = %+v, want the registered %+v", *observation.Process, *record.Process)
	}
}

func TestAFingerprintThatReadsIsWhatIsObserved(t *testing.T) {
	survey, record := observedRecord()
	other := runtimesession.ProcessFingerprint{PID: 42, StartToken: "Tue Oct 20 10:00:00 2026"}
	observation := survey.Observation(record, func(int) (runtimesession.ProcessFingerprint, error) { return other, nil })
	if observation.Process == nil || *observation.Process != other {
		t.Fatalf("process = %+v, want the fingerprint read %+v", observation.Process, other)
	}
}
