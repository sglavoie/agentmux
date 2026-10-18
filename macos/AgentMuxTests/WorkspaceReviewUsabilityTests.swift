import AppKit
import SwiftUI
import XCTest
import ScreenCaptureKit
import Vision
@testable import AgentMux

private struct UsabilityRunner: ASLWorkRunner {
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { then(work()) }
}
private final class UsabilityService: ASLService {
    var result = ASLResult(status: ASLStatus(available: true, mutationsEnabled: true), snapshot: ASLSnapshot(), meta: ASLSnapshotMeta())
    func snapshot() -> Result<ASLResult, ASLRequestError> { .success(result) }
    func question(planDir: String, questionID: String) -> Result<ASLQuestionBody, ASLRequestError> { .failure(.unsupported) }
    func act(_ params: ASLRequestParams) -> Result<ASLOperation, ASLRequestError> { XCTFail("Reading must not perform an action"); return .failure(.unsupported) }
}

@MainActor
final class WorkspaceReviewUsabilityTests: XCTestCase {
    private func command(_ value: @escaping ([String]) -> String) -> AMXCommand {
        var command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        command.runner = { _, args, _, _ in
            .finished(.init(status: 0, stdout: Data("{\"schema_version\":1,\"data\":\(value(args))}".utf8), stderr: Data()))
        }
        return command
    }
    private func views(_ view: NSView) -> [NSView] { [view] + view.subviews.flatMap(views) }
    private func key(_ text: String, flags: NSEvent.ModifierFlags, in window: NSWindow) -> NSEvent {
        NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: flags, timestamp: 0,
                        windowNumber: window.windowNumber, context: nil, characters: text,
                        charactersIgnoringModifiers: text, isARepeat: false, keyCode: 0)!
    }
    private func window<V: View>(_ root: V, width: Double = 780) -> (NSWindow, WorkspaceReviewHost<V>) {
        let host = WorkspaceReviewHost(rootView: root)
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: width, height: 428), styleMask: [.titled, .resizable], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false; window.contentView = host; window.makeKeyAndOrderFront(nil)
        return (window, host)
    }
    private func runs(_ data: @escaping () -> String) -> WorkspaceRunsModel {
        let service = UsabilityService()
        var row = ASLRow()
        row.rowID = "row"; row.runID = "run"; row.planDir = "/plan"; row.sessionID = "9"
        row.primaryState = "paused"; row.pauseReason = "verification-failed"
        row.actions = [ASLMethod.runResume]; row.actionToken = "fixture"
        service.result.snapshot?.rows = [row]
        let amx = command { _ in data() }
        let model = WorkspaceRunsModel(service: service, command: { amx }, runner: UsabilityRunner())
        model.refresh()
        return model
    }
    private func detail(passed: Bool = false, long: Bool = false) -> String {
        let sessions = (1...(long ? 50 : 1)).map { "{\"id\":\"\($0)\",\"status\":\"pending\",\"path\":\"/plan/\($0).md\"}" }.joined(separator: ",")
        return """
        {"plan_dir":"/plan","plan_title":"API","counts":{"complete":8,"pending":2},"sessions":[\(sessions)],"runner_state":{"run_id":"run","paused":true,"reason":"verification-failed","run_started_at":"","run_finished_at":""},"verification":[
        {"event":"verification_check_finished","at":"2026-10-18T10:00:00Z","session_id":"8","name":"Old failure","status":"failed","returncode":1},
        {"event":"verification_finished","at":"2026-10-18T10:00:01Z","session_id":"8","passed":false},
        {"event":"verification_check_finished","at":"2026-10-18T11:00:00Z","session_id":"9","name":"Unit tests","status":"\(passed ? "passed" : "failed")","returncode":\(passed ? 0 : 1),"duration_seconds":2.5},
        {"event":"verification_finished","at":"2026-10-18T11:00:01Z","session_id":"9","passed":\(passed),"check_count":1,"passed_count":\(passed ? 1 : 0)}],"verification_truncated":false}
        """
    }

    func testSidebarPreferencesAreIndependentAndClampedWithoutLosingWiderPreference() throws {
        let suite = "amx-review-usability-\(UUID())"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let changes = WorkspaceChangesModel(command: { nil }, defaults: defaults)
        let runs = WorkspaceRunsModel(command: { nil }, defaults: defaults)
        changes.layout.preferredWidth = 370; changes.layout.collapsed = true
        runs.layout.preferredWidth = 280
        let restored = WorkspaceChangesModel(command: { nil }, defaults: defaults)
        let restoredRuns = WorkspaceRunsModel(command: { nil }, defaults: defaults)
        XCTAssertTrue(restored.layout.collapsed)
        XCTAssertFalse(restoredRuns.layout.collapsed)
        XCTAssertEqual(restored.layout.width(in: 696, minimum: 180, detailMinimum: 360), 330)
        XCTAssertEqual(restored.layout.width(in: 1100, minimum: 180, detailMinimum: 360), 370)
        XCTAssertEqual(restoredRuns.layout.preferredWidth, 280)
        restored.layout.preferredWidth = .nan
        XCTAssertTrue(restored.layout.width(in: 696, minimum: 180, detailMinimum: 360).isFinite)
    }

    func testChangesTogglePreservesFileAndAnchorAndFocusFilesReopensSidebar() async throws {
        let patch = "@@ -1,80 +1,80 @@\n" + (1...80).map { " line \($0)\n" }.joined()
        let json = try JSONSerialization.data(withJSONObject: ["root": "/checkout", "base": "HEAD", "files": [["path": "file.swift", "added": 1, "deleted": 1, "binary": false, "untracked": false]], "patch": patch, "truncated": false])
        let amx = command { $0.first == "worktree" ? "{\"worktrees\":[]}" : String(decoding: json, as: UTF8.self) }
        let model = WorkspaceChangesModel(command: { amx }, runner: UsabilityRunner())
        model.open(directory: "/checkout")
        let (window, host) = window(WorkspaceChangesView(model: model), width: 720)
        defer { window.close() }
        try await Task.sleep(for: .milliseconds(200))
        let text = try XCTUnwrap(views(host).compactMap { $0 as? WorkspaceDiffTextView }.first)
        let scroll = try XCTUnwrap(text.enclosingScrollView)
        let width = scroll.frame.width
        XCTAssertGreaterThan(scroll.contentSize.height, 270, "No empty worktree toolbar is reserved")
        scroll.contentView.scroll(to: NSPoint(x: 0, y: 300)); scroll.reflectScrolledClipView(scroll.contentView)
        try await Task.sleep(for: .milliseconds(80))
        XCTAssertTrue(host.performKeyEquivalent(with: key("s", flags: [.command, .option], in: window)))
        try await Task.sleep(for: .milliseconds(150))
        XCTAssertTrue(model.layout.collapsed)
        XCTAssertEqual(model.selectedFile, "file.swift")
        XCTAssertGreaterThan(scroll.frame.width, width + 150)
        XCTAssertEqual(scroll.contentView.bounds.minY, 300, accuracy: 3)
        let pageDown = try XCTUnwrap(NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: [], timestamp: 0,
            windowNumber: window.windowNumber, context: nil, characters: "\u{f72d}", charactersIgnoringModifiers: "\u{f72d}", isARepeat: false, keyCode: 121))
        window.sendEvent(pageDown)
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertGreaterThan(scroll.contentView.bounds.minY, 400, "Collapsing the sidebar gives keyboard reading focus to the diff")
        XCTAssertTrue(host.performKeyEquivalent(with: key("f", flags: [.command, .shift], in: window)))
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertFalse(model.layout.collapsed)
        XCTAssertEqual(model.selectedFile, "file.swift")
        let responder = try XCTUnwrap(window.firstResponder as? NSView)
        XCTAssertLessThan(responder.convert(.zero, to: host).x, 230)
    }

    func testInspectionSelectsCurrentFailureAndInvalidatesWhenEvidenceChanges() {
        var passed = false
        let model = runs { self.detail(passed: passed) }
        model.onFocus = { _ in XCTFail("Inspection must open evidence, not the terminal") }
        model.inspectFailedCheck()
        XCTAssertEqual(model.inspectedVerification?.name, "Unit tests")
        XCTAssertEqual(model.inspectedVerificationIndex, 2)
        XCTAssertNotNil(model.inspectionRequest)
        passed = true; model.refresh()
        XCTAssertNil(model.inspectedVerification)
        XCTAssertNotNil(model.inspectionUnavailable)
        model.inspectFailedCheck(); XCTAssertNil(model.inspectionRequest)
        passed = false; model.refresh(); model.inspectFailedCheck()
        model.select(nil)
        XCTAssertNil(model.inspectedVerification)
    }

    func testInspectionNamesSummaryOnlyEvidenceHonestly() throws {
        var value = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(detail().utf8)) as? [String: Any])
        let records = try XCTUnwrap(value["verification"] as? [[String: Any]])
        value["verification"] = records.filter { ($0["event"] as? String) == "verification_finished" }
        let json = String(decoding: try JSONSerialization.data(withJSONObject: value), as: UTF8.self)
        let model = runs { json }
        XCTAssertEqual(model.inspectionTitle, "Inspect Verification")
        model.inspectFailedCheck()
        XCTAssertEqual(model.inspectedVerification?.event, "verification_finished")
        XCTAssertEqual(model.inspectedVerification?.sessionID, "9")
    }

    func testInspectionShortcutRevealsEvidenceBelowLongPlanAndSidebarRestoresFocus() async throws {
        let model = runs { self.detail(long: true) }
        model.onFocus = { _ in XCTFail("Inspect shortcut must not focus a session") }
        let (window, host) = window(WorkspaceRunsView(model: model, plan: WorkspaceRunPlanModel(command: { nil })))
        defer { window.close() }
        try await Task.sleep(for: .milliseconds(200))
        XCTAssertTrue(host.performKeyEquivalent(with: key("i", flags: [.command, .shift], in: window)))
        try await Task.sleep(for: .milliseconds(250))
        let scroll = try XCTUnwrap(views(host).compactMap { $0 as? NSScrollView }.max { $0.convert(.zero, to: host).x < $1.convert(.zero, to: host).x })
        XCTAssertEqual(model.inspectedVerification?.name, "Unit tests")
        XCTAssertGreaterThan(scroll.contentView.bounds.minY, 700, "Inspection expands evidence and actually reaches it")
        let selection = model.selection
        XCTAssertTrue(host.performKeyEquivalent(with: key("s", flags: [.command, .option], in: window)))
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertTrue(model.layout.collapsed)
        XCTAssertEqual(model.selection, selection)
        XCTAssertTrue(host.performKeyEquivalent(with: key("s", flags: [.command, .option], in: window)))
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertFalse(model.layout.collapsed)
        let responder = try XCTUnwrap(window.firstResponder as? NSView)
        XCTAssertLessThan(responder.convert(.zero, to: host).x, 240)
    }

    func testCopyAcknowledgementExpiresAndRepeatedCopyStartsANewInterval() async throws {
        guard #available(macOS 14.4, *) else { throw XCTSkip("Own-window capture requires macOS 14.4") }
        let board = NSPasteboard(name: .init("amx-copy-\(UUID())"))
        defer { board.releaseGlobally() }
        let code = "let café = \"🦊\"\n"
        let (window, host) = window(WorkspaceTranscriptCode(language: "swift", text: code, terms: [],
            preferences: WorkspaceReadingPreferences(), pasteboard: board).padding(12)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading))
        defer { window.close() }
        func clickCopy() throws {
            let point = host.convert(NSPoint(x: host.bounds.maxX - 70, y: host.isFlipped ? 24 : host.bounds.maxY - 24), to: nil)
            let up = try XCTUnwrap(NSEvent.mouseEvent(with: .leftMouseUp, location: point, modifierFlags: [], timestamp: 0,
                windowNumber: window.windowNumber, context: nil, eventNumber: 1, clickCount: 1, pressure: 0))
            let down = try XCTUnwrap(NSEvent.mouseEvent(with: .leftMouseDown, location: point, modifierFlags: [], timestamp: 0,
                windowNumber: window.windowNumber, context: nil, eventNumber: 0, clickCount: 1, pressure: 1))
            NSApp.postEvent(up, atStart: true)
            window.sendEvent(down)
        }
        func visibleText() async throws -> String {
            let content = try await SCShareableContent.currentProcess
            let target = try XCTUnwrap(content.windows.first { $0.windowID == CGWindowID(window.windowNumber) })
            let filter = SCContentFilter(desktopIndependentWindow: target)
            let config = SCStreamConfiguration()
            config.width = Int(window.frame.width * CGFloat(filter.pointPixelScale))
            config.height = Int(window.frame.height * CGFloat(filter.pointPixelScale))
            config.showsCursor = false
            let capture = try await SCScreenshotManager.captureImage(contentFilter: filter, configuration: config)
            let recognition = VNRecognizeTextRequest()
            recognition.recognitionLevel = .accurate
            try VNImageRequestHandler(cgImage: capture).perform([recognition])
            return (recognition.results ?? []).compactMap { $0.topCandidates(1).first?.string }.joined(separator: "\n")
        }
        try await Task.sleep(for: .milliseconds(150))
        try clickCopy()
        try await Task.sleep(for: .milliseconds(1000))
        XCTAssertEqual(board.string(forType: .string), code)
        let first = try await visibleText()
        XCTAssertTrue(first.contains("Copied"), first)
        try clickCopy()
        try await Task.sleep(for: .milliseconds(1100))
        let repeated = try await visibleText()
        XCTAssertTrue(repeated.contains("Copied"), "The first timer must not clear the second acknowledgement: " + repeated)
        try await Task.sleep(for: .milliseconds(1100))
        let expired = try await visibleText()
        XCTAssertTrue(expired.contains("Copy code"), expired)
        XCTAssertFalse(expired.contains("Copied"), expired)
        XCTAssertEqual(board.string(forType: .string), code)
    }
}
