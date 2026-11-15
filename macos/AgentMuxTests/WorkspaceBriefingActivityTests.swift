import AppKit
import Combine
import CoreImage
import SwiftUI
import ScreenCaptureKit
import Vision
import XCTest
@testable import AgentMux

private final class ActivityRunner: ASLWorkRunner {
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { pending.append { then(work()) } }
    func release(_ index: Int = 0) { pending.remove(at: index)() }
}

@MainActor
final class WorkspaceBriefingActivityTests: XCTestCase {
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

    func testBriefingWindowBuildsAndCloseDiscardsActivityReadWork() {
        let runner = ActivityRunner()
        let amx = command { _ in self.report([self.entry("one")]) }
        let model = WorkspaceActivityModel(command: { amx }, runner: runner, readStore: memory)
        let controller = WorkspaceBriefingWindow(model: .init(
            briefing: .init(command: { nil }, runner: runner), activity: model,
            summary: .init(command: { nil }, runner: runner)))
        var visible = false
        controller.onVisibilityChanged = { visible = $0 }
        controller.show()
        XCTAssertTrue(visible)
        XCTAssertEqual(controller.window?.title, "Briefing")
        controller.window?.close()
        runner.release()
        XCTAssertFalse(visible)
        XCTAssertNil(model.report)
    }
}


extension WorkspaceBriefingActivityTests {
    private func fixture(_ id: String = "event", kind: String = "state", title: String = "Working",
                         detail: String = "Permission requested → Working · applied", event: String = "PermissionResolved",
                         at: String = "2026-10-15T12:00:00Z", runtime: String = "runtime") -> WorkspaceActivityEntry {
        .init(id: id, at: at, kind: kind, title: title, detail: detail, event: event, harness: "claude",
              profile: "work", sessionID: "a", runtimeSessionID: runtime, runID: "run-one", traceID: "trace-one")
    }

    private var aslEvents: [(String, String, String)] { [
        ("AgentSessionLoopFinished", "run", "ASL run finished"),
        ("AgentSessionLoopPaused", "run", "ASL run paused"),
        ("AgentSessionLoopStarted", "run", "ASL run started"),
        ("AgentSessionLoopWaitStarted", "question", "ASL answer requested"),
        ("AgentSessionLoopVerificationFinished", "run", "ASL verification finished")
    ] }

    func testTransitionPreservesOriginalEvidenceAndChecksASLEventBeforeArrows() {
        for (detail, expected) in [
            ("Permission requested → Working · applied", "Permission requested → Working"),
            ("Working → Turn finished", "Working → Turn finished"), ("", ""),
            ("process exited with status 1 · settled", "process exited with status 1 · settled"),
            ("reason only · more evidence", "reason only · more evidence")
        ] {
            let entry = fixture(detail: detail)
            XCTAssertEqual(entry.transition, expected)
            XCTAssertEqual(entry.detail, detail)
        }
        for (event, kind, title) in aslEvents {
            for reason in ["applied", "before → after · reason"] {
                let entry = fixture(kind: kind, title: title, detail: reason, event: event)
                XCTAssertEqual(entry.transition, "", event)
                XCTAssertEqual(entry.detail, reason)
            }
        }
    }

    func testSummaryCountsKindsInOrderAndUsesLatestDateRatherThanArrayOrder() throws {
        let now = try XCTUnwrap(WorkspaceActivityEntry.date("2026-10-15T12:02:00Z"))
        let entries = [fixture("old", kind: "run", title: "Old", at: "2026-10-15T11:00:00Z"),
                       fixture("latest", kind: "permission"), fixture("question", kind: "question"),
                       fixture("done", kind: "completed"), fixture("error", kind: "error"), fixture("exit", kind: "exit")]
        let group = WorkspaceActivityModel.SessionGroup(id: "runtime", entries: entries)
        XCTAssertEqual(group.summary(now: now), "1 permission request · 1 question · 1 turn finished · 1 error · agent exited · 1 run update · latest: Working, 2m ago")
        let doubled = WorkspaceActivityModel.SessionGroup(id: "runtime", entries: entries + entries)
        XCTAssertEqual(doubled.summary(now: now), "2 permission requests · 2 questions · 2 turns finished · 2 errors · agent exited · 2 run updates · latest: Working, 2m ago")
        XCTAssertEqual(WorkspaceActivityModel.SessionGroup(id: "runtime", entries: [fixture()]).summary(now: now),
                       "1 state change · latest: Working, 2m ago")
        XCTAssertEqual(WorkspaceActivityModel.SessionGroup(id: "runtime", entries: [fixture(), fixture()]).summary(now: now),
                       "2 state changes · latest: Working, 2m ago")
        XCTAssertEqual(WorkspaceActivityModel.SessionGroup(id: "empty", entries: []).summary(now: now), "0 state changes")
    }

    func testRevealSurvivesLoadingRepeatsAndCancelsOnSupersedingNavigationOrClose() throws {
        let runner = ActivityRunner()
        let amx = command { _ in self.report([self.entry("one"), self.entry("two")]) }
        let model = WorkspaceActivityModel(command: { amx }, runner: runner,
            readStore: .init(load: { .init() }, save: { _ in XCTFail("Navigation must not save read markers") }))
        model.refresh()
        model.select("two", origin: .explicit)
        let first = try XCTUnwrap(model.explicitReveal)
        model.filterChanged() // Automatic fallback while the report is pending.
        XCTAssertEqual(model.explicitReveal, first)
        runner.release()
        XCTAssertEqual(model.selection, "two")
        XCTAssertEqual(model.explicitReveal, first)
        model.consumeReveal(first)
        model.select("two", origin: .explicit)
        let repeated = try XCTUnwrap(model.explicitReveal)
        XCTAssertGreaterThan(repeated.token, first.token)
        model.consumeReveal(first)
        XCTAssertEqual(model.explicitReveal, repeated, "A stale consumer cannot clear a newer link")
        model.select("one", origin: .explicit)
        XCTAssertEqual(model.explicitReveal?.entryID, "one")
        model.select(nil, origin: .explicit)
        XCTAssertNil(model.explicitReveal)
        model.filterChanged()
        XCTAssertEqual(model.selection, "one")
        XCTAssertNil(model.explicitReveal)
        model.select("two", origin: .explicit)
        model.clearFlightFilter()
        XCTAssertNil(model.explicitReveal, "Scope-only navigation supersedes an event link")
        model.select("two", origin: .explicit)
        model.setProject("Other", rows: [])
        XCTAssertNil(model.explicitReveal)
        model.select("two", origin: .explicit)
        model.close()
        XCTAssertNil(model.explicitReveal)
        while !runner.pending.isEmpty { runner.release() }
        XCTAssertNil(model.selection)
    }

    private func descendants(_ view: NSView) -> [NSView] { [view] + view.subviews.flatMap(descendants) }

    private func host(_ model: WorkspaceActivityModel, grouped: Bool = true) -> NSWindow {
        model.layout.preferredWidth = 500
        let window = NSWindow(contentRect: NSRect(x: 50, y: 50, width: 1150, height: 750), styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.contentView = NSHostingView(rootView: WorkspaceActivityView(model: model, grouped: grouped))
        window.makeKeyAndOrderFront(nil)
        return window
    }

    private func settle() async throws { try await Task.sleep(for: .milliseconds(200)) }

    func testHostedOpeningRevealManualCollapseAndAutomaticFallback() async throws {
        let runner = ActivityRunner()
        let other = entry("three").replacingOccurrences(of: "\"runtime\"", with: "\"other\"")
        let amx = command { _ in self.report([self.entry("one"), self.entry("two"), other]) }
        var saved = WorkspaceActivityReadState(), saves = 0
        let model = WorkspaceActivityModel(command: { amx }, runner: runner,
            readStore: .init(load: { saved }, save: { saved = $0; saves += 1 }))
        var reveals: [WorkspaceActivityModel.RevealRequest] = []
        let subscription = model.$explicitReveal.compactMap { $0 }.sink { reveals.append($0) }
        defer { subscription.cancel() }
        model.refresh(); runner.release()
        let window = host(model)
        defer { model.close(); window.close() }
        try await settle()
        let table = try XCTUnwrap(descendants(try XCTUnwrap(window.contentView)).compactMap { $0 as? NSTableView }.first)
        let collapsed = table.numberOfRows
        XCTAssertEqual(collapsed, 2, "Ordinary opening renders only the two session headers")
        XCTAssertEqual(model.selection, "one")
        XCTAssertNil(model.explicitReveal)
        XCTAssertTrue(reveals.isEmpty, "Automatic opening emits no reveal")
        func toggleFirstGroup() throws {
            let row = try XCTUnwrap(table.rowView(atRow: 0, makeIfNecessary: true))
            let button = try XCTUnwrap(descendants(row).compactMap { $0 as? NSButton }.first)
            button.performClick(nil)
        }
        try toggleFirstGroup(); try await settle()
        XCTAssertEqual(table.numberOfRows, collapsed + 2)
        XCTAssertEqual(saves, 0)
        // Native row selection and keyboard movement exercise the List binding.
        table.selectRowIndexes(IndexSet(integer: 1), byExtendingSelection: false)
        try await settle()
        XCTAssertEqual(model.selection, "one")
        window.makeFirstResponder(table)
        table.keyDown(with: try XCTUnwrap(NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: [], timestamp: 0,
            windowNumber: window.windowNumber, context: nil, characters: "\u{f701}", charactersIgnoringModifiers: "\u{f701}", isARepeat: false, keyCode: 125)))
        try await settle()
        XCTAssertEqual(model.selection, "two")
        XCTAssertEqual(reveals.last?.entryID, "two", "Keyboard selection emits an explicit reveal")
        XCTAssertNil(model.explicitReveal, "The hosted view consumes explicit row/keyboard reveals")
        try toggleFirstGroup(); try await settle()
        XCTAssertEqual(table.numberOfRows, collapsed)
        model.select("two", origin: .explicit); try await settle()
        XCTAssertEqual(table.numberOfRows, collapsed + 2)
        try toggleFirstGroup(); try await settle()
        model.select("two", origin: .explicit); try await settle()
        XCTAssertEqual(table.numberOfRows, collapsed + 2, "Repeated linking to the selected id reveals it again")
        try toggleFirstGroup(); try await settle()
        model.refresh(); runner.release(runner.pending.count - 1); try await settle()
        XCTAssertEqual(table.numberOfRows, collapsed, "Refresh preserves manual collapse")
        let revealsBeforeRead = reveals.count
        XCTAssertEqual(saves, 0)
        model.markSelectedRead(); try await settle()
        XCTAssertEqual(model.selection, "one")
        XCTAssertEqual(table.numberOfRows, collapsed, "Read fallback cannot reveal a group")
        model.undoMarkRead(); try await settle()
        XCTAssertEqual(model.selection, "two")
        XCTAssertEqual(table.numberOfRows, collapsed)
        XCTAssertEqual(saves, 2)
        XCTAssertEqual(reveals.count, revealsBeforeRead, "Read and undo select automatically")
        model.close(); try await settle()
        model.refresh()
        model.select("three", origin: .explicit) // New link arrives before refresh finishes.
        runner.release(runner.pending.count - 1); try await settle()
        XCTAssertEqual(model.selection, "three")
        XCTAssertEqual(table.numberOfRows, collapsed + 1)
        XCTAssertNil(model.explicitReveal)
        model.close(); try await settle()
        model.refresh(); runner.release(runner.pending.count - 1); try await settle()
        XCTAssertEqual(table.numberOfRows, collapsed, "Reopening resets previous expansion choices")
        XCTAssertEqual(saves, 2)
        model.close()
        model.select("three", origin: .explicit)
        try await settle()
        XCTAssertEqual(table.numberOfRows, collapsed + 1, "A link immediately after close belongs to the new presentation")
    }

    func testHostedLinkBeforeFirstReportAndBeforeViewMount() async throws {
        for linkBeforeMount in [false, true] {
            let runner = ActivityRunner()
            let other = entry("two").replacingOccurrences(of: "\"runtime\"", with: "\"other\"")
            let amx = command { _ in self.report([self.entry("one"), other]) }
            let model = WorkspaceActivityModel(command: { amx }, runner: runner,
                readStore: .init(load: { .init() }, save: { _ in XCTFail("Linking cannot mark read") }))
            model.refresh()
            if linkBeforeMount { model.select("two", origin: .explicit); runner.release() }
            let window = host(model)
            defer { model.close(); window.close() }
            try await settle()
            if !linkBeforeMount { model.select("two", origin: .explicit); try await settle() }
            if !linkBeforeMount {
                XCTAssertNil(model.report)
                XCTAssertEqual(model.explicitReveal?.entryID, "two")
                runner.release(); try await settle()
            }
            XCTAssertEqual(model.selection, "two")
            XCTAssertNil(model.explicitReveal)
            let table = try XCTUnwrap(descendants(try XCTUnwrap(window.contentView)).compactMap { $0 as? NSTableView }.first)
            XCTAssertEqual(table.numberOfRows, 3, "Only the linked session expands when its report arrives")
            XCTAssertTrue(model.readState.read.isEmpty)
        }
    }

    @available(macOS 14.4, *)
    private func visibleText(_ window: NSWindow) async throws -> [VNRecognizedTextObservation] {
        try await settle()
        let content = try await SCShareableContent.currentProcess
        let target = try XCTUnwrap(content.windows.first { $0.windowID == CGWindowID(window.windowNumber) })
        let filter = SCContentFilter(desktopIndependentWindow: target)
        let config = SCStreamConfiguration()
        config.width = Int(filter.contentRect.width * CGFloat(filter.pointPixelScale))
        config.height = Int(filter.contentRect.height * CGFloat(filter.pointPixelScale))
        config.ignoreShadowsSingleWindow = true; config.showsCursor = false
        let capture = try await SCScreenshotManager.captureImage(contentFilter: filter, configuration: config)
        let request = VNRecognizeTextRequest()
        request.recognitionLevel = .accurate; request.recognitionLanguages = ["en-US"]
        request.minimumTextHeight = 0.005
        let enlarged = CIImage(cgImage: capture).transformed(by: CGAffineTransform(scaleX: 3, y: 3))
        let ocrImage = CIContext().createCGImage(enlarged, from: enlarged.extent) ?? capture
        try VNImageRequestHandler(cgImage: ocrImage).perform([request])
        return request.results ?? []
    }

    @available(macOS 14.4, *)
    func testHostedRowsAndDetailHideReasonsUntilDiagnosticsOpens() async throws {
        for (event, kind, title) in [("PermissionResolved", "state", "Working")] + aslEvents {
            let runner = ActivityRunner()
            let reason = "internal evidence retained"
            let detail = event == "PermissionResolved" ? "Permission requested → Working · " + reason : reason
            let json = entry("one").replacingOccurrences(of: "Working → Turn finished", with: detail)
                .replacingOccurrences(of: "\"Stop\"", with: "\"\(event)\"")
                .replacingOccurrences(of: "\"completed\"", with: "\"\(kind)\"")
                .replacingOccurrences(of: "\"Turn finished\"", with: "\"\(title)\"")
                .replacingOccurrences(of: "\"run_id\":\"\"", with: "\"run_id\":\"run-one\"")
            let amx = command { _ in self.report([json]) }
            let model = WorkspaceActivityModel(command: { amx }, runner: runner,
                readStore: .init(load: { .init() }, save: { _ in XCTFail("Inspection cannot mark read") }))
            model.refresh(); runner.release()
            let window = host(model, grouped: false)
            defer { model.close(); window.close() }
            let before = try await visibleText(window)
            let text = before.compactMap { $0.topCandidates(1).first?.string }.joined(separator: "\n")
            XCTAssertTrue(text.contains(title), text)
            XCTAssertFalse(text.contains(reason), "Reason leaked before Diagnostics: \(text)")
            XCTAssertFalse(text.contains("Recorded event:"), text)
            let label = try XCTUnwrap(before.first { $0.topCandidates(1).first?.string.contains("Diagnostics") == true }, text)
            // OCR locates the real disclosure, avoiding an unavailable in-process SwiftUI AX tree.
            let box = label.boundingBox
            let point = NSPoint(x: box.minX * window.frame.width + 6, y: box.midY * window.frame.height)
            let up = try XCTUnwrap(NSEvent.mouseEvent(with: .leftMouseUp, location: point, modifierFlags: [], timestamp: 0,
                windowNumber: window.windowNumber, context: nil, eventNumber: 1, clickCount: 1, pressure: 0))
            let down = try XCTUnwrap(NSEvent.mouseEvent(with: .leftMouseDown, location: point, modifierFlags: [], timestamp: 0,
                windowNumber: window.windowNumber, context: nil, eventNumber: 0, clickCount: 1, pressure: 1))
            NSApp.postEvent(up, atStart: true); window.sendEvent(down)
            let after = try await visibleText(window).compactMap { $0.topCandidates(1).first?.string }.joined(separator: "\n")
            XCTAssertTrue(after.contains(reason), after)
            XCTAssertTrue(after.contains("Recorded event:"), after)
            XCTAssertTrue(after.contains("Trace:"), after)
            XCTAssertTrue(after.contains("Run:"), after)
            XCTAssertEqual(model.selected?.detail, detail)
        }
    }
}
