import AppKit
import SwiftUI
import XCTest
@testable import AgentMux

private struct SeamsImmediateRunner: ASLWorkRunner {
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { then(work()) }
}

@MainActor
final class WorkspaceSeamsPolishTests: XCTestCase {
    func testDaemonRestartIsVerifiedAndNeverRepeatedByReadRetry() {
        let model = DaemonSettingsModel(runner: SeamsImmediateRunner())
        var changed = true, failRead = false, restarts = 0, reads = 0
        var amx = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        amx.runner = { _, args, _, _ in
            XCTAssertEqual(Array(args.prefix(2)), ["config", "daemon-status"])
            reads += 1
            if failRead { return .finished(.init(status: 1, stdout: Data(), stderr: Data("unreachable".utf8))) }
            let data: [String: Any] = ["known": true, "changed": changed ? ["Notifications"] : [], "message": changed ? "Notifications settings changed" : "Saved daemon settings are applied."]
            return .finished(.init(status: 0, stdout: try! JSONSerialization.data(withJSONObject: ["schema_version": 1, "data": data]), stderr: Data()))
        }
        model.command = { amx }
        model.restart = { done in restarts += 1; done(true) }
        model.refresh()
        XCTAssertTrue(model.canRestart)
        model.apply()
        XCTAssertEqual(restarts, 1)
        XCTAssertEqual(reads, 2)
        XCTAssertTrue(model.error?.contains("not been verified") == true)
        XCTAssertFalse(model.canRestart)
        failRead = true; model.refresh()
        XCTAssertNotNil(model.error)
        failRead = false; changed = false; model.refresh()
        XCTAssertNil(model.error)
        XCTAssertEqual(model.status?.changed, [])
        XCTAssertEqual(restarts, 1)
        XCTAssertFalse(model.canRestart)
    }

    func testStatusEvidenceIsExplicitAndLegacySessionsStayUnknown() throws {
        let json = #"{"icon_state":"stop","reason":"command_approval","status_evidence":{"kind":"screen","at":100}}"#
        let session = try JSONDecoder().decode(CompanionSession.self, from: Data(json.utf8))
        let fields = WorkspaceInspector.inspection(session).sections.flatMap(\.fields)
        XCTAssertEqual(fields.first { $0.label == "Reason" }?.value, "Needs permission")
        XCTAssertEqual(fields.first { $0.label == "Evidence" }?.value, "Read from the terminal screen")
        XCTAssertNotNil(fields.first { $0.label == "Observed" })
        XCTAssertEqual(WorkspaceWording.evidence(nil), "Nothing has reported it yet")
        XCTAssertEqual(WorkspaceWording.evidence(.init(kind: "future", at: 100)), "From a source this app does not know")
        XCTAssertEqual(WorkspaceWording.evidence(.init(kind: "probe", at: 100)), "Seen by Agent Mux or set by you")
        XCTAssertEqual(WorkspaceWording.evidence(.init(kind: "harness", at: 100)), "Reported by the agent")
    }

    func testTerminalQueryKeepsLiteralTextAndExactConversationScope() throws {
        let query = #"\"race condition\" #{pane_id} 🦊"#
        let data = try JSONSerialization.data(withJSONObject: ["search": ["scope":"visible_screen", "query":query]])
        let look = try JSONDecoder().decode(PromptLook.self, from: data)
        XCTAssertEqual(look.search?.query.reveal(), query)
        XCTAssertFalse(String(describing: look.search).contains(query))
        let args = WorkspaceHistoryModel.arguments(query: look.search!.query.reveal(), project: nil, includeAutomated: true, conversation: "codex:exact")
        XCTAssertEqual(args.last, query)
        XCTAssertTrue(args.contains("codex:exact"))
    }

    func testLargeDiffRenderAndLayoutMeasurements() throws {
        // Real attributed text and TextKit layout, separate from background parsing.
        // Fixed fixture keeps before/after measurements comparable; no live files.
        let patch = "diff --git a/large b/large\n--- a/large\n+++ b/large\n@@ -1,6000 +1,6000 @@\n" +
            (0..<6000).map { "+line \($0) " + String(repeating: "source_text ", count: 24) }.joined(separator: "\n")
        let diff = WorkspaceDiff(patch)
        for wrap in [false, true] {
            let start = ProcessInfo.processInfo.systemUptime
            let rendered = WorkspaceDiffView.render(diff, compactMetadata: true)
            let renderMS = (ProcessInfo.processInfo.systemUptime - start) * 1000
            let text = NSTextView(frame: NSRect(x: 0, y: 0, width: 600, height: 400))
            text.isVerticallyResizable = true
            text.isHorizontallyResizable = !wrap
            text.textContainer?.widthTracksTextView = wrap
            text.textContainer?.containerSize = NSSize(width: wrap ? 600 : CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
            let layoutStart = ProcessInfo.processInfo.systemUptime
            text.textStorage?.setAttributedString(rendered.text)
            text.layoutManager?.ensureLayout(for: text.textContainer!)
            let layoutMS = (ProcessInfo.processInfo.systemUptime - layoutStart) * 1000
            let viewport = NSTextView(frame: NSRect(x: 0, y: 0, width: 600, height: 400))
            viewport.isVerticallyResizable = true
            viewport.isHorizontallyResizable = !wrap
            viewport.textContainer?.widthTracksTextView = wrap
            viewport.textContainer?.containerSize = NSSize(width: wrap ? 600 : CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
            viewport.layoutManager?.allowsNonContiguousLayout = true
            let viewportStart = ProcessInfo.processInfo.systemUptime
            viewport.textStorage?.setAttributedString(rendered.text)
            viewport.layoutManager?.ensureLayout(forBoundingRect: NSRect(x: 0, y: 0, width: 600, height: 400), in: viewport.textContainer!)
            let viewportMS = (ProcessInfo.processInfo.systemUptime - viewportStart) * 1000
            print("DIFF_RENDER_MEASUREMENT bytes=\(patch.utf8.count) wrap=\(wrap) attributed_ms=\(renderMS) full_layout_ms=\(layoutMS) viewport_layout_ms=\(viewportMS)")
            XCTAssertEqual(rendered.lines.count, diff.lines.count)
            XCTAssertTrue(text.string.contains("line 5999"))
        }
    }

    func testLargeDiffRapidSelectionWrapAndFinalHunkStayResponsive() async throws {
        let patch = "@@ -1,1 +1,6000 @@\n" + (0..<6000).map { "+line \($0) " + String(repeating: "source_text ", count: 24) }.joined(separator: "\n") + "\n@@ -9000,1 +9000,1 @@\n-old\n+final 🦊"
        let diff = WorkspaceDiff(patch)
        let host = NSHostingView(rootView: WorkspaceDiffView(diff: diff, identity: "initial", loading: false, jump: nil, jumpSequence: 0))
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 720, height: 450), styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false; window.contentView = host; window.orderFront(nil)
        defer { window.close() }
        var lastTick = ProcessInfo.processInfo.systemUptime, longestGap = 0.0
        let timer = Timer.scheduledTimer(withTimeInterval: 0.01, repeats: true) { _ in
            let now = ProcessInfo.processInfo.systemUptime
            longestGap = max(longestGap, now - lastTick); lastTick = now
        }
        defer { timer.invalidate() }
        for selection in 0..<30 {
            host.rootView = WorkspaceDiffView(diff: diff, identity: "file-\(selection)", loading: false, jump: nil, jumpSequence: 0, wrapLines: selection % 2 == 0)
            try await Task.sleep(for: .milliseconds(20))
        }
        host.rootView = WorkspaceDiffView(diff: diff, identity: "file-29", loading: false, jump: 1, jumpSequence: 1)
        try await Task.sleep(for: .milliseconds(150))
        func find(_ view: NSView) -> WorkspaceDiffTextView? {
            if let text = view as? WorkspaceDiffTextView { return text }
            return view.subviews.lazy.compactMap(find).first
        }
        let text = try XCTUnwrap(find(host))
        let finalLine = try XCTUnwrap(text.lineRect(diff.lines.count - 1))
        XCTAssertTrue(text.enclosingScrollView!.documentVisibleRect.intersects(finalLine))
        XCTAssertTrue(text.string.hasSuffix("+final 🦊"))
        print("DIFF_INTERACTION_MEASUREMENT selections=30 max_main_queue_gap_ms=\(longestGap * 1000)")
        XCTAssertLessThan(longestGap, 1, "A selection must not block input for a second")
    }

    func testLargeDiffKeepsReadingAnchorAcrossWrappingAndReopening() async throws {
        let suite = "amx.seams.\(UUID())"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let diff = WorkspaceDiff("@@ -1,6000 +1,6000 @@\n" + (0..<6000).map {
            " context \($0) " + String(repeating: "long source ", count: 24)
        }.joined(separator: "\n"))
        func view(wrap: Bool) -> WorkspaceDiffView {
            WorkspaceDiffView(diff: diff, identity: "large-file", loading: false, jump: nil, jumpSequence: 0,
                              reviewState: WorkspaceReviewState(defaults: defaults), wrapLines: wrap)
        }
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 720, height: 450), styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        defer { window.close() }
        func find(_ view: NSView) -> WorkspaceDiffTextView? {
            if let text = view as? WorkspaceDiffTextView { return text }
            return view.subviews.lazy.compactMap(find).first
        }
        let host = NSHostingView(rootView: view(wrap: false))
        window.contentView = host; window.orderFront(nil)
        try await Task.sleep(for: .milliseconds(100))
        let text = try XCTUnwrap(find(host))
        let scroll = try XCTUnwrap(text.enclosingScrollView)
        let middle = try XCTUnwrap(text.lineRect(3000))
        scroll.contentView.scroll(to: NSPoint(x: 0, y: middle.minY))
        scroll.reflectScrolledClipView(scroll.contentView)
        try await Task.sleep(for: .milliseconds(100))
        let before = try XCTUnwrap(text.readingAnchor())
        host.rootView = view(wrap: true)
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertEqual(text.readingAnchor()?.line.text, before.line.text)
        let saved = scroll.contentView.bounds.origin.y
        let reopened = NSHostingView(rootView: view(wrap: true))
        window.contentView = reopened
        try await Task.sleep(for: .milliseconds(100))
        let restored = try XCTUnwrap(find(reopened))
        XCTAssertEqual(restored.enclosingScrollView!.contentView.bounds.origin.y, saved, accuracy: 1)
        XCTAssertEqual(restored.readingAnchor()?.line.text, before.line.text)
    }
}
