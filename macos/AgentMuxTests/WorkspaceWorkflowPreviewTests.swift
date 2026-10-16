import AppKit
import SwiftUI
import ScreenCaptureKit
import Vision
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

    private func render<V: View>(_ view: V, name: String, width: CGFloat = 1000, height: CGFloat = 680,
                                 dark: Bool = false, expected: [String] = []) async throws {
        guard #available(macOS 14.4, *) else { throw XCTSkip("Own-window capture requires macOS 14.4") }
        let host = NSHostingView(rootView: view)
        let style: NSWindow.StyleMask = [.titled, .resizable]
        // Production minSize is the whole window, including its title bar.
        let contentRect = NSWindow.contentRect(forFrameRect: NSRect(x: 0, y: 0, width: width, height: height), styleMask: style)
        let window = NSWindow(contentRect: contentRect, styleMask: style, backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.contentView = host
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        host.wantsLayer = true
        window.orderFront(nil)
        defer { window.close() }
        window.layoutIfNeeded()
        host.layoutSubtreeIfNeeded()
        try await Task.sleep(for: .milliseconds(200))
        XCTAssertEqual(window.frame.width, width, accuracy: 1, "Content must fit the requested window width")
        XCTAssertEqual(window.frame.height, height, accuracy: 1, "Content must fit the requested window height")
        // Capture this process's fixture window directly. A separate
        // screencapture process needs permissions that this own-window API does not.
        let content = try await SCShareableContent.currentProcess
        let target = try XCTUnwrap(content.windows.first { $0.windowID == CGWindowID(window.windowNumber)
            && $0.owningApplication?.processID == ProcessInfo.processInfo.processIdentifier })
        let filter = SCContentFilter(desktopIndependentWindow: target)
        let configuration = SCStreamConfiguration()
        configuration.width = Int(filter.contentRect.width * CGFloat(filter.pointPixelScale))
        configuration.height = Int(filter.contentRect.height * CGFloat(filter.pointPixelScale))
        configuration.ignoreShadowsSingleWindow = true
        configuration.showsCursor = false
        let capture = try await SCScreenshotManager.captureImage(contentFilter: filter, configuration: configuration)
        let rep = NSBitmapImageRep(cgImage: capture)
        let png = try XCTUnwrap(rep.representation(using: .png, properties: [:]))
        let directory = URL(fileURLWithPath: "/tmp/agentmux-polish-previews", isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try png.write(to: directory.appendingPathComponent(name + ".png"))
        let recognition = VNRecognizeTextRequest()
        recognition.recognitionLevel = .accurate
        try VNImageRequestHandler(cgImage: capture).perform([recognition])
        let visible = (recognition.results ?? []).compactMap { $0.topCandidates(1).first?.string }.joined(separator: "\n")
        XCTAssertGreaterThan(visible.count, 40, "Capture must contain rendered content, not just a window frame")
        for text in expected { XCTAssertTrue(visible.localizedCaseInsensitiveContains(text), "Missing visible \(text) in \(name): \(visible)") }
        let attachment = XCTAttachment(data: png, uniformTypeIdentifier: "public.png")
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        XCTAssertEqual(CGFloat(rep.pixelsWide), width * CGFloat(filter.pointPixelScale), accuracy: 1)
        XCTAssertEqual(CGFloat(rep.pixelsHigh), height * CGFloat(filter.pointPixelScale), accuracy: 1)
    }

    func testHistoryPreview() async throws {
        let amx = command { args in
            if args[1] == "show" {
                return #"{"session":{"id":"one","harness":"claude","project":"API","cwd":"/Projects/api"},"messages_total":2,"messages_start":0,"match_messages":[0,1],"match_terms":["pagination"],"focus_message":0,"messages":[{"role":"user","text":"Add pagination to the activity endpoint."},{"role":"assistant","text":"Added cursor pagination and updated the endpoint tests. The full suite passed. The changes are ready for review."}]}"#
            }
            return #"{"results":[{"id":"one","harness":"claude","project":"API","profile":"work","cwd":"/Projects/api","preview":"Added cursor pagination and updated the endpoint tests.","matches":3},{"id":"two","harness":"codex","project":"Desktop","profile":"personal","preview":"Reviewed pagination for the conversation history view.","matches":1}]}"#
        }
        let model = WorkspaceHistoryModel(command: { amx }, runner: PreviewRunner())
        model.query = "pagination"
        model.liveConversations = ["claude:one"]
        model.search()
        XCTAssertEqual(model.entries.count, 2)
        XCTAssertEqual(model.transcript?.messages.count, 2)
        try await render(WorkspaceHistoryView(model: model), name: "history", expected: ["pagination", "Focus live session"])
        try await render(WorkspaceHistoryView(model: model), name: "history-dark-minimum", width: 720, height: 450, dark: true, expected: ["pagination", "Fork"])
    }

    func testChangesPreview() async throws {
        let amx = command { args in
            if args[0] == "worktree" { return #"{"worktrees":[]}"# }
            return #"{"root":"/Projects/api","base":"1234567890abcdef","files":[{"path":"src/pagination.go","added":18,"deleted":3,"binary":false,"untracked":false},{"path":"src/pagination_test.go","added":41,"deleted":0,"binary":false,"untracked":false}],"patch":"diff --git a/src/pagination.go b/src/pagination.go\n--- a/src/pagination.go\n+++ b/src/pagination.go\n@@ -12,3 +12,5 @@\n-func List() []Event {\n+func List(cursor string, limit int) Page {\n+    start := resolveCursor(cursor)\n+    return readPage(start, limit)\n }","truncated":false}"#
        }
        let model = WorkspaceChangesModel(command: { amx }, runner: PreviewRunner())
        model.open(directory: "/Projects/api")
        XCTAssertEqual(model.changes?.files.count, 2)
        XCTAssertFalse(model.patch.isEmpty)
        try await render(WorkspaceChangesView(model: model), name: "changes", expected: ["Checkout Changes", "cursor", "Next Hunk"])
        try await render(WorkspaceChangesView(model: model), name: "changes-dark-minimum", width: 720, height: 450, dark: true, expected: ["Checkout Changes", "Next Hunk"])
    }
}

private final class HeldPreviewRunner: ASLWorkRunner {
    var hold = false
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { if !hold { then(work()) } }
}

private final class PreviewASL: ASLService {
    var result = ASLResult(status: ASLStatus(available: true, mutationsEnabled: true), snapshot: ASLSnapshot(rows: []), meta: ASLSnapshotMeta())
    var failed = false
    func snapshot() -> Result<ASLResult, ASLRequestError> { failed ? .failure(.notRunning) : .success(result) }
    func question(planDir: String, questionID: String) -> Result<ASLQuestionBody, ASLRequestError> { .failure(.unsupported) }
    func act(_ params: ASLRequestParams) -> Result<ASLOperation, ASLRequestError> { XCTFail("Preview must not mutate a run"); return .failure(.unsupported) }
}

extension WorkspaceWorkflowPreviewTests {
    func testWaitingAndPaletteInBothAppearances() async throws {
        for dark in [false, true] {
            let inspector = WorkspaceInspectorView()
            let question = WorkspaceWaitingRow(key: .embedded("question"),
                title: "Platform services · pagination controller review", detail: "Question · 4m", glyph: "?", toolTip: "",
                answer: .prompt(runtimeSessionID: "question"), category: .question, contextVersion: "one")
            let completed = WorkspaceWaitingRow(key: .embedded("completed"),
                title: "Desktop · conversation history", detail: "Turn finished · 2m", glyph: "✓", toolTip: "", category: .completed)
            inspector.show(.init(waiting: [completed, question]))
            inspector.onContext = { _, done in done(SecretText("Should the endpoint keep the existing cursor format?")) }
            inspector.toggleContext(question)
            var changed = question
            changed.contextVersion = "two"
            inspector.show(.init(waiting: [completed, changed]))

            let palette = WorkspaceSwitcherView()
            palette.show([
                .init(place: .command(.splitChanges), glyph: "◫", title: "Split with Checkout Changes", detail: "Review beside the current session"),
                .init(place: .command(.changes), glyph: "±", title: "Checkout Changes", detail: "Compare the selected checkout", unavailableReason: "Select a session with a checkout first"),
            ])
            palette.enter("> cc")
            palette.isHidden = false
            let view = HStack(alignment: .top, spacing: 16) {
                PreviewNativeView(view: inspector).frame(width: 300)
                VStack(alignment: .leading, spacing: 12) {
                    Text("Command palette").font(.title2)
                    PreviewNativeView(view: palette).frame(height: 140)
                    Text("Abbreviations highlight matching characters; unavailable actions explain what is missing.")
                        .font(.callout).foregroundStyle(.secondary)
                    Spacer()
                }
            }.padding(12)
            try await render(view, name: "waiting-palette-\(dark ? "dark" : "light")", width: 850, height: 560, dark: dark,
                             expected: ["Needs a decision", "Completed turns", "Update list", "Changed", "Split with Checkout Changes", "Select a session"])
        }
    }

    func testMinimumWindowStatesInBothAppearances() async throws {
        for state in ["populated", "empty", "loading", "failed"] {
            for dark in [false, true] {
                let suffix = "\(state)-\(dark ? "dark" : "light")"
                let runner = HeldPreviewRunner()
                var available = true
                let amx = command { args in
                    if args[0] == "worktree" { return #"{"worktrees":[]}"# }
                    if args[1] == "show" {
                        return #"{"session":{"id":"one","harness":"claude"},"messages_total":1,"messages":[{"role":"assistant","text":"Pagination is complete. The endpoint tests passed.","timestamp":"2026-10-15T12:00:00Z"}]}"#
                    }
                    if args[1] == "changes" {
                        return state == "empty"
                            ? #"{"root":"/Projects/api","base":"1234567890abcdef","files":[],"truncated":false}"#
                            : #"{"root":"/Projects/platform-services-monorepo/packages/api","base":"1234567890abcdef","files":[{"path":"packages/api/src/activity/pagination_controller_test.go","added":41,"deleted":0,"binary":false,"untracked":false}],"patch":"@@ -10 +10,2 @@\n-old\n+pagination(cursor)\n+return page","truncated":false}"#
                    }
                    if args[1] == "activity" {
                        let events = state == "empty" ? "[]" : #"[{"id":"event-one","at":"2026-10-15T12:01:00Z","kind":"completed","title":"Platform services monorepo · activity endpoint pagination and history","detail":"Working → Turn finished","event":"Stop","harness":"claude","profile":"production-primary","session_id":"one","runtime_session_id":"runtime","run_id":"","trace_id":"trace"}]"#
                        return """
                        {"entries":\(events),"since":"2026-10-08T00:00:00Z","earliest_available":"2026-10-01T00:00:00Z","truncated":false,"corrupt_lines":0,"matched":1}
                        """
                    }
                    if args[0] == "plans" {
                        return #"{"plan_dir":"/plans/platform-services-monorepo/activity-pagination","plan_title":"Pagination","counts":{"complete":2,"pending":1},"sessions":[{"id":"3","status":"pending","path":"/plans/session-3.md"}],"runner_state":{"run_id":"run-one","paused":true,"reason":"verification-failed","run_started_at":"2026-10-15T12:00:00Z","run_finished_at":""},"verification":[],"verification_truncated":false}"#
                    }
                    return state == "empty" ? #"{"sessions":[]}"# : #"{"sessions":[{"id":"one","harness":"claude","title":"Platform services monorepo · activity endpoint pagination and history","project":"Platform services monorepo · activity endpoint pagination and history","profile":"production-primary","cwd":"/Projects/platform-services-monorepo/packages/api","snippet":"Added pagination. Tests pass."}]}"#
                }
                let getCommand: () -> AMXCommand? = { available ? amx : nil }
                let history = WorkspaceHistoryModel(command: getCommand, runner: runner)
                let changes = WorkspaceChangesModel(command: getCommand, runner: runner)
                let activity = WorkspaceActivityModel(command: getCommand, runner: runner, readStore: .init(load: { .init() }, save: { _ in }))
                let service = PreviewASL()
                if state != "empty" {
                    var row = ASLRow()
                    row.rowID = "one"; row.runID = "run-one"
                    row.planDir = "/plans/platform-services-monorepo/activity-pagination"
                    row.primaryState = "paused"; row.pauseReason = "verification-failed"
                    row.actions = [ASLMethod.runResume, ASLMethod.runAbort, ASLMethod.runStopAfterCurrent]
                    row.actionToken = "fixture-token"
                    service.result.snapshot = ASLSnapshot(rows: [row])
                }
                let runs = WorkspaceRunsModel(service: service, command: getCommand, runner: runner)
                let plan = WorkspaceRunPlanModel(command: getCommand, runner: runner)
                history.search(); changes.open(directory: "/Projects/api"); activity.refresh(); runs.refresh()
                if state == "loading" {
                    runner.hold = true
                    history.search(); changes.refresh(); activity.refresh(); runs.refresh()
                } else if state == "failed" {
                    available = false; service.failed = true
                    history.search(); changes.refresh(); activity.refresh(); runs.refresh()
                }
                let notice = state == "loading" || state == "failed" ? ["Showing the"] : []
                // Native disabled button text is deliberately faint; use retained
                // content for OCR in those states and assert authorization directly.
                if !notice.isEmpty { XCTAssertFalse(history.canAct) }
                let historyContent = state == "empty" ? ["No conversations found"]
                    : state == "populated" ? ["Resume", "Fork"] : ["Pagination is complete"]
                try await render(WorkspaceHistoryView(model: history), name: "history-\(suffix)", width: 720, height: 450, dark: dark,
                                 expected: ["Conversation History", "Search"] + notice + historyContent)
                try await render(WorkspaceChangesView(model: changes), name: "changes-\(suffix)", width: 720, height: 450, dark: dark,
                                 expected: ["Checkout Changes", "Compare"] + notice + (state == "empty" ? ["No changes"] : ["Next Hunk"]))
                try await render(WorkspaceActivityView(model: activity), name: "activity-\(suffix)", width: 760, height: 480, dark: dark,
                                 expected: ["Since Last Looked", "Mark shown read"] + notice + (state == "empty" ? ["All caught up"] : ["Supporting output"]))
                try await render(WorkspaceRunsView(model: runs, plan: plan), name: "runs-\(suffix)", width: 780, height: 500, dark: dark,
                                 expected: ["ASL Runs", "Run a Plan"] + notice + (state == "empty" ? ["No active runs"] : state == "failed" ? ["Focus Session"] : ["Resume", "Abort"]))
            }
        }
    }
}

private struct PreviewNativeView<T: NSView>: NSViewRepresentable {
    let view: T
    func makeNSView(context: Context) -> T { view }
    func updateNSView(_ nsView: T, context: Context) {}
}
