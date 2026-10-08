// Package notification decides whether, how, and through which backend the
// daemon tells the operator something. It owns the notification settings,
// the Alerter backend and its capability probe, the delivery decision
// pipeline (gates, permission debounce, cooldown), the global snooze,
// click-to-focus, and the disposition vocabulary.
//
// The snooze (snooze.go) is held in memory only and never written to config.
// It is gated right after quiet hours, and re-checked when a debounced
// permission notification fires.
//
// The package also owns the decision half of the companion app backend
// (app_backend.go): the AppBackend seam (Ready, Deliver, Retract) and the
// AppNotice a notification frame carries. Backend selection is per dispatch,
// inside runDispatch and after every gate, so the app path suppresses exactly
// what the Alerter path does; backend_unavailable means neither Alerter nor a
// ready app exists. A ready app is offered the notification first and has 3s
// to report delivered; anything short of that is withdrawn and recorded as
// app_fallback, and the Alerter path runs as if no app existed. After
// delivered the dispatch waits for the interaction up to the ordinary dispatch
// timeout: a click runs the same focusForClick an Alerter click does, and the
// mute, snooze, and answer actions are recorded the way replied is — the app
// carries them out, the daemon only names them.
//
// A notice still showing is retracted when a newer notification for its
// window passes every gate (never by one that is suppressed, which would leave
// the operator with nothing), when CancelPending or CancelPendingDisposition
// says the window moved on, and when the window closes. Dispatch and focus
// dispositions name the backend they came from.
//
// It must never import the daemon root; the daemon drives it through the
// Notifier seams rather than reaching into its state.
package notification
