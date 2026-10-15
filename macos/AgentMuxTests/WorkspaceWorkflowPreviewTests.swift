import AppKit
import SwiftUI
import XCTest
@testable import AgentMux

private struct PreviewRunner: ASLWorkRunner {
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { then(work()) }
}

@MainActor
final class WorkspaceWorkflowPreviewTests: XCTestCase {
    private func command(_ data: @escaping ([String]) -> String) -> AMXCommand {
        var amx = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        amx.runner = { _, args, _, _ in
            .finished(ProcessOutput(status: 0, stdout: Data("{\"schema_version\":1,\"data\":\(data(args))}".utf8), stderr: Data()))
        }
        return amx
    }

    private func render<V: View>(_ view: V, name: String) throws {
        let host = NSHostingView(rootView: view)
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1000, height: 680),
                              styleMask: [.titled, .resizable], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.contentView = host
        window.appearance = NSAppearance(named: .aqua)
        host.wantsLayer = true
        window.orderFront(nil)
        defer { window.close() }
        window.layoutIfNeeded()
        host.layoutSubtreeIfNeeded()
        RunLoop.current.run(until: Date().addingTimeInterval(0.3))
        // SwiftUI's render-server layers are absent from NSView cacheDisplay.
        // Capture only this fixture window, never the desktop or other apps.
        let path = "/tmp/agentmux-\(name)-preview.png"
        let output = ProcessOutput.run("/usr/sbin/screencapture", arguments: ["-x", "-o", "-l", String(window.windowNumber), path], timeout: 5)
        guard output?.status == 0 else { throw XCTSkip("Window capture is unavailable in this environment") }
        let png = try Data(contentsOf: URL(fileURLWithPath: path))
        let rep = try XCTUnwrap(NSBitmapImageRep(data: png))
        let attachment = XCTAttachment(data: png, uniformTypeIdentifier: "public.png")
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        XCTAssertGreaterThan(rep.pixelsWide, 700)
        XCTAssertGreaterThan(rep.pixelsHigh, 450)
    }

    func testHistoryPreview() throws {
        let amx = command { args in
            if args[1] == "show" {
                return #"{"session":{"id":"one","harness":"claude","project":"API","cwd":"/Projects/api"},"messages_total":2,"messages":[{"role":"user","text":"Add pagination to the activity endpoint."},{"role":"assistant","text":"Added cursor pagination and updated the endpoint tests. The full suite passed. The changes are ready for review."}]}"#
            }
            return #"{"results":[{"id":"one","harness":"claude","project":"API","profile":"work","cwd":"/Projects/api","preview":"Added cursor pagination and updated the endpoint tests.","matches":3},{"id":"two","harness":"codex","project":"Desktop","profile":"personal","preview":"Reviewed pagination for the conversation history view.","matches":1}]}"#
        }
        let model = WorkspaceHistoryModel(command: { amx }, runner: PreviewRunner())
        model.query = "pagination"
        model.liveConversations = ["claude:one"]
        model.search()
        XCTAssertEqual(model.entries.count, 2)
        XCTAssertEqual(model.transcript?.messages.count, 2)
        try render(WorkspaceHistoryView(model: model), name: "history")
    }

    func testChangesPreview() throws {
        let amx = command { args in
            if args[0] == "worktree" { return #"{"worktrees":[]}"# }
            return #"{"root":"/Projects/api","base":"1234567890abcdef","files":[{"path":"src/pagination.go","added":18,"deleted":3,"binary":false,"untracked":false},{"path":"src/pagination_test.go","added":41,"deleted":0,"binary":false,"untracked":false}],"patch":"diff --git a/src/pagination.go b/src/pagination.go\n--- a/src/pagination.go\n+++ b/src/pagination.go\n@@ -12,3 +12,5 @@\n-func List() []Event {\n+func List(cursor string, limit int) Page {\n+    start := resolveCursor(cursor)\n+    return readPage(start, limit)\n }","truncated":false}"#
        }
        let model = WorkspaceChangesModel(command: { amx }, runner: PreviewRunner())
        model.open(directory: "/Projects/api")
        XCTAssertEqual(model.changes?.files.count, 2)
        XCTAssertFalse(model.patch.isEmpty)
        try render(WorkspaceChangesView(model: model), name: "changes")
    }
}
