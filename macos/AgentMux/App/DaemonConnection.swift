import Foundation
import os

/// What the daemon connection asks of the app: the frames the feed carries,
/// and where to say how a start or a restart went.
@MainActor
protocol DaemonConnectionDelegate: AnyObject {
    /// A feed event, before the store applies it.
    func connection(_ connection: DaemonConnection, received event: SubscriptionClient.Event)
    /// The store has applied a feed event.
    func connectionApplied(_ connection: DaemonConnection)
    /// A line for the menu, and for the workspace while it has the keyboard.
    func connection(_ connection: DaemonConnection, report message: String)
    /// A failure the operator hears of at once.
    func connection(_ connection: DaemonConnection, failed title: String, message: String)
    /// A start or restart began or was decided.
    func connectionBusyChanged(_ connection: DaemonConnection)
    /// Records a change under way, for the quit confirmation.
    func connection(_ connection: DaemonConnection, beginChange label: String) -> Int
    func connection(_ connection: DaemonConnection, endChange token: Int)
}

/// The app's line to the daemon: the companion feed, the health checks made
/// while that feed is not live, and the daemon starts and restarts the
/// operator asks for. Every answer lands in the `SessionStore`.
///
/// Health is polled only while the feed is not live, every `healthInterval`:
/// a live feed carries the daemon's own status in every snapshot, so the
/// timer stops while it is live and starts again when it is not.
@MainActor
final class DaemonConnection {
    nonisolated private static let logger = Logger(subsystem: "io.github.sglavoie.agentmux", category: "app")

    /// How often the daemon line is refreshed while the companion feed is
    /// not live: with no daemon, or one too old to stream.
    nonisolated static let healthInterval: TimeInterval = 10
    /// How often the feed redials after Start Daemon, for `DaemonStart.deadline`.
    nonisolated static let startPollInterval: TimeInterval = 0.25

    weak var delegate: DaemonConnectionDelegate?

    private let store: SessionStore
    private let request: () -> SubscribeParams
    private let health: WorkRunner
    private let control: WorkRunner
    private let makeClient: () -> DaemonClient

    private var subscription: SubscriptionClient?
    private var lastFeed: FeedState?
    /// Whether the subscription is waiting out a retry delay, as against
    /// dialing: only then does a reconnect request start a dial sooner, and
    /// one sent mid-dial would drop the connection it was about to make.
    private var subscriptionWaiting = false
    private var lastDisconnect: SubscriptionClient.Disconnect?
    private var healthInFlight = false
    private(set) var healthTimer: Timer?
    private var stopped = false

    /// The `amx daemon start` the app is waiting on, until it has started or
    /// failed, and its exit code once it has exited.
    private var startPID: pid_t?
    private var startExit: Int32?
    private var startLog: URL?
    /// Set while `amx daemon restart` runs.
    private var restartRunning = false

    init(store: SessionStore, request: @escaping () -> SubscribeParams,
         health: WorkRunner = QueueWorkRunner(queue: DispatchQueue(label: "io.github.sglavoie.agentmux.health", qos: .utility)),
         control: WorkRunner, makeClient: @escaping () -> DaemonClient = { DaemonClient() }) {
        self.store = store
        self.request = request
        self.health = health
        self.control = control
        self.makeClient = makeClient
    }

    // MARK: Feed

    /// Opens the companion feed, once; later calls, and any after `stop`, do
    /// nothing.
    func start() {
        guard subscription == nil, !stopped else { return }
        let client = SubscriptionClient(request: request) { [weak self] event in
            DispatchQueue.main.async {
                MainActor.assumeIsolated { self?.handle(event) }
            }
        }
        subscription = client
        client.start()
    }

    func stop() {
        stopped = true
        healthTimer?.invalidate()
        healthTimer = nil
        subscription?.stop()
    }

    /// Drops the stream and dials again at once, so the next subscribe
    /// carries what changed.
    func reconnectNow() {
        subscription?.reconnectNow()
    }

    /// Asks the subscription to dial at once if it is waiting to retry.
    func reconnectIfWaiting() {
        guard subscriptionWaiting else { return }
        subscriptionWaiting = false
        subscription?.reconnectNow()
    }

    private func handle(_ event: SubscriptionClient.Event) {
        // An event the client was already reporting, or one still queued for
        // the main thread, can arrive after `stop`: it changes nothing.
        guard !stopped else { return }
        switch event {
        case .connecting:
            subscriptionWaiting = false
        case .subscribed:
            lastDisconnect = nil
        case let .disconnected(reason, retryIn):
            subscriptionWaiting = true
            // The raw reason, once per change; the store shows an operator
            // phrase.
            if reason != lastDisconnect {
                lastDisconnect = reason
                Self.logger.notice("feed disconnected: \(String(describing: reason), privacy: .public); retry in \(retryIn)s")
            }
            if case .bye = reason {
                // A restart is held as reconnecting; look again once the hold
                // is over, in case nothing else does.
                DispatchQueue.main.asyncAfter(deadline: .now() + SessionStore.byeHold + 0.1) { [weak self] in
                    MainActor.assumeIsolated { self?.refreshStatus() }
                }
            }
        default:
            break
        }
        delegate?.connection(self, received: event)
        store.apply(event)
        delegate?.connectionApplied(self)
        if store.feed != lastFeed {
            lastFeed = store.feed
            Self.logger.notice("feed: \(String(describing: self.store.feed), privacy: .public)")
            if store.feed != .live { refreshStatus() }
            if store.feed == .live { store.replace(notice: DaemonStart.failedNotice, with: nil) }
            syncHealthTimer()
        }
    }

    // MARK: Health

    /// Asks the daemon for its health while the feed is not live; a live
    /// feed carries the daemon's own status in every snapshot.
    func refreshStatus() {
        syncHealthTimer()
        guard store.feed != .live, !healthInFlight else { return }
        healthInFlight = true
        let client = makeClient
        health.run({ client().health() }, then: { [weak self] status in
            guard let self else { return }
            self.healthInFlight = false
            if status != self.store.health {
                Self.logger.notice("daemon: \(DaemonStatusLine.text(for: status), privacy: .public)")
            }
            self.store.apply(health: status)
            if Self.shouldReconnect(after: status, feed: self.store.feed) { self.reconnectIfWaiting() }
        })
    }

    /// Runs the health timer exactly while the feed is not live.
    private func syncHealthTimer() {
        let wanted = Self.pollsHealth(feed: store.feed) && !stopped
        if wanted, healthTimer == nil {
            let timer = Timer(timeInterval: Self.healthInterval, repeats: true) { [weak self] _ in
                MainActor.assumeIsolated { self?.refreshStatus() }
            }
            RunLoop.main.add(timer, forMode: .common)
            healthTimer = timer
        } else if !wanted, let timer = healthTimer {
            timer.invalidate()
            healthTimer = nil
        }
    }

    /// Whether health is polled with the feed in `feed`: only while it is not
    /// live.
    nonisolated static func pollsHealth(feed: FeedState) -> Bool {
        feed != .live
    }

    /// Whether a health answer means the feed should dial now: the daemon
    /// answers, yet the feed is not live and not merely too old to subscribe.
    nonisolated static func shouldReconnect(after status: DaemonStatus, feed: FeedState) -> Bool {
        switch status {
        case .ready, .degraded: break
        case .notRunning, .unresponsive: return false
        }
        switch feed {
        case .notRunning, .reconnecting: return true
        case .live, .connecting, .daemonTooOld: return false
        }
    }

    // MARK: Start and restart

    /// What the daemon start or restart under way is doing, or nil. While one
    /// runs, another is refused and the menu greys both out: a restart
    /// arriving behind a start, or a second restart, would stop the daemon
    /// the first had just brought up.
    var busy: String? {
        if restartRunning { return DaemonStart.restartingNotice }
        if startPID != nil { return DaemonStart.startingNotice }
        return nil
    }

    /// Runs `amx daemon start` detached, only ever because the operator chose
    /// Start Daemon: the app never starts the daemon on its own. Its standard
    /// error goes to a file the log quotes when the start fails.
    func startDaemon(_ command: AMXCommand) {
        if let busy { return report(busy) }
        let log = DaemonStart.stderrLog()
        let exited: @Sendable (pid_t, Int32) -> Void = { [weak self] pid, code in
            DispatchQueue.main.async {
                MainActor.assumeIsolated { self?.startExited(pid: pid, code: code) }
            }
        }
        guard let pid = command.spawnDetached(["daemon", "start"], extraEnvironment: DaemonStart.detachedEnvironment,
                                              stderrPath: log?.path, onExit: exited) else {
            return report("Could not run \(command.binary.path) daemon start.")
        }
        Self.logger.notice("started amx daemon (pid \(pid))")
        startPID = pid
        startExit = nil
        startLog = log
        store.show(notice: DaemonStart.startingNotice)
        reconnectWhileStarting(until: Date().addingTimeInterval(DaemonStart.deadline))
    }

    /// Redials every `startPollInterval` until the feed is live or `deadline`
    /// passes, so a daemon that just started is listed within a moment of
    /// listening rather than after the backoff's next step. Each pass also
    /// decides whether the start has succeeded or failed.
    private func reconnectWhileStarting(until deadline: Date) {
        let passed = Date() >= deadline
        checkStart(deadlinePassed: passed)
        guard startPID != nil, store.feed != .live, !passed else { return }
        reconnectIfWaiting()
        DispatchQueue.main.asyncAfter(deadline: .now() + Self.startPollInterval) { [weak self] in
            MainActor.assumeIsolated { self?.reconnectWhileStarting(until: deadline) }
        }
    }

    private func startExited(pid: pid_t, code: Int32) {
        Self.logger.notice("amx daemon start (pid \(pid)) exited \(code)")
        guard pid == startPID else { return }
        startExit = code
        checkStart(deadlinePassed: false)
    }

    /// Replaces the Starting notice once the start is decided: cleared when
    /// the daemon answers, "did not start" when it failed. The reason stays
    /// in the log, never in the menu.
    private func checkStart(deadlinePassed: Bool) {
        guard let pid = startPID else { return }
        switch DaemonStart.verdict(feed: store.feed, exitCode: startExit, deadlinePassed: deadlinePassed) {
        case .waiting:
            return
        case .started:
            startPID = nil
            store.replace(notice: DaemonStart.startingNotice, with: nil)
            delegate?.connectionBusyChanged(self)
        case .failed:
            startPID = nil
            let exit = startExit.map { "exited \($0)" } ?? "still running"
            let tail = startLog.flatMap { try? Data(contentsOf: $0) }.map { DaemonStart.tail($0) } ?? ""
            Self.logger.error("amx daemon start (pid \(pid)) did not start the daemon (\(exit, privacy: .public)): \(tail, privacy: .public)")
            report(DaemonStart.failedNotice)
            delegate?.connectionBusyChanged(self)
        }
    }

    /// Restart Daemon: `amx daemon restart`, which returns once the new
    /// daemon answers, so the feed dials it at once.
    func restartDaemon(_ command: AMXCommand, completion: ((Bool) -> Void)? = nil) {
        if let busy { completion?(false); return report(busy) }
        restartRunning = true
        let change = delegate?.connection(self, beginChange: "restarting the daemon")
        store.show(notice: DaemonStart.restartingNotice)
        control.run({ command.run(["daemon", "restart"], timeout: AMXCommand.actionTimeout, mutates: true) },
                    then: { [weak self] result in
            guard let self else { return }
            if let change { self.delegate?.connection(self, endChange: change) }
            self.restartRunning = false
            self.finishRestart(result)
            if case let .success(output) = result { completion?(output.status == 0) } else { completion?(false) }
            self.delegate?.connectionBusyChanged(self)
        })
    }

    private func finishRestart(_ result: Result<ProcessOutput, AMXCommandError>) {
        let failure: String?
        switch result {
        case let .failure(error):
            failure = error.message
        case let .success(output) where output.status != 0:
            failure = DaemonStart.tail(output.stderr, lines: 1, characters: 200)
            Self.logger.error("amx daemon restart exited \(output.status): \(DaemonStart.tail(output.stderr), privacy: .public)")
        case let .success(output):
            failure = nil
            Self.logger.notice("\(DaemonStart.tail(output.stdout), privacy: .public)")
        }
        guard let failure else {
            store.replace(notice: DaemonStart.restartingNotice, with: nil)
            subscription?.reconnectNow()
            return
        }
        delegate?.connection(self, failed: "Could not restart the daemon", message: failure.isEmpty ? "amx daemon restart failed" : failure)
    }

    private func report(_ message: String) {
        delegate?.connection(self, report: message)
    }
}
