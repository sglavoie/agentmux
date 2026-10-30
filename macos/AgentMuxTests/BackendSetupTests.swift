import XCTest
@testable import AgentMux

final class BackendSetupTests: XCTestCase {
    private func decode(_ json: String, status: Int32 = 0) -> AMXCommandResult<BackendStatus> {
        AMXEnvelope.decode(BackendStatus.self, stdout: Data(json.utf8), status: status)
    }

    // MARK: Where a launch goes

    func testFollowingCLIUsesTheReportedDestinationAndNeverGuesses() {
        XCTAssertEqual(LaunchBackendPreference.followCLI.resolved(cliBackend: "tmux"), .workspace)
        XCTAssertEqual(LaunchBackendPreference.followCLI.resolved(cliBackend: "kitty"), .kitty)
        XCTAssertNil(LaunchBackendPreference.followCLI.resolved(cliBackend: nil))
        XCTAssertNil(LaunchBackendPreference.followCLI.resolved(cliBackend: "future"))
        XCTAssertEqual(LaunchBackendPreference.workspace.resolved(cliBackend: "kitty"), .workspace)
        XCTAssertEqual(LaunchBackendPreference.kitty.resolved(cliBackend: "tmux"), .kitty)
        XCTAssertEqual(LaunchBackendPreference.stored("automatic"), .workspace)
        XCTAssertEqual(LaunchBackendPreference.stored(nil), .workspace)
    }

    func testLaunchMenuNamesTheEffectiveDestinationAndItsSource() {
        var input = MenuBuilder.ActionsInput()
        input.launchBackend = .followCLI
        input.cliLaunchBackend = "tmux"
        XCTAssertEqual(MenuBuilder.entries(for: .projects, input).first,
                       .info("Launch in workspace (tmux) · CLI configuration"))
        input.cliLaunchBackend = "kitty"
        XCTAssertEqual(MenuBuilder.entries(for: .projects, input).first,
                       .info("Launch in Kitty · CLI configuration"))
        input.cliLaunchBackendSource = "compatibility default"
        XCTAssertEqual(MenuBuilder.entries(for: .projects, input).first, .info("Launch in Kitty · compatibility default"))
        input.cliLaunchBackend = "tmux"
        input.cliLaunchBackendSource = "default"
        XCTAssertEqual(MenuBuilder.entries(for: .projects, input).first, .info("Launch in workspace (tmux) · default"))
        XCTAssertTrue(MenuBuilder.entries(for: .projects, input).contains(.action(.launchSettings, "Launch Settings…")))
        input.launchBackend = .workspace
        XCTAssertEqual(MenuBuilder.entries(for: .projects, input).first,
                       .info("Launch in workspace (tmux) · app preference"))
    }

    func testEveryChoiceHasItsOwnLabel() {
        let labels = LaunchBackendPreference.allCases.map(\.label)
        XCTAssertEqual(Set(labels).count, LaunchBackendPreference.allCases.count)
        XCTAssertFalse(labels.contains(""))
    }

    // MARK: The report

    func testTmuxDefaultReportResolvesToWorkspace() {
        let result = decode(#"{"schema_version":1,"data":{"launch_backend":"tmux","launch_backend_source":"default"}}"#)
        guard case let .success(status) = result else { return XCTFail("Default report did not decode") }
        XCTAssertEqual(status.launchBackend, "tmux")
        XCTAssertEqual(status.launchBackendSource, "default")
        XCTAssertEqual(LaunchBackendPreference.followCLI.resolved(cliBackend: status.launchBackend), .workspace)
    }

    func testAWorkingSetupDecodesAndReadsAsOneLineEach() throws {
        let result = decode("""
        {"schema_version":1,"data":{"launch_backend":"kitty",
          "tmux":{"available":true,"path":"/Users/x/.local/share/agent-mux/runtime/tmux-3.7c-ab/bin/tmux",
                  "version":"3.7c","source":"bundled","verified":true,"min_version":"3.5","candidates":[]},
          "server":{"directory":"/tmp/d","socket":"/tmp/d/tmux.sock","running":true,
                    "binary":"/Users/x/.local/share/agent-mux/runtime/tmux-3.7c-ab/bin/tmux","sessions":3,"agents":2},
          "problems":[]}}
        """)
        let status = try XCTUnwrap(result.value)
        XCTAssertTrue(status.tmux.available)
        XCTAssertTrue(status.tmux.verified)
        XCTAssertEqual(status.server.agents, 2)
        XCTAssertEqual(status.tmuxLine, "tmux 3.7c (bundled with Agent Mux, verified)")
        XCTAssertEqual(status.serverLine, "Running, 2 agents running")

        let setup = BackendSetup(result.result)
        XCTAssertEqual(setup.lines.map(\.0), ["tmux", "Session server"])
        XCTAssertTrue(setup.notes.isEmpty)
    }

    func testAMissingTmuxIsShownWithWhatToDoAboutIt() throws {
        let result = decode("""
        {"schema_version":1,"data":{"launch_backend":"tmux",
          "tmux":{"available":false,"verified":false,"min_version":"3.5",
                  "candidates":[{"source":"path","problem":"tmux not found"}]},
          "server":{"directory":"/tmp/d","socket":"/tmp/d/tmux.sock","running":false,"sessions":0,"agents":0},
          "problems":[{"code":"tmux_unavailable","severity":"error",
                       "message":"The tmux backend cannot start agents: no supported tmux found",
                       "hint":"Install tmux 3.5 or newer (`brew install tmux`)."}]}}
        """)
        let status = try XCTUnwrap(result.value)
        XCTAssertEqual(status.tmuxLine, "Not available (needs 3.5 or newer)")
        XCTAssertEqual(status.serverLine, "Not running; the first workspace launch starts it")

        let notes = BackendSetup(result.result).notes
        XCTAssertEqual(notes.count, 1)
        XCTAssertTrue(notes[0].warning)
        XCTAssertTrue(notes[0].text.contains("cannot start agents"))
        XCTAssertTrue(notes[0].text.contains("brew install tmux"), "the fix must be shown with the problem")
    }

    func testAServerKeepingItsTmuxAcrossAnUpdateIsANoteNotAWarning() throws {
        let result = decode("""
        {"schema_version":1,"data":{"tmux":{"available":true,"version":"3.8","source":"bundled","verified":true},
          "server":{"running":true,"binary":"/old/bin/tmux","sessions":1,"agents":1},
          "problems":[{"code":"server_keeps_previous_tmux","severity":"info",
                       "message":"The running server keeps the tmux it was started with (/old/bin/tmux)."}]}}
        """)
        let notes = BackendSetup(result.result).notes
        XCTAssertEqual(notes.count, 1)
        XCTAssertFalse(notes[0].warning)
        XCTAssertEqual(try XCTUnwrap(result.value).serverLine, "Running, 1 agent running")
    }

    func testEachSourceIsNamedInWords() {
        var status = BackendStatus()
        status.tmux.available = true
        status.tmux.version = "3.7c"
        for (source, phrase) in [
            "path": "found on PATH", "config": "set in config.toml", "environment": "set by AMX_TMUX",
            "installed": "installed by Agent Mux", "bundled": "bundled with Agent Mux",
        ] {
            status.tmux.source = source
            XCTAssertEqual(status.tmuxLine, "tmux 3.7c (\(phrase))")
        }
    }

    func testAnAmxThatCannotReportIsSaidSo() {
        let result = decode("Error: unknown command \"backend\" for \"amx\"\n", status: 1)
        let setup = BackendSetup(result.result)
        guard case .unavailable = setup else { return XCTFail("\(setup)") }
        XCTAssertEqual(setup.lines.map(\.1), ["unknown"])
        XCTAssertEqual(setup.notes.count, 1)
        XCTAssertTrue(setup.notes[0].warning)
        XCTAssertTrue(setup.notes[0].text.hasPrefix("This amx could not report the backend setup"))
    }

    /// One field of an unexpected type costs that field, not the report.
    func testAFieldOfTheWrongTypeKeepsTheRest() throws {
        let status = try XCTUnwrap(decode(#"""
        {"schema_version":1,"data":{"launch_backend":7,"tmux":{"available":"yes","path":"/t/tmux","version":3.5,"source":"bundled"},
        "server":{"running":true,"agents":"two"},"problems":[{"code":"x","severity":"info","message":1}]}}
        """#.replacingOccurrences(of: "\n", with: "")).value)
        XCTAssertEqual(status.launchBackend, "")
        XCTAssertFalse(status.tmux.available)
        XCTAssertEqual(status.tmux.path, "/t/tmux")
        XCTAssertEqual(status.tmux.version, "")
        XCTAssertEqual(status.tmux.source, "bundled")
        XCTAssertTrue(status.server.running)
        XCTAssertEqual(status.server.agents, 0)
        XCTAssertEqual(status.problems, [BackendStatus.Problem(code: "x", severity: "info", message: "")])
    }

    func testAHealthReplyWithAFieldOfTheWrongTypeStillAnswers() throws {
        let health = try JSONDecoder().decode(HealthResponse.self, from: Data(#"""
        {"ok":true,"ready":"yes","pid":"12","degraded_reason":5,"build":{"version":"0.9.0","dirty":"no","revision":"abc"}}
        """#.utf8))
        XCTAssertTrue(health.ok)
        XCTAssertFalse(health.ready)
        XCTAssertNil(health.pid)
        XCTAssertNil(health.degradedReason)
        XCTAssertEqual(health.build, DaemonBuild(version: "0.9.0", revision: "abc"))
    }

    func testAnEmptyReportDecodesToItsDefaults() throws {
        let status = try XCTUnwrap(decode(#"{"schema_version":1,"data":{}}"#).value)
        XCTAssertEqual(status, BackendStatus())
        XCTAssertEqual(status.tmuxLine, "Not available")
    }

    // MARK: The bundled runtime

    private let resources = URL(fileURLWithPath: "/Applications/Agent Mux.app/Contents/Resources")
    private var bundledFiles: Set<String> {
        ["\(resources.path)/tmux/SHA256SUMS", "\(resources.path)/tmux/bin/tmux"]
    }

    func testEveryAmxIsToldWhereTheBundledRuntimeIs() {
        let files = bundledFiles
        let environment = LaunchEnvironment(home: "/Users/x", path: "/usr/bin")
            .namingBundledRuntime(resources: resources) { files.contains($0) }
        XCTAssertEqual(environment.forwarded["AMX_TMUX_RUNTIME"], "\(resources.path)/tmux")
        XCTAssertEqual(environment.commandEnvironment["AMX_TMUX_RUNTIME"], "\(resources.path)/tmux")
    }

    func testABundleWithoutARuntimeNamesNone() {
        let base = LaunchEnvironment(home: "/Users/x", path: "/usr/bin")
        XCTAssertEqual(base.namingBundledRuntime(resources: resources) { _ in false }, base)
        XCTAssertEqual(base.namingBundledRuntime(resources: nil) { _ in true }, base)
        // A runtime without its checksums cannot be verified, so it is not offered.
        XCTAssertEqual(base.namingBundledRuntime(resources: resources) { $0.hasSuffix("/bin/tmux") }, base)
    }

    func testTheOperatorsOwnRuntimeIsKept() {
        let files = bundledFiles
        var base = LaunchEnvironment(home: "/Users/x", path: "/usr/bin")
        base.forwarded["AMX_TMUX_RUNTIME"] = "/opt/custom/tmux"
        let environment = base.namingBundledRuntime(resources: resources) { files.contains($0) }
        XCTAssertEqual(environment.forwarded["AMX_TMUX_RUNTIME"], "/opt/custom/tmux")
    }
}

extension BackendSetupTests {
    func testAppDestinationExplainsCLIOverride() {
        let text = LaunchBackendPreference.workspace.explanation(cliBackend: "kitty")
        XCTAssertTrue(text.contains("workspace (tmux)"))
        XCTAssertTrue(text.contains("Command-line launches use Kitty"))
        XCTAssertTrue(text.contains("overrides config.toml"))
        XCTAssertEqual(LaunchBackendPreference.kitty.destination(cliBackend: "tmux"), "Kitty")
    }
}
