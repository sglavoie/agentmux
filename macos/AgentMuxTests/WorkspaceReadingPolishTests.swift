import AppKit
import SwiftUI
import XCTest
@testable import AgentMux

final class WorkspaceReadingPolishTests: XCTestCase {
    func testTranscriptFormattingKeepsCodeLiteralAndHandlesUnfinishedFences() {
        let text = "# Review\n\nUse **bold** and `inline`.\n\n- First\n2. Second\n\n```swift\nlet value = \"# literal\"\n\n// keep blank lines\n```\n\n~~~sh\necho unfinished"
        XCTAssertEqual(WorkspaceTranscriptBlock.parse(text), [
            .heading(1, "Review"), .paragraph("Use **bold** and `inline`."),
            .item("•", "First"), .item("2.", "Second"),
            .code(language: "swift", text: "let value = \"# literal\"\n\n// keep blank lines"),
            .code(language: "sh", text: "echo unfinished")
        ])
    }

    func testSearchHighlightsRenderedMarkdownWithoutLosingInlineCode() {
        let value = WorkspaceTranscriptBlock.inline("Read **café** and `value`.", terms: ["CAFÉ", "value"])
        XCTAssertEqual(String(value.characters), "Read café and value.")
        XCTAssertTrue(value.runs.contains { $0.inlinePresentationIntent?.contains(.code) == true && $0.backgroundColor != nil })
        XCTAssertTrue(value.runs.contains { $0.inlinePresentationIntent?.contains(.stronglyEmphasized) == true && $0.backgroundColor != nil })
    }

    func testPickerTypingAndArrowsOnlyHighlightAvailableChoices() {
        let choices = [WorkspaceChoice(id: "bad", label: "Unavailable", detail: "", problem: "Missing profile"),
                       WorkspaceChoice(id: "one", label: "First", detail: ""),
                       WorkspaceChoice(id: "two", label: "Second", detail: "")]
        XCTAssertEqual(WorkspaceChoiceNavigation.highlight(in: choices, keeping: "bad"), "one")
        XCTAssertEqual(WorkspaceChoiceNavigation.highlight(in: choices, keeping: "two"), "two")
        XCTAssertEqual(WorkspaceChoiceNavigation.move(1, in: choices, from: nil), "one")
        XCTAssertEqual(WorkspaceChoiceNavigation.move(-1, in: choices, from: "one"), "one")
        XCTAssertEqual(WorkspaceChoiceNavigation.move(1, in: choices, from: "one"), "two")
        XCTAssertNil(WorkspaceChoiceNavigation.highlight(in: [choices[0]], keeping: "bad"))
        XCTAssertNil(WorkspaceChoiceNavigation.move(1, in: [choices[0]], from: nil))
    }
}

extension WorkspaceReadingPolishTests {
    @MainActor
    func testPickerEscapeReturnsKeyboardToOpenerAndEnterSkipsUnavailableChoice() async throws {
        var selected = ""
        let picker = WorkspaceChoicePicker(title: "Project", selection: Binding(get: { selected }, set: { selected = $0 }),
            label: "Default", defaultLabel: "Default", choices: [
                .init(id: "bad", label: "Match unavailable", detail: "", problem: "Missing profile"),
                .init(id: "good", label: "Match available", detail: "")
            ])
        let host = NSHostingView(rootView: picker.padding())
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 650, height: 450), styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false; window.contentView = host; window.makeKeyAndOrderFront(nil)
        defer { window.close() }
        window.layoutIfNeeded(); host.layoutSubtreeIfNeeded()
        try await Task.sleep(for: .milliseconds(100))
        func openPicker() throws {
            let point = host.convert(NSPoint(x: host.bounds.width * 0.7, y: host.bounds.midY), to: nil)
            let up = try XCTUnwrap(NSEvent.mouseEvent(with: .leftMouseUp, location: point, modifierFlags: [], timestamp: 0,
                windowNumber: window.windowNumber, context: nil, eventNumber: 1, clickCount: 1, pressure: 0))
            let down = try XCTUnwrap(NSEvent.mouseEvent(with: .leftMouseDown, location: point, modifierFlags: [], timestamp: 0,
                windowNumber: window.windowNumber, context: nil, eventNumber: 0, clickCount: 1, pressure: 1))
            // AppKit button tracking consumes the queued mouse-up synchronously.
            NSApp.postEvent(up, atStart: true)
            window.sendEvent(down)
        }
        try openPicker()
        try await Task.sleep(for: .milliseconds(200))
        let popover = try XCTUnwrap(NSApp.windows.first { $0 !== window && $0.isVisible && ($0.firstResponder as? NSTextView)?.isFieldEditor == true })
        let editor = try XCTUnwrap(popover.firstResponder as? NSTextView)
        editor.insertText("Match", replacementRange: NSRange(location: NSNotFound, length: 0))
        try await Task.sleep(for: .milliseconds(100))
        func key(_ text: String, code: UInt16, in target: NSWindow) throws {
            let event = try XCTUnwrap(NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: [], timestamp: 0,
                windowNumber: target.windowNumber, context: nil, characters: text, charactersIgnoringModifiers: text, isARepeat: false, keyCode: code))
            target.sendEvent(event)
        }
        try key("\r", code: 36, in: popover)
        try await Task.sleep(for: .milliseconds(200))
        XCTAssertEqual(selected, "good")
        XCTAssertFalse(popover.isVisible)
        XCTAssertFalse((window.firstResponder as? NSTextView)?.isFieldEditor == true)
        try openPicker()
        try await Task.sleep(for: .milliseconds(200))
        let reopened = try XCTUnwrap(NSApp.windows.first { $0 !== window && $0.isVisible && ($0.firstResponder as? NSTextView)?.isFieldEditor == true })
        try key("\u{1b}", code: 53, in: reopened)
        try await Task.sleep(for: .milliseconds(200))
        XCTAssertFalse(reopened.isVisible)
        XCTAssertEqual(selected, "good", "Escape must not change the selection")
        XCTAssertTrue((window.firstResponder as? NSView)?.isDescendant(of: host) == true)
    }
}
