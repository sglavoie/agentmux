import XCTest
@testable import AgentMux

private final class ActivityRunner: ASLWorkRunner {
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { pending.append { then(work()) } }
    func release(_ index: Int = 0) { pending.remove(at: index)() }
}

@MainActor
final class WorkspaceActivityTests: XCTestCase {
    private func entry(_ id: String, at: String = "2026-10-15T12:00:00Z") -> String {
        """
        {"id":"\(id)","at":"\(at)","kind":"completed","title":"Turn finished","detail":"Working → Turn finished","event":"Stop","harness":"claude","profile":"work","session_id":"a","runtime_session_id":"runtime","run_id":"","trace_id":"trace"}
        """
    }
    private func report(_ entries: [String]) -> String {
        """
        {"schema_version":1,"data":{"entries":[\(entries.joined(separator: ","))],"since":"2026-10-08T00:00:00Z","earliest_available":"2026-10-01T00:00:00Z","truncated":false,"corrupt_lines":0,"matched":\(entries.count)}}
        """
    }
    private func command(_ response: @escaping ([String]) -> String) -> AMXCommand {
        var command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        command.runner = { _, args, _, _ in .finished(ProcessOutput(status: 0, stdout: Data(response(args).utf8), stderr: Data())) }
        return command
    }
    private var memory: WorkspaceActivityReadStore { .init(load: { .init() }, save: { _ in }) }

    func testSelectedReadAndUndoRestorePersistedMarkersAndSelectionWithoutHidingLateEvents() throws {
        let runner = ActivityRunner()
        var saved = WorkspaceActivityReadState()
        var failSave = false
        var response = report([entry("one"), entry("two")])
        let amx = command { _ in response }
        let store = WorkspaceActivityReadStore(load: { saved }, save: {
            if failSave { throw CocoaError(.fileWriteNoPermission) }
            saved = $0
        })
        let model = WorkspaceActivityModel(command: { amx }, runner: runner, readStore: store)
        model.refresh(); runner.release()
        model.markSelectedRead()
        XCTAssertEqual(Set(saved.read.keys), ["one"])
        XCTAssertEqual(model.visible.map(\.id), ["two"])
        XCTAssertTrue(model.canUndoRead)
        response = report([entry("one"), entry("two"), entry("late", at: "2026-10-14T00:00:00Z")])
        model.refresh(); runner.release(runner.pending.count - 1)
        failSave = true
        model.undoMarkRead()
        XCTAssertTrue(model.canUndoRead, "failed Undo remains retryable")
        XCTAssertEqual(Set(saved.read.keys), ["one"])
        failSave = false
        model.undoMarkRead()
        XCTAssertTrue(saved.read.isEmpty)
        XCTAssertNil(saved.lastMarkedAt)
        XCTAssertEqual(model.selection, "one")
        XCTAssertEqual(model.visible.map(\.id), ["one", "two", "late"])
        XCTAssertFalse(model.canUndoRead)
        model.markVisibleRead()
        XCTAssertEqual(Set(saved.read.keys), ["one", "two", "late"])
        model.undoMarkRead()
        XCTAssertTrue(saved.read.isEmpty)
    }

    func testMarkReadPersistsOnlyLoadedIDsAndLateOlderEventStaysUnread() throws {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString).appendingPathComponent("read.json")
        defer { try? FileManager.default.removeItem(at: url.deletingLastPathComponent()) }
        let store = WorkspaceActivityReadStore.file(url)
        let runner = ActivityRunner()
        var response = report([entry("one")])
        let amx = command { _ in response }
        let model = WorkspaceActivityModel(command: { amx }, runner: runner, readStore: store)
        model.refresh(); runner.release()
        XCTAssertNil(try store.load().lastMarkedAt, "opening must not mark read")
        model.markVisibleRead()
        XCTAssertTrue(model.visible.isEmpty)
        XCTAssertEqual(Set(try store.load().read.keys), ["one"])
        response = report([entry("one"), entry("late", at: "2026-10-14T00:00:00Z")])
        let reopened = WorkspaceActivityModel(command: { amx }, runner: runner, readStore: store)
        reopened.refresh(); runner.release(1) // earlier model still has a transcript read queued
        XCTAssertEqual(reopened.visible.map(\.id), ["late"])
        XCTAssertEqual((try FileManager.default.attributesOfItem(atPath: url.path)[.posixPermissions] as? NSNumber)?.intValue, 0o600)
    }

    func testFailedSaveDoesNotHideEventsAndCorruptReadStateIsVisible() {
        let runner = ActivityRunner()
        let amx = command { _ in self.report([self.entry("one")]) }
        let store = WorkspaceActivityReadStore(load: { throw CocoaError(.fileReadCorruptFile) }, save: { _ in throw CocoaError(.fileWriteNoPermission) })
        let model = WorkspaceActivityModel(command: { amx }, runner: runner, readStore: store)
        XCTAssertNotNil(model.readError)
        model.refresh(); runner.release()
        model.markVisibleRead()
        XCTAssertEqual(model.visible.count, 1)
        XCTAssertTrue(model.readState.read.isEmpty)
        XCTAssertTrue(model.readError?.contains("not saved") == true)
    }

    func testExcerptExcludesNewerAndUntimestampedOutputAndChecksIdentity() {
        let runner = ActivityRunner()
        var calls: [[String]] = []
        let amx = command { args in
            calls.append(args)
            if args[1] == "activity" { return self.report([self.entry("one")]) }
            return #"{"schema_version":1,"data":{"session":{"id":"a","harness":"claude"},"messages_total":100,"messages":[{"role":"assistant","text":"old","timestamp":"2026-10-15T11:00:00Z"},{"role":"assistant","text":"nearest","timestamp":"2026-10-15T11:59:59.123Z"},{"role":"assistant","text":"future","timestamp":"2026-10-15T12:01:00Z"},{"role":"assistant","text":"unknown"},{"role":"user","text":"user","timestamp":"2026-10-15T11:59:59.999Z"}]}}"#
        }
        let model = WorkspaceActivityModel(command: { amx }, runner: runner, readStore: memory)
        model.refresh(); runner.release(); runner.release()
        XCTAssertEqual(model.excerpt?.text.reveal(), "nearest")
        XCTAssertEqual(calls[1], ["session", "show", "--json", "--last", "50", "--", "claude:a"])
        XCTAssertTrue(model.canLoadEarlier)
        model.loadEarlier(); runner.release()
        XCTAssertTrue(calls.last?.contains("150") == true)
    }

    func testLateRefreshAndClosedTranscriptCannotReplaceCurrentState() {
        let runner = ActivityRunner()
        let amx = command { args in self.report([self.entry(args.contains("30") ? "new" : "old")]) }
        let model = WorkspaceActivityModel(command: { amx }, runner: runner, readStore: memory)
        model.refresh(); model.days = 30; model.refresh()
        runner.release(1); runner.release()
        XCTAssertEqual(model.visible.map(\.id), ["new"])
        model.close(); runner.release()
        XCTAssertNil(model.transcript)
        XCTAssertNil(model.selection)
        XCTAssertFalse(model.reading)
    }

    func testTranscriptMismatchIsRejected() {
        let runner = ActivityRunner()
        let amx = command { args in
            if args[1] == "activity" { return self.report([self.entry("one")]) }
            return #"{"schema_version":1,"data":{"session":{"id":"a","harness":"codex"},"messages_total":0}}"#
        }
        let model = WorkspaceActivityModel(command: { amx }, runner: runner, readStore: memory)
        model.refresh(); runner.release(); runner.release()
        XCTAssertNil(model.transcript)
        XCTAssertNotNil(model.transcriptError)
    }

    func testUnavailableRefreshRetainsEventsButCannotMarkThemRead() {
        let runner = ActivityRunner()
        let amx = command { _ in self.report([self.entry("one")]) }
        var available = true
        let model = WorkspaceActivityModel(command: { available ? amx : nil }, runner: runner, readStore: memory)
        model.refresh(); runner.release()
        available = false; model.refresh(); model.markVisibleRead()
        XCTAssertEqual(model.visible.count, 1)
        XCTAssertTrue(model.readState.read.isEmpty)
        XCTAssertNotNil(model.error)
    }

    func testActivityWindowBuildsAndCloseDiscardsReadWork() {
        let runner = ActivityRunner()
        let amx = command { _ in self.report([self.entry("one")]) }
        let model = WorkspaceActivityModel(command: { amx }, runner: runner, readStore: memory)
        let controller = WorkspaceActivityWindow(model: model)
        var visible = false
        controller.onVisibilityChanged = { visible = $0 }
        controller.show()
        XCTAssertTrue(visible)
        XCTAssertEqual(controller.window?.title, "Since Last Looked")
        controller.window?.close()
        runner.release()
        XCTAssertFalse(visible)
        XCTAssertNil(model.report)
    }
}
