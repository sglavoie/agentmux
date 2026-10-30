package notification

// The companion app backend, seen from the notifier: every gate still runs
// before it is chosen, a missing or failed delivery falls back to Alerter, a
// click focuses through the same path an Alerter click does, the app's safe
// actions are recorded the way replied is, and a newer event retracts a notice
// the operator no longer needs.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// fakeApp is an AppBackend whose reports the test scripts. respond, when set,
// runs inside Deliver and may report on the notice immediately.
type fakeApp struct {
	mu            sync.Mutex
	ready         bool
	notices       []AppNotice
	retracted     []string
	reasons       []string
	open          map[string]chan AppInteraction
	respond       func(notice AppNotice, report func(string))
	beforeDeliver func(AppNotice)
}

func newFakeApp() *fakeApp {
	return &fakeApp{ready: true, open: make(map[string]chan AppInteraction)}
}

func (a *fakeApp) Ready() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ready
}

func (a *fakeApp) Deliver(_ context.Context, notice AppNotice) (<-chan AppInteraction, error) {
	if a.beforeDeliver != nil {
		a.beforeDeliver(notice)
	}
	a.mu.Lock()
	ch := make(chan AppInteraction, 2)
	a.notices = append(a.notices, notice)
	a.open[notice.NotificationID] = ch
	respond := a.respond
	a.mu.Unlock()
	if respond != nil {
		respond(notice, func(disposition string) { a.report(notice.NotificationID, disposition) })
	}
	return ch, nil
}

// report delivers one disposition for id; a terminal one settles it.
func (a *fakeApp) report(id, disposition string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ch, ok := a.open[id]
	if !ok {
		return
	}
	ch <- AppInteraction{Disposition: disposition}
	if disposition != protocol.DispositionDelivered {
		delete(a.open, id)
	}
}

func (a *fakeApp) Retract(id, reason string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.retracted = append(a.retracted, id)
	a.reasons = append(a.reasons, reason)
	if ch, ok := a.open[id]; ok {
		delete(a.open, id)
		ch <- AppInteraction{Disposition: AppRetracted}
	}
}

func (a *fakeApp) deliveries() []AppNotice {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.notices)
}

func (a *fakeApp) retractions() ([]string, []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.retracted), slices.Clone(a.reasons)
}

// reportAll reports disposition for every open notice.
func (a *fakeApp) reportAll(disposition string) {
	a.mu.Lock()
	ids := make([]string, 0, len(a.open))
	for id := range a.open {
		ids = append(ids, id)
	}
	a.mu.Unlock()
	for _, id := range ids {
		a.report(id, disposition)
	}
}

func deliverThen(terminal string) func(AppNotice, func(string)) {
	return func(_ AppNotice, report func(string)) {
		report(protocol.DispositionDelivered)
		if terminal != "" {
			report(terminal)
		}
	}
}

// backendLog records dispositions with the backend that produced them.
type backendLog struct {
	dispositionLog
	backends map[string]string
}

func (l *backendLog) observe(d NotificationDisposition) {
	l.dispositionLog.observe(d)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.backends == nil {
		l.backends = make(map[string]string)
	}
	l.backends[d.Stage+":"+d.Outcome+":"+d.Reason] = d.Backend
}

func (l *backendLog) backend(entry string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.backends[entry]
}

func newAppTestNotifier(t *testing.T, cfg *config.NotificationConfig, app *fakeApp) (*Notifier, *callRecorder, *backendLog) {
	t.Helper()
	n, rec := newTestNotifier(cfg)
	n.SetAppBackend(app)
	log := &backendLog{}
	n.SetObserver(log.observe)
	return n, rec, log
}

func TestAppBackendClickFocusesWithoutAlerter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := newFakeApp()
		app.respond = deliverThen(protocol.DispositionClicked)
		n, rec, log := newAppTestNotifier(t, &config.NotificationConfig{Stop: true}, app)
		focus := &fakeFocusClient{live: focusTestWindows(42)}
		n.SetFocus(focus)

		n.MaybeNotifyTracedTarget(focusTarget(42, "work", "unix:/tmp/kitty-a"), "W:api", "stop", false, NotificationTrace{SessionID: "s-1"})
		synctest.Wait()

		if rec.len() != 0 {
			t.Fatalf("alerter dispatched %d times although the app delivered", rec.len())
		}
		notices := app.deliveries()
		if len(notices) != 1 {
			t.Fatalf("app notices = %d, want 1", len(notices))
		}
		got := notices[0]
		if got.NotificationID == "" || got.WindowID != 42 || !got.Navigable || got.Title != "W:api" || got.Subtitle != "Session stopped" || got.Action != "stop" || got.SessionID != "s-1" {
			t.Fatalf("notice = %+v", got)
		}
		if !slices.Equal(got.Actions, []string{protocol.NotificationActionMute, protocol.NotificationActionSnooze}) {
			t.Fatalf("actions = %v", got.Actions)
		}
		if !log.has("dispatch:sent:send_success") || log.backend("dispatch:sent:send_success") != BackendApp {
			t.Fatalf("no app send_success disposition: %v", log.entries)
		}
		if !log.has("focus:focused:focus_success") || log.backend("focus:focused:focus_success") != BackendApp {
			t.Fatalf("click did not focus through the app path: %v", log.entries)
		}
		tabs, windows := focus.snapshot()
		if len(tabs) != 1 || len(windows) != 1 {
			t.Fatalf("focus calls = %v / %v", tabs, windows)
		}
		if log.has("dispatch:fallback:app_fallback") {
			t.Fatal("a delivered notice recorded app_fallback")
		}
	})
}

func TestAppBackendMissingAckFallsBackToAlerter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := newFakeApp() // never reports anything
		n, rec, log := newAppTestNotifier(t, &config.NotificationConfig{Stop: true}, app)

		n.MaybeNotify(1, "W:api", "work", "stop", false)
		synctest.Wait()
		if rec.len() != 0 {
			t.Fatal("alerter ran before the delivered window elapsed")
		}
		time.Sleep(appDeliveredTimeout)
		synctest.Wait()

		if rec.len() != 1 {
			t.Fatalf("alerter dispatches = %d, want 1 after the missing ack", rec.len())
		}
		if !log.has("dispatch:fallback:app_fallback") {
			t.Fatalf("no app_fallback disposition: %v", log.entries)
		}
		if log.backend("dispatch:sent:send_success") != BackendAlerter {
			t.Fatalf("send_success backend = %q, want alerter", log.backend("dispatch:sent:send_success"))
		}
		ids, reasons := app.retractions()
		if len(ids) != 1 || ids[0] != app.deliveries()[0].NotificationID || reasons[0] != "unconfirmed" {
			t.Fatalf("retractions = %v %v; the unconfirmed notice must be withdrawn", ids, reasons)
		}
	})
}

func TestAppBackendFailedDeliveryFallsBackToAlerter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := newFakeApp()
		app.respond = func(_ AppNotice, report func(string)) { report(protocol.DispositionFailed) }
		n, rec, log := newAppTestNotifier(t, &config.NotificationConfig{Stop: true}, app)

		n.MaybeNotify(1, "W:api", "work", "stop", false)
		synctest.Wait()

		if rec.len() != 1 || !log.has("dispatch:fallback:app_fallback") {
			t.Fatalf("alerter dispatches = %d, dispositions = %v; want a fallback", rec.len(), log.entries)
		}
	})
}

func TestAppBackendNotReadyUsesAlerter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := newFakeApp()
		app.ready = false
		n, rec, log := newAppTestNotifier(t, &config.NotificationConfig{Stop: true}, app)

		n.MaybeNotify(1, "W:api", "work", "stop", false)
		synctest.Wait()

		if len(app.deliveries()) != 0 || rec.len() != 1 {
			t.Fatalf("app notices = %d, alerter dispatches = %d; want alerter only", len(app.deliveries()), rec.len())
		}
		if log.has("dispatch:fallback:app_fallback") {
			t.Fatal("an app that was never ready recorded app_fallback")
		}
	})
}

func TestAppBackendAvailableWithoutAlerter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := newFakeApp()
		app.respond = deliverThen(protocol.DispositionDismissed)
		n, _, log := newAppTestNotifier(t, &config.NotificationConfig{Stop: true}, app)
		n.dispatch = nil

		n.MaybeNotify(1, "W:api", "work", "stop", false)
		synctest.Wait()

		if log.has("decision:suppressed:backend_unavailable") || log.has("dispatch:suppressed:backend_unavailable") {
			t.Fatalf("backend_unavailable with a ready app: %v", log.entries)
		}
		if !log.has("focus:suppressed:dismissed") {
			t.Fatalf("dismissal not recorded: %v", log.entries)
		}
	})
}

func TestAppBackendFallbackWithoutAlerterIsBackendUnavailable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := newFakeApp()
		app.respond = func(_ AppNotice, report func(string)) { report(protocol.DispositionFailed) }
		n, _, log := newAppTestNotifier(t, &config.NotificationConfig{Stop: true}, app)
		n.dispatch = nil

		n.MaybeNotify(1, "W:api", "work", "stop", false)
		synctest.Wait()

		if !log.has("dispatch:fallback:app_fallback") || !log.has("dispatch:suppressed:backend_unavailable") {
			t.Fatalf("dispositions = %v; want app_fallback then backend_unavailable", log.entries)
		}
	})
}

// TestAppBackendEveryGateStillSuppresses: no gate is bypassed because a
// companion would deliver; the app never sees a suppressed notification.
func TestAppBackendEveryGateStillSuppresses(t *testing.T) {
	quietNow := time.Date(2026, 10, 2, 23, 0, 0, 0, time.Local)
	cases := []struct {
		name   string
		cfg    config.NotificationConfig
		action string
		pipe   bool
		setup  func(*Notifier)
		want   string
	}{
		{name: "pipe_mode", cfg: config.NotificationConfig{Stop: true}, action: "stop", pipe: true, want: "pipe_mode"},
		{name: "muted", cfg: config.NotificationConfig{Stop: true}, action: "stop", setup: func(n *Notifier) { n.SetIsMuted(func(int) bool { return true }) }, want: "muted"},
		{name: "unsupported_action", cfg: config.NotificationConfig{Stop: true}, action: "active", want: "unsupported_action"},
		{name: "config_disabled", cfg: config.NotificationConfig{Stop: false, Error: true}, action: "stop", want: "config_disabled"},
		{name: "quiet_hours", cfg: config.NotificationConfig{Stop: true, QuietHours: "22:00-08:00"}, action: "stop", setup: func(n *Notifier) { n.now = func() time.Time { return quietNow } }, want: "quiet_hours"},
		{name: "snoozed", cfg: config.NotificationConfig{Stop: true}, action: "stop", setup: func(n *Notifier) {
			if _, err := n.SetSnooze(protocol.SnoozeModeIndefinite, time.Time{}); err != nil {
				panic(err)
			}
		}, want: "snoozed"},
		{name: "cooldown", cfg: config.NotificationConfig{Stop: true}, action: "stop", setup: func(n *Notifier) { n.MarkNotified(1, "stop", n.now()) }, want: "cooldown"},
		{name: "focus_unavailable", cfg: config.NotificationConfig{Stop: true}, action: "stop", setup: func(n *Notifier) { n.SetFocus(&fakeFocusClient{}) }, want: "focus_unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				app := newFakeApp()
				app.respond = deliverThen(protocol.DispositionDismissed)
				cfg := tc.cfg
				n, rec, log := newAppTestNotifier(t, &cfg, app)
				if tc.setup != nil {
					tc.setup(n)
				}
				target := NotificationTarget{WindowID: 1, Account: "work", Navigable: true, KittyListenOn: "unix:/tmp/kitty-a"}
				if tc.want == "focus_unavailable" {
					target.KittyListenOn = ""
				}
				n.MaybeNotifyTracedTarget(target, "W:api", tc.action, tc.pipe, NotificationTrace{})
				synctest.Wait()

				if len(app.deliveries()) != 0 || rec.len() != 0 {
					t.Fatalf("app notices = %d, alerter = %d; want both suppressed", len(app.deliveries()), rec.len())
				}
				if !log.has("decision:suppressed:" + tc.want) {
					t.Fatalf("dispositions = %v; want decision:suppressed:%s", log.entries, tc.want)
				}
			})
		})
	}
}

func TestAppBackendDebouncedPermissionStillDebounces(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := newFakeApp()
		app.respond = deliverThen(protocol.DispositionDismissed)
		n, _, log := newAppTestNotifier(t, &config.NotificationConfig{Permission: true, PermissionDebounceMs: 200}, app)

		n.MaybeNotify(1, "W:api", "work", "permission", false)
		n.CancelPending(1)
		time.Sleep(time.Second)
		synctest.Wait()
		if len(app.deliveries()) != 0 {
			t.Fatal("a cancelled debouncing permission reached the app")
		}

		n.MaybeNotify(1, "W:api", "work", "permission", false)
		time.Sleep(time.Second)
		synctest.Wait()
		notices := app.deliveries()
		if len(notices) != 1 || len(notices[0].Actions) != 0 {
			t.Fatalf("notices = %+v; want one permission notice offering no actions", notices)
		}
		if !log.has("focus:suppressed:dismissed") {
			t.Fatalf("dispositions = %v", log.entries)
		}
	})
}

// TestAppBackendRecordsSafeActionsLikeReplied: mute, snooze, and answer end a
// notification without a click and are recorded by name.
func TestAppBackendRecordsSafeActionsLikeReplied(t *testing.T) {
	for _, disposition := range []string{protocol.DispositionActionMute, protocol.DispositionActionSnooze, protocol.DispositionActionAnswer, protocol.DispositionActionRespond} {
		t.Run(disposition, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				app := newFakeApp()
				app.respond = deliverThen(disposition)
				n, _, log := newAppTestNotifier(t, &config.NotificationConfig{Stop: true}, app)
				focus := &fakeFocusClient{live: focusTestWindows(1)}
				n.SetFocus(focus)

				n.MaybeNotifyTracedTarget(focusTarget(1, "work", "unix:/tmp/kitty-a"), "W:api", "stop", false, NotificationTrace{})
				synctest.Wait()

				if !log.has("focus:suppressed:" + disposition) {
					t.Fatalf("dispositions = %v; want focus:suppressed:%s", log.entries, disposition)
				}
				if tabs, _ := focus.snapshot(); len(tabs) != 0 {
					t.Fatalf("%s focused a window", disposition)
				}
			})
		})
	}
}

// A permission prompt is never answered from a banner. On a session that can
// be written to with nothing attached, the banner may offer to show the
// prompt and take the answer there; anywhere else it offers nothing.
func TestAppBackendOffersRespondOnlyWhereAPromptCanBeAnsweredInPlace(t *testing.T) {
	tmux := NotificationTarget{WindowID: 1 << 40, RuntimeSessionID: "rs-0123456789abcdef0123456789abcdef", Backend: BackendKindTmux}
	if got := appActions(tmux, "permission", NotificationTrace{}); !slices.Equal(got, []string{protocol.NotificationActionRespond}) {
		t.Fatalf("tmux permission actions = %v; want respond alone", got)
	}
	for name, target := range map[string]NotificationTarget{
		"a Kitty window":          {WindowID: 3, Navigable: true},
		"a Kitty runtime session": {WindowID: 3, Navigable: true, RuntimeSessionID: "rs-0123456789abcdef0123456789abcdef", Backend: BackendKindKitty},
		"tmux with no session":    {WindowID: 3, Backend: BackendKindTmux},
	} {
		if got := appActions(target, "permission", NotificationTrace{}); len(got) != 0 {
			t.Errorf("%s: permission actions = %v; want none", name, got)
		}
	}
	if got := appActions(tmux, "stop", NotificationTrace{}); slices.Contains(got, protocol.NotificationActionRespond) {
		t.Fatalf("stop actions = %v; respond is for a prompt", got)
	}
}

// A tmux session has no Kitty window, but the app mutes it by its runtime
// session, so its banners offer Mute like a Kitty window's.
func TestAppBackendOffersMuteForRuntimeSessions(t *testing.T) {
	want := []string{protocol.NotificationActionMute, protocol.NotificationActionSnooze}
	for name, target := range map[string]NotificationTarget{
		"a tmux session":                {WindowID: 1 << 40, Navigable: true, RuntimeSessionID: "rs-0123456789abcdef0123456789abcdef", Backend: BackendKindTmux},
		"a tmux session with no window": {Navigable: true, RuntimeSessionID: "rs-0123456789abcdef0123456789abcdef", Backend: BackendKindTmux},
		"a Kitty window":                {WindowID: 3, Navigable: true},
	} {
		if got := appActions(target, "stop", NotificationTrace{}); !slices.Equal(got, want) {
			t.Errorf("%s: stop actions = %v; want %v", name, got, want)
		}
	}
	if got := appActions(NotificationTarget{WindowID: 1 << 40, RuntimeSessionID: "rs-0123456789abcdef0123456789abcdef", Backend: BackendKindTmux}, "stop", NotificationTrace{}); slices.Contains(got, protocol.NotificationActionMute) {
		t.Fatalf("a session nothing can show offered mute: %v", got)
	}
	tmux := NotificationTarget{WindowID: 1 << 40, Navigable: true, RuntimeSessionID: "rs-0123456789abcdef0123456789abcdef", Backend: BackendKindTmux}
	if got := appActions(tmux, "permission", NotificationTrace{}); !slices.Equal(got, []string{protocol.NotificationActionRespond}) {
		t.Fatalf("tmux permission actions = %v; want respond alone", got)
	}
}

func TestAppBackendOffersAnswerOnlyForASLQuestions(t *testing.T) {
	target := NotificationTarget{WindowID: 3, Navigable: true}
	if got := appActions(target, "elicitation", NotificationTrace{RunID: "run-1"}); !slices.Contains(got, protocol.NotificationActionAnswer) {
		t.Fatalf("ASL question actions = %v; want answer", got)
	}
	if got := appActions(target, "elicitation", NotificationTrace{}); slices.Contains(got, protocol.NotificationActionAnswer) {
		t.Fatalf("plain elicitation actions = %v; want no answer", got)
	}
	if got := appActions(NotificationTarget{WindowID: -1}, "hook_silence", NotificationTrace{}); !slices.Equal(got, []string{protocol.NotificationActionSnooze}) {
		t.Fatalf("profile-wide actions = %v; want snooze only", got)
	}
}

func TestAppBackendRetractsOnSupersede(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := newFakeApp()
		app.respond = deliverThen("")
		n, _, log := newAppTestNotifier(t, &config.NotificationConfig{Stop: true, Error: true}, app)

		n.MaybeNotify(1, "W:api", "work", "stop", false)
		synctest.Wait()
		first := app.deliveries()[0].NotificationID

		n.MaybeNotify(1, "W:api", "work", "failure", false)
		synctest.Wait()

		ids, reasons := app.retractions()
		if len(ids) != 1 || ids[0] != first || reasons[0] != "superseded" {
			t.Fatalf("retractions = %v %v; want the stop notice superseded", ids, reasons)
		}
		if !log.has("focus:suppressed:retracted") {
			t.Fatalf("dispositions = %v; want the superseded notice settled as retracted", log.entries)
		}
		if len(app.deliveries()) != 2 {
			t.Fatalf("notices = %d; want the failure delivered too", len(app.deliveries()))
		}
		app.reportAll(protocol.DispositionDismissed)
		synctest.Wait()
	})
}

// Cancellation can arrive while the backend is still accepting the notice,
// before Deliver returns. It must withdraw that notice once acceptance ends,
// without showing the obsolete event through Alerter.
func TestAppBackendCancellationDuringDelivery(t *testing.T) {
	for _, beforeAcceptance := range []bool{true, false} {
		for _, reason := range []string{"superseded", "window_closed"} {
			name := fmt.Sprintf("before_acceptance=%t/%s", beforeAcceptance, reason)
			t.Run(name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					app := newFakeApp()
					n, rec, log := newAppTestNotifier(t, &config.NotificationConfig{Stop: true}, app)
					defer func() {
						app.reportAll(protocol.DispositionDismissed)
						synctest.Wait()
					}()
					cancel := func() {
						if reason == "window_closed" {
							n.PurgeWindow(1)
						} else {
							n.CancelPending(1)
						}
					}
					if beforeAcceptance {
						app.beforeDeliver = func(AppNotice) { cancel() }
					}
					app.respond = func(_ AppNotice, report func(string)) {
						report(protocol.DispositionDelivered)
						if !beforeAcceptance {
							cancel()
						}
					}
					n.MaybeNotify(1, "W:api", "work", "stop", false)
					synctest.Wait()
					ids, reasons := app.retractions()
					if len(ids) != 1 || ids[0] != app.deliveries()[0].NotificationID || reasons[0] != reason {
						t.Fatalf("retractions = %v %v; want the notice withdrawn once as %s", ids, reasons, reason)
					}
					if rec.len() != 0 || log.has("dispatch:fallback:app_fallback") {
						t.Fatal("a cancelled notice fell back to Alerter")
					}
					if !log.has("dispatch:cancelled:retracted") {
						t.Fatalf("no cancellation recorded: %v", log.entries)
					}
				})
			})
		}
	}
}

func TestAppBackendCancellationDuringDeliveryWaitsForFinish(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := newFakeApp()
		n, _, log := newAppTestNotifier(t, &config.NotificationConfig{Stop: true}, app)
		defer func() {
			app.reportAll(protocol.DispositionDismissed)
			synctest.Wait()
		}()
		var pending PendingCancellation
		app.beforeDeliver = func(AppNotice) { pending = n.CancelPendingDisposition(1) }
		app.respond = deliverThen("")
		n.MaybeNotify(1, "W:api", "work", "stop", false)
		synctest.Wait()
		if ids, _ := app.retractions(); len(ids) != 0 {
			t.Fatalf("a cancellation retracted %v before FinishCancellation", ids)
		}
		n.FinishCancellation(pending)
		synctest.Wait()
		ids, reasons := app.retractions()
		if len(ids) != 1 || ids[0] != app.deliveries()[0].NotificationID || reasons[0] != "superseded" {
			t.Fatalf("retractions = %v %v; want the accepted notice superseded once", ids, reasons)
		}
		if !log.has("focus:suppressed:retracted") {
			t.Fatalf("no settled retraction recorded: %v", log.entries)
		}
	})
}

func TestAppBackendSuppressedEventDoesNotRetract(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := newFakeApp()
		app.respond = deliverThen("")
		n, _, _ := newAppTestNotifier(t, &config.NotificationConfig{Stop: true}, app)

		n.MaybeNotify(1, "W:api", "work", "stop", false)
		synctest.Wait()
		n.MaybeNotify(1, "W:api", "work", "stop", false) // inside the cooldown
		synctest.Wait()

		if ids, _ := app.retractions(); len(ids) != 0 {
			t.Fatalf("a suppressed event retracted %v; the operator would be left with nothing", ids)
		}
		app.reportAll(protocol.DispositionDismissed)
		synctest.Wait()
	})
}

func TestAppBackendRetractsOnCancelPendingAndWindowClose(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cancel func(*Notifier)
		reason string
	}{
		{"cancel_pending", func(n *Notifier) { n.CancelPending(1) }, "superseded"},
		{"cancel_pending_disposition", func(n *Notifier) { n.FinishCancellation(n.CancelPendingDisposition(1)) }, "superseded"},
		{"purge_window", func(n *Notifier) { n.PurgeWindow(1) }, "window_closed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				app := newFakeApp()
				app.respond = deliverThen("")
				n, _, log := newAppTestNotifier(t, &config.NotificationConfig{Stop: true}, app)

				n.MaybeNotify(1, "W:api", "work", "stop", false)
				n.MaybeNotify(2, "W:web", "work", "stop", false)
				synctest.Wait()
				tc.cancel(n)
				synctest.Wait()

				ids, reasons := app.retractions()
				if len(ids) != 1 || reasons[0] != tc.reason {
					t.Fatalf("retractions = %v %v; want window 1's notice only, reason %s", ids, reasons, tc.reason)
				}
				if !log.has("focus:suppressed:retracted") {
					t.Fatalf("dispositions = %v", log.entries)
				}
				app.reportAll(protocol.DispositionDismissed)
				synctest.Wait()
			})
		})
	}
}

// CancelPendingDisposition only takes the notices, so a caller holding its own
// locks never reaches the companion; FinishCancellation retracts them.
func TestAppBackendCancelPendingDispositionDefersRetraction(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := newFakeApp()
		app.respond = deliverThen("")
		n, _, _ := newAppTestNotifier(t, &config.NotificationConfig{Stop: true}, app)

		n.MaybeNotify(1, "W:api", "work", "stop", false)
		synctest.Wait()
		cancellation := n.CancelPendingDisposition(1)
		synctest.Wait()
		if ids, _ := app.retractions(); len(ids) != 0 {
			t.Fatalf("CancelPendingDisposition retracted %v before FinishCancellation", ids)
		}
		n.FinishCancellation(cancellation)
		synctest.Wait()
		if ids, reasons := app.retractions(); len(ids) != 1 || reasons[0] != "superseded" {
			t.Fatalf("retractions = %v %v; want the window's notice, superseded", ids, reasons)
		}
		app.reportAll(protocol.DispositionDismissed)
		synctest.Wait()
	})
}

func TestAppBackendInteractionTimeoutRetracts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := newFakeApp()
		app.respond = deliverThen("")
		n, _, log := newAppTestNotifier(t, &config.NotificationConfig{Stop: true}, app)

		n.MaybeNotify(1, "W:api", "work", "stop", false)
		synctest.Wait()
		time.Sleep(n.dispatchTimeout)
		synctest.Wait()

		if !log.has("focus:suppressed:interaction_timeout") {
			t.Fatalf("dispositions = %v", log.entries)
		}
		if _, reasons := app.retractions(); len(reasons) != 1 || reasons[0] != "interaction_timeout" {
			t.Fatalf("retraction reasons = %v", reasons)
		}
	})
}

func TestTestAppDeliveryReportsTheApp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := newTestNotifier(&config.NotificationConfig{})
		if got := n.TestAppDelivery(context.Background()); got.Delivered || got.Reason != "app_unavailable" {
			t.Fatalf("no app: %+v", got)
		}

		app := newFakeApp()
		app.respond = deliverThen("")
		n.SetAppBackend(app)
		if got := n.TestAppDelivery(context.Background()); !got.Delivered || got.Backend != BackendApp {
			t.Fatalf("delivering app: %+v", got)
		}

		silent := newFakeApp()
		n.SetAppBackend(silent)
		if got := n.TestAppDelivery(context.Background()); got.Delivered || got.Reason != "app_unconfirmed" {
			t.Fatalf("silent app: %+v", got)
		}
		if ids, _ := silent.retractions(); len(ids) != 1 {
			t.Fatal("an unconfirmed test notice was not withdrawn")
		}
	})
}

// TestAppNoticePreservesKittySocket: window IDs repeat across Kitty instances,
// so the notice carries the socket the daemon captured for its target, and the
// app can raise and mute the pane that actually raised it.
func TestAppNoticePreservesKittySocket(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := newFakeApp()
		app.respond = deliverThen(protocol.DispositionDismissed)
		n, _, _ := newAppTestNotifier(t, &config.NotificationConfig{Stop: true}, app)
		n.SetFocus(&fakeFocusClient{live: focusTestWindows(4)})

		n.MaybeNotifyTracedTarget(focusTarget(4, "work", "unix:/tmp/kitty-b"), "W:api", "stop", false, NotificationTrace{})
		synctest.Wait()

		notices := app.deliveries()
		if len(notices) != 1 {
			t.Fatalf("notices = %+v; want one", notices)
		}
		if got := notices[0].KittySocket; got != "unix:/tmp/kitty-b" {
			t.Fatalf("notice socket = %q; want the target's unix:/tmp/kitty-b", got)
		}
		data, err := json.Marshal(notices[0])
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]any
		if err := json.Unmarshal(data, &wire); err != nil {
			t.Fatal(err)
		}
		if wire["kitty_socket"] != "unix:/tmp/kitty-b" || wire["window_id"] != float64(4) {
			t.Fatalf("frame = %s; want kitty_socket and window_id together", data)
		}
	})
}

// A notice for a runtime session on a backend other than Kitty names the
// session and nothing Kitty could act on: no window ID, no socket. It offers
// mute, which the app carries out by runtime session. It is navigable by its runtime session, which the app asks the daemon to show
// (focus_session), so the click is recorded as delegated: the notifier shows
// nothing a second time and never reaches the Kitty focus client.
func TestAppNoticeForANonKittyRuntimeSessionCarriesNoKittyTarget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := newFakeApp()
		app.respond = deliverThen(protocol.DispositionClicked)
		n, rec, log := newAppTestNotifier(t, &config.NotificationConfig{Permission: true}, app)
		focus := &fakeFocusClient{live: focusTestWindows(42)}
		n.SetFocus(focus)
		var shown []string
		n.SetSessionFocus(func(id string) error { shown = append(shown, id); return nil })

		const slot = 1<<40 + 3
		n.MaybeNotifyTracedTarget(NotificationTarget{
			WindowID: slot, Account: "work", Navigable: true, RuntimeSessionID: "rs-0123456789abcdef0123456789abcdef", Backend: "tmux",
		}, "W:api", "elicitation", false, NotificationTrace{RunID: "run-1"})
		synctest.Wait()

		notices := app.deliveries()
		if len(notices) != 1 || rec.len() != 0 {
			t.Fatalf("app notices = %d, alerter dispatches = %d; want 1 and 0", len(notices), rec.len())
		}
		got := notices[0]
		if got.WindowID != 0 || got.KittySocket != "" || !got.Navigable ||
			got.RuntimeSessionID != "rs-0123456789abcdef0123456789abcdef" || got.Backend != "tmux" || got.RunID != "run-1" {
			t.Fatalf("notice = %+v", got)
		}
		if !slices.Equal(got.Actions, []string{protocol.NotificationActionMute, protocol.NotificationActionSnooze, protocol.NotificationActionAnswer}) {
			t.Fatalf("actions = %v; want mute by runtime session, snooze, and answer", got.Actions)
		}
		if !log.has("focus:suppressed:focus_delegated") || len(shown) != 0 {
			t.Fatalf("click on the app's non-Kitty notice: log %v, shown %v; want it left to the app", log.entries, shown)
		}
		if tabs, windows := focus.snapshot(); len(tabs) != 0 || len(windows) != 0 {
			t.Fatalf("a non-Kitty notice reached the Kitty focus client: %v / %v", tabs, windows)
		}
	})
}

// A Kitty runtime session's notice keeps its window and socket and adds the
// session's identity.
func TestAppNoticeForAKittyRuntimeSessionKeepsItsWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := newFakeApp()
		app.respond = deliverThen(protocol.DispositionDismissed)
		n, _, _ := newAppTestNotifier(t, &config.NotificationConfig{Stop: true}, app)
		target := focusTarget(42, "work", "unix:/tmp/kitty-a")
		target.RuntimeSessionID = "rs-0123456789abcdef0123456789abcdef"
		target.Backend = BackendKindKitty
		n.MaybeNotifyTracedTarget(target, "W:api", "stop", false, NotificationTrace{})
		synctest.Wait()

		notices := app.deliveries()
		if len(notices) != 1 {
			t.Fatalf("app notices = %d, want 1", len(notices))
		}
		if got := notices[0]; got.WindowID != 42 || got.KittySocket != "unix:/tmp/kitty-a" || !got.Navigable ||
			got.RuntimeSessionID != target.RuntimeSessionID || got.Backend != BackendKindKitty {
			t.Fatalf("notice = %+v", got)
		}
	})
}
