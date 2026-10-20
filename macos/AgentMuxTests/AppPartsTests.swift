import AppKit
import XCTest
@testable import AgentMux

/// Runs the work at once, on the calling thread.
private struct InlineRunner: WorkRunner {
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { then(work()) }
}

/// Answers snooze requests from a script and records them.
private final class ScriptedSnooze: SnoozeService {
    var answers: [SnoozeOutcome]
    private(set) var requests: [SnoozeRequest] = []

    init(_ answers: [SnoozeOutcome]) { self.answers = answers }

    func snooze(_ request: SnoozeRequest) -> SnoozeOutcome {
        requests.append(request)
        return answers.isEmpty ? .failed("no answer scripted") : answers.removeFirst()
    }
}

/// The types the app delegate's state was split into, and the small rules
/// beside them: each is tested without the app.
@MainActor
final class AppPartsTests: XCTestCase {
    private func preferences() -> PreferencesModel {
        PreferencesModel(defaults: UserDefaults(suiteName: "AppPartsTests-\(UUID().uuidString)")!)
    }

    private func liveStore(pid: Int = 7) -> SessionStore {
        let store = SessionStore()
        var snapshot = WorkspaceFixture.snapshot([])
        snapshot.daemon.pid = pid
        store.apply(snapshot: snapshot)
        return store
    }

    // MARK: SnoozeController

    func testSnoozeWithoutALiveFeedFailsWithoutAsking() {
        let service = ScriptedSnooze([])
        let controller = SnoozeController(store: SessionStore(), preferences: preferences(), runner: InlineRunner(), service: { service })
        var failures: [(String, String, Bool)] = []
        controller.onFailure = { failures.append(($0, $1, $2)) }
        controller.snooze(.oneHour, banner: true)
        XCTAssertTrue(service.requests.isEmpty)
        XCTAssertEqual(failures.map(\.0), [SnoozeController.failedTitle])
        XCTAssertEqual(failures.map(\.1), [DaemonPhrase.notConnected + "."])
        XCTAssertEqual(failures.map(\.2), [true])
    }

    func testSnoozeMirrorsTheDaemonsAnswer() {
        let now = Date(timeIntervalSince1970: 1_000_000)
        let until = Int64(now.timeIntervalSince1970) + 3600
        let service = ScriptedSnooze([.state(SnoozeState(snoozedUntil: until))])
        let model = preferences()
        let controller = SnoozeController(store: liveStore(), preferences: model, runner: InlineRunner(),
                                          service: { service }, now: { now })
        controller.snooze(.oneHour)
        XCTAssertEqual(service.requests.count, 1)
        XCTAssertEqual(model.preferences.snoozeUntil?.timeIntervalSince1970, TimeInterval(until))
        XCTAssertNotNil(model.defaults.string(forKey: SnoozeMirror.daemonKey))
    }

    func testAnOldDaemonIsProbedOnceAndMarkedUnsupported() {
        let service = ScriptedSnooze([.unsupported])
        let controller = SnoozeController(store: liveStore(), preferences: preferences(), runner: InlineRunner(), service: { service })
        XCTAssertTrue(controller.supported)
        controller.reconcile()
        XCTAssertFalse(controller.supported)
        XCTAssertEqual(service.requests, [.read])
        controller.reconcile()
        XCTAssertEqual(service.requests, [.read], "a daemon already probed is not asked again")
    }

    func testAProbeThatFailsIsAskedAgainWithTheNextSnapshot() {
        let service = ScriptedSnooze([.failed("busy"), .state(SnoozeState())])
        let controller = SnoozeController(store: liveStore(), preferences: preferences(), runner: InlineRunner(), service: { service })
        controller.reconcile()
        XCTAssertNil(controller.probedDaemon)
        controller.reconcile()
        XCTAssertNotNil(controller.probedDaemon)
        XCTAssertEqual(service.requests, [.read, .read])
    }

    // MARK: DaemonConnection

    func testHealthIsPolledOnlyWhileTheFeedIsNotLive() {
        XCTAssertFalse(DaemonConnection.pollsHealth(feed: .live))
        XCTAssertTrue(DaemonConnection.pollsHealth(feed: .connecting))
        XCTAssertTrue(DaemonConnection.pollsHealth(feed: .notRunning))
        XCTAssertTrue(DaemonConnection.pollsHealth(feed: .daemonTooOld))
        XCTAssertEqual(AppDelegate.shouldReconnect(after: .ready(nil), feed: .notRunning),
                       DaemonConnection.shouldReconnect(after: .ready(nil), feed: .notRunning))
    }

    func testTheHealthTimerStopsOnceTheFeedIsLive() {
        let store = SessionStore()
        let connection = DaemonConnection(store: store, request: { SubscribeParams() }, health: InlineRunner(), control: InlineRunner())
        defer { connection.stop() }
        connection.refreshStatus()
        XCTAssertNotNil(connection.healthTimer, "not live: the daemon line is polled")
        store.apply(snapshot: WorkspaceFixture.snapshot([]))
        connection.refreshStatus()
        XCTAssertNil(connection.healthTimer, "live: every snapshot carries the daemon's status")
        connection.stop()
        connection.refreshStatus()
        XCTAssertNil(connection.healthTimer)
    }

    func testNothingIsBusyBeforeAStartOrRestart() {
        let connection = DaemonConnection(store: SessionStore(), request: { SubscribeParams() }, control: InlineRunner())
        XCTAssertNil(connection.busy)
    }

    // MARK: AMXLookup

    func testRetriesAreBounded() {
        XCTAssertEqual(AMXLookup.retryDelay(attempt: 0), AMXLookup.retryDelays.first)
        XCTAssertEqual(AMXLookup.retryDelay(attempt: AMXLookup.retryDelays.count - 1), AMXLookup.retryDelays.last)
        XCTAssertNil(AMXLookup.retryDelay(attempt: AMXLookup.retryDelays.count))
        XCTAssertNil(AMXLookup.retryDelay(attempt: -1))
    }

    // MARK: Wording

    func testAmxIsEitherStillBeingFoundOrNotFound() {
        XCTAssertEqual(AMXWording.unavailable(notFound: nil), AMXWording.resolving)
        XCTAssertEqual(AMXWording.unavailable(notFound: AMXWording.notFound), AMXWording.notFound)
        XCTAssertEqual(ActionGate().unavailableReason(.sessions), AMXWording.resolving)
        XCTAssertTrue(AMXWording.isNotFound(AMXWording.notFound))
        XCTAssertTrue(AMXWording.isNotFound("Could not launch: \(AMXWording.invalidOverride)"))
        XCTAssertFalse(AMXWording.isNotFound(AMXWording.resolving), "waiting fixes it; Settings does not")
    }

    // MARK: WorkspaceError

    func testAnErrorOffersTheFixThatApplies() {
        XCTAssertEqual(WorkspaceError.recovery(for: AMXWording.notFound, daemonIsDown: true), .openSettings)
        XCTAssertEqual(WorkspaceError.recovery(for: "connection refused", daemonIsDown: true), .startDaemon)
        XCTAssertNil(WorkspaceError.recovery(for: "connection refused", daemonIsDown: false))
    }

    func testAnErrorsButtonsAndWhatTheyDo() {
        let error = WorkspaceError(title: "api could not be cleared", message: "daemon not running",
                                   details: "error: dial unix: no such file\ndaemon not running", recovery: .startDaemon)
        XCTAssertEqual(error.buttonTitles, ["OK", "Start Daemon", "Copy Details"])
        XCTAssertEqual(error.choice(at: 0), .dismiss)
        XCTAssertEqual(error.choice(at: 1), .recover(.startDaemon))
        XCTAssertEqual(error.choice(at: 2), .copy)
        XCTAssertEqual(error.copiedText, "api could not be cleared\ndaemon not running\n\nerror: dial unix: no such file\ndaemon not running")

        let plain = WorkspaceError(title: "x could not be named", message: "name too long")
        XCTAssertEqual(plain.buttonTitles, ["OK", "Copy Details"])
        XCTAssertEqual(plain.choice(at: 1), .copy)
        XCTAssertEqual(plain.copiedText, "x could not be named\nname too long", "no details: nothing repeated")
    }

    func testCopyDetailsPutsTheTextOnThePasteboard() {
        let pasteboard = NSPasteboard(name: NSPasteboard.Name("AppPartsTests-\(UUID().uuidString)"))
        defer { pasteboard.releaseGlobally() }
        let error = WorkspaceError(title: "t", message: "m", details: "d")
        var recovered: WorkspaceError.Recovery?
        WorkspaceErrorAlert.answer(NSApplication.ModalResponse(rawValue: NSApplication.ModalResponse.alertFirstButtonReturn.rawValue + 1),
                                   to: error, pasteboard: pasteboard) { recovered = $0 }
        XCTAssertNil(recovered)
        XCTAssertEqual(pasteboard.string(forType: .string), "t\nm\n\nd")
    }

    func testAFailedOperatorEventSaysWhatAmxSaid() {
        XCTAssertEqual(AppDelegate.operatorEventFailure(status: 1, stderr: "warning: x\nerror: daemon is not running\n"),
                       "error: daemon is not running")
        XCTAssertEqual(AppDelegate.operatorEventFailure(status: 3, stderr: ""), "amx hook-indicator exited 3 without saying why.")
        XCTAssertEqual(AppDelegate.operatorEventFailedTitle("user_clear", name: "api"), "api could not be cleared")
        XCTAssertEqual(AppDelegate.operatorEventFailedTitle("user_active", name: ""), "The session could not be marked active")
    }

    func testKittyIsInUseOnlyWhenTheDaemonOrASessionSaysSo() {
        XCTAssertFalse(AppDelegate.kittyInUse(snapshotSocket: "", sessions: [WorkspaceFixture.tmux(1, project: "api")]))
        XCTAssertTrue(AppDelegate.kittyInUse(snapshotSocket: "unix:/tmp/kitty-1", sessions: []))
        XCTAssertTrue(AppDelegate.kittyInUse(snapshotSocket: "", sessions: [WorkspaceFixture.kitty(4, project: "web")]))
    }

    // MARK: Config path and watcher

    func testTheConfigPathIsExpandedForWatching() {
        let environment = LaunchEnvironment(home: "/Users/me", path: "/bin", forwarded: ["AMX_CONFIG": "~/amx.toml"])
        XCTAssertEqual(AMXConfigPath.expanded(environment, home: "/Users/me"), "/Users/me/amx.toml")
        XCTAssertEqual(AMXConfigPath.resolve(environment, home: "/Users/me"), "~/amx.toml")
        XCTAssertEqual(AMXConfigPath.expanded(nil, home: "/Users/me"), "/Users/me/.config/agent-mux/config.toml")
    }

    func testTheWatcherSeesAnInPlaceWriteAndAnAtomicReplace() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent("AppPartsTests-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let file = directory.appendingPathComponent("config.toml")
        try Data("a = 1\n".utf8).write(to: file)

        var changes = 0
        let watcher = ConfigWatcher(debounce: 0.05) { changes += 1 }
        defer { watcher.stop() }
        watcher.watch(file.path)

        let first = expectation(description: "in-place write")
        let handle = try FileHandle(forWritingTo: file)
        handle.seekToEndOfFile()
        handle.write(Data("b = 2\n".utf8))
        try handle.close()
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) { first.fulfill() }
        wait(for: [first], timeout: 2)
        XCTAssertGreaterThanOrEqual(changes, 1)

        let seen = changes
        let second = expectation(description: "atomic replace")
        try Data("c = 3\n".utf8).write(to: file, options: .atomic)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) { second.fulfill() }
        wait(for: [second], timeout: 2)
        XCTAssertGreaterThan(changes, seen, "a rename over the file is a change too")
    }

    // MARK: QueueWorkRunner

    func testTheMainActorRunHandsTheResultBackOnMain() {
        let done = expectation(description: "result on main")
        QueueWorkRunner(queue: DispatchQueue(label: "AppPartsTests")).run({ 41 + 1 }, onMain: { value in
            XCTAssertTrue(Thread.isMainThread)
            XCTAssertEqual(value, 42)
            done.fulfill()
        })
        wait(for: [done], timeout: 2)
    }
}
