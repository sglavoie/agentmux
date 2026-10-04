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
            return output
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

    func testTimeoutOrLaunchFailure() {
        let amx = command(Recorder(), output: nil)
        XCTAssertEqual(amx.json(Mute.self, ["x"], timeout: 2), .failure(.launchFailedOrTimedOut(binary: "/bin/amx", timeout: 2)))
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
            default: return ProcessOutput(status: 1, stdout: Data(), stderr: Data())
            }
            return ProcessOutput(status: 0, stdout: Data(text.utf8), stderr: Data())
        }
        XCTAssertEqual(AMXCapabilities.probe(amx), AMXCapabilities(notificationsSnooze: true))
        XCTAssertEqual(recorder.calls.map(\.1), [["--help"], ["notifications", "--help"]])
    }
}
