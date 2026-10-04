import Foundation

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

    func health(timeout: TimeInterval = healthTimeout) -> DaemonStatus {
        let reply: Data
        do {
            let request = try JSONEncoder().encode(DaemonRequest.health)
            reply = try roundTrip(socketPath, request, timeout)
        } catch let error as LineSocketError {
            return error.meansNotRunning ? .notRunning : .unresponsive(error.message)
        } catch {
            return .unresponsive(error.localizedDescription)
        }
        return Self.status(fromHealthReply: reply)
    }

    static func status(fromHealthReply reply: Data) -> DaemonStatus {
        guard let response = try? JSONDecoder().decode(HealthResponse.self, from: reply) else {
            return .unresponsive("unreadable health reply")
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
            return error.meansNotRunning ? .notRunning : .failed(error.message)
        } catch {
            return .failed(error.localizedDescription)
        }
        return Self.focusOutcome(fromReply: reply)
    }

    static func focusOutcome(fromReply reply: Data) -> FocusOutcome {
        guard let response = try? JSONDecoder().decode(DaemonResponse.self, from: reply) else {
            return .failed("unreadable focus_window reply")
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

    /// The daemon's own deadline for notification_snooze is 1s.
    static let snoozeTimeout: TimeInterval = 2

    /// Sets, clears, or (with an empty mode) reads the global snooze.
    func notificationSnooze(_ request: SnoozeRequest, timeout: TimeInterval = snoozeTimeout) -> SnoozeOutcome {
        let reply: Data
        do {
            let encoded = try JSONEncoder().encode(DaemonRequest.snoozing(request))
            reply = try roundTrip(socketPath, encoded, timeout)
        } catch let error as LineSocketError {
            return error.meansNotRunning ? .notRunning : .failed(error.message)
        } catch {
            return .failed(error.localizedDescription)
        }
        return Self.snoozeOutcome(fromReply: reply)
    }

    static func snoozeOutcome(fromReply reply: Data) -> SnoozeOutcome {
        guard let response = try? JSONDecoder().decode(DaemonResponse.self, from: reply) else {
            return .failed("unreadable notification_snooze reply")
        }
        if response.ok {
            return .state(response.snooze ?? SnoozeState())
        }
        let message = response.error ?? ""
        if message.hasPrefix("unknown request type") { return .unsupported }
        return .failed(message.isEmpty ? "notification_snooze refused" : message)
    }
}
