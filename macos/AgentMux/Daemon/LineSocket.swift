import Darwin
import Foundation

/// Why a Unix-socket exchange failed. `noSocket` and `refused` both mean
/// nothing is listening; the rest mean something is, but did not answer.
enum LineSocketError: Error, Equatable {
    case pathTooLong(String)
    /// The socket file does not exist.
    case noSocket
    /// The file exists but nothing accepts on it: a daemon that died uncleanly.
    case refused
    case timedOut
    /// The peer closed the connection before a full line arrived.
    case closed
    /// A line longer than the reader's ceiling.
    case lineTooLong
    case system(String, Int32)

    /// Whether the error says nothing is listening at all.
    var meansNotRunning: Bool {
        self == .noSocket || self == .refused
    }

    /// What the operator is told: a few plain phrases, since the raw
    /// `message` (a syscall and its errno) means nothing in a menu. Log
    /// `message` beside it.
    var operatorMessage: String {
        switch self {
        case .noSocket, .refused: return DaemonPhrase.notRunning
        case .timedOut: return DaemonPhrase.notAnswering
        case .closed: return DaemonPhrase.closedConnection
        case .lineTooLong: return "the daemon's reply was too large"
        case .pathTooLong, .system: return DaemonPhrase.unreachable
        }
    }

    /// The raw transport error, for the log.
    var message: String {
        switch self {
        case let .pathTooLong(path): return "socket path is too long: \(path)"
        case .noSocket: return "no socket"
        case .refused: return "connection refused"
        case .timedOut: return "no reply in time"
        case .closed: return "connection closed before a reply"
        case .lineTooLong: return "reply exceeded the size limit"
        case let .system(call, code): return "\(call): \(String(cString: strerror(code)))"
        }
    }
}

/// The operator phrases daemon transport failures are shown as.
enum DaemonPhrase {
    static let notRunning = "the daemon is not running"
    static let notAnswering = "the daemon is not answering"
    static let closedConnection = "the daemon closed the connection"
    static let unreachable = "could not reach the daemon"
    static let unreadable = "the daemon sent a reply this app cannot read"
    static let unreadableSnapshot = "the daemon sent sessions this app cannot read; update the app or amx"
    static let restarting = "the daemon is restarting"
    static let tooManyConnections = "too many app connections — quit other copies"
    static let shuttingDown = "the daemon is shutting down"
    static let refused = "the daemon refused the connection"
    /// What an action that needs the live feed says without it, in the menu
    /// and in a refusal alike.
    static let notConnected = "The daemon is not connected"

    /// A daemon refusal code (`subscriber_limit`, `shutting_down`, …) as a
    /// phrase; an unknown one is a plain refusal.
    static func refusal(_ code: String) -> String {
        switch code {
        case "subscriber_limit": return tooManyConnections
        case "shutting_down": return shuttingDown
        default: return refused
        }
    }
}

/// A blocking newline-delimited connection to a Unix socket — the daemon's
/// framing, one JSON document per line. Every read and write is bounded by
/// the deadline the caller passes, so a daemon that accepts and never answers
/// cannot hang the caller. Not thread-safe: one owner at a time.
final class LineSocket {
    /// The daemon's response ceiling is 64 MiB; anything larger is not a reply.
    static let defaultMaxLineBytes = 64 << 20

    private var fd: Int32 = -1
    private var buffer = Data()
    /// One read buffer for the socket's life, rather than one per line.
    private var chunk = [UInt8](repeating: 0, count: 64 * 1024)
    let maxLineBytes: Int

    init(maxLineBytes: Int = LineSocket.defaultMaxLineBytes) {
        self.maxLineBytes = maxLineBytes
    }

    deinit {
        close()
    }

    var isOpen: Bool { fd >= 0 }

    func connect(path: String) throws {
        close()
        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        let bytes = Array(path.utf8)
        guard bytes.count <= SocketPath.maxLength else { throw LineSocketError.pathTooLong(path) }
        withUnsafeMutableBytes(of: &address.sun_path) { raw in
            raw.copyBytes(from: bytes)
            raw[bytes.count] = 0
        }
        let socketFD = socket(AF_UNIX, SOCK_STREAM, 0)
        guard socketFD >= 0 else { throw LineSocketError.system("socket", errno) }
        var on: Int32 = 1
        setsockopt(socketFD, SOL_SOCKET, SO_NOSIGPIPE, &on, socklen_t(MemoryLayout<Int32>.size))
        let result = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.connect(socketFD, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard result == 0 else {
            let code = errno
            Darwin.close(socketFD)
            switch code {
            case ENOENT: throw LineSocketError.noSocket
            case ECONNREFUSED: throw LineSocketError.refused
            default: throw LineSocketError.system("connect", code)
            }
        }
        fd = socketFD
        buffer.removeAll()
    }

    /// Writes `line` and its terminating newline.
    func writeLine(_ line: Data, deadline: Date) throws {
        guard fd >= 0 else { throw LineSocketError.closed }
        var payload = line
        payload.append(0x0A)
        try payload.withUnsafeBytes { raw in
            var offset = 0
            while offset < raw.count {
                try wait(for: Int16(POLLOUT), deadline: deadline)
                let written = Darwin.write(fd, raw.baseAddress! + offset, raw.count - offset)
                if written < 0 {
                    if errno == EINTR || errno == EAGAIN { continue }
                    if errno == EPIPE { throw LineSocketError.closed }
                    throw LineSocketError.system("write", errno)
                }
                offset += written
            }
        }
    }

    /// The next line, without its newline.
    func readLine(deadline: Date) throws -> Data {
        guard fd >= 0 else { throw LineSocketError.closed }
        while true {
            if let newline = buffer.firstIndex(of: 0x0A) {
                guard newline - buffer.startIndex <= maxLineBytes else { throw LineSocketError.lineTooLong }
                let line = buffer[buffer.startIndex..<newline]
                buffer.removeSubrange(buffer.startIndex...newline)
                return Data(line)
            }
            guard buffer.count <= maxLineBytes else { throw LineSocketError.lineTooLong }
            try wait(for: Int16(POLLIN), deadline: deadline)
            let socketFD = fd
            let count = chunk.withUnsafeMutableBytes { Darwin.read(socketFD, $0.baseAddress, $0.count) }
            if count < 0 {
                if errno == EINTR || errno == EAGAIN { continue }
                throw LineSocketError.system("read", errno)
            }
            if count == 0 { throw LineSocketError.closed }
            buffer.append(contentsOf: chunk[0..<count])
        }
    }

    func close() {
        if fd >= 0 {
            Darwin.close(fd)
            fd = -1
        }
        buffer.removeAll()
    }

    private func wait(for events: Int16, deadline: Date) throws {
        while true {
            let remaining = deadline.timeIntervalSinceNow
            guard remaining > 0 else { throw LineSocketError.timedOut }
            var descriptor = pollfd(fd: fd, events: events, revents: 0)
            let ready = poll(&descriptor, 1, Int32(min(remaining * 1000, Double(Int32.max)).rounded(.up)))
            if ready < 0 {
                if errno == EINTR { continue }
                throw LineSocketError.system("poll", errno)
            }
            if ready > 0 { return }
        }
    }

    /// One request/response pair on a fresh connection, as the daemon serves
    /// every request but `subscribe`.
    static func roundTrip(path: String, request: Data, timeout: TimeInterval) throws -> Data {
        let socket = LineSocket()
        defer { socket.close() }
        let deadline = Date().addingTimeInterval(timeout)
        try socket.connect(path: path)
        try socket.writeLine(request, deadline: deadline)
        return try socket.readLine(deadline: deadline)
    }
}
