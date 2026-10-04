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
}
