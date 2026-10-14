import Foundation
import os

/// What one health request found.
enum DaemonStatus: Equatable {
    case ready(DaemonBuild?)
    /// The daemon answered but is not ready: it started degraded and has not
    /// recovered, or refused the request.
    case degraded(reason: String?, build: DaemonBuild?)
    /// Nothing listens on the socket.
    case notRunning
    /// Something listens but did not answer usefully.
    case unresponsive(String)

    var build: DaemonBuild? {
        switch self {
        case let .ready(build), let .degraded(_, build): return build
        case .notRunning, .unresponsive: return nil
        }
    }
}

/// What one focus_window request did.
enum FocusOutcome: Equatable {
    case focused(FocusWindowResult)
    /// The window is gone; the menu was showing a session that has closed.
    case staleTarget
    /// The daemon does not know that Kitty socket, so it will not dial it.
    case unknownSocket
    /// The daemon predates focus_window.
    case unsupported
    case notRunning
    case failed(String)
}

/// What one focus_session request did.
enum FocusSessionOutcome: Equatable {
    /// The daemon asked something to show the session: this workspace, by a
    /// `workspace_view` frame, or a terminal attached to the session's server.
    case shown
    /// The session's agent stopped.
    case staleTarget
    /// Nothing is connected to show it.
    case noViewer
    /// The daemon predates focus_session.
    case unsupported
    case notRunning
    case failed(String)
}

/// What one notification_snooze request did.
enum SnoozeOutcome: Equatable {
    /// The snooze now in force.
    case state(SnoozeState)
    /// The daemon predates notification_snooze.
    case unsupported
    case notRunning
    case failed(String)
}

/// One-shot requests to the daemon, each on its own connection. Blocking:
/// call off the main thread.
struct DaemonClient {
    /// The daemon's own deadline for a health request.
    static let healthTimeout: TimeInterval = 1

    var socketPath: String = SocketPath.daemon()
    var roundTrip: (_ path: String, _ request: Data, _ timeout: TimeInterval) throws -> Data = LineSocket.roundTrip

    private static let logger = Logger(subsystem: "io.github.sglavoie.agentmux", category: "daemon")

    /// The operator phrase for a transport failure; the raw error is logged.
    static func phrase(_ error: LineSocketError) -> String {
        logger.notice("daemon request: \(error.message, privacy: .public)")
        return error.operatorMessage
    }

    func health(timeout: TimeInterval = healthTimeout) -> DaemonStatus {
        let reply: Data
        do {
            let request = try JSONEncoder().encode(DaemonRequest.health)
            reply = try roundTrip(socketPath, request, timeout)
        } catch let error as LineSocketError {
            return error.meansNotRunning ? .notRunning : .unresponsive(Self.phrase(error))
        } catch {
            return .unresponsive(error.localizedDescription)
        }
        return Self.status(fromHealthReply: reply)
    }

    static func status(fromHealthReply reply: Data) -> DaemonStatus {
        guard let response = try? JSONDecoder().decode(HealthResponse.self, from: reply) else {
            return .unresponsive(DaemonPhrase.unreadable)
        }
        guard response.ok else {
            return .degraded(reason: response.error ?? "health refused", build: response.build)
        }
        return response.ready
            ? .ready(response.build)
            : .degraded(reason: response.degradedReason, build: response.build)
    }

    /// The daemon's own deadline for focus_window is 3s; allow for the trip.
    static let focusTimeout: TimeInterval = 4

    func focusWindow(socket: String, windowID: Int, timeout: TimeInterval = focusTimeout) -> FocusOutcome {
        let reply: Data
        do {
            let request = try JSONEncoder().encode(DaemonRequest.focusing(socket: socket, windowID: windowID))
            reply = try roundTrip(socketPath, request, timeout)
        } catch let error as LineSocketError {
            return error.meansNotRunning ? .notRunning : .failed(Self.phrase(error))
        } catch {
            return .failed(error.localizedDescription)
        }
        return Self.focusOutcome(fromReply: reply)
    }

    static func focusOutcome(fromReply reply: Data) -> FocusOutcome {
        guard let response = try? JSONDecoder().decode(DaemonResponse.self, from: reply) else {
            return .failed(DaemonPhrase.unreadable)
        }
        if response.ok {
            return .focused(response.focused ?? FocusWindowResult(windowID: 0))
        }
        switch response.error ?? "" {
        case "stale_target": return .staleTarget
        case "unknown_socket": return .unknownSocket
        case "": return .failed("focus_window refused")
        case let message where message.hasPrefix("unknown request type"): return .unsupported
        case let message: return .failed(message)
        }
    }

    /// Asks the daemon to show a runtime session that has no Kitty window.
    func focusSession(runtimeSessionID: String, timeout: TimeInterval = focusTimeout) -> FocusSessionOutcome {
        let reply: Data
        do {
            let request = try JSONEncoder().encode(DaemonRequest.focusing(runtimeSessionID: runtimeSessionID))
            reply = try roundTrip(socketPath, request, timeout)
        } catch let error as LineSocketError {
            return error.meansNotRunning ? .notRunning : .failed(Self.phrase(error))
        } catch {
            return .failed(error.localizedDescription)
        }
        return Self.focusSessionOutcome(fromReply: reply)
    }

    static func focusSessionOutcome(fromReply reply: Data) -> FocusSessionOutcome {
        guard let response = try? JSONDecoder().decode(DaemonResponse.self, from: reply) else {
            return .failed(DaemonPhrase.unreadable)
        }
        if response.ok { return .shown }
        switch response.error ?? "" {
        case "stale_target": return .staleTarget
        case "no_viewer": return .noViewer
        case "": return .failed("focus_session refused")
        case let message where message.hasPrefix("unknown request type"): return .unsupported
        case let message: return .failed(message)
        }
    }

    /// The daemon's own deadline for notification_snooze is 1s.
    static let snoozeTimeout: TimeInterval = 2

    /// Sets, clears, or (with an empty mode) reads the global snooze.
    func notificationSnooze(_ request: SnoozeRequest, timeout: TimeInterval = snoozeTimeout) -> SnoozeOutcome {
        let reply: Data
        do {
            let encoded = try JSONEncoder().encode(DaemonRequest.snoozing(request))
            reply = try roundTrip(socketPath, encoded, timeout)
        } catch let error as LineSocketError {
            return error.meansNotRunning ? .notRunning : .failed(Self.phrase(error))
        } catch {
            return .failed(error.localizedDescription)
        }
        return Self.snoozeOutcome(fromReply: reply)
    }

    static func snoozeOutcome(fromReply reply: Data) -> SnoozeOutcome {
        guard let response = try? JSONDecoder().decode(DaemonResponse.self, from: reply) else {
            return .failed(DaemonPhrase.unreadable)
        }
        if response.ok {
            return .state(response.snooze ?? SnoozeState())
        }
        let message = response.error ?? ""
        if message.hasPrefix("unknown request type") { return .unsupported }
        return .failed(message.isEmpty ? "notification_snooze refused" : message)
    }

    /// The daemon's own deadline for notification_disposition is 1s.
    static let dispositionTimeout: TimeInterval = 2

    /// Reports what became of one notification. Nil on success, else why it
    /// did not reach the daemon; the daemon answers ok for a notification it
    /// is no longer waiting on, so there is nothing to retry.
    func notificationDisposition(_ params: DispositionParams, timeout: TimeInterval = dispositionTimeout) -> String? {
        let reply: Data
        do {
            let encoded = try JSONEncoder().encode(DaemonRequest.disposing(params))
            reply = try roundTrip(socketPath, encoded, timeout)
        } catch let error as LineSocketError {
            return Self.phrase(error)
        } catch {
            return error.localizedDescription
        }
        return Self.dispositionError(fromReply: reply)
    }

    static func dispositionError(fromReply reply: Data) -> String? {
        guard let response = try? JSONDecoder().decode(DaemonResponse.self, from: reply) else {
            return "unreadable notification_disposition reply"
        }
        return response.ok ? nil : (response.error ?? "notification_disposition refused")
    }

    // MARK: ASL

    /// `asl.ReadDeadline` (the bridge's 2s read timeout plus 1s), plus the trip.
    static let aslReadTimeout: TimeInterval = 4
    /// `asl.ActionDeadline` (the bridge's 5s action timeout plus 1s), plus the
    /// trip.
    static let aslActionTimeout: TimeInterval = 7

    /// The daemon's ASL projection, with the tokens an answer is sent under.
    func aslSnapshot(timeout: TimeInterval = aslReadTimeout) -> Result<ASLResult, ASLRequestError> {
        aslExchange(.aslSnapshot, timeout: timeout).flatMap { result in
            result.snapshot == nil ? .failure(.transport("daemon returned no ASL snapshot")) : .success(result)
        }
    }

    /// One question's body. The caller holds it only while the panel shows it.
    func aslQuestion(planDir: String, questionID: String, timeout: TimeInterval = aslReadTimeout) -> Result<ASLQuestionBody, ASLRequestError> {
        aslExchange(.aslQuestion(planDir: planDir, questionID: questionID), timeout: timeout).flatMap { result in
            result.question.map { .success($0) } ?? .failure(.transport("daemon returned no question body"))
        }
    }

    /// Submits one mutation, exactly once: a timeout is reported as an
    /// unknown outcome rather than resent.
    func aslAction(_ params: ASLRequestParams, timeout: TimeInterval = aslActionTimeout) -> Result<ASLOperation, ASLRequestError> {
        aslExchange(.aslAction(params), timeout: timeout).flatMap { result in
            result.operation.map { .success($0) } ?? .failure(.transport("daemon returned no operation receipt"))
        }
    }

    private func aslExchange(_ request: DaemonRequest, timeout: TimeInterval) -> Result<ASLResult, ASLRequestError> {
        let reply: Data
        do {
            let encoded = try JSONEncoder().encode(request)
            reply = try roundTrip(socketPath, encoded, timeout)
        } catch let error as LineSocketError {
            return .failure(error.meansNotRunning ? .notRunning : .transport(Self.phrase(error)))
        } catch {
            return .failure(.transport(error.localizedDescription))
        }
        return Self.aslResult(fromReply: reply)
    }

    static func aslResult(fromReply reply: Data) -> Result<ASLResult, ASLRequestError> {
        guard let response = try? JSONDecoder().decode(DaemonResponse.self, from: reply) else {
            return .failure(.transport(DaemonPhrase.unreadable))
        }
        guard response.ok else {
            let message = response.error ?? ""
            if message.hasPrefix("unknown request type") { return .failure(.unsupported) }
            return .failure(.refused(message.isEmpty ? "daemon refused the ASL request" : message))
        }
        guard let result = response.aslResult else {
            return .failure(.transport("daemon returned no ASL result"))
        }
        return .success(result)
    }
}
