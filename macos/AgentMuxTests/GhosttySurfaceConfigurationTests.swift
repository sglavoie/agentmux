import GhosttyKit
import XCTest
@testable import AgentMux

final class GhosttySurfaceConfigurationTests: XCTestCase {
    func testWithoutAProgramNothingIsOverridden() {
        let configuration = Ghostty.SurfaceConfiguration(arguments: ["ignored"])
        XCTAssertNil(configuration.commandLine)
        configuration.withSurfaceConfig { config in
            XCTAssertNil(config.command)
            XCTAssertNil(config.working_directory)
            XCTAssertEqual(config.env_var_count, 0)
            XCTAssertEqual(config.font_size, 0)
            XCTAssertFalse(config.wait_after_command)
        }
    }

    func testAttachmentRunsAmxAttachForOneRuntimeSession() {
        let amx = URL(fileURLWithPath: "/Applications/Agent Mux.app/Contents/Resources/bin/amx")
        let configuration = Ghostty.SurfaceConfiguration.attachment(
            amx: amx,
            runtimeSessionID: "rs-0123456789abcdef0123456789abcdef",
            environment: ["PATH": "/opt/bin:/usr/bin", "AMX_CONFIG": "/tmp/c.toml"]
        )
        XCTAssertEqual(configuration.program, amx)
        XCTAssertEqual(configuration.arguments, ["attach", "rs-0123456789abcdef0123456789abcdef"])
        XCTAssertEqual(
            configuration.commandLine,
            "'/usr/bin/env' '/Applications/Agent Mux.app/Contents/Resources/bin/amx' 'attach' 'rs-0123456789abcdef0123456789abcdef'"
        )
        // The marker is what keeps the session's status line off in a
        // terminal the workspace hosts.
        XCTAssertEqual(configuration.passedEnvironment.map(\.name), ["AMX_CONFIG", "AMX_WORKSPACE_HOSTED", "PATH"])
        XCTAssertEqual(configuration.environment["AMX_WORKSPACE_HOSTED"], "1")
        XCTAssertEqual(configuration.scrollback, .server, "an attachment's history is the tmux server's from the start")
        XCTAssertEqual(Ghostty.SurfaceConfiguration().scrollback, .terminal)
    }

    func testEnvironmentDropsWhatGhosttyOwnsOrCannotCarry() {
        let configuration = Ghostty.SurfaceConfiguration(environment: [
            "TERM": "dumb",
            "COLORTERM": "no",
            "TERM_PROGRAM": "kitty",
            "TERMINFO": "/nowhere",
            "": "empty-name",
            "A=B": "assignment",
            "NUL": "a\0b",
            "MULTI": "line one\nline two",
            "KEPT": "value with spaces",
        ])
        let passed = configuration.passedEnvironment
        XCTAssertEqual(passed.map(\.name), ["KEPT", "MULTI"])
        XCTAssertEqual(passed.map(\.value), ["value with spaces", "line one\nline two"])
    }

    func testSurfaceConfigCarriesEveryField() {
        var configuration = Ghostty.SurfaceConfiguration(
            program: URL(fileURLWithPath: "/bin/cat"),
            arguments: ["-u"],
            environment: ["B": "2", "A": "1"],
            workingDirectory: URL(fileURLWithPath: "/tmp")
        )
        configuration.fontSize = 13
        configuration.waitAfterCommand = true
        configuration.withSurfaceConfig { config in
            XCTAssertEqual(config.command.map { String(cString: $0) }, "'/usr/bin/env' '/bin/cat' '-u'")
            XCTAssertEqual(config.working_directory.map { String(cString: $0) }, "/tmp")
            XCTAssertEqual(config.font_size, 13)
            XCTAssertTrue(config.wait_after_command)
            XCTAssertEqual(config.env_var_count, 2)
            let variables = (0..<config.env_var_count).map { i in
                "\(String(cString: config.env_vars[i].key))=\(String(cString: config.env_vars[i].value))"
            }
            XCTAssertEqual(variables, ["A=1", "B=2"])
        }
    }

    /// Ghostty runs a surface's command as `bash -c "exec -l <text>"`.
    private func runUnderGhosttysExecPrefix(_ configuration: Ghostty.SurfaceConfiguration) throws -> String {
        let commandLine = try XCTUnwrap(configuration.commandLine)
        let bash = Process()
        bash.executableURL = URL(fileURLWithPath: "/bin/bash")
        bash.arguments = ["--noprofile", "--norc", "-c", "exec -l \(commandLine)"]
        let out = Pipe()
        bash.standardOutput = out
        try bash.run()
        bash.waitUntilExit()
        XCTAssertEqual(bash.terminationStatus, 0)
        return String(decoding: out.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self)
    }

    func testEveryWordSurvivesTheShellIntact() throws {
        let configuration = Ghostty.SurfaceConfiguration(
            program: URL(fileURLWithPath: "/bin/sh"),
            arguments: ["-c", #"printf '%s|' "$0" "$@""#, "zero", "it's", "a b", "$HOME", "*", ""]
        )
        XCTAssertEqual(try runUnderGhosttysExecPrefix(configuration), "zero|it's|a b|$HOME|*||")
    }

    /// `exec -l` would hand the program a dashed argv[0], which makes a shell a
    /// login shell and stops amx recognizing its own name.
    func testTheProgramDoesNotSeeTheLoginDash() throws {
        let configuration = Ghostty.SurfaceConfiguration(
            program: URL(fileURLWithPath: "/bin/bash"),
            arguments: ["--noprofile", "--norc", "-c", #"shopt -q login_shell && echo login || echo plain; echo "$0""#]
        )
        XCTAssertEqual(try runUnderGhosttysExecPrefix(configuration), "plain\n/bin/bash\n")
    }

    func testAProgramPathWithAnEqualsSignStillRuns() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("amx=surface-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }
        let script = dir.appendingPathComponent("say")
        try "#!/bin/sh\necho \"said $1\"\n".write(to: script, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: script.path)

        let configuration = Ghostty.SurfaceConfiguration(program: script, arguments: ["a b"])
        XCTAssertTrue(try XCTUnwrap(configuration.commandLine).contains("'/bin/sh' '-c'"))
        XCTAssertEqual(try runUnderGhosttysExecPrefix(configuration), "said a b\n")
    }
}
