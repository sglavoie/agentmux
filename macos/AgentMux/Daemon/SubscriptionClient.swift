import Foundation

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
/// heartbeat) arrives within two heartbeat intervals, which is how a socket
/// left half-open by sleep is noticed.
///
/// Decoding is tolerant: a line that is not a frame is counted and skipped,
/// and an unknown frame type is reported as `.unknown` and otherwise ignored.
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
        /// The connection failed or closed.
        case lost(String)
        /// Something asked for a fresh connection (a wake from sleep).
        case reconnectRequested
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
        let thread = Thread { [weak self] in
            self?.run()
        }
        thread.name = "io.github.sglavoie.agentmux.subscription"
        thread.qualityOfService = .utility
        self.thread = thread
        lock.unlock()
        thread.start()
    }

    /// Ends the subscription. No event is reported after it returns; the
    /// thread closes its connection within one poll slice. It does not wait
    /// for that, so a main-thread caller never blocks on the utility thread.
    func stop() {
        lock.lock()
        guard !stopped else { lock.unlock(); return }
        stopped = true
        lock.unlock()
        wake.signal()
    }

    /// Reports `event` unless the client has been stopped.
    private func emit(_ event: Event) {
        lock.lock()
        let live = !stopped
        lock.unlock()
        if live { onEvent(event) }
    }

    /// Drops the current connection, if any, and dials again at once. Sent on
    /// wake from sleep, when the old socket may be half-open.
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
            case .unsupported:
                // An old daemon stays old until it is restarted; look again
                // at the slowest cadence.
                delay = backoff.maximum
            case .reconnectRequested:
                delay = 0
            case .bye:
                // A daemon that said goodbye is usually restarting.
                delay = backoff.initial
            default:
                delay = backoff.delay(afterFailures: failures)
            }
            failures += 1
            emit(.disconnected(reason, retryIn: delay))
            sleep(delay)
        }
    }

    /// Waits `delay` unless stopped or asked to reconnect sooner.
    private func sleep(_ delay: TimeInterval) {
        let deadline = Date().addingTimeInterval(delay)
        while !isStopped {
            let remaining = deadline.timeIntervalSinceNow
            if remaining <= 0 { return }
            if wake.wait(timeout: .now() + remaining) == .success {
                if isStopped || takeReconnectRequest() { return }
            }
        }
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
            return error.meansNotRunning ? .notRunning : .lost(error.message)
        } catch {
            return .lost(error.localizedDescription)
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
        var lastFrame = Date()
        while true {
            if isStopped { return .lost("stopped") }
            if takeReconnectRequest() { return .reconnectRequested }
            let line: Data
            do {
                line = try socket.readLine(deadline: Date().addingTimeInterval(Self.pollSlice))
            } catch LineSocketError.timedOut {
                if Date().timeIntervalSince(lastFrame) >= watchdog { return .heartbeatLost }
                continue
            } catch let error as LineSocketError {
                return .lost(error.message)
            } catch {
                return .lost(error.localizedDescription)
            }
            guard let frame = Self.decodeFrame(line) else {
                lock.lock(); badLinesCount += 1; lock.unlock()
                continue
            }
            lastFrame = Date()
            emit(.frame(frame))
            if case let .bye(_, reason) = frame { return .bye(reason) }
        }
    }

    private enum AckOutcome {
        case success(SubscriptionAck)
        case failure(Disconnect)
    }

    private func readAck(_ socket: LineSocket) -> AckOutcome {
        let deadline = Date().addingTimeInterval(Self.ackTimeout)
        let line: Data
        do {
            line = try socket.readLine(deadline: deadline)
        } catch let error as LineSocketError {
            return .failure(.lost(error.message))
        } catch {
            return .failure(.lost(error.localizedDescription))
        }
        guard let response = try? JSONDecoder().decode(DaemonResponse.self, from: line) else {
            return .failure(.lost("unreadable subscribe reply"))
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

    /// One stream line as a frame; nil when it is not one.
    static func decodeFrame(_ line: Data) -> StreamFrame? {
        let decoder = JSONDecoder()
        guard let header = try? decoder.decode(StreamFrameHeader.self, from: line) else { return nil }
        switch header.type {
        case "snapshot":
            guard let payload = try? decoder.decode(StreamFramePayload<CompanionSnapshot>.self, from: line) else { return nil }
            return .snapshot(seq: header.seq, payload.data)
        case "heartbeat":
            return .heartbeat(seq: header.seq)
        case "bye":
            let reason = (try? decoder.decode(StreamFramePayload<ByeFrame>.self, from: line))?.data.reason ?? ""
            return .bye(seq: header.seq, reason: reason)
        case "notification":
            guard let object = try? JSONSerialization.jsonObject(with: line) as? [String: Any],
                  let data = object["data"],
                  let raw = try? JSONSerialization.data(withJSONObject: data) else { return nil }
            return .notification(seq: header.seq, raw)
        case "notification_retract":
            guard let payload = try? decoder.decode(StreamFramePayload<NotificationRetractFrame>.self, from: line) else { return nil }
            return .notificationRetract(seq: header.seq, payload.data)
        case "workspace_view":
            guard let payload = try? decoder.decode(StreamFramePayload<WorkspaceViewRequest>.self, from: line) else { return nil }
            return .workspaceView(seq: header.seq, payload.data)
        default:
            return .unknown(seq: header.seq, type: header.type)
        }
    }
}
