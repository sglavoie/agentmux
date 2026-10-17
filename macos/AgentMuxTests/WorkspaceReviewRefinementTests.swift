import AppKit
import SwiftUI
import XCTest
@testable import AgentMux

private final class ReviewRunner: ASLWorkRunner {
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { pending.append { then(work()) } }
    func finish(_ index: Int = 0) { pending.remove(at: index)() }
}

@MainActor
final class WorkspaceReviewRefinementTests: XCTestCase {
    private func command(_ response: @escaping ([String]) -> [String: Any]) -> AMXCommand {
        var amx = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        amx.runner = { _, args, _, _ in
            .finished(.init(status: 0, stdout: try! JSONSerialization.data(withJSONObject: ["schema_version": 1, "data": response(args)]), stderr: Data()))
        }
        return amx
    }

    func testRefreshKeepsAnchorUpdatesExistingTextAndOffersNewMessages() {
        let runner = ReviewRunner()
        var total = 100
        var revision = "old"
        let amx = command { args in
            if args[1] != "show" { return ["sessions": [["id": "a", "harness": "claude"]]] }
            let start = args.firstIndex(of: "--from").map { Int(args[$0 + 1])! } ?? total - 50
            let count = args.firstIndex(of: "--count").map { Int(args[$0 + 1])! } ?? 50
            return ["session": ["id": "a", "harness": "claude"], "messages_total": total, "messages_start": start,
                    "messages": (start..<min(start + count, total)).map { ["role": "assistant", "text": "\(revision) \($0)"] }]
        }
        let model = WorkspaceHistoryModel(command: { amx }, runner: runner)
        model.search(); runner.finish(); runner.finish()
        model.rememberVisibleMessage(70)
        total = 103; revision = "updated"
        model.search(); runner.finish()
        XCTAssertEqual(model.transcript?.messages.first?.text.reveal(), "old 50", "preview remains readable during refresh")
        runner.finish()
        XCTAssertEqual(model.scrollRequest?.message, 70)
        XCTAssertEqual(model.transcript?.messages.first?.text.reveal(), "updated 50")
        XCTAssertEqual(model.newMessages, 3)
        model.refreshTranscript(); runner.finish()
        XCTAssertEqual(model.newMessages, 3, "refreshing twice must not double count new messages")
        model.showNewMessages(); runner.finish()
        XCTAssertEqual(model.scrollRequest?.message, 100)
        XCTAssertEqual(model.transcript?.end, 103)
        XCTAssertEqual(model.newMessages, 0)
    }

    func testHistoryPagesPreserveSelectionAndSupersededReadsNeverStart() {
        let runner = ReviewRunner()
        var requests: [[String]] = []
        let amx = command { args in
            requests.append(args)
            if args[1] == "show" { return ["session": ["id": args.last!.components(separatedBy: ":").last!, "harness": "claude"], "messages_total": 0] }
            let offset = Int(args[args.firstIndex(of: "--offset")! + 1])!
            let count = Int(args[args.firstIndex(of: "--limit")! + 1])!
            return ["results": (offset..<min(125, offset + count)).map { ["id": "row-\($0)", "harness": "claude", "preview": "needle phrase"] },
                    "total": 125, "has_more": offset + count < 125, "hidden_automated": 4, "match_terms": ["needle phrase"]]
        }
        let model = WorkspaceHistoryModel(command: { amx }, runner: runner)
        model.query = "obsolete"; model.search()
        model.query = "needle"; model.search()
        runner.finish(); XCTAssertTrue(requests.isEmpty, "cancelled queued work must not spawn amx")
        runner.finish(); runner.finish()
        XCTAssertEqual(model.entries.count, 50)
        XCTAssertEqual(model.resultTerms, ["needle phrase"])
        XCTAssertEqual(model.hiddenAutomated, 4)
        let selected = model.selection
        model.loadMoreResults(); model.loadMoreResults()
        XCTAssertEqual(runner.pending.count, 1)
        runner.finish()
        XCTAssertEqual(model.entries.count, 100)
        XCTAssertEqual(model.selection, selected)
        XCTAssertTrue(runner.pending.isEmpty, "appending results must not reload the selected transcript")
        model.loadMoreResults(); runner.finish()
        XCTAssertEqual(model.entries.count, 125)
        XCTAssertFalse(model.hasMoreResults)
        model.query = "later"; model.search(); model.close(); runner.finish()
        XCTAssertEqual(requests.filter { $0.last == "later" }.count, 0)
    }

    func testExactScopeAndHighlightUseBackendSemantics() throws {
        let args = WorkspaceHistoryModel.arguments(query: "\"needle phrase\"", project: nil, includeAutomated: false, conversation: "codex:exact", offset: 50)
        XCTAssertEqual(args[args.firstIndex(of: "--conversation")! + 1], "codex:exact")
        XCTAssertEqual(args.last, "\"needle phrase\"")
        let highlighted = WorkspaceHighlight.text("Needle phrase then needle", terms: ["needle phrase"])
        let marked = highlighted.runs.filter { $0.backgroundColor != nil }.map { String(highlighted[$0.range].characters) }
        XCTAssertEqual(marked, ["Needle phrase"])
    }

    func testDiffCopiesPartialUnicodeSourceWithoutGutterOrMarkers() {
        let diff = WorkspaceDiff("diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1 +1,2 @@\n-old\n+new 🦊 café\n context\n")
        let rendered = WorkspaceDiffView.render(diff)
        let text = WorkspaceDiffTextView()
        text.diff = diff; text.lineRanges = rendered.lines
        text.textStorage?.setAttributedString(rendered.text)
        XCTAssertEqual(text.string, diff.patch, "unified text contains no line-number decoration")
        let selected = (text.string as NSString).range(of: "+new 🦊 café\n context")
        text.setSelectedRange(selected)
        let pasteboard = NSPasteboard.withUniqueName()
        defer { pasteboard.releaseGlobally() }
        var acknowledgement: WorkspaceCopyFeedback?
        text.onCopy = { acknowledgement = $0 }
        text.copySource(to: pasteboard)
        XCTAssertEqual(acknowledgement?.message, "Copied")
        XCTAssertEqual(pasteboard.string(forType: .string), "new 🦊 café\ncontext")
        let partial = (text.string as NSString).range(of: "🦊 café")
        XCTAssertEqual(text.sourceText(in: partial), "🦊 café")
        XCTAssertFalse(text.sourceText(in: NSRange(location: 0, length: text.string.utf16.count)).contains("@@"))
    }

    func testShiftedDiffKeepsContentAnchorAndWrapFontPreferencesRestore() async throws {
        let suite = "amx.refinement.\(UUID())"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let model = WorkspaceChangesModel(command: { nil }, defaults: defaults)
        model.diffFontSize = 16; model.wrapDiffLines = true
        let restored = WorkspaceChangesModel(command: { nil }, defaults: defaults)
        XCTAssertEqual(restored.diffFontSize, 16); XCTAssertTrue(restored.wrapDiffLines)
        let original = "@@ -1,200 +1,200 @@\n" + (0..<200).map { " context \($0) " + String(repeating: "long ", count: 15) }.joined(separator: "\n")
        let shifted = "@@ -1,220 +1,220 @@\n" + (0..<20).map { "+inserted \($0)\n" }.joined() + original.components(separatedBy: "\n").dropFirst().joined(separator: "\n")
        func view(_ patch: String, wrap: Bool = false) -> WorkspaceDiffView {
            WorkspaceDiffView(diff: WorkspaceDiff(patch), identity: "file", loading: false, jump: nil, jumpSequence: 0, fontSize: 16, wrapLines: wrap)
        }
        let host = NSHostingView(rootView: view(original))
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 500, height: 350), styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false; window.contentView = host; window.orderFront(nil)
        defer { window.close() }
        func settle() async throws { window.layoutIfNeeded(); try await Task.sleep(for: .milliseconds(100)) }
        try await settle()
        func descendants(_ v: NSView) -> [NSView] { [v] + v.subviews.flatMap(descendants) }
        let scroll = try XCTUnwrap(descendants(host).compactMap { $0 as? NSScrollView }.first)
        let text = try XCTUnwrap(scroll.documentView as? WorkspaceDiffTextView)
        XCTAssertEqual(scroll.documentVisibleRect.minX, 0, accuracy: 1, "the ruler must not scroll away the beginning of a line")
        scroll.contentView.scroll(to: NSPoint(x: 0, y: 650)); scroll.reflectScrolledClipView(scroll.contentView)
        let before = try XCTUnwrap(text.readingAnchor())
        host.rootView = view(shifted); try await settle()
        let after = try XCTUnwrap(text.readingAnchor())
        XCTAssertEqual(after.line.text, before.line.text)
        XCTAssertEqual(after.offset, before.offset, accuracy: 2)
        host.rootView = view(shifted, wrap: true); try await settle()
        XCTAssertFalse(scroll.hasHorizontalScroller)
        XCTAssertTrue(text.textContainer?.widthTracksTextView == true)
        XCTAssertEqual(text.readingAnchor()?.line.text, before.line.text)
        XCTAssertEqual((text.textStorage?.attribute(.font, at: 0, effectiveRange: nil) as? NSFont)?.pointSize, 16)
    }

    func testMinimumChangesWindowReservesAtLeast240PointsForDiff() async throws {
        let amx = command { args in
            if args.first == "worktree" { return ["worktrees": []] }
            return ["root": "/repo", "base": "abc", "files": [["path": "a.swift", "added": 100, "deleted": 0, "binary": false, "untracked": false]],
                    "patch": "@@ -0,0 +1,100 @@\n" + (0..<100).map { "+source \($0)\n" }.joined(), "truncated": false]
        }
        let runner = ReviewRunner()
        let model = WorkspaceChangesModel(command: { amx }, runner: runner)
        model.open(directory: "/repo"); while !runner.pending.isEmpty { runner.finish() }
        let style: NSWindow.StyleMask = [.titled, .resizable]
        let rect = NSWindow.contentRect(forFrameRect: NSRect(x: 0, y: 0, width: 720, height: 450), styleMask: style)
        let window = NSWindow(contentRect: rect, styleMask: style, backing: .buffered, defer: false)
        let host = NSHostingView(rootView: WorkspaceChangesView(model: model))
        window.isReleasedWhenClosed = false; window.contentView = host; window.orderFront(nil)
        defer { window.close() }
        try await Task.sleep(for: .milliseconds(200))
        func descendants(_ v: NSView) -> [NSView] { [v] + v.subviews.flatMap(descendants) }
        let scroll = try XCTUnwrap(descendants(host).compactMap { $0 as? NSScrollView }.first { $0.documentView is WorkspaceDiffTextView })
        XCTAssertEqual(window.frame.size, NSSize(width: 720, height: 450))
        XCTAssertGreaterThanOrEqual(scroll.contentSize.height, 240)
        XCTAssertEqual(scroll.documentVisibleRect.minX, 0, accuracy: 1)
    }
}

final class HistoryReadCancellationTests: XCTestCase {
    func testCancellationStopsRunningReadPromptlyAndPreventsQueuedLaunch() {
        let cancelled = ProcessCancellation(); cancelled.cancel()
        XCTAssertEqual(ProcessOutput.execute("/bin/sh", arguments: ["-c", "exit 99"], timeout: 10, cancellation: cancelled), .cancelled)
        let active = ProcessCancellation()
        DispatchQueue.global().asyncAfter(deadline: .now() + 0.1) { active.cancel() }
        let start = Date()
        XCTAssertEqual(ProcessOutput.execute("/bin/sh", arguments: ["-c", "sleep 30 & wait"], timeout: 10, cancellation: active), .cancelled)
        XCTAssertLessThan(Date().timeIntervalSince(start), 2)
    }

    func testCancellationNeverAppliesToMutations() {
        let cancelled = ProcessCancellation(); cancelled.cancel()
        var command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        var ran = false
        command.runner = { _, _, _, _ in ran = true; return .finished(.init(status: 0, stdout: Data(), stderr: Data())) }
        _ = command.run(["session", "continue"], mutates: true, cancellation: cancelled)
        XCTAssertTrue(ran)
    }
}

extension WorkspaceReviewRefinementTests {
    func testLargeArchiveTypingAndSelectionLatency() async throws {
        let archive: [[String: Any]] = (0..<10000).map { ["id": "session-\($0)", "harness": "claude", "project": "API", "preview": "Needle phrase in a conversation", "matches": 1] }
        let full = try JSONSerialization.data(withJSONObject: ["results": archive, "total": archive.count, "has_more": false])
        let page = try JSONSerialization.data(withJSONObject: ["results": Array(archive.prefix(50)), "total": archive.count, "has_more": true, "match_terms": ["needle"]])
        func decodeTime(_ data: Data) throws -> Double {
            let start = Date()
            for _ in 0..<5 { _ = try JSONDecoder().decode(WorkspaceHistorySearch.self, from: data) }
            return Date().timeIntervalSince(start) * 1000 / 5
        }
        let fullTime = try decodeTime(full), pageTime = try decodeTime(page)
        var amx = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        amx.runner = { _, args, _, _ in
            let payload: Data
            if args[1] == "show" {
                payload = try! JSONSerialization.data(withJSONObject: ["session": ["id": String(args.last!.split(separator: ":").last!), "harness": "claude"],
                    "messages_total": 1, "messages": [["role": "assistant", "text": "needle result"]]])
            } else { payload = page }
            return .finished(.init(status: 0, stdout: Data("{\"schema_version\":1,\"data\":".utf8) + payload + Data("}".utf8), stderr: Data()))
        }
        let model = WorkspaceHistoryModel(command: { amx })
        let window = WorkspaceHistoryWindow(model: model)
        // Rendering remains part of the measurement; the fixture never invokes an installed binary.
        window.show(project: "API", conversation: nil)
        defer { window.window?.close() }
        let typedAt = Date()
        model.query = "needle"; model.scheduleSearch()
        while model.loading || model.reading || model.entries.isEmpty {
            try await Task.sleep(for: .milliseconds(10))
            if Date().timeIntervalSince(typedAt) > 3 { XCTFail("history did not settle"); return }
        }
        window.window?.layoutIfNeeded()
        let typing = Date().timeIntervalSince(typedAt) * 1000
        XCTAssertEqual(model.entries.count, 50)
        XCTAssertEqual(model.totalResults, 10000)
        let selectedAt = Date()
        model.select("claude:session-40")
        while model.reading {
            try await Task.sleep(for: .milliseconds(10))
            if Date().timeIntervalSince(selectedAt) > 3 { XCTFail("selection did not settle"); return }
        }
        window.window?.layoutIfNeeded()
        let selection = Date().timeIntervalSince(selectedAt) * 1000
        XCTAssertEqual(model.transcript?.session.id, "session-40")
        print(String(format: "HISTORY LATENCY archive=10000 decode-all=%.2fms decode-page=%.2fms typing-with-debounce=%.2fms selection=%.2fms", fullTime, pageTime, typing, selection))
        XCTAssertLessThan(typing, 2000)
        XCTAssertLessThan(selection, 1000)
    }

    func testRestoredHistoryIncludesPreviouslyLoadedSelection() throws {
        let suite = "amx.history.pages.\(UUID())"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        WorkspaceReviewState(defaults: defaults).history = .init(query: "", scope: "All history", automated: false,
            project: "", conversation: nil, selection: "claude:row-85", message: 12, loadedResults: 100)
        let runner = ReviewRunner()
        let amx = command { args in
            if args[1] == "show" { return ["session": ["id": "row-85", "harness": "claude"], "messages_total": 0] }
            let count = Int(args[args.firstIndex(of: "--limit")! + 1])!
            return ["sessions": (0..<count).map { ["id": "row-\($0)", "harness": "claude"] }, "total": 120, "has_more": true]
        }
        let model = WorkspaceHistoryModel(command: { amx }, runner: runner, defaults: defaults)
        model.search(); runner.finish(); runner.finish()
        XCTAssertEqual(model.entries.count, 100)
        XCTAssertEqual(model.selection, "claude:row-85")
    }
}

extension WorkspaceReviewRefinementTests {
    func testFailedRefreshRetainsPreviewAndNewMessageOffer() {
        let runner = ReviewRunner()
        var available = true
        var total = 1
        let amx = command { args in
            if args[1] != "show" { return ["sessions": [["id": "a", "harness": "claude"]]] }
            return ["session": ["id": "a", "harness": "claude"], "messages_total": total, "messages_start": 0,
                    "messages": [["role": "assistant", "text": "retained"]]]
        }
        let model = WorkspaceHistoryModel(command: { available ? amx : nil }, runner: runner)
        model.search(); runner.finish(); runner.finish()
        total = 61
        model.refreshTranscript(); runner.finish()
        XCTAssertEqual(model.newMessages, 60)
        available = false
        model.showNewMessages()
        XCTAssertEqual(model.newMessages, 60)
        XCTAssertEqual(model.transcript?.messages.first?.text.reveal(), "retained")
        XCTAssertNotNil(model.transcriptError)
        XCTAssertFalse(model.reading)
    }
}
