import XCTest
@testable import AgentMux

final class AMXCommandTests: XCTestCase {
    private struct Mute: Decodable, Equatable {
        let windowID: Int
        let muted: Bool
        let changed: Bool
        enum CodingKeys: String, CodingKey {
            case windowID = "window_id"
            case muted, changed
        }
    }

    private func decode(_ text: String, status: Int32 = 0, stderr: String = "") -> AMXCommandResult<Mute> {
        AMXEnvelope.decode(Mute.self, stdout: Data(text.utf8), stderr: Data(stderr.utf8), status: status)
    }

    // MARK: Envelope

    func testSuccessEnvelope() {
        let result = decode(#"{"schema_version":1,"data":{"window_id":7,"muted":true,"changed":true,"extra":1}}"# + "\n")
        XCTAssertEqual(result, .success(Mute(windowID: 7, muted: true, changed: true)))
    }

    func testPartialSuccessKeepsTheData() {
        let result = decode(#"{"schema_version":1,"data":{"window_id":7,"muted":true,"changed":true}}"#, status: 1)
        XCTAssertEqual(result, .partial(Mute(windowID: 7, muted: true, changed: true), status: 1))
        XCTAssertEqual(result.value?.windowID, 7)
    }

    func testErrorEnvelopeWithCandidatesAndParameters() {
        let text = """
        {"schema_version":1,"error":{"code":"ambiguous_project","message":"title API matches 2 projects",\
        "candidates":[{"id":"work/API","display_name":"API","canonical_path":"/p/api"},{"id":"home/API","display_name":"API"}],\
        "parameters":[{"name":"profile","description":"profile name","required":false}]}}
        """
        guard case let .failure(.command(error, status)) = decode(text, status: 1) else {
            return XCTFail("expected a command error")
        }
        XCTAssertEqual(status, 1)
        XCTAssertEqual(error.code, "ambiguous_project")
        XCTAssertEqual(error.message, "title API matches 2 projects")
        XCTAssertEqual(error.candidates, [
            .init(id: "work/API", displayName: "API", canonicalPath: "/p/api"),
            .init(id: "home/API", displayName: "API", canonicalPath: nil),
        ])
        XCTAssertEqual(error.parameters, [.init(name: "profile", description: "profile name", required: false)])
        XCTAssertEqual(AMXCommandError.command(error, status: 1).message, "title API matches 2 projects")
    }

    func testErrorEnvelopeIsAFailureEvenWithAZeroExit() {
        guard case let .failure(.command(error, status)) = decode(#"{"schema_version":1,"error":{"code":"x","message":""}}"#) else {
            return XCTFail("expected a command error")
        }
        XCTAssertEqual(status, 1)
        XCTAssertEqual(error, AMXJSONError(code: "x", message: ""))
        XCTAssertEqual(AMXCommandError.command(error, status: 1).message, "x")
    }

    func testUnsupportedSchemaVersion() {
        XCTAssertEqual(decode(#"{"schema_version":2,"data":{}}"#), .failure(.unsupportedSchema(2)))
        XCTAssertEqual(decode(#"{"data":{}}"#), .failure(.unsupportedSchema(0)))
    }

    func testTextOutputIsUndecodableWithStderrDetail() {
        let result = decode("", status: 1, stderr: "Error: unknown command \"project\" for \"amx\"\nRun 'amx --help' for usage.\n")
        guard case let .failure(.undecodable(status, detail)) = result else { return XCTFail("expected undecodable") }
        XCTAssertEqual(status, 1)
        XCTAssertTrue(detail.contains("unknown command \"project\""))
    }

    func testDataOfTheWrongShapeIsUndecodable() {
        guard case .failure(.undecodable) = decode(#"{"schema_version":1,"data":{"window_id":"seven"}}"#) else {
            return XCTFail("expected undecodable")
        }
        guard case .failure(.undecodable) = decode(#"{"schema_version":1}"#) else {
            return XCTFail("expected undecodable")
        }
    }

    func testFirstLineOfMultiLineOutput() {
        let text = #"{"schema_version":1,"data":{"window_id":1,"muted":false,"changed":false}}"# + "\n" + #"{"schema_version":1,"data":null}"#
        XCTAssertEqual(decode(text), .success(Mute(windowID: 1, muted: false, changed: false)))
    }

    func testBareArrayData() {
        let result = AMXEnvelope.decode([String].self, stdout: Data(#"{"schema_version":1,"data":["a","b"]}"#.utf8), status: 0)
        XCTAssertEqual(result, .success(["a", "b"]))
    }

    // MARK: Environment

    func testChildEnvironmentStripsInheritedKittyAndSetsTheSocket() {
        let inherited = [
            "KITTY_WINDOW_ID": "4", "KITTY_LISTEN_ON": "unix:/tmp/kitty-1", "KITTY_PID": "1",
            "PATH": "/usr/bin", "HOME": "/old", "LANG": "en_US.UTF-8", "AMX_CONFIG": "/inherited.toml",
        ]
        let launch = LaunchEnvironment(home: "/Users/me", path: "/opt/bin:/usr/bin", forwarded: ["AMX_CONFIG": "/shell.toml"])
        let environment = AMXCommand.childEnvironment(inherited: inherited, launch: launch, kittyListenOn: "unix:/tmp/kitty-9")
        XCTAssertEqual(environment["KITTY_LISTEN_ON"], "unix:/tmp/kitty-9")
        XCTAssertNil(environment["KITTY_WINDOW_ID"])
        XCTAssertNil(environment["KITTY_PID"])
        XCTAssertEqual(environment["PATH"], "/opt/bin:/usr/bin")
        XCTAssertEqual(environment["HOME"], "/Users/me")
        XCTAssertEqual(environment["AMX_CONFIG"], "/shell.toml")
        XCTAssertEqual(environment["LANG"], "en_US.UTF-8")
    }

    func testChildEnvironmentWithoutASocketCarriesNoKittyAtAll() {
        let launch = LaunchEnvironment(home: "/h", path: "/p")
        for socket in [nil, ""] as [String?] {
            let environment = AMXCommand.childEnvironment(
                inherited: ["KITTY_LISTEN_ON": "unix:/tmp/kitty-1"], launch: launch, kittyListenOn: socket
            )
            XCTAssertTrue(environment.keys.filter { $0.hasPrefix("KITTY_") }.isEmpty)
        }
    }

    // MARK: Runs

    private final class Recorder {
        var calls: [(String, [String], [String: String], TimeInterval)] = []
    }

    private func command(_ recorder: Recorder, output: ProcessOutput?) -> AMXCommand {
        var command = AMXCommand(
            binary: URL(fileURLWithPath: "/bin/amx"),
            launchEnvironment: LaunchEnvironment(home: "/h", path: "/p"),
            inheritedEnvironment: ["KITTY_WINDOW_ID": "3"]
        )
        command.runner = { executable, arguments, environment, timeout in
            recorder.calls.append((executable, arguments, environment, timeout))
            return output.map(ProcessRun.finished) ?? .timedOut
        }
        return command
    }

    func testJSONAppendsTheFlagOnceAndPassesTheSocket() {
        let recorder = Recorder()
        let out = ProcessOutput(status: 0, stdout: Data(#"{"schema_version":1,"data":{"window_id":2,"muted":true,"changed":true}}"#.utf8), stderr: Data())
        let amx = command(recorder, output: out)
        XCTAssertEqual(amx.json(Mute.self, ["mute-toggle", "--window-id", "2"], kittyListenOn: "unix:/tmp/k"),
                       .success(Mute(windowID: 2, muted: true, changed: true)))
        _ = amx.json(Mute.self, ["mute-toggle", "--json"])
        XCTAssertEqual(recorder.calls.map(\.1), [["mute-toggle", "--window-id", "2", "--json"], ["mute-toggle", "--json"]])
        XCTAssertEqual(recorder.calls[0].0, "/bin/amx")
        XCTAssertEqual(recorder.calls[0].2["KITTY_LISTEN_ON"], "unix:/tmp/k")
        XCTAssertNil(recorder.calls[0].2["KITTY_WINDOW_ID"])
        XCTAssertNil(recorder.calls[1].2["KITTY_LISTEN_ON"])
        XCTAssertEqual(recorder.calls[0].3, AMXCommand.defaultTimeout)
    }

    func testTimeoutIsNotALaunchFailure() {
        let amx = command(Recorder(), output: nil)
        XCTAssertEqual(amx.json(Mute.self, ["x"], timeout: 2), .failure(.timedOut(binary: "/bin/amx", timeout: 2, mutates: false)))
        XCTAssertEqual(AMXCommandError.timedOut(binary: "/bin/amx", timeout: 2, mutates: false).message, "amx did not answer within 2s")
    }

    func testAMutatingCommandThatTimesOutHasAnUnknownOutcome() {
        let amx = command(Recorder(), output: nil)
        let result = amx.json(Mute.self, ["project", "launch"], timeout: AMXCommand.launchTimeout, mutates: true)
        guard case let .failure(error) = result else { return XCTFail("\(result)") }
        XCTAssertEqual(error, .timedOut(binary: "/bin/amx", timeout: 120, mutates: true))
        XCTAssertEqual(error.message, "amx did not answer within 120s; the outcome is unknown — check the session list")
    }

    func testLaunchFailureIsReportedAsSuch() {
        var amx = command(Recorder(), output: nil)
        amx.runner = { _, _, _, _ in .launchFailed(reason: "no such file") }
        let result = amx.json(Mute.self, ["x"], mutates: true)
        XCTAssertEqual(result, .failure(.launchFailed(binary: "/bin/amx", reason: "no such file")))
        XCTAssertEqual(AMXCommandError.launchFailed(binary: "/bin/amx", reason: "no such file").message,
                       "/bin/amx could not be started: no such file")
    }

    func testMutatingCommandsHaveLongTimeouts() {
        XCTAssertGreaterThanOrEqual(AMXCommand.launchTimeout, 120)
        XCTAssertGreaterThanOrEqual(AMXCommand.actionTimeout, 30)
        XCTAssertGreaterThan(AMXCommand.actionTimeout, AMXCommand.defaultTimeout)
    }

    func testDataThatDoesNotDecodeReadsAsAVersionMismatch() {
        let result = decode(#"{"schema_version":1,"data":{"window_id":"seven"}}"#)
        guard case let .failure(error) = result else { return XCTFail("\(result)") }
        XCTAssertEqual(error.message, AMXEnvelope.undecodableData)
        XCTAssertFalse(error.message.contains("DecodingError"))
        XCTAssertFalse(error.message.contains("typeMismatch"))
    }

    func testMapKeepsPartialAndFailure() {
        let partial: AMXCommandResult<Int> = .partial(2, status: 3)
        XCTAssertEqual(partial.map { $0 * 2 }, .partial(4, status: 3))
        let failed: AMXCommandResult<Int> = .failure(.unsupportedSchema(2))
        XCTAssertEqual(failed.map { $0 * 2 }, .failure(.unsupportedSchema(2)))
    }

    func testRealProcessReportsAMissingExecutable() {
        guard case .launchFailed = ProcessOutput.execute("/nonexistent/amx", arguments: [], timeout: 2) else {
            return XCTFail("a missing executable never starts")
        }
        XCTAssertEqual(ProcessOutput.execute("/bin/sleep", arguments: ["5"], timeout: 0.2), .timedOut)
    }

    func testRealProcessTimesOut() throws {
        let start = Date()
        XCTAssertNil(ProcessOutput.run("/bin/sleep", arguments: ["5"], timeout: 0.3))
        XCTAssertLessThan(Date().timeIntervalSince(start), 3)
        let echo = try XCTUnwrap(ProcessOutput.run("/bin/sh", arguments: ["-c", "echo out; echo err >&2; exit 3"], timeout: 5))
        XCTAssertEqual(echo.status, 3)
        XCTAssertEqual(String(decoding: echo.stdout, as: UTF8.self), "out\n")
        XCTAssertEqual(String(decoding: echo.stderr, as: UTF8.self), "err\n")
    }

    func testChildThatClosesItsPipesStillTimesOut() {
        let start = Date()
        XCTAssertNil(ProcessOutput.run("/bin/sh", arguments: ["-c", "exec 1>&- 2>&-; exec sleep 5"], timeout: 0.2))
        XCTAssertLessThan(Date().timeIntervalSince(start), 1.5)
    }

    /// A login shell that leaves something in the background holding its
    /// stdout: the run ends with the shell, keeping what it printed.
    func testABackgroundedGrandchildHoldingThePipeDoesNotTimeOut() throws {
        let start = Date()
        let output = try XCTUnwrap(ProcessOutput.run("/bin/sh", arguments: ["-c", "echo report; sleep 5 & exit 0"], timeout: 3))
        XCTAssertLessThan(Date().timeIntervalSince(start), 1.5)
        XCTAssertEqual(output.status, 0)
        XCTAssertEqual(String(decoding: output.stdout, as: UTF8.self), "report\n")
    }

    func testLongOutputIsReadWhole() throws {
        let output = try XCTUnwrap(ProcessOutput.run("/bin/sh", arguments: ["-c", "head -c 300000 /dev/zero"], timeout: 5))
        XCTAssertEqual(output.stdout.count, 300_000)
    }

    func testADetachedChildIsReapedWhenItExits() throws {
        var pid: pid_t = 0
        var argv: [UnsafeMutablePointer<CChar>?] = [strdup("/usr/bin/true"), nil]
        defer { argv.forEach { free($0) } }
        XCTAssertEqual(posix_spawn(&pid, "/usr/bin/true", nil, nil, &argv, nil), 0)
        AMXCommand.reapWhenExited(pid)
        let deadline = Date().addingTimeInterval(3)
        // A zombie still answers signal 0; a reaped child does not.
        while kill(pid, 0) == 0, Date() < deadline { Thread.sleep(forTimeInterval: 0.02) }
        XCTAssertEqual(kill(pid, 0), -1)
        XCTAssertEqual(errno, ESRCH)
    }

    func testChildThatIgnoresTerminateIsKilled() {
        let start = Date()
        XCTAssertNil(ProcessOutput.run("/bin/sh", arguments: ["-c", "trap '' TERM; exec 1>&- 2>&-; while :; do sleep 0.05; done"], timeout: 0.2))
        XCTAssertLessThan(Date().timeIntervalSince(start), 1.5 + ProcessOutput.terminationGrace)
    }

    // MARK: Help probe

    static let rootHelp = """
    Agent Mux is a multi-harness session manager for Kitty.

    Usage:
      amx [flags]
      amx [command]

    Available Commands:
      config             Manage amx configuration
      mute-toggle        Toggle mute on the focused Agent Mux tab
      notifications      Inspect and test native notifications
      open-dashboard     Bring the dashboard forward, opening it if absent
      project            List and launch configured projects

    Flags:
          --debug           enable verbose debug output
      -h, --help            help for amx
      -v, --version         version for amx

    Use "amx [command] --help" for more information about a command; --json is not a flag here.
    """

    func testHelpParsesCommandsAndFlags() {
        let help = AMXHelp(Self.rootHelp)
        XCTAssertEqual(help.subcommands, ["config", "mute-toggle", "notifications", "open-dashboard", "project"])
        XCTAssertEqual(help.flags, ["--debug", "--help", "--version"])
    }

    /// cobra command groups, as `amx --help` prints them since its commands
    /// were grouped: the titles are arbitrary, so every group is read.
    static let groupedRootHelp = """
    Agent Mux is a multi-harness session manager for Kitty.

    Usage:
      amx [flags]
      amx [command]

    Examples:
      amx project launch API

    Sessions and projects:
      open-dashboard     Focus the Agent Mux dashboard
      project            List configured projects and launch sessions from them

    Kitty tabs:
      mute-toggle        Toggle mute on the focused Agent Mux tab

    Setup and diagnostics:
      config             Manage amx configuration
      notifications      Inspect and test native notifications

    Additional Commands:
      help               Help about any command

    Flags:
          --debug           enable verbose debug output
      -h, --help            help for amx
    """

    func testHelpParsesGroupedCommands() {
        let help = AMXHelp(Self.groupedRootHelp)
        XCTAssertEqual(help.subcommands, ["config", "help", "mute-toggle", "notifications", "open-dashboard", "project"])
        XCTAssertEqual(help.flags, ["--debug", "--help"])
    }

    func testCapabilitiesFromHelp() {
        let notifications = AMXHelp("Available Commands:\n  snooze      Snooze\n  test        Test\n")
        let mute = AMXHelp("Flags:\n  -h, --help   help\n      --json   JSON output\n\nGlobal Flags:\n      --debug   debug\n")
        XCTAssertEqual(
            AMXCapabilities(root: AMXHelp(Self.rootHelp), notifications: notifications, muteToggle: mute),
            AMXCapabilities(project: true, openDashboard: true, notificationsSnooze: true, muteToggleJSON: true)
        )
        XCTAssertEqual(AMXCapabilities(root: nil, notifications: nil, muteToggle: nil), .none)
        let old = AMXHelp("Available Commands:\n  config   Manage\n  mute-toggle  Toggle\n")
        XCTAssertEqual(AMXCapabilities(root: old, notifications: nil, muteToggle: AMXHelp("Flags:\n  -h, --help  help\n")), .none)
    }

    func testProbeAsksOnlyForWhatTheRootLists() {
        let recorder = Recorder()
        var amx = command(recorder, output: nil)
        amx.runner = { executable, arguments, environment, timeout in
            recorder.calls.append((executable, arguments, environment, timeout))
            let text: String
            switch arguments {
            case ["--help"]: text = "Available Commands:\n  notifications  N\n"
            case ["notifications", "--help"]: text = "Available Commands:\n  snooze  S\n"
            default: return .finished(ProcessOutput(status: 1, stdout: Data(), stderr: Data()))
            }
            return .finished(ProcessOutput(status: 0, stdout: Data(text.utf8), stderr: Data()))
        }
        XCTAssertEqual(AMXCapabilities.probe(amx), AMXCapabilities(notificationsSnooze: true))
        XCTAssertEqual(recorder.calls.map(\.1), [["--help"], ["notifications", "--help"]])
    }
}
