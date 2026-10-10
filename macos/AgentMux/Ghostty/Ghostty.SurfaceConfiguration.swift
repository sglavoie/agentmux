import Foundation
import GhosttyKit

extension Ghostty {
    /// What one surface runs and how: its own program, arguments, environment,
    /// and working directory, so several terminals share one `Ghostty.App`
    /// without sharing a command. Everything left unset falls back to the app
    /// configuration, whose command is the user's shell.
    struct SurfaceConfiguration: Equatable {
        /// The program to run, by absolute path; nil runs the app
        /// configuration's command.
        var program: URL?
        /// Arguments after the program. Ignored without one.
        var arguments: [String] = []
        /// Variables added to what the program inherits from the app. A name
        /// Ghostty sets itself (`reservedEnvironment`) or that could not be
        /// passed intact is dropped.
        var environment: [String: String] = [:]
        /// Where the program starts; nil or a missing directory leaves
        /// Ghostty's default.
        var workingDirectory: URL?
        /// The font size in points, or nil for the app configuration's.
        var fontSize: Float?
        /// Keeps an exited surface open so its last output stays readable.
        /// Ghostty forces this on for any surface with a program of its own;
        /// the host hears about the exit through `GhosttySurfaceDelegate`.
        var waitAfterCommand = false

        /// A view attached to a runtime session: `amx attach <id>`, which
        /// validates the target, records the attachment, and takes over
        /// interactive ownership from any other view.
        static func attachment(
            amx: URL,
            runtimeSessionID: String,
            environment: [String: String] = [:]
        ) -> SurfaceConfiguration {
            SurfaceConfiguration(program: amx, arguments: ["attach", runtimeSessionID], environment: environment)
        }

        /// Ghostty sets these for the terminal it is; a caller's value would
        /// misdescribe it to the program.
        static let reservedEnvironment: Set<String> = [
            "TERM", "COLORTERM", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "TERMINFO",
        ]

        /// The variables that reach Ghostty, by name. They travel as C strings,
        /// so a NUL anywhere, or an empty name or one holding `=`, cannot be
        /// passed and is dropped rather than truncated.
        var passedEnvironment: [(name: String, value: String)] {
            environment.keys.sorted().compactMap { name in
                guard let value = environment[name],
                      !name.isEmpty, !name.contains("="), !name.contains("\0"), !value.contains("\0"),
                      !Self.reservedEnvironment.contains(name) else {
                    return nil
                }
                return (name, value)
            }
        }

        /// The shell text Ghostty runs, or nil without a program.
        ///
        /// Ghostty runs a surface's command as `bash -c "exec -l <text>"`, and
        /// `exec -l` puts a dash before argv[0]. Going through `env` keeps the
        /// dash off the program: amx recognizes itself by an argv[0] basename
        /// of exactly `amx`, and a shell given a dashed name would read login
        /// profiles. Every word is quoted, so none is split or expanded.
        var commandLine: String? {
            guard let program else { return nil }
            var words = ["/usr/bin/env"]
            if program.path.contains("=") {
                // env would read `/a=b/prog` as an assignment.
                words += ["/bin/sh", "-c", #"exec "$0" "$@""#]
            }
            words.append(program.path)
            words += arguments
            guard words.allSatisfy({ !$0.contains("\0") }) else { return nil }
            return words.map(Self.shellQuoted).joined(separator: " ")
        }

        static func shellQuoted(_ word: String) -> String {
            "'" + word.replacingOccurrences(of: "'", with: "'\\''") + "'"
        }

        /// Calls `body` with a `ghostty_surface_config_s` describing this
        /// configuration. Its strings live only for the call; Ghostty copies
        /// what it keeps while creating the surface.
        func withSurfaceConfig<T>(_ body: (inout ghostty_surface_config_s) -> T) -> T {
            var allocated: [UnsafeMutablePointer<CChar>] = []
            defer { allocated.forEach { free($0) } }
            func cString(_ string: String) -> UnsafePointer<CChar>? {
                guard let copy = strdup(string) else { return nil }
                allocated.append(copy)
                return UnsafePointer(copy)
            }

            var config = ghostty_surface_config_new()
            config.font_size = fontSize ?? 0
            config.wait_after_command = waitAfterCommand
            if let commandLine {
                config.command = cString(commandLine)
            }
            if let workingDirectory {
                config.working_directory = cString(workingDirectory.path)
            }
            var variables = passedEnvironment.compactMap { pair -> ghostty_env_var_s? in
                guard let name = cString(pair.name), let value = cString(pair.value) else { return nil }
                return ghostty_env_var_s(key: name, value: value)
            }
            return variables.withUnsafeMutableBufferPointer { buffer in
                if let base = buffer.baseAddress, !buffer.isEmpty {
                    config.env_vars = base
                    config.env_var_count = buffer.count
                }
                return body(&config)
            }
        }
    }
}
