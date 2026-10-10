import XCTest
@testable import AgentMux

final class BackendSetupTests: XCTestCase {
    private func decode(_ json: String, status: Int32 = 0) -> AMXCommandResult<BackendStatus> {
        AMXEnvelope.decode(BackendStatus.self, stdout: Data(json.utf8), status: status)
    }

    // MARK: Where a launch goes

    func testAutomaticLaunchesInKittyOnlyWhenOneIsRunning() {
        XCTAssertFalse(LaunchBackendPreference.automatic.launchesInWorkspace(kittyAvailable: true))
        XCTAssertTrue(LaunchBackendPreference.automatic.launchesInWorkspace(kittyAvailable: false))
    }

    func testAnExplicitChoiceIsNeverOverriddenByWhatIsRunning() {
        for kitty in [true, false] {
            XCTAssertTrue(LaunchBackendPreference.workspace.launchesInWorkspace(kittyAvailable: kitty))
            // Kitty with no Kitty fails where it is asked; it does not
            // quietly become a tmux launch the operator did not choose.
            XCTAssertFalse(LaunchBackendPreference.kitty.launchesInWorkspace(kittyAvailable: kitty))
        }
    }

    func testEveryChoiceHasItsOwnLabel() {
        let labels = LaunchBackendPreference.allCases.map(\.label)
        XCTAssertEqual(Set(labels).count, LaunchBackendPreference.allCases.count)
        XCTAssertFalse(labels.contains(""))
    }

    // MARK: The report

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
