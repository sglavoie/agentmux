import Foundation
import os

/// One line of a companion stream, decoded.
enum StreamFrame: Equatable {
    case snapshot(seq: UInt64, CompanionSnapshot)
    case heartbeat(seq: UInt64)
    case bye(seq: UInt64, reason: String)
    /// A notification frame. Its payload is left undecoded: the app's
    /// notification presenter owns that shape.
    case notification(seq: UInt64, Data)
    case notificationRetract(seq: UInt64, NotificationRetractFrame)
    /// A view a surface the workspace embeds asked the daemon to have shown.
    case workspaceView(seq: UInt64, WorkspaceViewRequest)
    /// A frame type this app does not know; a reader skips it.
    case unknown(seq: UInt64, type: String)
}

/// The reconnect delays: doubling from `initial` up to `maximum`, back to
/// `initial` once a subscription is acknowledged.
struct ReconnectBackoff: Equatable {
    var initial: TimeInterval = 0.5
    var maximum: TimeInterval = 10

    func delay(afterFailures failures: Int) -> TimeInterval {
        guard failures > 0 else { return initial }
        let exponent = min(failures, 30)
        return min(maximum, initial * pow(2, Double(exponent)))
    }
}

/// Holds a companion subscription open for as long as it is started: it
/// connects, sends `subscribe`, reads the ack and then frames, and reconnects
/// whenever the stream ends — on a `bye`, on EOF, or when no frame (not even a
/// heartbeat) arrives within two heartbeat intervals, which is how a daemon
/// that stopped answering is noticed.
///
/// Decoding is tolerant: a line that is not a frame is counted, logged (the
/// first few), and skipped, and an unknown frame type is reported as
/// `.unknown` and otherwise ignored. A snapshot that does not decode is the
/// exception: it ends the stream as `.unreadableSnapshot`, since skipping it
/// would leave older sessions on show as if they were current.
///
/// The client runs on a thread of its own and reports through `onEvent`, on
/// that thread; the caller hops to wherever it keeps state.
final class SubscriptionClient {
    enum Event: Equatable {
        /// About to dial; `attempt` counts consecutive failures before it.
        case connecting(attempt: Int)
        case subscribed(SubscriptionAck)
        case frame(StreamFrame)
        /// The stream ended, or never opened. The client retries after
        /// `retryIn` seconds.
        case disconnected(Disconnect, retryIn: TimeInterval)
    }

    enum Disconnect: Equatable {
        /// Nothing listens on the socket.
        case notRunning
        /// The daemon answered subscribe with "unknown request type": it
        /// predates the companion protocol.
        case unsupported
        /// The daemon refused this subscription (`subscriber_limit`,
        /// `shutting_down`, …).
        case refused(String)
        /// The daemon said goodbye on purpose.
        case bye(String)
        /// No frame within the heartbeat watchdog.
        case heartbeatLost
        /// The connection failed or closed; the text is an operator phrase
        /// (`DaemonPhrase`), the raw error having gone to the log.
        case lost(String)
        /// Something asked for a fresh connection (a daemon just started).
        case reconnectRequested
        /// A snapshot frame this app cannot read: a daemon newer or older
        /// than the app in a way the lenient decoding does not absorb.
        case unreadableSnapshot
    }

    /// How long the ack may take once the request is written.
    static let ackTimeout: TimeInterval = 3
    /// The heartbeat interval assumed when an ack does not state one.
    static let defaultHeartbeat: TimeInterval = 15
    /// How often a blocked read wakes to look at `stop` and the watchdog.
    static let pollSlice: TimeInterval = 0.25

    let socketPath: String
    let backoff: ReconnectBackoff
    private let request: () -> SubscribeParams
    private let onEvent: (Event) -> Void

    private let lock = NSLock()
    private var stopped = false
    private var reconnectRequested = false
    private var started = false
    private var thread: Thread?
    private let wake = DispatchSemaphore(value: 0)
    private var badLinesCount = 0

    init(
        socketPath: String = SocketPath.daemon(),
        backoff: ReconnectBackoff = ReconnectBackoff(),
        request: @escaping () -> SubscribeParams,
        onEvent: @escaping (Event) -> Void
    ) {
        self.socketPath = socketPath
        self.backoff = backoff
        self.request = request
        self.onEvent = onEvent
    }

    deinit {
        stop()
    }

    /// Lines that were not frames, since the client was created.
    var badLines: Int {
        lock.lock(); defer { lock.unlock() }
        return badLinesCount
    }

    /// Starts the connection thread. A client runs once: starting it again,
    /// or after `stop()`, does nothing; make a new client instead.
    func start() {
        lock.lock()
        guard !started else { lock.unlock(); return }
        started = true
        // The thread holds the client strongly while `run()` runs, so the
        // client outlives its last outside reference until `stop()`: `deinit`
        // is not what ends it. That is deliberate, since a subscription
        // dropped mid-read must still close its socket on this thread, and
        // every owner stops its client (the app on quit and on resubscribe,
        // tests in tearDown). `stop()` returns at once and the thread exits
        // within one poll slice, releasing the client.
        let thread = Thread { [weak self] in
            self?.run()
        }
        thread.name = "io.github.sglavoie.agentmux.subscription"
        thread.qualityOfService = .utility
        self.thread = thread
        lock.unlock()
        thread.start()
    }

    /// Ends the subscription: no event is reported once the thread sees it,
    /// and the thread closes its connection within one poll slice. It does
    /// not wait for that, so a main-thread caller never blocks on the
    /// utility thread; an event the thread was already reporting when this
    /// was called can still reach `onEvent` after it returns, so an owner
    /// that must hear nothing after stopping checks that itself.
    func stop() {
        lock.lock()
        guard !stopped else { lock.unlock(); return }
        stopped = true
        lock.unlock()
        wake.signal()
    }

    /// Reports `event` unless the client has been stopped. The check and
    /// the call are not atomic: `onEvent` runs outside the lock, so it never
    /// holds it, and a `stop()` in between does not hold this event back.
    private func emit(_ event: Event) {
        lock.lock()
        let live = !stopped
        lock.unlock()
        if live { onEvent(event) }
    }

    /// Drops the current connection, if any, and dials again at once, with
    /// the backoff back at its start. Sent while the app starts the daemon,
    /// after it restarts it, and when the notification claim changes, which
    /// the daemon learns only from a subscribe.
    func reconnectNow() {
        lock.lock()
        reconnectRequested = true
        lock.unlock()
        wake.signal()
    }

    private var isStopped: Bool {
        lock.lock(); defer { lock.unlock() }
        return stopped
    }

    private func takeReconnectRequest() -> Bool {
        lock.lock(); defer { lock.unlock() }
        let requested = reconnectRequested
        reconnectRequested = false
        return requested
    }

    private func run() {
        var failures = 0
        while !isStopped {
            emit(.connecting(attempt: failures))
            let reason = session(onSubscribed: { failures = 0 })
            if isStopped { return }
            let delay: TimeInterval
            switch reason {
            case .unsupported, .unreadableSnapshot:
                // An old daemon stays old until it is restarted, and a
                // snapshot this app cannot read stays so until one of the two
                // is updated; look again at the slowest cadence.
                delay = backoff.maximum
            case .reconnectRequested:
                delay = 0
            case .bye:
                // A daemon that said goodbye is usually restarting.
                delay = backoff.initial
            default:
                delay = backoff.delay(afterFailures: failures)
            }
            // A requested reconnect is not a failure: whatever asked for it
            // (a daemon just started) expects the next dials to come quickly.
            failures = reason == .reconnectRequested ? 0 : failures + 1
            emit(.disconnected(reason, retryIn: delay))
            if sleep(delay) { failures = 0 }
        }
    }

    /// Waits `delay` unless stopped or asked to reconnect sooner; true when
    /// a reconnect request cut it short.
    private func sleep(_ delay: TimeInterval) -> Bool {
        let deadline = Date().addingTimeInterval(delay)
        while !isStopped {
            let remaining = deadline.timeIntervalSinceNow
            if remaining <= 0 { return false }
            if wake.wait(timeout: .now() + remaining) == .success {
                if isStopped { return false }
                if takeReconnectRequest() { return true }
            }
        }
        return false
    }

    private static let logger = Logger(subsystem: "io.github.sglavoie.agentmux", category: "subscription")

    /// The operator phrase a transport failure is reported as; the raw error
    /// goes to the log.
    private static func lost(_ error: Error) -> Disconnect {
        if let error = error as? LineSocketError {
            logger.notice("subscription: \(error.message, privacy: .public)")
            return .lost(error.operatorMessage)
        }
        logger.notice("subscription: \(error.localizedDescription, privacy: .public)")
        return .lost(DaemonPhrase.unreachable)
    }

    /// One connection from dial to end; returns why it ended.
    private func session(onSubscribed: () -> Void) -> Disconnect {
        _ = takeReconnectRequest()
        let socket = LineSocket()
        defer { socket.close() }
        do {
            try socket.connect(path: socketPath)
            let line = try JSONEncoder().encode(DaemonRequest.subscribing(request()))
            try socket.writeLine(line, deadline: Date().addingTimeInterval(Self.ackTimeout))
        } catch let error as LineSocketError {
            return error.meansNotRunning ? .notRunning : Self.lost(error)
        } catch {
            return Self.lost(error)
        }

        let ack: SubscriptionAck
        switch readAck(socket) {
        case let .success(value): ack = value
        case let .failure(reason): return reason
        }
        onSubscribed()
        emit(.subscribed(ack))

        let heartbeat = ack.heartbeatMS > 0 ? TimeInterval(ack.heartbeatMS) / 1000 : Self.defaultHeartbeat
        let watchdog = heartbeat * 2
        // Measured on the clock that stops while the Mac sleeps: both ends of
        // this local socket sleep together, so the sleep itself is not a
        // silence, and a stream that is still good after a wake is kept.
        // One that is not ends at its EOF, or a watchdog after the wake.
        var lastFrame = ProcessInfo.processInfo.systemUptime
        while true {
            if isStopped { return .lost("stopped") }
            if takeReconnectRequest() { return .reconnectRequested }
            let line: Data
            do {
                line = try socket.readLine(deadline: Date().addingTimeInterval(Self.pollSlice))
            } catch LineSocketError.timedOut {
                if ProcessInfo.processInfo.systemUptime - lastFrame >= watchdog { return .heartbeatLost }
                continue
            } catch {
                return Self.lost(error)
            }
            let frame: StreamFrame
            switch Self.decodeLine(line) {
            case let .success(decoded):
                frame = decoded
            case let .failure(problem):
                lock.lock(); badLinesCount += 1; let count = badLinesCount; lock.unlock()
                if count <= Self.loggedBadLines {
                    Self.logger.error("subscription: skipped a \(line.count)-byte line that is not a readable frame (type \(problem.type ?? "unknown", privacy: .public)): \(problem.detail, privacy: .public)")
                }
                // Sessions shown from the last snapshot would be presented
                // as live while every newer one is unreadable: the feed says
                // so instead, and dials again at the slowest cadence.
                if problem.type == "snapshot" { return .unreadableSnapshot }
                continue
            }
            lastFrame = ProcessInfo.processInfo.systemUptime
            emit(.frame(frame))
            if case let .bye(_, reason) = frame { return .bye(reason) }
        }
    }

    /// How many unreadable lines are logged, so a daemon that sends nothing
    /// else does not fill the log.
    static let loggedBadLines = 5

    private enum AckOutcome {
        case success(SubscriptionAck)
        case failure(Disconnect)
    }

    private func readAck(_ socket: LineSocket) -> AckOutcome {
        let deadline = Date().addingTimeInterval(Self.ackTimeout)
        let line: Data
        do {
            line = try socket.readLine(deadline: deadline)
        } catch {
            return .failure(Self.lost(error))
        }
        let response: DaemonResponse
        do {
            response = try JSONDecoder().decode(DaemonResponse.self, from: line)
        } catch {
            Self.logger.notice("subscription: unreadable subscribe reply: \(Self.describe(error), privacy: .public)")
            return .failure(.lost(DaemonPhrase.unreadable))
        }
        return Self.ackOutcome(response)
    }

    private static func ackOutcome(_ response: DaemonResponse) -> AckOutcome {
        if response.ok, let ack = response.subscription {
            return .success(ack)
        }
        let error = response.error ?? ""
        if error.hasPrefix("unknown request type") {
            return .failure(.unsupported)
        }
        return .failure(.refused(error.isEmpty ? "subscribe refused" : error))
    }

    /// What a subscribe reply means, for tests.
    static func classifyAck(_ response: DaemonResponse) -> Result<SubscriptionAck, DisconnectError> {
        switch ackOutcome(response) {
        case let .success(ack): return .success(ack)
        case let .failure(reason): return .failure(DisconnectError(reason: reason))
        }
    }

    struct DisconnectError: Error, Equatable {
        let reason: Disconnect
    }

    /// One stream line as a frame; nil when it is not one. For tests: the
    /// stream itself reads lines through `decodeLine`, which logs why.
    static func decodeFrame(_ line: Data) -> StreamFrame? {
        try? decodeLine(line).get()
    }

    /// Why a stream line is not a frame, in words fit for the log: the frame
    /// type when the header could be read, and what failed, never the
    /// payload, which can carry session titles and paths.
    struct FrameProblem: Error, Equatable {
        /// nil when the line has no readable header.
        let type: String?
        let detail: String
    }

    /// One stream line as a frame, or why it is not one.
    static func decodeLine(_ line: Data) -> Result<StreamFrame, FrameProblem> {
        let decoder = JSONDecoder()
        let header: StreamFrameHeader
        do {
            header = try decoder.decode(StreamFrameHeader.self, from: line)
        } catch {
            return .failure(FrameProblem(type: nil, detail: describe(error)))
        }
        func payload<T: Decodable>(_ type: T.Type) -> Result<T, FrameProblem> {
            do {
                return .success(try decoder.decode(StreamFramePayload<T>.self, from: line).data)
            } catch {
                return .failure(FrameProblem(type: header.type, detail: describe(error)))
            }
        }
        switch header.type {
        case "snapshot":
            return payload(CompanionSnapshot.self).map { .snapshot(seq: header.seq, $0) }
        case "heartbeat":
            return .success(.heartbeat(seq: header.seq))
        case "bye":
            // A bye whose reason cannot be read still ends the stream.
            let reason: String
            do {
                reason = try decoder.decode(StreamFramePayload<ByeFrame>.self, from: line).data.reason
            } catch {
                logger.notice("subscription: bye without a readable reason: \(describe(error), privacy: .public)")
                reason = ""
            }
            return .success(.bye(seq: header.seq, reason: reason))
        case "notification":
            // Only an object can be written back out: `data(withJSONObject:)`
            // raises an exception Swift cannot catch for null, a string, or a
            // number, which would take the app down.
            guard let object = try? JSONSerialization.jsonObject(with: line) as? [String: Any],
                  let data = object["data"] as? [String: Any],
                  JSONSerialization.isValidJSONObject(data),
                  let raw = try? JSONSerialization.data(withJSONObject: data) else {
                return .failure(FrameProblem(type: header.type, detail: "its data is not an object"))
            }
            return .success(.notification(seq: header.seq, raw))
        case "notification_retract":
            return payload(NotificationRetractFrame.self).map { .notificationRetract(seq: header.seq, $0) }
        case "workspace_view":
            return payload(WorkspaceViewRequest.self).map { .workspaceView(seq: header.seq, $0) }
        default:
            return .success(.unknown(seq: header.seq, type: header.type))
        }
    }

    /// A decoding error as its kind and the path to the field, with no value
    /// from the line.
    /// A decoding failure in words fit for the log: where it failed, never
    /// the payload. DaemonClient logs its replies' failures the same way.
    static func describe(_ error: Error) -> String {
        func path(_ keys: [CodingKey]) -> String {
            keys.isEmpty ? "the top level" : keys.map { $0.intValue.map(String.init) ?? $0.stringValue }.joined(separator: ".")
        }
        switch error {
        case let DecodingError.typeMismatch(type, context):
            return "\(path(context.codingPath)) is not \(type)"
        case let DecodingError.valueNotFound(type, context):
            return "\(path(context.codingPath)) has no \(type)"
        case let DecodingError.keyNotFound(key, context):
            return "\(path(context.codingPath + [key])) is missing"
        case let DecodingError.dataCorrupted(context):
            return context.codingPath.isEmpty ? "not JSON" : "\(path(context.codingPath)) is malformed"
        default:
            return "undecodable"
        }
    }
}
