import Foundation
import os

/// The daemon request the snooze controller makes. Blocking: called off the
/// main thread. `DaemonClient` is the real one.
protocol SnoozeService {
    func snooze(_ request: SnoozeRequest) -> SnoozeOutcome
}

extension DaemonClient: SnoozeService {
    func snooze(_ request: SnoozeRequest) -> SnoozeOutcome { notificationSnooze(request) }
}

/// The daemon's notification snooze, as the app keeps it. Notifications ▸,
/// a banner's Snooze, and Preferences' Resume Now set or clear it here; each
/// answer is mirrored into `amxSnoozeUntil`, and every live snapshot keeps
/// that mirror in step, re-applying the snooze to a daemon that restarted
/// without it.
@MainActor
final class SnoozeController {
    nonisolated private static let logger = Logger(subsystem: "io.github.sglavoie.agentmux", category: "app")

    static let failedTitle = "Could not change the snooze"

    /// False once the daemon answered that it has no notification_snooze.
    private(set) var supported = true
    /// The daemon a read-only snooze request has been sent to, which is how
    /// an old daemon's Notifications submenu is greyed out before it is used.
    private(set) var probedDaemon: String?
    private var probeInFlight = false
    private var reapplyInFlight = false
    /// The daemon a re-apply was last attempted against, so a refused one is
    /// not retried on every snapshot.
    private var reapplyAttempted: String?

    private let store: SessionStore
    private let preferences: PreferencesModel
    private let service: () -> SnoozeService
    private let runner: WorkRunner
    private let now: () -> Date

    /// A snooze that failed: its title and message, and whether a banner's
    /// button asked for it, in which case no menu is open to show it.
    var onFailure: (_ title: String, _ message: String, _ banner: Bool) -> Void = { _, _, _ in }

    init(store: SessionStore, preferences: PreferencesModel, runner: WorkRunner,
         service: @escaping () -> SnoozeService = { DaemonClient() }, now: @escaping () -> Date = Date.init) {
        self.store = store
        self.preferences = preferences
        self.runner = runner
        self.service = service
        self.now = now
    }

    /// Sets or clears the daemon's snooze. `banner`: a notification's Snooze,
    /// whose failures are reported where the operator sees them now.
    func snooze(_ choice: SnoozeChoice, banner: Bool = false) {
        guard let daemon = store.snapshot?.daemon, store.feed == .live else {
            return onFailure(Self.failedTitle, DaemonPhrase.notConnected + ".", banner)
        }
        let request = choice.request(now: now())
        let service = self.service
        runner.run({ service().snooze(request) }, then: { [weak self] outcome in
            self?.finish(outcome, daemon: SnoozeMirror.identity(of: daemon), banner: banner)
        })
    }

    private func finish(_ outcome: SnoozeOutcome, daemon: String, banner: Bool = false) {
        switch outcome {
        case let .state(state):
            Self.logger.notice("snooze: \(state.description(now: self.now()), privacy: .public)")
            record(state.mirrored(now: now()), daemon: daemon)
        case .unsupported:
            supported = false
            onFailure(Self.failedTitle, MenuBuilder.snoozeUnsupported, banner)
        case .notRunning:
            store.apply(health: .notRunning)
            if banner { onFailure(Self.failedTitle, "The daemon is not running.", true) }
        case let .failed(message):
            Self.logger.error("snooze: \(message, privacy: .public)")
            onFailure(Self.failedTitle, message, banner)
        }
    }

    private func record(_ until: Date?, daemon: String) {
        if !SnoozeMirror.same(preferences.preferences.snoozeUntil, until) {
            preferences.preferences.snoozeUntil = until
        }
        preferences.defaults.set(daemon, forKey: SnoozeMirror.daemonKey)
    }

    /// Keeps `amxSnoozeUntil` in step with the latest snapshot, and
    /// re-applies the snooze to a daemon that restarted without it.
    func reconcile() {
        guard store.feed == .live, let snapshot = store.snapshot, snapshot.daemon.pid > 0,
              !probeInFlight, !reapplyInFlight else { return }
        let daemon = SnoozeMirror.identity(of: snapshot.daemon)
        guard probedDaemon == daemon else { return probe(daemon) }
        guard supported else { return }
        let defaults = preferences.defaults
        let resumeQueued = defaults.bool(forKey: SnoozeMirror.resumeQueuedKey)
        let decision = SnoozeMirror.reconcile(
            daemon: daemon,
            live: SnoozeState(snapshot.notification),
            stored: preferences.preferences.snoozeUntil,
            storedDaemon: defaults.string(forKey: SnoozeMirror.daemonKey),
            resumeQueued: resumeQueued,
            now: now()
        )
        // Any decision but .resume means the daemon has nothing left to
        // resume, so a queued request is spent.
        if resumeQueued, decision != .resume { defaults.removeObject(forKey: SnoozeMirror.resumeQueuedKey) }
        let service = self.service
        switch decision {
        case .keep:
            if defaults.string(forKey: SnoozeMirror.daemonKey) != daemon {
                defaults.set(daemon, forKey: SnoozeMirror.daemonKey)
            }
        case let .adopt(until):
            record(until, daemon: daemon)
        case .resume:
            reapplyInFlight = true
            Self.logger.notice("snooze: sending the resume queued while the feed was down")
            runner.run({ service().snooze(.resume) }, then: { [weak self] outcome in
                guard let self else { return }
                self.reapplyInFlight = false
                // A daemon that went away is asked again when it is back;
                // any answer from it spends the request.
                if outcome != .notRunning {
                    self.preferences.defaults.removeObject(forKey: SnoozeMirror.resumeQueuedKey)
                }
                self.finish(outcome, daemon: daemon)
            })
        case let .reapply(request):
            guard reapplyAttempted != daemon else { return }
            reapplyAttempted = daemon
            reapplyInFlight = true
            Self.logger.notice("snooze: re-applying to restarted daemon \(daemon, privacy: .public)")
            runner.run({ service().snooze(request) }, then: { [weak self] outcome in
                guard let self else { return }
                self.reapplyInFlight = false
                self.finish(outcome, daemon: daemon)
            })
        }
    }

    /// Asks a newly seen daemon for its snooze without changing it, then
    /// reconciles once the answer says whether it has a snooze at all.
    private func probe(_ daemon: String) {
        probeInFlight = true
        let service = self.service
        runner.run({ service().snooze(.read) }, then: { [weak self] outcome in
            guard let self else { return }
            self.probeInFlight = false
            switch outcome {
            case .state:
                self.probedDaemon = daemon
                self.supported = true
            case .unsupported:
                self.probedDaemon = daemon
                self.supported = false
                Self.logger.notice("snooze: daemon \(daemon, privacy: .public) has no notification_snooze")
            case .notRunning, .failed:
                // Asked again with the next snapshot.
                return
            }
            self.reconcile()
        })
    }
}
