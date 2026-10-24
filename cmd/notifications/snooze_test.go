package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// fakeSnoozeClient records every request and answers with state or err.
type fakeSnoozeClient struct {
	requests []protocol.SnoozeRequest
	state    protocol.SnoozeState
	err      error
}

func (f *fakeSnoozeClient) NotificationSnooze(req protocol.SnoozeRequest) (protocol.SnoozeState, error) {
	f.requests = append(f.requests, req)
	return f.state, f.err
}

var snoozeTestNow = time.Date(2026, 10, 3, 14, 0, 0, 0, time.UTC)

func useSnoozeClient(t *testing.T, client *fakeSnoozeClient) {
	t.Helper()
	resetNotificationsTestState(t)
	notificationsSnoozeClient = func() snoozeClient { return client }
	notificationsNow = func() time.Time { return snoozeTestNow }
}

func runSnooze(t *testing.T, jsonOut bool, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	notificationsSnoozeCmd.SetOut(&out)
	snoozeJSON = jsonOut
	err := notificationsSnoozeCmd.RunE(notificationsSnoozeCmd, args)
	return out.String(), err
}

func TestParseSnoozeArgs(t *testing.T) {
	cases := []struct {
		args []string
		want protocol.SnoozeRequest
	}{
		{[]string{"1h"}, protocol.SnoozeRequest{Mode: protocol.SnoozeModeUntil, Until: snoozeTestNow.Add(time.Hour).Unix()}},
		{[]string{"1h30m"}, protocol.SnoozeRequest{Mode: protocol.SnoozeModeUntil, Until: snoozeTestNow.Add(90 * time.Minute).Unix()}},
		{[]string{"until", "16:30"}, protocol.SnoozeRequest{Mode: protocol.SnoozeModeUntil, Until: time.Date(2026, 10, 3, 16, 30, 0, 0, time.UTC).Unix()}},
		// A time already behind us today means tomorrow, and so does now itself.
		{[]string{"until", "08:00"}, protocol.SnoozeRequest{Mode: protocol.SnoozeModeUntil, Until: time.Date(2026, 10, 4, 8, 0, 0, 0, time.UTC).Unix()}},
		{[]string{"until", "14:00"}, protocol.SnoozeRequest{Mode: protocol.SnoozeModeUntil, Until: time.Date(2026, 10, 4, 14, 0, 0, 0, time.UTC).Unix()}},
		{[]string{"indefinite"}, protocol.SnoozeRequest{Mode: protocol.SnoozeModeIndefinite}},
		{[]string{"off"}, protocol.SnoozeRequest{Mode: protocol.SnoozeModeClear}},
	}
	for _, tc := range cases {
		got, err := parseSnoozeArgs(tc.args, snoozeTestNow)
		if err != nil || got != tc.want {
			t.Errorf("parseSnoozeArgs(%q) = %+v, %v; want %+v", tc.args, got, err, tc.want)
		}
	}
	for _, bad := range [][]string{{"0s"}, {"-1h"}, {"soon"}, {"until"}, {"until", "25:00"}, {"after", "08:00"}} {
		if got, err := parseSnoozeArgs(bad, snoozeTestNow); err == nil {
			t.Errorf("parseSnoozeArgs(%q) = %+v; want an error", bad, got)
		}
	}
}

func TestSnoozeCommandSendsRequestAndPrints(t *testing.T) {
	until := snoozeTestNow.Add(time.Hour).Unix()
	client := &fakeSnoozeClient{state: protocol.SnoozeState{SnoozedUntil: until}}
	useSnoozeClient(t, client)

	out, err := runSnooze(t, false, "1h")
	if err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 1 || client.requests[0] != (protocol.SnoozeRequest{Mode: protocol.SnoozeModeUntil, Until: until}) {
		t.Fatalf("requests = %+v", client.requests)
	}
	if out != "Notifications are snoozed until 2026-10-03 15:00.\n" {
		t.Fatalf("output = %q", out)
	}
}

func TestSnoozeCommandWithoutArgumentsReadsTheSnooze(t *testing.T) {
	until := snoozeTestNow.Add(30 * time.Minute).Unix()
	client := &fakeSnoozeClient{state: protocol.SnoozeState{SnoozedUntil: until}}
	useSnoozeClient(t, client)

	out, err := runSnooze(t, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 1 || client.requests[0] != (protocol.SnoozeRequest{}) {
		t.Fatalf("requests = %+v; want one read with no mode", client.requests)
	}
	if out != "Notifications are snoozed until 2026-10-03 14:30.\n" {
		t.Fatalf("output = %q", out)
	}
}

func TestSnoozeCommandJSONContract(t *testing.T) {
	until := snoozeTestNow.Add(time.Hour).Unix()
	cases := map[string]struct {
		state protocol.SnoozeState
		args  []string
		want  string
	}{
		"timed":      {protocol.SnoozeState{SnoozedUntil: until}, []string{"1h"}, `{"schema_version":1,"data":{"snoozed":true,"indefinite":false,"snoozed_until":` + jsonInt(until) + `,"until":"2026-10-03T15:00:00Z"}}`},
		"indefinite": {protocol.SnoozeState{Indefinite: true}, []string{"indefinite"}, `{"schema_version":1,"data":{"snoozed":true,"indefinite":true}}`},
		"off":        {protocol.SnoozeState{}, []string{"off"}, `{"schema_version":1,"data":{"snoozed":false,"indefinite":false}}`},
		"read":       {protocol.SnoozeState{Indefinite: true}, nil, `{"schema_version":1,"data":{"snoozed":true,"indefinite":true}}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			useSnoozeClient(t, &fakeSnoozeClient{state: tc.state})
			out, err := runSnooze(t, true, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(out); got != tc.want {
				t.Fatalf("json = %s\nwant   %s", got, tc.want)
			}
		})
	}
}

func jsonInt(v int64) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func TestSnoozeCommandFailuresUseTheErrorEnvelope(t *testing.T) {
	cases := map[string]struct {
		client  *fakeSnoozeClient
		args    []string
		alive   bool
		code    string
		message string
	}{
		"invalid": {&fakeSnoozeClient{}, []string{"soon"}, true, "invalid_arguments", "not a duration"},
		// A daemon that is not there is said in plain words, never as the
		// socket error that revealed it.
		"not running": {&fakeSnoozeClient{err: errors.New("dial unix /tmp/amx.sock: no such file")}, []string{"1h"}, false, "daemon_not_running", cli.DaemonNotRunningMessage},
		"refused":     {&fakeSnoozeClient{err: errors.New("daemon error: notification_snooze: unknown snooze mode")}, []string{"1h"}, true, "daemon_error", "unknown snooze mode"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			useSnoozeClient(t, tc.client)
			notificationsHealth = func() ipc.Health { return ipc.Health{Alive: tc.alive} }
			out, err := runSnooze(t, true, tc.args...)
			var exit *cli.ExitError
			if !errors.As(err, &exit) || exit.Code == 0 || !exit.Quiet {
				t.Fatalf("err = %v; want a quiet nonzero exit", err)
			}
			var envelope cli.JSONErrorEnvelope
			if decodeErr := json.Unmarshal([]byte(out), &envelope); decodeErr != nil {
				t.Fatalf("decoding %q: %v", out, decodeErr)
			}
			if envelope.SchemaVersion != 1 || envelope.Error.Code != tc.code || !strings.Contains(envelope.Error.Message, tc.message) {
				t.Fatalf("envelope = %+v; want code %s mentioning %q", envelope, tc.code, tc.message)
			}
			if name == "invalid" && len(tc.client.requests) != 0 {
				t.Fatal("an invalid argument still reached the daemon")
			}

			// Text mode fails with the same nonzero status and no envelope.
			useSnoozeClient(t, tc.client)
			notificationsHealth = func() ipc.Health { return ipc.Health{Alive: tc.alive} }
			out, err = runSnooze(t, false, tc.args...)
			if err == nil || out != "" || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("text mode: out = %q, err = %v; want a bare error mentioning %q", out, err, tc.message)
			}
		})
	}
}

func TestNotificationsTestReportsSnoozeAsSuppressed(t *testing.T) {
	client := &fakeSnoozeClient{state: protocol.SnoozeState{SnoozedUntil: snoozeTestNow.Add(time.Hour).Unix()}}
	useSnoozeClient(t, client)
	notificationsLoadConfig = func() (*config.Config, error) { return &config.Config{}, nil }
	sent := false
	notificationsTestDelivery = func(context.Context, *config.NotificationConfig) (notification.NotificationDeliveryTestResult, error) {
		sent = true
		return notification.NotificationDeliveryTestResult{}, nil
	}
	var out bytes.Buffer
	notificationsTestCmd.SetOut(&out)

	err := notificationsTestCmd.RunE(notificationsTestCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "suppressed") || !strings.Contains(err.Error(), "snooze off") {
		t.Fatalf("err = %v; want a suppressed error naming `snooze off`", err)
	}
	if sent {
		t.Fatal("a snoozed test still sent a notification")
	}
	if len(client.requests) != 1 || client.requests[0] != (protocol.SnoozeRequest{}) {
		t.Fatalf("requests = %+v; want one read", client.requests)
	}

	// Cleared: the same test goes through.
	client.state = protocol.SnoozeState{}
	notificationsTestDelivery = func(context.Context, *config.NotificationConfig) (notification.NotificationDeliveryTestResult, error) {
		sent = true
		return notification.NotificationDeliveryTestResult{Capability: protocol.NotificationCapability{Backend: "alerter"}}, nil
	}
	if err := notificationsTestCmd.RunE(notificationsTestCmd, nil); err != nil || !sent {
		t.Fatalf("after clear: err = %v, sent = %v", err, sent)
	}
}
