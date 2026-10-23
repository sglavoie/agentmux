import AppKit
import SwiftUI
import XCTest
@testable import AgentMux

private final class DailyRunner: ASLWorkRunner {
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { pending.append { then(work()) } }
    func drain() { while !pending.isEmpty { pending.removeFirst()() } }
}

@MainActor
final class WorkspaceDailyPolishTests: XCTestCase {
    private func key(_ text: String, code: UInt16, flags: NSEvent.ModifierFlags = [], window: NSWindow) -> NSEvent {
        NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: flags, timestamp: 0, windowNumber: window.windowNumber,
                        context: nil, characters: text, charactersIgnoringModifiers: text, isARepeat: false, keyCode: code)!
    }

    func testProjectGuidanceDistinguishesSetupLoadingFailureAndReady() {
        XCTAssertTrue(ProjectCatalog.guidance(.init(value: []), unavailable: nil).contains("Set Up First Project"))
        XCTAssertTrue(ProjectCatalog.offersSetup(.init(value: []), unavailable: nil))
        XCTAssertFalse(ProjectCatalog.offersSetup(.init(), unavailable: nil))
        XCTAssertFalse(ProjectCatalog.offersSetup(.init(value: [], loading: true), unavailable: nil))
        XCTAssertFalse(ProjectCatalog.offersSetup(.init(value: [], error: "bad config"), unavailable: nil))
        XCTAssertFalse(ProjectCatalog.offersSetup(.init(value: []), unavailable: "amx unavailable"))
        XCTAssertFalse(ProjectCatalog.offersSetup(.init(value: [.init(profile: "work", title: "API")]), unavailable: nil))
        XCTAssertEqual(ProjectCatalog.guidance(.init(), unavailable: nil), "Loading configured projects…")
        XCTAssertTrue(ProjectCatalog.guidance(.init(error: "bad config"), unavailable: nil).contains("bad config"))
        XCTAssertEqual(ProjectCatalog.guidance(.init(value: []), unavailable: "amx unavailable"), "amx unavailable")
        XCTAssertTrue(ProjectCatalog.guidance(.init(value: [.init(profile: "work", title: "API")]), unavailable: nil).contains("configured project"))
    }

    func testLeavingWaitingRestoresFocusWithoutReattachingDetachedSession() throws {
        let app = FakeWorkspaceApp()
        let workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        defer { workspace.window.close() }
        app.store.onChange = { workspace.refresh() }
        workspace.openForTesting()
        app.store.apply(snapshot: WorkspaceFixture.snapshot([WorkspaceFixture.tmux(1, project: "API")]))
        let id = WorkspaceFixture.runtimeID(1)
        workspace.focus(runtimeSessionID: id)
        workspace.detachSession(.embedded(id))
        workspace.focusSidebar()
        let made = app.made
        workspace.focusWaiting()
        XCTAssertTrue(workspace.window.firstResponder === workspace.inspector)
        XCTAssertFalse(workspace.placeholder.button.performKeyEquivalent(with: key("\r", code: 36, window: workspace.window)),
                       "the detached pane's default button must not claim the reader's Return")
        workspace.inspector.keyDown(with: key("\u{1b}", code: 53, window: workspace.window))
        XCTAssertTrue(workspace.window.firstResponder === workspace.sidebar)
        XCTAssertEqual(app.made, made)
        XCTAssertEqual(workspace.content, .detached(runtimeSessionID: id))
    }

    func testWaitingKeyboardReadsAnswersReturnsAndRejectsChangedQuestions() async throws {
        let preferences = WorkspaceReadingPreferences()
        let inspector = WorkspaceInspectorView(preferences: preferences)
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 300, height: 650), styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false; window.contentView = inspector; window.makeKeyAndOrderFront(nil)
        defer { window.close() }
        func row(_ id: String) -> WorkspaceWaitingRow {
            .init(key: .embedded(id), title: "Project \(id)", detail: "Needs a decision", glyph: "?", toolTip: "",
                  answer: .prompt(runtimeSessionID: id), category: .question, contextVersion: "one")
        }
        let one = row("one"), two = row("two")
        var contexts: [(WorkspaceWaitingRow, (WorkspaceWaitingContext.Result) -> Void)] = []
        var answers: [WorkspaceWaitingAnswer] = []
        inspector.onContext = { contexts.append(($0, $1)) }
        inspector.onAnswer = { answers.append($0) }
        inspector.show(.init(waiting: [one, two])); inspector.focusReader()
        XCTAssertTrue(window.firstResponder === inspector)
        XCTAssertEqual(inspector.readingKey, one.key)
        contexts[0].1(.loaded(SecretText("terminal output\n" + String(repeating: "line\n", count: 80))))
        window.layoutIfNeeded()
        let first = try XCTUnwrap(inspector.lines.compactMap { $0 as? WorkspaceWaitingRowView }.first)
        XCTAssertGreaterThan(first.contextScroll.frame.height, 160)
        preferences.fontSize = 20
        try await Task.sleep(for: .milliseconds(30))
        XCTAssertEqual(first.contextLabel.font?.pointSize, 20)
        let origin = first.contextScroll.contentView.bounds.origin.y
        inspector.keyDown(with: key("", code: 121, window: window))
        XCTAssertGreaterThan(first.contextScroll.contentView.bounds.origin.y, origin)
        inspector.keyDown(with: key("", code: 116, window: window))
        XCTAssertEqual(first.contextScroll.contentView.bounds.origin.y, origin, accuracy: 1)
        window.sendEvent(key("\r", code: 36, window: window))
        XCTAssertEqual(answers, [one.answer!])
        // Returning from an answer panel retains the reader's first responder.
        var keyboard = KeyboardReturn(); keyboard.note(frontmost: NSRunningApplication.current)
        let panel = NSPanel(contentRect: NSRect(x: 0, y: 0, width: 200, height: 100), styleMask: [.titled], backing: .buffered, defer: false)
        panel.isReleasedWhenClosed = false; panel.makeKeyAndOrderFront(nil); panel.close(); keyboard.giveBack()
        XCTAssertTrue(window.firstResponder === inspector)
        var changed = one; changed.contextVersion = "two"
        inspector.show(.init(waiting: [changed, two]))
        inspector.keyDown(with: key("\r", code: 36, window: window))
        XCTAssertEqual(answers.count, 1, "a held item must not answer a replacement question")
        contexts[0].1(.loaded(SecretText("obsolete")))
        XCTAssertFalse(first.contextLabel.stringValue.contains("obsolete"))
        inspector.keyDown(with: key("n", code: 45, window: window))
        XCTAssertEqual(inspector.readingKey, two.key)
        inspector.keyDown(with: key("\r", code: 36, window: window))
        XCTAssertEqual(answers.last, two.answer)
        inspector.show(.init(waiting: [changed]))
        inspector.keyDown(with: key("n", code: 45, window: window))
        XCTAssertEqual(inspector.readingKey, changed.key, "removing the answered item must not skip the next remaining item")
        var left = false; inspector.onLeaveReader = { left = true }
        inspector.keyDown(with: key("\u{1b}", code: 53, window: window))
        XCTAssertTrue(left)
    }

    func testReadingPreferencesPersistProseIndependentlyAndWaitingUsesProportionalQuestions() async throws {
        let suite = "amx.daily.\(UUID())"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let preferences = WorkspaceReadingPreferences(defaults: defaults)
        preferences.proseSize = 21; preferences.fontSize = 17
        let restored = WorkspaceReadingPreferences(defaults: defaults)
        XCTAssertEqual(restored.effectiveProseSize, 21); XCTAssertEqual(restored.effectiveFontSize, 17)
        let row = WorkspaceWaitingRow(key: .init(socket: "socket", windowID: 1), title: "Question", detail: "Waiting", glyph: "?", toolTip: "")
        let view = WorkspaceWaitingRowView(row)
        view.configureReading(preferences: restored, height: 300)
        XCTAssertEqual(view.contextLabel.font?.pointSize, 21)
        XCTAssertFalse(view.contextLabel.font?.isFixedPitch == true)
        restored.proseSize = .nan
        XCTAssertEqual(restored.effectiveProseSize, 13)
        XCTAssertEqual(WorkspaceReadingPreferences(defaults: defaults).effectiveProseSize, 13)
    }

    func testHistoryRefreshShortcutReadsOnlyTranscriptEvenWhenSearchFailsAndKeepsFocus() async throws {
        let runner = DailyRunner()
        var requests: [[String]] = []
        var failedSearch = false
        var command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        command.runner = { _, args, _, _ in
            requests.append(args)
            if failedSearch && args[1] != "show" { return .finished(.init(status: 1, stdout: Data(), stderr: Data("search unavailable".utf8))) }
            let data: [String: Any] = args[1] == "show"
                ? ["session": ["id": "one", "harness": "claude"], "messages_total": 30, "messages_start": 0,
                   "messages": (0..<30).map { ["role": "assistant", "text": "Reading message \($0)"] }]
                : ["sessions": [["id": "one", "harness": "claude", "project": "API", "cwd": "/repo"]]]
            return .finished(.init(status: 0, stdout: try! JSONSerialization.data(withJSONObject: ["schema_version": 1, "data": data]), stderr: Data()))
        }
        let model = WorkspaceHistoryModel(command: { command }, runner: runner)
        model.search(); runner.drain()
        let host = WorkspaceReviewHost(rootView: WorkspaceHistoryView(model: model))
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 720, height: 428), styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false; window.contentView = host; window.makeKeyAndOrderFront(nil)
        defer { window.close() }
        try await Task.sleep(for: .milliseconds(200))
        failedSearch = true; model.search(); runner.drain()
        XCTAssertNotNil(model.error)
        model.rememberVisibleMessage(12)
        let responder = window.firstResponder
        requests = []
        XCTAssertTrue(host.performKeyEquivalent(with: key("r", code: 15, flags: .command, window: window)))
        XCTAssertTrue(host.performKeyEquivalent(with: key("r", code: 15, flags: .command, window: window)))
        XCTAssertEqual(runner.pending.count, 1, "busy shortcut must match the disabled button")
        runner.drain()
        XCTAssertEqual(requests.count, 1)
        XCTAssertEqual(requests.first?[1], "show")
        XCTAssertEqual(model.scrollRequest?.message, 12)
        XCTAssertTrue(window.firstResponder === responder)
    }

    func testHistoryMinimumWindowKeepsAtLeast240PointsOfTranscriptAtLargeReadingSize() async throws {
        let runner = DailyRunner()
        var command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        command.runner = { _, args, _, _ in
            let data: [String: Any] = args[1] == "show"
                ? ["session": ["id": "one", "harness": "claude"], "messages_total": 50, "messages_start": 0,
                   "messages": (0..<50).map { ["role": "assistant", "text": "Message \($0) with readable prose."] }]
                : ["sessions": [["id": "one", "harness": "claude", "project": "Very long project name that must truncate", "cwd": "/repo"]]]
            return .finished(.init(status: 0, stdout: try! JSONSerialization.data(withJSONObject: ["schema_version": 1, "data": data]), stderr: Data()))
        }
        let model = WorkspaceHistoryModel(command: { command }, runner: runner)
        model.search(); runner.drain()
        let preferences = WorkspaceReadingPreferences(); preferences.proseSize = 24
        let host = NSHostingView(rootView: WorkspaceHistoryView(preferences: preferences, model: model))
        let style: NSWindow.StyleMask = [.titled, .resizable]
        let content = NSWindow.contentRect(forFrameRect: NSRect(x: 0, y: 0, width: 720, height: 450), styleMask: style)
        let window = NSWindow(contentRect: content, styleMask: style, backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false; window.contentView = host; window.orderFront(nil)
        defer { window.close() }
        try await Task.sleep(for: .milliseconds(200))
        func descendants(_ view: NSView) -> [NSView] { [view] + view.subviews.flatMap(descendants) }
        let scrolls = descendants(host).compactMap { $0 as? NSScrollView }
        let transcript = try XCTUnwrap(scrolls.max { $0.convert(.zero, to: host).x < $1.convert(.zero, to: host).x })
        XCTAssertEqual(window.frame.size, NSSize(width: 720, height: 450))
        XCTAssertGreaterThanOrEqual(transcript.contentSize.height, 240)
        let height = transcript.contentSize.height
        model.refreshTranscript()
        try await Task.sleep(for: .milliseconds(400))
        XCTAssertEqual(transcript.contentSize.height, height, accuracy: 1, "progress must not move the reading area")
        runner.drain()
    }
}
