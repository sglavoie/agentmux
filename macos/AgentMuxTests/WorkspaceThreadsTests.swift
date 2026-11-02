import AppKit
import SwiftUI
import XCTest
@testable import AgentMux

private final class ThreadsRunner: ASLWorkRunner {
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { pending.append { then(work()) } }
    func release(_ index: Int = 0) { pending.remove(at: index)() }
    func drain() { while !pending.isEmpty { release() } }
}

@MainActor
final class WorkspaceThreadsTests: XCTestCase {
    private let source = ConversationIdentity(harness: "claude", sessionID: "same")
    private let receiving = ConversationIdentity(harness: "codex", sessionID: "same")
    private func link(_ id: String, _ source: ConversationIdentity, _ target: ConversationIdentity?) -> ConversationLink {
        ConversationLink(id: id, source: source, target: target, targetKey: WorkspaceFixture.runtimeID(2), kind: "handoff", artifactPath: "/handoffs/one.md", createdAt: "2026-11-01T12:00:00Z")
    }
    private func jsonLink(_ id: String, _ source: ConversationIdentity, _ target: ConversationIdentity) -> [String: Any] {
        ["id": id, "source": ["harness": source.harness, "session_id": source.sessionID],
         "target": ["harness": target.harness, "session_id": target.sessionID], "target_key": WorkspaceFixture.runtimeID(2),
         "kind": "handoff", "artifact_path": "/handoffs/one.md", "created_at": "2026-11-01T12:00:00Z"]
    }

    func testGroupsThroughMissingIntermediateConversationsAndKeepsHarnessIdentity() {
        let middle = ConversationIdentity(harness: "copilot", sessionID: "middle")
        let links = [link("one", source, middle), link("two", middle, receiving), link("cycle", receiving, source)]
        let items = [RecentConversation(id: "same", harness: "codex"), RecentConversation(id: "same", harness: "claude"),
                     RecentConversation(id: "same", harness: "copilot")]
        let groups = ConversationThreadGroup.group(items, links: links)
        XCTAssertEqual(groups.count, 2)
        XCTAssertEqual(Set(groups[0].conversations.map(\.selector)), Set(["claude:same", "codex:same"]))
        XCTAssertEqual(groups[1].conversations.map(\.selector), ["copilot:same"])
    }

    func testInspectorReadsSharedHistoryAndDiscardsLateRepliesAfterSelectionAndClose() {
        let runner = ThreadsRunner()
        var calls: [[String]] = []
        let command = BriefingFixture.command { args in calls.append(args); return ["thread_links": [self.jsonLink("one", self.source, self.receiving)]] }
        let model = WorkspaceThreadsModel(command: { command }, runner: runner)
        model.select(source)
        model.select(receiving)
        runner.release(1)
        XCTAssertEqual(model.conversation, receiving)
        XCTAssertEqual(model.links.count, 1)
        runner.release()
        XCTAssertEqual(model.conversation, receiving)
        XCTAssertEqual(model.links.first?.source, source)
        XCTAssertEqual(calls, [["session", "threads", "--json"], ["session", "threads", "--json"]])
        model.refresh(); model.close(); runner.release()
        XCTAssertTrue(model.links.isEmpty)
        XCTAssertNil(model.conversation)
    }

    func testReopenedBriefingGroupsClosedCLIHandoffsAndLoadsOlderRecapsFromSharedResponse() {
        let runner = ThreadsRunner()
        let command = BriefingFixture.command { args in
            if args[1] == "recap" { return BriefingFixture.recap(args[args.firstIndex(of: "--")! + 1]) }
            return ["sessions": [["id": "same", "harness": "codex", "project": "API"]], "total": 50,
                    "related_conversations": [["id": "same", "harness": "claude", "project": "API"]],
                    "thread_links": [self.jsonLink("one", self.source, self.receiving)]]
        }
        for _ in 0..<2 {
            let model = WorkspaceBriefingModel(command: { command }, runner: runner)
            model.update(projects: [.init(profile: "work", title: "API")], sessions: [], live: true)
            model.open(preferredProject: "API"); model.refresh(); runner.drain()
            XCTAssertEqual(model.recent.count, 1)
            XCTAssertEqual(model.conversations.count, 2, "the older source is outside the recent result window")
            XCTAssertEqual(model.threads.count, 1)
            XCTAssertEqual(model.recaps.count, 2)
            XCTAssertTrue(model.rows.isEmpty, "both agents have ended")
            XCTAssertEqual(model.threadLinks.first?.source, source)
            model.close()
        }
    }

    func testTranscriptDecodesLinksAndOldRepliesRemainCompatible() throws {
        let json: [String: Any] = ["session": ["id": "same", "harness": "codex"], "messages": [],
                                   "thread_links": [jsonLink("one", source, receiving)], "thread_warning": "History is incomplete"]
        let transcript = try JSONDecoder().decode(WorkspaceTranscript.self, from: JSONSerialization.data(withJSONObject: json))
        XCTAssertEqual(transcript.threadLinks.first?.target, receiving)
        XCTAssertEqual(transcript.threadWarning, "History is incomplete")
        let titled = try JSONDecoder().decode(RecentConversation.self, from: Data(#"{"id":"same","harness":"codex","project":"API","title":"Review pagination"}"#.utf8))
        XCTAssertEqual(titled.conversationTitle, "Review pagination")
        XCTAssertEqual(titled.displayName, "API", "project labels retain their existing meaning")
        let old = try JSONDecoder().decode(WorkspaceTranscript.self, from: Data(#"{"session":{"id":"same","harness":"codex"},"messages":[]}"#.utf8))
        XCTAssertTrue(old.threadLinks.isEmpty)
    }

    func testRelatedConversationInAnotherProjectKeepsItsLiveStatusAndFocus() {
        let runner = ThreadsRunner()
        let command = BriefingFixture.command { args in
            if args[1] == "recap" { return BriefingFixture.recap(args[args.firstIndex(of: "--")! + 1]) }
            return ["sessions": [["id": "same", "harness": "codex", "project": "API"]], "total": 1,
                    "related_conversations": [["id": "same", "harness": "claude", "project": "Other"]],
                    "thread_links": [self.jsonLink("one", self.source, self.receiving)]]
        }
        let model = WorkspaceBriefingModel(command: { command }, runner: runner)
        model.update(projects: [.init(profile: "work", title: "API")],
                     sessions: [BriefingFixture.session("rs-a", harness: "claude", project: "Other", conversation: "same")], live: true)
        model.open(preferredProject: "API"); model.refresh(); runner.drain()
        let older = model.conversations.first { $0.identity == source }!
        XCTAssertEqual(model.liveRows(for: older).count, 1)
        var focused: AttentionCycler.Key?
        model.onFocus = { focused = $0 }
        model.continueConversation(older)
        XCTAssertEqual(focused, .embedded("rs-a"))
        model.close()
    }

    func testLinksOpenExactSourceAndTarget() async throws {
        let next = ConversationIdentity(harness: "copilot", sessionID: "next")
        let links = [link("one", source, receiving), link("two", receiving, next), link("pending", receiving, nil)]
        var opened: [ConversationIdentity] = []
        let host = NSHostingView(rootView: ConversationLinksView(links: links, conversation: receiving, onOpen: { opened.append($0) }))
        let window = NSWindow(contentRect: NSRect(x: 100, y: 100, width: 600, height: 250), styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false; window.contentView = host; window.makeKeyAndOrderFront(nil)
        defer { window.close() }
        try await Task.sleep(for: .milliseconds(200))
        func buttons(_ view: NSView) -> [NSButton] {
            (view as? NSButton).map { [$0] } ?? view.subviews.flatMap(buttons)
        }
        let controls = buttons(host)
        XCTAssertEqual(controls.count, 2, "the pending receiver has no conversation to open")
        for button in controls {
            button.performClick(nil)
            try await Task.sleep(for: .milliseconds(100))
        }
        XCTAssertEqual(opened, [source, next])
    }
}
