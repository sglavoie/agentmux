import AppKit
import SwiftUI
import XCTest
@testable import AgentMux

@MainActor
final class WorkspaceWorkflowPolishTests: XCTestCase {
    func testWordingPreservesMeaningAndUnknownStates() {
        XCTAssertEqual(WorkspaceWording.status("fast_forward"), "Ready to land")
        XCTAssertEqual(WorkspaceWording.status("diverged_clean"), "Branches diverged; merge required")
        XCTAssertEqual(WorkspaceWording.status("not_ready"), "Not ready")
        XCTAssertEqual(WorkspaceWording.status("future_state"), "Future state")
        XCTAssertEqual(WorkspaceWording.relativeTime("invalid", now: Date()), "Time unavailable")
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        let raw = ISO8601DateFormatter().string(from: now.addingTimeInterval(-720))
        XCTAssertFalse(WorkspaceWording.relativeTime(raw, now: now).isEmpty)
        XCTAssertNotEqual(WorkspaceWording.relativeTime(raw, now: now), raw)
    }

    func testReviewHostDoesNotClaimShortcutsFromAnotherPaneOrItsFieldEditor() {
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 800, height: 600),
                              styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        defer { window.close() }
        let container = NSView(frame: window.contentLayoutRect)
        window.contentView = container
        let inside = NSTextField(frame: NSRect(x: 0, y: 0, width: 200, height: 30))
        let host = WorkspaceReviewHost(rootView: ReviewTestField(field: inside))
        let other = NSTextField(frame: NSRect(x: 400, y: 0, width: 200, height: 30))
        host.frame = NSRect(x: 0, y: 0, width: 400, height: 500)
        container.addSubview(host); container.addSubview(other)
        host.layoutSubtreeIfNeeded()
        window.makeFirstResponder(inside)
        XCTAssertTrue(host.ownsKeyboard)
        window.makeFirstResponder(other)
        XCTAssertFalse(host.ownsKeyboard)
        let event = NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: .command,
                                    timestamp: 0, windowNumber: window.windowNumber, context: nil,
                                    characters: "g", charactersIgnoringModifiers: "g", isARepeat: false, keyCode: 5)!
        XCTAssertFalse(host.performKeyEquivalent(with: event))
        window.makeFirstResponder(inside)
        XCTAssertTrue(host.ownsKeyboard)
    }
}

private struct PolishRunner: ASLWorkRunner {
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { then(work()) }
}

extension WorkspaceWorkflowPolishTests {
    func testHistoryKeyboardFindMatchAndResumeKeepSelectionAcrossRefresh() async throws {
        var command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        command.runner = { _, args, _, _ in
            let data = args[1] == "show"
                ? #"{"session":{"id":"one","harness":"claude","project":"API","cwd":"/repo"},"messages_total":3,"messages_start":0,"match_messages":[0,1,2],"match_terms":["needle"],"focus_message":0,"messages":[{"role":"user","text":"needle one"},{"role":"assistant","text":"needle two"},{"role":"user","text":"needle three"}]}"#
                : #"{"results":[{"id":"one","harness":"claude","project":"API","profile":"work","cwd":"/repo","preview":"needle","matches":2}]}"#
            return .finished(ProcessOutput(status: 0, stdout: Data("{\"schema_version\":1,\"data\":\(data)}".utf8), stderr: Data()))
        }
        let model = WorkspaceHistoryModel(command: { command }, runner: PolishRunner())
        model.query = "needle"; model.search()
        let host = WorkspaceReviewHost(rootView: WorkspaceHistoryView(model: model))
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1000, height: 680),
                              styleMask: [.titled, .closable], backing: .buffered, defer: false)
        host.closesWindow = true
        window.isReleasedWhenClosed = false; window.contentView = host
        window.makeKeyAndOrderFront(nil)
        defer { window.close() }
        try await Task.sleep(for: .milliseconds(200))
        func key(_ text: String, flags: NSEvent.ModifierFlags = .command, code: UInt16 = 5) -> Bool {
            let event = NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: flags, timestamp: 0,
                                        windowNumber: window.windowNumber, context: nil, characters: text,
                                        charactersIgnoringModifiers: text, isARepeat: false, keyCode: code)!
            return host.performKeyEquivalent(with: event)
        }
        XCTAssertTrue(host.ownsKeyboard)
        XCTAssertTrue(key("g"))
        XCTAssertEqual(model.matchIndex, 1)
        XCTAssertTrue(key("G", flags: [.command, .shift]))
        XCTAssertEqual(model.matchIndex, 0)
        let responder = window.firstResponder
        let selection = model.selection
        model.search()
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertEqual(model.selection, selection)
        XCTAssertTrue(window.firstResponder === responder)
        var resumed: [String] = []
        var forked: [Bool] = []
        model.onAction = { conversation, fork, done in
            forked.append(fork); resumed.append(conversation.selector); done(nil)
        }
        XCTAssertTrue(key("\r", code: 36))
        XCTAssertEqual(resumed, ["claude:one"])
        XCTAssertEqual(forked, [false])
        XCTAssertTrue(key("\r", flags: [.command, .shift], code: 36))
        XCTAssertEqual(forked, [false, true])
        XCTAssertTrue(key("w", code: 13))
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertFalse(window.isVisible)
    }
}

private struct ReviewTestField: NSViewRepresentable {
    let field: NSTextField
    func makeNSView(context: Context) -> NSTextField { field }
    func updateNSView(_ view: NSTextField, context: Context) {}
}

extension WorkspaceWorkflowPolishTests {
    func testChangesKeyboardHunksRefreshAndEditor() async throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let file = directory.appendingPathComponent("change.txt")
        try Data("fixture".utf8).write(to: file)
        let patch = "@@ -1,80 +1,80 @@\n" + (0..<80).map { " context \($0)\n" }.joined()
            + "@@ -100,1 +100,1 @@\n-old\n+new\n"
        let payload: [String: Any] = ["root": directory.path, "base": "abc", "files": [["path": "change.txt", "added": 1, "deleted": 1, "binary": false, "untracked": false]], "patch": patch, "truncated": false]
        let data = try JSONSerialization.data(withJSONObject: ["schema_version": 1, "data": payload])
        var reads = 0
        var command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        command.runner = { _, args, _, _ in
            if args.first == "worktree" {
                return .finished(ProcessOutput(status: 0, stdout: Data(#"{"schema_version":1,"data":{"worktrees":[]}}"#.utf8), stderr: Data()))
            }
            reads += 1
            return .finished(ProcessOutput(status: 0, stdout: data, stderr: Data()))
        }
        let model = WorkspaceChangesModel(command: { command }, runner: PolishRunner())
        model.open(directory: directory.path)
        let host = WorkspaceReviewHost(rootView: WorkspaceChangesView(model: model))
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1050, height: 700),
                              styleMask: [.titled, .closable], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false; window.contentView = host
        window.makeKeyAndOrderFront(nil)
        defer { window.close() }
        try await Task.sleep(for: .milliseconds(200))
        func key(_ text: String, flags: NSEvent.ModifierFlags = .command) -> Bool {
            let event = NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: flags, timestamp: 0,
                                        windowNumber: window.windowNumber, context: nil, characters: text,
                                        charactersIgnoringModifiers: text, isARepeat: false, keyCode: text == "e" ? 14 : text == "r" ? 15 : 5)!
            return host.performKeyEquivalent(with: event)
        }
        func descendants(_ view: NSView) -> [NSView] { [view] + view.subviews.flatMap(descendants) }
        let scroll = try XCTUnwrap(descendants(host).compactMap { $0 as? NSScrollView }.first { $0.documentView is NSTextView })
        XCTAssertTrue(host.ownsKeyboard)
        XCTAssertTrue(key("g"))
        try await Task.sleep(for: .milliseconds(100))
        let firstHunk = scroll.contentView.bounds.origin.y
        XCTAssertTrue(key("g"))
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertGreaterThan(scroll.contentView.bounds.origin.y, firstHunk)
        XCTAssertTrue(key("G", flags: [.command, .shift]))
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertEqual(scroll.contentView.bounds.origin.y, firstHunk, accuracy: 1)
        var opened: [String] = []
        model.onOpenFile = { opened.append($0) }
        XCTAssertTrue(key("e"))
        XCTAssertEqual(opened, [file.path])
        XCTAssertTrue(key("E", flags: [.command, .shift]))
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertTrue((window.firstResponder as? NSTextView)?.isFieldEditor == true)
        XCTAssertTrue(key("F", flags: [.command, .shift]))
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertFalse((window.firstResponder as? NSTextView)?.isFieldEditor == true)
        let before = reads
        let responder = window.firstResponder
        XCTAssertTrue(key("r"))
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertGreaterThan(reads, before)
        XCTAssertEqual(model.selectedFile, "change.txt")
        XCTAssertTrue(window.firstResponder === responder)
    }
}
