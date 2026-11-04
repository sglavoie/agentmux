import XCTest
@testable import AgentMux

final class ProjectCatalogTests: XCTestCase {
    private let t0 = Date(timeIntervalSince1970: 1_800_000_000)

    /// An AMXCommand whose runner records every call and answers `reply`.
    private func command(_ reply: @escaping ([String]) -> ProcessOutput?) -> (AMXCommand, () -> [(args: [String], env: [String: String])]) {
        var calls: [(args: [String], env: [String: String])] = []
        var amx = AMXCommand(binary: URL(fileURLWithPath: "/opt/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        amx.inheritedEnvironment = ["TERM_PROGRAM": "Ghostty", "LANG": "C"]
        amx.runner = { _, args, env, _ in
            calls.append((args, env))
            return reply(args).map(ProcessRun.finished) ?? .timedOut
        }
        return (amx, { calls })
    }

    private func output(_ text: String, status: Int32 = 0) -> ProcessOutput {
        ProcessOutput(status: status, stdout: Data(text.utf8), stderr: Data())
    }

    // MARK: Directory survey

    func testSurveyReportsOnlyDirectoriesThatAreNotThere() {
        let projects = [
            ProjectEntry(profile: "work", title: "API", directory: "/src/api"),
            ProjectEntry(profile: "home", title: "API", directory: "/src/api"),
            ProjectEntry(profile: "work", title: "Gone", directory: "/src/gone"),
            ProjectEntry(profile: "work", title: "Anywhere"),
        ]
        var asked: [String] = []
        let missing = ProjectCatalog.survey(projects) { path in
            asked.append(path)
            return path == "/src/api"
        }
        XCTAssertEqual(missing, ["/src/gone"])
        XCTAssertEqual(asked.sorted(), ["/src/api", "/src/gone"], "each directory once; a project naming none is not checked")
    }

    func testDirectoryExistsRejectsFilesAndMissingPaths() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        let file = root.appendingPathComponent("file")
        try Data().write(to: file)

        XCTAssertTrue(ProjectCatalog.directoryExists(root.path))
        XCTAssertFalse(ProjectCatalog.directoryExists(file.path), "a file is not a project directory")
        XCTAssertFalse(ProjectCatalog.directoryExists(root.appendingPathComponent("absent").path))
    }

    func testDirectoryExistsExpandsAConfiguredTilde() {
        // amx lists a directory as configuration writes it, `~` and all.
        XCTAssertTrue(ProjectCatalog.directoryExists("~"))
        XCTAssertEqual(ProjectCatalog.expandingTilde("~/projects/api"), NSHomeDirectory() + "/projects/api")
        XCTAssertEqual(ProjectCatalog.expandingTilde("/srv/api"), "/srv/api")
    }

    // MARK: Cache

    func testCatalogRefreshesAfterTenSeconds() {
        var catalog = ProjectCatalog()
        XCTAssertTrue(catalog.needsRefresh(now: t0), "an empty catalog loads")
        XCTAssertTrue(catalog.state.begin())
        XCTAssertFalse(catalog.state.begin(), "one fetch at a time")
        XCTAssertFalse(catalog.needsRefresh(now: t0), "not while one is running")
        catalog.state.finish(.success([ProjectEntry(profile: "work", title: "API")]), at: t0)
        XCTAssertFalse(catalog.needsRefresh(now: t0.addingTimeInterval(9)))
        XCTAssertTrue(catalog.needsRefresh(now: t0.addingTimeInterval(10)))
    }

    func testFailedRefreshKeepsTheCachedList() {
        var state = FetchState<[ProjectEntry]>()
        _ = state.begin()
        state.finish(.success([ProjectEntry(profile: "work", title: "API")]), at: t0)
        _ = state.begin()
        state.finish(.failure(.undecodable(status: 1, detail: "boom")), at: t0.addingTimeInterval(70))
        XCTAssertEqual(state.value?.map(\.title), ["API"])
        XCTAssertEqual(state.error, "boom")
        XCTAssertEqual(state.fetchedAt, t0, "a failure is not a fresh list")
        XCTAssertFalse(state.loading)
        _ = state.begin()
        state.finish(.success([]), at: t0.addingTimeInterval(80))
        XCTAssertNil(state.error)
        state.reset()
        XCTAssertNil(state.value)
    }

    func testGroupedKeepsConfigurationOrder() {
        let projects = [
            ProjectEntry(profile: "work", title: "API"),
            ProjectEntry(profile: "home", title: "Blog"),
            ProjectEntry(profile: "work", title: "Web"),
        ]
        let groups = ProjectCatalog.grouped(projects)
        XCTAssertEqual(groups.map(\.profile), ["work", "home"])
        XCTAssertEqual(groups[0].projects.map(\.title), ["API", "Web"])
        XCTAssertEqual(groups[1].projects.map(\.title), ["Blog"])
    }

    // MARK: amx project list / launch

    func testMissingConfigurationOffersSetupButInvalidAndUnreadableFilesRemainErrors() throws {
        for code in ["config_missing", "config_invalid", "config_unreadable", "unknown"] {
            let (amx, _) = command { _ in
                self.output("{\"schema_version\":1,\"error\":{\"code\":\"\(code)\",\"message\":\"Configuration unavailable\"}}", status: 1)
            }
            var catalog = ProjectCatalog()
            _ = catalog.state.begin()
            catalog.state.finish(ProjectCatalog.fetch(amx), at: t0)
            XCTAssertEqual(catalog.state.errorCode, code)
            XCTAssertEqual(ProjectCatalog.offersSetup(catalog.state, unavailable: nil), code == "config_missing")
            XCTAssertFalse(ProjectCatalog.offersSetup(catalog.state, unavailable: "amx unavailable"))
            if code == "config_missing" {
                XCTAssertTrue(ProjectCatalog.guidance(catalog.state, unavailable: nil).contains("Welcome"))
            } else {
                XCTAssertTrue(ProjectCatalog.guidance(catalog.state, unavailable: nil).contains("Could not refresh projects"))
            }
            _ = catalog.state.begin()
            XCTAssertFalse(ProjectCatalog.offersSetup(catalog.state, unavailable: nil), "wait for the current config check")
            catalog.state.finish(.success([ProjectEntry(profile: "work", title: "API")]), at: t0)
            XCTAssertNil(catalog.state.errorCode)
            XCTAssertFalse(ProjectCatalog.offersSetup(catalog.state, unavailable: nil))
        }
    }

    func testFetchDecodesTheProjectList() {
        let (amx, calls) = command { _ in
            self.output(#"{"schema_version":1,"data":[{"profile":"work","title":"API","harness":"claude","directory":"/p/api","groups":["be"]},{"profile":"home","title":"Blog","harness":"codex","directory":"/p/blog","groups":[]}]}"#)
        }
        let result = ProjectCatalog.fetch(amx)
        XCTAssertEqual(try result.get(), [
            ProjectEntry(profile: "work", title: "API", harness: "claude", directory: "/p/api", groups: ["be"]),
            ProjectEntry(profile: "home", title: "Blog", harness: "codex", directory: "/p/blog"),
        ])
        XCTAssertEqual(calls().first?.args, ["project", "list", "--json"])
        XCTAssertEqual(calls().first?.env["TERM_PROGRAM"], "Ghostty")
    }

    func testAnOlderAmxWithoutProjectFailsUndecodable() {
        let (amx, _) = command { _ in
            ProcessOutput(status: 1, stdout: Data(), stderr: Data("Error: unknown command \"project\" for \"amx\"".utf8))
        }
        guard case let .failure(.undecodable(_, detail)) = ProjectCatalog.fetch(amx) else {
            return XCTFail("expected an undecodable failure")
        }
        XCTAssertTrue(detail.contains("unknown command"))
    }

    func testLaunchNamesTitleProfileAndBackend() {
        let project = ProjectEntry(profile: "work", title: "API")
        XCTAssertEqual(ProjectCatalog.launchArguments(for: project), ["project", "launch", "--title", "API", "--profile", "work"])
        XCTAssertEqual(ProjectCatalog.launchArguments(for: ProjectEntry(profile: "", title: "X")), ["project", "launch", "--title", "X"])
        XCTAssertEqual(ProjectCatalog.launchArguments(for: project, directory: "/src/hotfix"),
                       ["project", "launch", "--title", "API", "--profile", "work", "--directory", "/src/hotfix"])
        XCTAssertEqual(ProjectCatalog.workspaceLaunchArguments(for: project, directory: "/src/hotfix").suffix(4),
                       ["--directory", "/src/hotfix", "--backend", "tmux"])

        let (amx, calls) = command { _ in
            self.output(#"{"schema_version":1,"data":{"action":"session.launch","changed":true,"message":"launched","project":{"profile":"work","title":"API","harness":"claude","directory":"/p","groups":[]},"runtime_session_id":"rs-1"}}"#)
        }
        let result = amx.json(ProjectLaunchResult.self, ProjectCatalog.launchArguments(for: project))
        XCTAssertEqual(result.value?.runtimeSessionID, "rs-1")
        XCTAssertEqual(result.value?.project?.title, "API")
        XCTAssertEqual(calls().first?.args.last, "--json")
    }

    func testLaunchWithoutRuntimeIdentityDecodes() {
        let result = AMXEnvelope.decode(ProjectLaunchResult.self, stdout: Data(#"{"schema_version":1,"data":{"action":"session.launch","changed":true,"project":{"profile":"w","title":"T"}}}"#.utf8), status: 0)
        XCTAssertEqual(result.value?.runtimeSessionID, "")
        XCTAssertEqual(result.value?.changed, true)
    }

    func testLaunchErrorsDecode() {
        let text = #"{"schema_version":1,"error":{"code":"not_found","message":"no configured project matches work/Nope","candidates":[{"id":"work/API","display_name":"API","canonical_path":"/p"}]}}"#
        guard case let .failure(.command(error, _)) = AMXEnvelope.decode(ProjectLaunchResult.self, stdout: Data(text.utf8), status: 1) else {
            return XCTFail("expected a command error")
        }
        XCTAssertEqual(error.code, "not_found")
        XCTAssertEqual(error.candidates.map(\.id), ["work/API"])
    }

}
