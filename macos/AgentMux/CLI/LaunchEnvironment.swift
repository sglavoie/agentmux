import Foundation
import os

/// The environment `amx` runs with. An app launched from Finder or launchd
/// inherits a minimal environment, so the user's login shell is asked for the
/// variables amx reads — its `PATH`, the `XDG_*` base directories, and every
/// `AMX_*` override (`AMX_CONFIG` above all) — as an interactive shell would
/// set them. `HOME` is passed through.
struct LaunchEnvironment: Equatable {
    /// Variable-name prefixes taken from the login shell besides `PATH`. An
    /// `AMX_CONFIG` exported from `~/.zshrc` never reaches a Finder-launched
    /// app, so without these the app would read another configuration than
    /// the terminal's.
    static let forwardedPrefixes = ["XDG_", "AMX_"]

    var home: String
    var path: String
    /// Forwarded variables the shell set to a non-empty value.
    var forwarded: [String: String] = [:]

    /// Variables a child `amx` receives on top of its inherited ones.
    var commandEnvironment: [String: String] {
        forwarded.merging(["HOME": home, "PATH": path]) { _, fixed in fixed }
    }

    static func isForwarded(_ name: String) -> Bool {
        forwardedPrefixes.contains { name.hasPrefix($0) && name.count > $0.count }
    }

    static func current(processEnvironment: [String: String] = ProcessInfo.processInfo.environment) -> LaunchEnvironment {
        let shell = processEnvironment["SHELL"] ?? "/bin/zsh"
        // Interactive first, since that is where most profiles export these;
        // a shell that cannot start interactively still reports its login PATH.
        let reported = loginShellEnvironment(shell: shell, interactive: true)
            ?? loginShellEnvironment(shell: shell, interactive: false)
        return resolve(processEnvironment: processEnvironment, reported: reported)
    }

    /// Combines what the login shell reported with what the app inherited. A
    /// variable the app itself inherited (a `launchctl setenv`) is the fallback
    /// for one the shell leaves unset.
    static func resolve(processEnvironment: [String: String], reported: [String: String]?) -> LaunchEnvironment {
        let home = processEnvironment["HOME"] ?? FileManager.default.homeDirectoryForCurrentUser.path
        let fallbackPath = processEnvironment["PATH"] ?? "/usr/bin:/bin:/usr/sbin:/sbin"
        var forwarded: [String: String] = [:]
        for (name, value) in processEnvironment where isForwarded(name) && !value.isEmpty {
            forwarded[name] = value
        }
        for (name, value) in reported ?? [:] where isForwarded(name) && !value.isEmpty {
            forwarded[name] = value
        }
        let path = reported?["PATH"].flatMap { $0.isEmpty ? nil : $0 } ?? fallbackPath
        return LaunchEnvironment(home: home, path: path, forwarded: forwarded)
    }

    static let beginMarker = "__AMX_ENV_BEGIN__"
    static let endMarker = "__AMX_ENV_END__"

    /// The shell script printing the whole environment as NUL-terminated
    /// `NAME=value` records between markers, so anything a profile prints is
    /// ignored and a value holding a newline survives.
    static var reportScript: String {
        "printf '%s' '\(beginMarker)'; /usr/bin/env -0; printf '%s' '\(endMarker)'"
    }

    private static let logger = Logger(subsystem: "io.github.sglavoie.agentmux", category: "environment")

    /// Runs `shell -l [-i] -c` and reads the report. Nil on failure, on a
    /// report without a `PATH`, or after `timeout`. A profile that leaves a
    /// process in the background holding the shell's stdout does not run
    /// into the timeout: the report is read once the shell itself exits.
    static func loginShellEnvironment(shell: String, interactive: Bool, timeout: TimeInterval = 5) -> [String: String]? {
        let arguments = (interactive ? ["-l", "-i"] : ["-l"]) + ["-c", reportScript]
        guard let output = ProcessOutput.run(shell, arguments: arguments, timeout: timeout) else {
            logger.warning("login shell \(shell, privacy: .public) failed to start or timed out")
            return nil
        }
        let text = String(decoding: output.stdout, as: UTF8.self)
        guard let report = extractReport(from: text, begin: beginMarker, end: endMarker),
              let path = report["PATH"], !path.isEmpty else {
            return nil
        }
        return report
    }

    /// The `NAME=value` records between the markers; empty values are dropped.
    static func extractReport(from output: String, begin: String, end: String) -> [String: String]? {
        guard let start = output.range(of: begin),
              let stop = output.range(of: end, range: start.upperBound..<output.endIndex) else {
            return nil
        }
        var report: [String: String] = [:]
        for record in output[start.upperBound..<stop.lowerBound].split(separator: "\0") {
            guard let equals = record.firstIndex(of: "=") else { continue }
            let value = String(record[record.index(after: equals)...])
            if !value.isEmpty {
                report[String(record[..<equals])] = value
            }
        }
        return report
    }
}
