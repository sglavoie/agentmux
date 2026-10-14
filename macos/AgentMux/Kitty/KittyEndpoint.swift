import AppKit

/// Which Kitty socket a focus request should name.
///
/// The daemon dials only sockets it learned itself, so the candidates are
/// tried in order and an `unknown_socket` refusal moves on to the next: the
/// socket Preferences names, if any, then the session's own socket, then the socket the snapshot's window tree was read
/// through, then `unix:/tmp/kitty-<pid>` for every running Kitty whose socket
/// file exists — the address `listen_on unix:/tmp/kitty` gives each instance.
enum KittyEndpoint {
    static let bundleIdentifier = "net.kovidgoyal.kitty"

    /// What to try for one session; empty when Kitty exposes no socket at all.
    static func candidates(
        override: String = "",
        rowSocket: String,
        snapshotSocket: String,
        kittyPIDs: [pid_t],
        fileExists: (String) -> Bool = { FileManager.default.fileExists(atPath: $0) }
    ) -> [String] {
        var ordered: [String] = []
        func add(_ socket: String) {
            let trimmed = socket.trimmingCharacters(in: .whitespaces)
            if !trimmed.isEmpty, !ordered.contains(trimmed) { ordered.append(trimmed) }
        }
        add(override)
        add(rowSocket)
        add(snapshotSocket)
        for pid in kittyPIDs.sorted() {
            let path = "/tmp/kitty-\(pid)"
            if fileExists(path) { add("unix:" + path) }
        }
        return ordered
    }

    /// The socket of the Kitty with process `pid`: a known socket whose
    /// address names that process, else its default `/tmp/kitty-<pid>` when
    /// that file exists, else empty.
    static func socket(
        forPID pid: pid_t,
        known: [String],
        fileExists: (String) -> Bool = { FileManager.default.fileExists(atPath: $0) }
    ) -> String {
        if let match = known.first(where: { Self.pid(fromSocket: $0) == pid }) { return match }
        let path = "/tmp/kitty-\(pid)"
        return fileExists(path) ? "unix:" + path : ""
    }

    /// Where a command that opens something new should land: the Kitty the
    /// operator used last, when it still runs, ahead of the usual order.
    static func actionCandidates(
        override: String,
        lastActiveKittyPID: pid_t?,
        snapshot: CompanionSnapshot?,
        kittyPIDs: [pid_t],
        fileExists: (String) -> Bool = { FileManager.default.fileExists(atPath: $0) }
    ) -> [String] {
        var front = ""
        if let pid = lastActiveKittyPID, kittyPIDs.contains(pid) {
            let known = [snapshot?.kitty.socket ?? ""] + (snapshot?.kitty.sockets ?? [])
            front = socket(forPID: pid, known: known.filter { !$0.isEmpty }, fileExists: fileExists)
        }
        return candidates(
            override: override, rowSocket: front, snapshotSocket: snapshot?.kitty.socket ?? "",
            kittyPIDs: kittyPIDs, fileExists: fileExists
        )
    }

    /// The running Kitty processes.
    static func runningKittyPIDs() -> [pid_t] {
        NSRunningApplication.runningApplications(withBundleIdentifier: bundleIdentifier).map(\.processIdentifier)
    }

    /// The process a socket belongs to, when its address names one
    /// (`unix:/tmp/kitty-1234`, or `…-1234` with no scheme).
    static func pid(fromSocket socket: String) -> pid_t? {
        var path = Substring(socket)
        if path.hasPrefix("unix:") { path = path.dropFirst("unix:".count) }
        guard let dash = path.lastIndex(of: "-") else { return nil }
        let digits = path[path.index(after: dash)...]
        guard !digits.isEmpty, digits.allSatisfy(\.isASCII), digits.allSatisfy(\.isNumber) else { return nil }
        return pid_t(digits)
    }

    /// The menu's explanation when there is nothing to try.
    static let listenOnMissing = "Kitty has no remote-control socket: add “allow_remote_control yes” and “listen_on unix:/tmp/kitty” to kitty.conf, then restart Kitty."
}
