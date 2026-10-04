import Foundation

/// Where the amx daemon listens, computed exactly as `ipc.SocketPath` does:
/// `$HOME/.local/share/agent-mux/daemon.sock`, or, with no usable home,
/// `agent-mux/daemon.sock` under the temporary directory (`$TMPDIR`, else
/// `/tmp`, as Go's `os.TempDir` reads it).
enum SocketPath {
    /// The longest path `sockaddr_un.sun_path` holds on macOS, terminator excluded.
    static let maxLength = MemoryLayout.size(ofValue: sockaddr_un().sun_path) - 1

    static func daemon(environment: [String: String] = ProcessInfo.processInfo.environment) -> String {
        if let home = environment["HOME"], !home.isEmpty {
            return (home as NSString).appendingPathComponent(".local/share/agent-mux/daemon.sock")
        }
        var temp = environment["TMPDIR"] ?? ""
        if temp.isEmpty { temp = "/tmp" }
        return (temp as NSString).appendingPathComponent("agent-mux/daemon.sock")
    }
}
