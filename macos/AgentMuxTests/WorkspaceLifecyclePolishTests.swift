import AppKit
import ApplicationServices
import SwiftUI
import XCTest
@testable import AgentMux

@MainActor
final class WorkspaceLifecyclePolishTests: XCTestCase {
    func testUpdateAgeUsesOldestReadAndDoesNotHideFailuresOrMissingReads() {
        let now = Date(timeIntervalSince1970: 10_000)
        let old = WorkspaceReadObservation(title: "Transcript", refreshedAt: now.addingTimeInterval(-125))
        let new = WorkspaceReadObservation(title: "Results", refreshedAt: now.addingTimeInterval(-2))
        XCTAssertEqual(WorkspaceReviewStatus(reads: [new, old]).label(at: now), "Updated 2m ago")
        XCTAssertEqual(WorkspaceReviewStatus(reads: [new]).label(at: now), "Updated 2s ago")
        XCTAssertEqual(WorkspaceReviewStatus(reads: [new]).label(at: now.addingTimeInterval(-10)), "Updated 0s ago")
        let missing = WorkspaceReadObservation(title: "Diff")
        XCTAssertEqual(WorkspaceReviewStatus(reads: [missing]).label(at: now), "Not updated yet")
        XCTAssertTrue(missing.description.contains("Not read yet"))
        let failed = WorkspaceReadObservation(title: "Transcript", retained: true, refreshedAt: old.refreshedAt,
            feedback: .init(target: "Transcript", message: "Read failed"))
        XCTAssertEqual(WorkspaceReviewStatus(reads: [new, failed]).label(at: now), "Refresh failed")
        XCTAssertTrue(failed.description.contains("Previous results"))
    }

    private func key(_ text: String, code: UInt16, window: NSWindow) -> NSEvent {
        NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: [], timestamp: 0,
            windowNumber: window.windowNumber, context: nil, characters: text, charactersIgnoringModifiers: text,
            isARepeat: false, keyCode: code)!
    }

    func testWaitingControlsFitAndHintsFollowFocusWhileHeldQuestionsStayProtected() async throws {
        let suite = "amx-consistency-\(UUID())"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let preferences = WorkspaceReadingPreferences(defaults: defaults)
        preferences.proseSize = 24
        let inspector = WorkspaceInspectorView(preferences: preferences)
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 300, height: 600), styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false; window.contentView = inspector; window.makeKeyAndOrderFront(nil)
        defer { window.close() }
        let row = WorkspaceWaitingRow(key: .embedded("fixture"), title: "API — deployment review", detail: "Waiting for your answer", glyph: "?", toolTip: "",
            answer: .prompt(runtimeSessionID: "fixture"), category: .question, contextVersion: "first")
        var answers: [WorkspaceWaitingAnswer] = []
        inspector.onContext = { _, receive in receive(.loaded(SecretText("Which deployment should proceed?\n\nReview staging before choosing production."))) }
        inspector.onAnswer = { answers.append($0) }
        inspector.show(.init(waiting: [row]))
        window.makeFirstResponder(nil)
        window.layoutIfNeeded(); inspector.layoutSubtreeIfNeeded()
        XCTAssertTrue(inspector.updatesButton.isHidden)
        XCTAssertTrue(inspector.readerHint.isHidden)
        let category = inspector.filterPicker.convert(inspector.filterPicker.bounds, to: inspector)
        let order = inspector.oldestButton.convert(inspector.oldestButton.bounds, to: inspector)
        XCTAssertEqual(category.midY, order.midY, accuracy: 5, "Filter and ordering share one row")
        XCTAssertLessThanOrEqual(order.maxX, inspector.bounds.maxX - 8)
        for width in [220.0, 300.0, 420.0] {
            window.setContentSize(NSSize(width: width, height: 600))
            window.layoutIfNeeded(); inspector.layoutSubtreeIfNeeded()
            let ordering = inspector.oldestButton.convert(inspector.oldestButton.bounds, to: inspector)
            XCTAssertLessThanOrEqual(ordering.maxX, inspector.bounds.maxX - 8)
            XCTAssertGreaterThan(inspector.filterPicker.bounds.width, 40)
        }
        window.setContentSize(NSSize(width: 300, height: 600))
        inspector.focusReader()
        try await Task.sleep(for: .milliseconds(80))
        XCTAssertFalse(inspector.readerHint.isHidden)
        let card = try XCTUnwrap(inspector.lines.compactMap { $0 as? WorkspaceWaitingRowView }.first)
        XCTAssertGreaterThan(card.contextScroll.bounds.height, 300, "Controls leave room for reading at 300 × 600")
        window.makeFirstResponder(nil)
        XCTAssertTrue(inspector.readerHint.isHidden, "Hints disappear even without pressing Escape")
        inspector.focusReader()
        window.sendEvent(key("\r", code: 36, window: window))
        XCTAssertEqual(answers, [row.answer!])
        var changed = row; changed.contextVersion = "replacement"
        inspector.show(.init(waiting: [changed]))
        XCTAssertFalse(inspector.updatesButton.isHidden)
        XCTAssertFalse(card.answerButton!.isEnabled)
        window.sendEvent(key("\r", code: 36, window: window))
        XCTAssertEqual(answers.count, 1)
        inspector.updatesButton.performClick(nil)
        XCTAssertTrue(inspector.updatesButton.isHidden)
        XCTAssertEqual(inspector.visibleWaiting.first?.contextVersion, "replacement")
    }

    func testAccessibleWaitingActionsReadAnswerAndReturnToOriginalTerminal() async throws {
        let app = FakeWorkspaceApp()
        let workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        defer { workspace.window.close() }
        app.store.onChange = { workspace.refresh() }
        workspace.openForTesting()
        let first = WorkspaceFixture.tmux(1, project: "API")
        let waiting = WorkspaceFixture.tmux(2, project: "Deploy", icon: "key", attention: true)
        app.store.apply(snapshot: WorkspaceFixture.snapshot([first, waiting]))
        workspace.focus(runtimeSessionID: WorkspaceFixture.runtimeID(1))
        let terminal = try XCTUnwrap(workspace.terminalView(for: WorkspaceFixture.runtimeID(1)))
        workspace.inspector.onContext = { _, receive in receive(.loaded(SecretText("Approve deployment to staging?"))) }
        workspace.focusWaiting()
        let card = try XCTUnwrap(workspace.inspector.lines.compactMap { $0 as? WorkspaceWaitingRowView }.first)
        let children = try XCTUnwrap(card.accessibilityChildren()).compactMap { $0 as? NSAccessibilityProtocol }
        let context = try XCTUnwrap(children.first { $0.accessibilityLabel()?.contains("context") == true || $0.accessibilityLabel()?.contains("Context") == true })
        (context as? NSObject)?.accessibilityPerformAction(.press)
        try await Task.sleep(for: .milliseconds(100))
        // Focus entry already expanded the reader; toggle again if the action closed it.
        if card.contextScroll.isHidden {
            (context as? NSObject)?.accessibilityPerformAction(.press)
            try await Task.sleep(for: .milliseconds(100))
        }
        XCTAssertEqual(card.contextLabel.stringValue, "Approve deployment to staging?")
        XCTAssertTrue(card.accessibilityChildren()?.contains { ($0 as AnyObject) === card.contextScroll } == true)
        let answer = try XCTUnwrap(children.first { $0.accessibilityLabel()?.hasPrefix("Answer ") == true })
        (answer as? NSObject)?.accessibilityPerformAction(.press)
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertEqual(app.answers, [.prompt(runtimeSessionID: WorkspaceFixture.runtimeID(2))])
        workspace.window.sendEvent(key("\u{1b}", code: 53, window: workspace.window))
        XCTAssertTrue(workspace.window.firstResponder === terminal)
        XCTAssertEqual(app.made, [WorkspaceFixture.runtimeID(1)], "Reading or answering must not attach the other session")
    }

    func testCodeCopyIsExposedThroughAccessibilityAndCopiesLiteralText() async throws {
        let board = NSPasteboard(name: .init("amx-accessible-copy-\(UUID())"))
        defer { board.releaseGlobally() }
        let code = "let café = \"🦊\"\n"
        let host = NSHostingView(rootView: WorkspaceTranscriptCode(language: "swift", text: code, terms: [],
            preferences: WorkspaceReadingPreferences(), pasteboard: board).padding())
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 600, height: 300), styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false; window.contentView = host; window.makeKeyAndOrderFront(nil)
        defer { window.close() }
        try await Task.sleep(for: .milliseconds(200))
        var visited = Set<ObjectIdentifier>()
        func elements(_ object: NSObject) -> [NSObject] {
            guard visited.insert(ObjectIdentifier(object)).inserted else { return [] }
            let children = (object as? NSAccessibilityProtocol)?.accessibilityChildren() as? [NSObject]
                ?? object.accessibilityAttributeValue(.children) as? [NSObject] ?? []
            return [object] + children.flatMap(elements)
        }
        let tree = elements(host)
        let copy = tree.first {
            ($0.accessibilityAttributeValue(.role) as? String) == NSAccessibility.Role.button.rawValue &&
            ($0.accessibilityAttributeValue(.title) as? String ?? $0.accessibilityAttributeValue(.description) as? String) == "Copy code"
        }
        if copy == nil && !AXIsProcessTrusted() {
            throw XCTSkip("SwiftUI virtual elements are not exposed to this hosted lookup, and system Accessibility access is unavailable. Complete the recorded VoiceOver workflow; this is not an accessibility pass.")
        }
        let button = try XCTUnwrap(copy, "Copy code must be reachable in the accessibility tree")
        button.accessibilityPerformAction(.press)
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertEqual(board.string(forType: .string), code)
    }
}
