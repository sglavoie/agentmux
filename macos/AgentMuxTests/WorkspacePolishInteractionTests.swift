import AppKit
import SwiftUI
import XCTest
@testable import AgentMux

private final class PolishRunner: ASLWorkRunner {
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { pending.append { then(work()) } }
    func finish(_ index: Int = 0) { pending.remove(at: index)() }
}

@MainActor
final class WorkspacePolishInteractionTests: XCTestCase {
    func testDiffScrollIsRestoredPerFileAndHunkNavigationRevealsItsTarget() async throws {
        let patch = WorkspaceDiff((0..<20).map { hunk in
            "@@ -\(hunk * 10 + 1),10 +\(hunk * 10 + 1),10 @@\n" + (0..<10).map { " context \(hunk):\($0)" }.joined(separator: "\n")
        }.joined(separator: "\n"))
        func view(_ file: String, jump: Int? = nil, sequence: Int = 0) -> WorkspaceDiffView {
            WorkspaceDiffView(diff: patch, identity: file, loading: false, jump: jump, jumpSequence: sequence)
        }
        let host = NSHostingView(rootView: view("a"))
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 650, height: 400), styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false; window.contentView = host; window.orderFront(nil)
        defer { window.close() }
        func settle() async throws { window.layoutIfNeeded(); try await Task.sleep(for: .milliseconds(50)) }
        func descendants(_ v: NSView) -> [NSView] { [v] + v.subviews.flatMap(descendants) }
        try await settle()
        let scroll = try XCTUnwrap(descendants(host).compactMap { $0 as? NSScrollView }.first)
        scroll.contentView.scroll(to: NSPoint(x: 0, y: 500)); scroll.reflectScrolledClipView(scroll.contentView)
        let saved = scroll.contentView.bounds.origin.y
        XCTAssertGreaterThan(saved, 300)
        host.rootView = view("b"); try await settle()
        XCTAssertEqual(scroll.contentView.bounds.origin.y, 0, accuracy: 1)
        scroll.contentView.scroll(to: NSPoint(x: 0, y: 100)); scroll.reflectScrolledClipView(scroll.contentView)
        host.rootView = view("a"); try await settle()
        XCTAssertEqual(scroll.contentView.bounds.origin.y, saved, accuracy: 1)
        host.rootView = view("a", jump: 18, sequence: 1); try await settle()
        let text = try XCTUnwrap(scroll.documentView as? NSTextView)
        let layout = try XCTUnwrap(text.layoutManager)
        let container = try XCTUnwrap(text.textContainer)
        let range = WorkspaceDiffView.render(patch).hunks[18]
        let glyphs = layout.glyphRange(forCharacterRange: range, actualCharacterRange: nil)
        var rect = layout.boundingRect(forGlyphRange: glyphs, in: container)
        rect.origin.y += text.textContainerInset.height
        XCTAssertTrue(scroll.documentVisibleRect.intersects(rect), "next/previous hunk must reveal the selected hunk")
    }

    func testHistoryOpensOldMatchesAndPagesWithoutLosingTheReadingAnchor() throws {
        let runner = PolishRunner()
        var reads = 0
        var command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        command.runner = { _, args, _, _ in
            let data: [String: Any]
            if args[1] == "search" {
                data = ["results": [["id": "one", "harness": "claude", "matches": 3]]]
            } else {
                reads += 1
                XCTAssertTrue(args.contains("--find"))
                let matches = [5, 180, 220]
                let match = Int(args[args.firstIndex(of: "--match")! + 1])! - 1
                let from = args.firstIndex(of: "--from").map { Int(args[$0+1])! } ?? max(0, matches[match]-25)
                let count = args.firstIndex(of: "--count").map { Int(args[$0+1])! } ?? 50
                data = ["session": ["id": "one", "harness": "claude"], "messages_total": 240,
                        "messages_start": from, "match_messages": matches, "match_terms": ["needle"], "focus_message": matches[match],
                        "messages": (from..<min(240,from+count)).map { ["role": "assistant", "text": matches.contains($0) ? "needle \($0)" : "reply \($0)"] }]
            }
            return .finished(ProcessOutput(status: 0, stdout: try! JSONSerialization.data(withJSONObject: ["schema_version": 1, "data": data]), stderr: Data()))
        }
        let model = WorkspaceHistoryModel(command: { command }, runner: runner)
        model.query = "needle"; model.search(); runner.finish(); runner.finish()
        XCTAssertEqual(model.scrollRequest?.message, 5, "the match is outside the latest 50 messages")
        XCTAssertEqual(model.transcript?.start, 0)
        model.moveMatch(1); runner.finish()
        XCTAssertEqual(model.transcript?.start, 155)
        XCTAssertEqual(model.scrollRequest?.message, 180)
        model.loadEarlier(anchor: 175); runner.finish()
        XCTAssertEqual(model.transcript?.start, 55)
        XCTAssertEqual(model.transcript?.end, 205)
        XCTAssertEqual(model.scrollRequest?.message, 175)
        XCTAssertEqual(model.transcript?.messages.count, 150)
        model.loadLater(anchor: 185); runner.finish()
        XCTAssertEqual(model.transcript?.end, 240)
        XCTAssertEqual(model.scrollRequest?.message, 185)
        let previousReads = reads
        model.moveMatch(1)
        XCTAssertEqual(model.scrollRequest?.message, 220, "an already loaded match needs no read")
        XCTAssertEqual(reads, previousReads)
        model.search(); runner.finish()
        XCTAssertEqual(runner.pending.count, 1, "refresh must read fresh content while retaining the preview")
        XCTAssertNotNil(model.transcript)
        runner.finish()
        XCTAssertEqual(model.scrollRequest?.message, 220)
    }

    func testUnifiedDiffNumbersIgnoreMetadataAndNoNewlineMarkers() {
        let diff = WorkspaceDiff("""
        diff --git a/a b/a
        --- a/a
        +++ b/a
        @@ -10,2 +10,3 @@ function
         context
        -old
        +new
        +extra
        \\ No newline at end of file
        @@ -0,0 +20 @@
        +inserted 🦊
        """)
        XCTAssertEqual(diff.hunks, [3, 9])
        XCTAssertEqual(diff.lines[4].old, 10); XCTAssertEqual(diff.lines[4].new, 10)
        XCTAssertEqual(diff.lines[5].old, 11); XCTAssertNil(diff.lines[5].new)
        XCTAssertNil(diff.lines[6].old); XCTAssertEqual(diff.lines[6].new, 11)
        XCTAssertEqual(diff.lines[7].new, 12)
        XCTAssertNil(diff.lines[8].old); XCTAssertNil(diff.lines[8].new)
        XCTAssertEqual(diff.lines[10].new, 20)
        let rendered = WorkspaceDiffView.render(diff)
        XCTAssertEqual(rendered.hunks.count, 2)
        XCTAssertTrue((rendered.text.string as NSString).substring(with: rendered.hunks[1]).contains("@@ -0,0 +20 @@"))
        XCTAssertNotNil(rendered.text.attribute(.backgroundColor, at: rendered.hunks[0].location, effectiveRange: nil))
    }

    func testPaletteAliasesAbbreviationsRankingAndUnicodeRanges() {
        let settings = WorkspaceSwitcher.Entry(place: .command(.settings), glyph: "", title: "Settings…", detail: "")
        var split = WorkspaceSwitcher.Entry(place: .command(.splitChanges), glyph: "", title: "Split with Checkout Changes", detail: "")
        split.relevance = 10
        let unavailable = WorkspaceSwitcher.Entry(place: .command(.changes), glyph: "", title: "Diff", detail: "", unavailableReason: "Select a checkout")
        XCTAssertEqual(WorkspaceSwitcher.matches("> preferences", in: [settings, split]), [settings])
        XCTAssertEqual(WorkspaceSwitcher.matches("> swcc", in: [settings, split]), [split])
        XCTAssertEqual(WorkspaceSwitcher.matches("> diff", in: [split, unavailable]), [unavailable, split], "an exact unavailable name still wins and explains itself")
        XCTAssertEqual(WorkspaceSwitcher.matches(">", in: [settings, unavailable, split]), [split, settings, unavailable])
        let text = "🦊 Révision"
        let ranges = WorkspaceSwitcher.highlightRanges("rv", in: text)
        XCTAssertEqual(ranges.map { (text as NSString).substring(with: $0) }, ["R", "v"])
        XCTAssertEqual(WorkspaceSwitcher.highlightRanges("revision", in: text), [NSRange(location: 3, length: 8)])
    }

    func testWaitingGroupsPrioritizeDecisionsAndHoldAnOpenContext() throws {
        func row(_ id: String, _ category: WorkspaceWaitingFilter) -> WorkspaceWaitingRow {
            WorkspaceWaitingRow(key: .embedded(id), title: id, detail: "Waiting", glyph: "", toolTip: "",
                                answer: category == .question ? .prompt(runtimeSessionID: id) : nil,
                                category: category, contextVersion: "one")
        }
        let completed = row("done", .completed), question = row("question", .question), newer = row("new", .permission)
        let inspector = WorkspaceInspectorView()
        inspector.show(.init(waiting: [completed, question]))
        XCTAssertEqual(inspector.visibleWaiting.map(\.key), [question.key, completed.key])
        XCTAssertTrue(inspector.lines.flatMap(\.subviews).compactMap { ($0 as? NSTextField)?.stringValue }.contains("Needs a decision (1)"))
        inspector.onContext = { _, done in done(.loaded(SecretText("Read this before answering"))) }
        inspector.toggleContext(question)
        inspector.show(.init(waiting: [newer, completed, question]))
        XCTAssertEqual(inspector.visibleWaiting.map(\.key), [question.key, completed.key])
        XCTAssertTrue(inspector.updatesButton.isEnabled)
        var changed = question; changed.contextVersion = "two"
        inspector.show(.init(waiting: [newer, completed, changed]))
        let held = try XCTUnwrap(inspector.lines.compactMap { $0 as? WorkspaceWaitingRowView }.first { $0.row.key == question.key })
        XCTAssertEqual(held.answerButton?.isEnabled, false, "a held stale prompt is never answerable")
        inspector.applyWaitingUpdates(nil)
        XCTAssertEqual(inspector.visibleWaiting.map(\.key), [newer.key, changed.key, completed.key])
        XCTAssertFalse(inspector.updatesButton.isEnabled)
    }
}
