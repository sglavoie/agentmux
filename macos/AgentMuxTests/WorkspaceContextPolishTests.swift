import AppKit
import SwiftUI
import XCTest
@testable import AgentMux

private struct ContextRunner: ASLWorkRunner {
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { then(work()) }
}

@MainActor
final class WorkspaceContextPolishTests: XCTestCase {
    func testDiffPositionRestoresInANewHostingView() async throws {
        let suite = "amx.tests.context.\(UUID())"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let patch = WorkspaceDiff("@@ -1,200 +1,200 @@\n" + (0..<200).map { " context \($0)" }.joined(separator: "\n"))
        func view() -> WorkspaceDiffView {
            WorkspaceDiffView(diff: patch, identity: "checkout/base/file", loading: false, jump: nil, jumpSequence: 0,
                              reviewState: WorkspaceReviewState(defaults: defaults))
        }
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 700, height: 400), styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        defer { window.close() }
        func scroll(_ host: NSView) throws -> NSScrollView {
            func children(_ view: NSView) -> [NSView] { [view] + view.subviews.flatMap(children) }
            return try XCTUnwrap(children(host).compactMap { $0 as? NSScrollView }.first)
        }
        let first = NSHostingView(rootView: view())
        window.contentView = first; window.orderFront(nil)
        try await Task.sleep(for: .milliseconds(100))
        let before = try scroll(first)
        before.contentView.scroll(to: NSPoint(x: 0, y: 700))
        before.reflectScrolledClipView(before.contentView)
        try await Task.sleep(for: .milliseconds(100))
        let saved = before.contentView.bounds.origin.y
        XCTAssertGreaterThan(saved, 500)
        let second = NSHostingView(rootView: view())
        window.contentView = second
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertEqual(try scroll(second).contentView.bounds.origin.y, saved, accuracy: 1)
    }

    func testSplitReviewRestoresFocusAndDividerAndUserNavigationCancelsPendingRestore() throws {
        let app = FakeWorkspaceApp()
        let workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        defer { workspace.window.close() }
        app.store.onChange = { workspace.refresh() }
        workspace.openForTesting()
        workspace.window.setContentSize(NSSize(width: 1800, height: 800))
        app.store.apply(snapshot: WorkspaceFixture.snapshot([WorkspaceFixture.tmux(1, project: "API"), WorkspaceFixture.tmux(2, project: "Web")]))
        let review = FocusableView()
        var directory = ""
        workspace.makeRestoredReview = { path in
            directory = path
            return .init(view: review, canClose: { true }, close: {}, directory: { path })
        }
        var saved: WorkspaceRestoration.Layout?
        workspace.onLayoutChanged = { saved = $0 }
        workspace.restoreLayout(.init(active: .review("/repo"), other: .session("session:" + WorkspaceFixture.runtimeID(1)), activeOnRight: true, fraction: 0.6))
        workspace.window.layoutIfNeeded()
        XCTAssertEqual(directory, "/repo")
        XCTAssertEqual(workspace.content, .review)
        XCTAssertTrue(workspace.window.firstResponder === review)
        XCTAssertEqual(try XCTUnwrap(saved?.fraction), 0.6, accuracy: 0.02)

        app.holdsAnswers = true
        workspace.restoreLayout(.init(active: .session("session:" + WorkspaceFixture.runtimeID(1)), other: .session("session:" + WorkspaceFixture.runtimeID(2)), activeOnRight: false))
        workspace.select(.embedded(WorkspaceFixture.runtimeID(2)), focus: true)
        app.answer()
        XCTAssertEqual(workspace.state.selection, .embedded(WorkspaceFixture.runtimeID(2)))
    }
    func testHistoryRestoresSearchSelectionAndRequestsSavedMessageWindow() throws {
        let suite = "amx.tests.context.\(UUID())"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        WorkspaceReviewState(defaults: defaults).history = .init(query: "needle", scope: "This project", automated: true,
            project: "API", conversation: nil, selection: "claude:old", message: 125)
        var calls: [[String]] = []
        var command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        command.runner = { _, args, _, _ in
            calls.append(args)
            let data = args[1] == "show"
                ? #"{"session":{"id":"old","harness":"claude","project":"API","cwd":"/repo"},"messages_total":200,"messages_start":125,"messages":[{"role":"assistant","text":"needle"}]}"#
                : #"{"results":[{"id":"new","harness":"claude","project":"API","cwd":"/repo"},{"id":"old","harness":"claude","project":"API","cwd":"/repo"}]}"#
            return .finished(ProcessOutput(status: 0, stdout: Data("{\"schema_version\":1,\"data\":\(data)}".utf8), stderr: Data()))
        }
        let model = WorkspaceHistoryModel(command: { command }, runner: ContextRunner(), defaults: defaults)
        model.open(project: "API", conversation: nil)
        XCTAssertEqual(model.selection, "claude:old")
        XCTAssertEqual(model.query, "needle")
        XCTAssertEqual(model.scope, .project)
        XCTAssertTrue(model.includeAutomated)
        XCTAssertEqual(model.scrollRequest?.message, 125)
        XCTAssertTrue(calls.contains { $0.contains("--from") && $0.contains("105") })
        model.rememberVisibleMessage(125)
        model.close()
        let reopened = WorkspaceHistoryModel(command: { command }, runner: ContextRunner(), defaults: defaults)
        reopened.open(project: "API", conversation: nil)
        XCTAssertEqual(reopened.selection, "claude:old")
        XCTAssertEqual(reopened.scrollRequest?.message, 125)
    }

    func testComparisonAndScrollSurviveNewModelAndRemainCheckoutScoped() throws {
        let suite = "amx.tests.context.\(UUID())"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let saved = WorkspaceReviewState(defaults: defaults)
        saved.saveComparison(.init(base: "main", file: "b.swift"), target: ["--directory", "/repo"])
        saved.savePosition(NSPoint(x: 40, y: 720), id: "/repo\u{0}sha\u{0}b.swift")
        var calls: [[String]] = []
        var command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        command.runner = { _, args, _, _ in
            calls.append(args)
            let data = args.first == "worktree" ? #"{"worktrees":[]}"#
                : #"{"root":"/repo","base":"sha","files":[{"path":"a.swift","added":1,"deleted":0,"binary":false,"untracked":false},{"path":"b.swift","added":1,"deleted":0,"binary":false,"untracked":false}],"patch":"+new","truncated":false}"#
            return .finished(ProcessOutput(status: 0, stdout: Data("{\"schema_version\":1,\"data\":\(data)}".utf8), stderr: Data()))
        }
        let model = WorkspaceChangesModel(command: { command }, runner: ContextRunner(), defaults: defaults)
        model.open(directory: "/repo")
        XCTAssertEqual(model.base, "main")
        XCTAssertEqual(model.selectedFile, "b.swift")
        XCTAssertTrue(calls.first?.contains("main") == true)
        XCTAssertEqual(model.reviewState.position(model.diffIdentity), NSPoint(x: 40, y: 720))
        XCTAssertEqual(model.reviewState.position("/elsewhere\u{0}sha\u{0}b.swift"), .zero)
        XCTAssertNil(model.reviewState.comparison(["--directory", "/elsewhere"]))
    }

    func testSplitRestoresBothOwnershipChecksAndActiveSide() {
        let app = FakeWorkspaceApp()
        let workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        defer { workspace.window.close() }
        app.store.onChange = { workspace.refresh() }
        workspace.openForTesting()
        let one = WorkspaceFixture.runtimeID(1), two = WorkspaceFixture.runtimeID(2)
        let layout = WorkspaceRestoration.Layout(active: .session("session:" + one), other: .session("session:" + two), activeOnRight: false)
        workspace.restoreLayout(layout)
        XCTAssertTrue(app.made.isEmpty)
        app.statuses[two] = .live(clients: 1)
        app.store.apply(snapshot: WorkspaceFixture.snapshot([WorkspaceFixture.tmux(1, project: "API"), WorkspaceFixture.tmux(2, project: "Web")]))
        XCTAssertEqual(app.asked, [two, one])
        XCTAssertEqual(app.made, [one])
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: one))
        XCTAssertEqual(workspace.otherContent, .detached(runtimeSessionID: two))
        XCTAssertFalse(workspace.activeOnRight)
        workspace.focusOtherPane()
        XCTAssertEqual(workspace.content, .detached(runtimeSessionID: two))
        XCTAssertEqual(app.made, [one])
    }

    func testRestoredShellKeepsPlaceWithoutStartingProgramAndRecordsSwaps() throws {
        let app = FakeWorkspaceApp()
        let workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        defer { workspace.window.close() }
        app.store.onChange = { workspace.refresh() }
        workspace.openForTesting()
        app.store.apply(snapshot: WorkspaceFixture.snapshot([WorkspaceFixture.tmux(1, project: "API")]))
        let shell = WorkspaceUtilityView(kind: .shell, target: "/repo", title: "Review shell")
        var saved: WorkspaceRestoration.Layout?
        workspace.onLayoutChanged = { saved = $0 }
        workspace.restoreLayout(.init(active: .utility(shell), other: .session("session:" + WorkspaceFixture.runtimeID(1)), activeOnRight: true))
        XCTAssertEqual(workspace.content, .viewEnded(id: shell.id))
        XCTAssertTrue(app.madeViews.isEmpty)
        XCTAssertEqual(saved?.active, .utility(shell))
        workspace.swapPanes()
        XCTAssertFalse(try XCTUnwrap(saved).activeOnRight)
        let suite = "amx.tests.context.\(UUID())"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        WorkspaceRestoration.saveLayout(saved, to: defaults)
        XCTAssertEqual(WorkspaceRestoration.load(from: defaults).layout, saved)
    }
}
