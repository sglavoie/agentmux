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
    func testWaitingReadStatesAndFormattedQuestionAtNarrowWidth() async throws {
        for dark in [false, true] {
            let inspector = WorkspaceInspectorView()
            let row = WorkspaceWaitingRow(key: .embedded("fixture"), title: "API review", detail: "Waiting for your answer", glyph: "?", toolTip: "",
                answer: .asl(.init(runID: "fixture-run", planDir: "/plans/api")), category: .question)
            var receive: ((WorkspaceWaitingContext.Result) -> Void)?
            inspector.onContext = { _, done in receive = done }
            inspector.show(.init(waiting: [row])); inspector.toggleContext(row)
            try await render(PreviewNativeView(view: inspector), name: "seams-waiting-loading-\(dark)", width: 320, height: 600, dark: dark, expected: ["Loading context", "Answer"])
            receive?(.loaded(SecretText("# Deployment review\n\n- Check staging\n- Keep rollback available\n\n```sh\nmake check\n```")))
            try await render(PreviewNativeView(view: inspector), name: "seams-waiting-loaded-\(dark)", width: 320, height: 600, dark: dark, expected: ["Deployment review", "Copy code", "Read"])
            inspector.refreshContext(row)
            receive?(.failed(SecretText("Connection lost. Try again.")))
            try await render(PreviewNativeView(view: inspector), name: "seams-waiting-failed-\(dark)", width: 320, height: 600, dark: dark, expected: ["Context read failed", "Retry", "Deployment review"])
        }
    }

    func testNativeRecoveryAndRestoredFeedbackPreviews() async throws {
        let amx = command { _ in
            #"{"session":{"id":"recorded-conversation","harness":"codex","profile":"work","project":"API","cwd":"/Projects/api"}}"#
        }
        let model = WorkspaceResumeModel(identity: .init(harness: "codex", sessionID: "recorded-conversation"), command: { amx }, runner: PreviewRunner())
        model.load()
        for dark in [false, true] {
            try await render(WorkspaceResumeView(model: model, cancel: {}), name: "recovery-native-\(dark)", width: 460, height: 279, dark: dark,
                             expected: ["Resume Conversation", "Profile: work", "Workspace", "recorded-conversation"])
        }
        let suite = "amx-feedback-preview-\(UUID())"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let original = WorkspaceFeedbackHistory(defaults: defaults)
        let failure = WorkspaceFeedback(target: "/Projects/api", message: "Landing could not complete: checkout changed.", nextStep: "Review the current checkout before trying again.")
        original.record(failure, destination: .checkout("/Projects/api"))
        original.record(failure, destination: .checkout("/Projects/api"))
        let reopened = WorkspaceFeedbackHistory(defaults: defaults)
        for dark in [false, true] {
            try await render(WorkspaceFeedbackHistoryView(history: reopened), name: "recovery-feedback-\(dark)", width: 420, height: 392, dark: dark,
                             expected: ["Repeated 2 times", "checkout changed", "Dismiss"])
        }
    }

    private func command(_ data: @escaping ([String]) -> String) -> AMXCommand {
        var amx = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        amx.runner = { _, args, _, _ in
            .finished(ProcessOutput(status: 0, stdout: Data("{\"schema_version\":1,\"data\":\(data(args))}".utf8), stderr: Data()))
        }
        return amx
    }

    /// Text as the captures are compared: without whitespace, in lower case,
    /// and with the glyphs text recognition confuses in caption-size text
    /// (i, l, I, 1, |) read as one. On a 1x display Vision reads a correctly
    /// drawn "Refresh failed" as "Refresh falled", and "Previous" as
    /// "Prevlous", so an exact match would fail on what the window shows.
    nonisolated static func ocrFolded(_ text: String) -> String {
        String(text.lowercased().compactMap { character -> Character? in
            if character.isWhitespace { return nil }
            return "l1|!".contains(character) ? "i" : character
        })
    }

    private func render<V: View>(_ view: V, name: String, width: CGFloat = 1000, height: CGFloat = 680,
                                 dark: Bool = false, highContrast: Bool = false, expected: [String] = []) async throws {
        guard #available(macOS 14.4, *) else { throw XCTSkip("Own-window capture requires macOS 14.4") }
        let host = NSHostingView(rootView: view)
        let style: NSWindow.StyleMask = [.titled, .resizable]
        // Production minSize is the whole window, including its title bar.
        let contentRect = NSWindow.contentRect(forFrameRect: NSRect(x: 0, y: 0, width: width, height: height), styleMask: style)
        let window = NSWindow(contentRect: contentRect, styleMask: style, backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.contentView = host
        window.appearance = NSAppearance(named: highContrast
            ? (dark ? .accessibilityHighContrastDarkAqua : .accessibilityHighContrastAqua)
            : (dark ? .darkAqua : .aqua))
        host.wantsLayer = true
        window.orderFront(nil)
        defer { window.close() }
        window.layoutIfNeeded()
        host.layoutSubtreeIfNeeded()
        try await Task.sleep(for: .milliseconds(450))
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
        recognition.recognitionLanguages = ["en-US"]
        recognition.minimumTextHeight = 0.005
        try VNImageRequestHandler(cgImage: capture).perform([recognition])
        let visible = (recognition.results ?? []).compactMap { $0.topCandidates(1).first?.string }.joined(separator: "\n")
        XCTAssertGreaterThan(visible.count, 40, "Capture must contain rendered content, not just a window frame")
        let read = Self.ocrFolded(visible)
        for text in expected { XCTAssertTrue(read.contains(Self.ocrFolded(text)), "Missing visible \(text) in \(name): \(visible)") }
        let attachment = XCTAttachment(data: png, uniformTypeIdentifier: "public.png")
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        XCTAssertEqual(CGFloat(rep.pixelsWide), width * CGFloat(filter.pointPixelScale), accuracy: 1)
        XCTAssertEqual(CGFloat(rep.pixelsHigh), height * CGFloat(filter.pointPixelScale), accuracy: 1)
    }

    func testConsistencyAtLargeTextAndIncreasedContrast() async throws {
        let suite = "amx-contrast-\(UUID())"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let preferences = WorkspaceReadingPreferences(defaults: defaults)
        preferences.proseSize = 24; preferences.fontSize = 20
        for dark in [false, true] {
            let inspector = WorkspaceInspectorView(preferences: preferences)
            let row = WorkspaceWaitingRow(key: .embedded("fixture"), title: "API — deployment review", detail: "Waiting for your answer", glyph: "?", toolTip: "",
                answer: .asl(.init(runID: "fixture-run", planDir: "/plans/api")), category: .question)
            inspector.onContext = { _, receive in receive(.loaded(SecretText("Which deployment should proceed?\n\nReview the staging results before choosing production."))) }
            inspector.show(.init(waiting: [row])); inspector.toggleContext(row)
            try await render(PreviewNativeView(view: inspector), name: "consistency-waiting-contrast-\(dark)", width: 320, height: 600,
                dark: dark, highContrast: true, expected: ["Which deployment", "Next Waiting", "Oldest first", "Answer"])
            let transcript = WorkspaceTranscriptText(text: "# Deployment review\n\nInspect the failed check before resuming.\n\n```swift\nlet café = \"🦊\"\n```", terms: [], preferences: preferences)
            let read = WorkspaceReadObservation(title: "Transcript", refreshedAt: Date().addingTimeInterval(-125))
            try await render(VStack(alignment: .leading, spacing: 16) {
                WorkspaceReviewStatus(reads: [read])
                transcript
            }.padding().frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading),
                name: "consistency-reading-contrast-\(dark)", width: 720, height: 450, dark: dark, highContrast: true,
                expected: ["Updated 2m ago", "Deployment review", "Copy code"])
        }
    }

    func testHistoryPreview() async throws {
        let amx = command { args in
            if args[1] == "show" {
                return #"{"session":{"id":"one","harness":"claude","project":"API","cwd":"/Projects/api"},"messages_total":2,"messages_start":0,"match_messages":[0,1],"match_terms":["pagination"],"focus_message":0,"messages":[{"role":"user","text":"Add pagination to the activity endpoint."},{"role":"assistant","text":"Added cursor pagination and updated the endpoint tests. The full suite passed. The changes are ready for review."}]}"#
            }
            return #"{"match_terms":["pagination"],"results":[{"id":"one","harness":"claude","project":"API","profile":"work","cwd":"/Projects/api","last_at":"2026-10-16T12:00:00Z","preview":"Added cursor pagination and updated the endpoint tests.","matches":3},{"id":"two","harness":"codex","project":"Desktop","profile":"personal","preview":"Reviewed pagination for the conversation history view.","matches":1}]}"#
        }
        let model = WorkspaceHistoryModel(command: { amx }, runner: PreviewRunner())
        model.query = "pagination"
        model.liveConversations = ["claude:one"]
        model.search()
        XCTAssertEqual(model.entries.count, 2)
        XCTAssertEqual(model.transcript?.messages.count, 2)
        try await render(WorkspaceHistoryView(model: model), name: "history", expected: ["pagination", "Focus Live Session"])
        try await render(WorkspaceHistoryView(model: model), name: "history-dark-minimum", width: 720, height: 450, dark: true, expected: ["pagination", "Fork"])
    }

    func testDailyPolishReadingAndEmptyWorkspaceCaptures() async throws {
        let preferences = WorkspaceReadingPreferences()
        preferences.proseSize = 22
        let amx = command { args in
            if args[1] == "activity" {
                return #"{"entries":[{"id":"one","at":"2026-10-17T12:00:00Z","kind":"completed","title":"API review complete","detail":"Working → Turn finished","event":"Stop","harness":"claude","profile":"work","session_id":"one","runtime_session_id":"one","run_id":"","trace_id":""}],"since":"2026-10-10T00:00:00Z","earliest_available":"2026-10-10T00:00:00Z","truncated":false,"corrupt_lines":0,"matched":1}"#
            }
            if args[1] == "show" {
                return #"{"session":{"id":"one","harness":"claude","project":"API","cwd":"/Projects/api"},"messages_total":2,"messages_start":0,"messages":[{"role":"user","text":"Review the existing workflow."},{"role":"assistant","timestamp":"2026-10-17T11:59:00Z","text":"Keep reading positions stable. Make each action clear and easy to reach.\n\n- Preserve selection.\n- Keep the answer controls visible."}]}"#
            }
            return #"{"sessions":[{"id":"one","harness":"claude","project":"API","profile":"work","cwd":"/Projects/api","snippet":"Review the existing workflow."}]}"#
        }
        let history = WorkspaceHistoryModel(command: { amx }, runner: PreviewRunner())
        history.search()
        let activity = WorkspaceActivityModel(command: { amx }, runner: PreviewRunner(), readStore: .init(load: { .init() }, save: { _ in }))
        activity.refresh()
        for dark in [false, true] {
            try await render(WorkspaceHistoryView(preferences: preferences, model: history), name: "daily-history-large-\(dark)",
                             width: 720, height: 450, dark: dark, expected: ["Resume", "Fork", "Review the existing workflow"])
            try await render(WorkspaceActivityView(preferences: preferences, model: activity), name: "daily-activity-large-\(dark)",
                             width: 760, height: 480, dark: dark, expected: ["Mark Read", "Undo", "Supporting output"])
            let inspector = WorkspaceInspectorView(preferences: preferences)
            let row = WorkspaceWaitingRow(key: .embedded("one"), title: "API · deployment review", detail: "Question · 2m", glyph: "?", toolTip: "",
                answer: .asl(.init(runID: "run-one", planDir: "/plans/api")), category: .question)
            inspector.onContext = { _, done in done(.loaded(SecretText("Which deployment should proceed?\n\nThe endpoint checks passed. Review the staging results before choosing production.\n\n" + String(repeating: "Keep the current deployment available until verification completes.\n\n", count: 10)))) }
            inspector.show(.init(waiting: [row])); inspector.focusReader()
            try await render(PreviewNativeView(view: inspector), name: "daily-waiting-large-\(dark)", width: 320, height: 600, dark: dark,
                             expected: ["Answer", "Which deployment", "Next Waiting"])
            let app = FakeWorkspaceApp()
            app.store.apply(snapshot: WorkspaceFixture.snapshot([]))
            let workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
            workspace.openForTesting(); workspace.refresh()
            try await render(PreviewNativeView(view: workspace.placeholder), name: "daily-empty-workspace-\(dark)", width: 720, height: 450,
                             dark: dark, expected: ["Launch Project", "Conversation History", "Launch in Workspace"])
            workspace.window.close()
        }
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
        try await render(WorkspaceChangesView(model: model), name: "changes", expected: ["Checkout Changes", "cursor", "Compare"])
        try await render(WorkspaceChangesView(model: model), name: "changes-dark-minimum", width: 720, height: 450, dark: true, expected: ["Checkout Changes", "Compare"])
    }

    func testLaunchSettingsRouteShowsTheLaunchPreference() async throws {
        let suite = "agentmux-launch-preview-\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let model = PreferencesModel(defaults: defaults)
        var backend = BackendStatus()
        backend.launchBackend = "kitty"
        backend.launchBackendSource = "compatibility default"
        model.snapshot.backend = .status(backend)
        let view = PreferencesView(model: model, dashboardRecorder: HotKeyRecorder(),
                                   attentionRecorder: HotKeyRecorder(), workspaceRecorder: HotKeyRecorder())
            .task {
                try? await Task.sleep(for: .milliseconds(50))
                model.launchSettingsRequest += 1
            }
        try await render(view, name: "launch-settings", width: 560, height: 450,
                         expected: ["Launch projects in", "Workspace", "CLI launches", "compatibility default", "Follow CLI"])
    }

    func testTranscriptTablesQuotesAndNestedListsAtMinimumSize() async throws {
        let text = "| Check | Result |\n| --- | --- |\n| Routing | **Passed** |\n| Resume | Preserved |\n\n> Review note\n> - Parent item\n>   - Nested detail\n\n```sh\nprintf '%s' 'a deliberately long line that should also be readable with code wrapping enabled'\n```"
        for dark in [false, true] {
            try await render(ScrollView { WorkspaceTranscriptText(text: text, terms: ["Routing"]).padding(16) },
                name: "transcript-structure-\(dark)", width: 720, height: 450, dark: dark,
                expected: ["Routing", "Passed", "Review note", "Nested detail", "Copy code"])
        }
    }

    func testRichTranscriptAtMinimumSize() async throws {
        let prose = "# Pagination review\nUse **bounded pages** and `next_cursor`.\n\n- Preserve the existing ordering.\n- Return a cursor only when more results exist.\n\n```go\nfunc next_cursor(page Page) string {\n    return page.Cursor\n}\n```\n\n## Verification\n1. Empty results remain empty.\n2. A second page never repeats a record.\n\n" + String(repeating: "Long transcripts remain selectable and scrollable.\n\n", count: 40)
        for dark in [false, true] {
            try await render(ScrollView { WorkspaceTranscriptText(text: prose, terms: ["next_cursor"]).padding(16) },
                             name: "transcript-rich-\(dark)", width: 720, height: 450, dark: dark,
                             expected: ["Pagination review", "next_cursor", "Copy code", "return page.Cursor"])
        }
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
            inspector.onContext = { _, done in done(.loaded(SecretText("Should the endpoint keep the existing cursor format?"))) }
            inspector.toggleContext(question)
            try await render(PreviewNativeView(view: inspector).padding(12), name: "waiting-action-\(dark ? "dark" : "light")",
                             width: 340, height: 560, dark: dark,
                             expected: ["Needs a decision", "Answer", "Should the endpoint"])
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
                             expected: ["Needs a decision", "Completed turns", "Update List", "Changed", "Split with Checkout Changes", "Select a session"])
        }
    }

    func testMinimumWindowStatesInBothAppearances() async throws {
        for state in ["populated", "empty", "loading", "failed", "initial-failure"] {
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
                if state == "initial-failure" { available = false; service.failed = true }
                history.search(); changes.open(directory: "/Projects/api"); activity.refresh(); runs.refresh()
                if state == "loading" {
                    runner.hold = true
                    history.search(); changes.refresh(); activity.refresh(); runs.refresh()
                } else if state == "failed" {
                    available = false; service.failed = true
                    history.search(); changes.refresh(); activity.refresh(); runs.refresh()
                }
                let failed = state == "failed" || state == "initial-failure"
                let notice = failed ? ["Refresh failed", "Retry"] : state == "loading" ? ["Refreshing"] : []
                // Native disabled button text is deliberately faint; use retained
                // content for OCR in those states and assert authorization directly.
                if !notice.isEmpty { XCTAssertFalse(history.canAct) }
                let historyContent = state == "initial-failure" ? ["Search results could not be loaded"] : state == "empty" ? ["No conversations found"]
                    : state == "populated" ? ["Resume", "Fork"] : ["Pagination is complete"]
                try await render(WorkspaceHistoryView(model: history), name: "history-\(suffix)", width: 720, height: 450, dark: dark,
                                 expected: ["Search"] + notice + historyContent)
                try await render(WorkspaceChangesView(model: changes), name: "changes-\(suffix)", width: 720, height: 450, dark: dark,
                                 expected: ["Checkout Changes", "Compare"] + notice + (state == "initial-failure" ? ["Comparison could not be loaded"] : state == "empty" ? ["No changes"] : ["pagination_controller_test.go"]))
                try await render(WorkspaceActivityView(model: activity), name: "activity-\(suffix)", width: 760, height: 480, dark: dark,
                                 expected: ["Since Last Looked", "Mark Read"] + notice + (state == "initial-failure" ? ["Activity could not be loaded"] : state == "empty" ? ["All caught up"] : ["Supporting output"]))
                try await render(WorkspaceRunsView(model: runs, plan: plan), name: "runs-\(suffix)", width: 780, height: 500, dark: dark,
                                 expected: ["ASL Runs", "Run a Plan"] + notice + (state == "initial-failure" ? ["Runs could not be loaded"] : state == "empty" ? ["No active runs"] : state == "failed" ? ["Inspect Failed Check"] : ["Resume", "Abort"]))
            }
        }
    }
}

private struct PreviewNativeView<T: NSView>: NSViewRepresentable {
    let view: T
    func makeNSView(context: Context) -> T { view }
    func updateNSView(_ nsView: T, context: Context) {}
}

extension WorkspaceWorkflowPreviewTests {
    func testPlanChoicesPreview() async throws {
        let amx = command { args in
            if args.first == "project" { return #"[{"title":"API","profile":"work","directory":"/Projects/api"}]"# }
            return #"[{"name":"work","harness":"claude"},{"name":"review-only","harness":"codex"}]"#
        }
        let model = WorkspaceRunPlanModel(command: { amx }, runner: PreviewRunner())
        model.plan = "/Plans/Improve session review"
        for dark in [false, true] {
            try await render(WorkspaceRunPlanView(model: model), name: "plan-choices-\(dark)", width: 720, height: 640, dark: dark,
                             expected: ["Project", "Supervisor", "Infer from plan workspace", "Use project default", "Launch in workspace", "Check Readiness"])
        }
    }
}

extension WorkspaceWorkflowPreviewTests {
    func testChangedWordsAndCompactHeadersInBothAppearances() async throws {
        let amx = command { args in
            if args.first == "worktree" { return #"{"worktrees":[]}"# }
            return #"{"root":"/Projects/api","base":"HEAD","files":[{"path":"pagination.swift","added":2,"deleted":2,"binary":false,"untracked":false}],"patch":"diff --git a/pagination.swift b/pagination.swift\nindex 123..456 100644\n--- a/pagination.swift\n+++ b/pagination.swift\n@@ -1 +1 @@\n-let page = readEvents(limit: 20, archived: true)\n+let page = readEvents(limit: 50, archived: false)\n@@ -12 +12 @@\n-return page.previousCursor\n+return page.nextCursor\n","truncated":false}"#
        }
        let changes = WorkspaceChangesModel(command: { amx }, runner: PreviewRunner())
        changes.open(directory: "/Projects/api")
        changes.wrapDiffLines = true
        for dark in [false, true] {
            try await render(WorkspaceChangesView(model: changes), name: "finishing-diff-\(dark)", width: 720, height: 450, dark: dark,
                             expected: ["Hunk 1 of 2", "readEvents", "previousCursor", "nextCursor"])
            changes.layout.collapsed = true
            try await render(WorkspaceChangesView(model: changes), name: "usability-diff-collapsed-\(dark)", width: 720, height: 450, dark: dark,
                             expected: ["Hunk 1 of 2", "readEvents", "previousCursor", "nextCursor"])
            changes.layout.collapsed = false
        }
    }

    func testRefinedHistoryAndWrappedDiff() async throws {
        var total = 3
        let historyCommand = command { args in
            if args[1] == "show" {
                let messages = (0..<total).map { ["role": "assistant", "text": "Pagination reply \($0)"] }
                let object: [String: Any] = ["session": ["id": "one", "harness": "claude", "project": "API"],
                    "messages_total": total, "messages_start": 0, "messages": messages]
                return String(decoding: try! JSONSerialization.data(withJSONObject: object), as: UTF8.self)
            }
            return #"{"sessions":[{"id":"one","harness":"claude","project":"API","snippet":"Pagination review"}],"total":1}"#
        }
        let history = WorkspaceHistoryModel(command: { historyCommand }, runner: PreviewRunner())
        history.search()
        history.rememberVisibleMessage(1)
        total = 5
        history.refreshTranscript()
        XCTAssertEqual(history.newMessages, 2)
        try await render(WorkspaceHistoryView(model: history), name: "history-new-messages", width: 720, height: 450,
                         expected: ["2 new messages", "Pagination"])
        let emptyCommand = command { _ in #"{"results":[],"total":0,"hidden_automated":7,"match_terms":["needle"]}"# }
        let empty = WorkspaceHistoryModel(command: { emptyCommand }, runner: PreviewRunner())
        empty.query = "needle"; empty.scope = .project; empty.open(project: "API", conversation: nil)
        try await render(WorkspaceHistoryView(model: empty), name: "history-empty-scoped", width: 720, height: 450, dark: true,
                         expected: ["Clear Search", "Search All History", "7 automated matches"])
        let changesCommand = command { args in
            if args.first == "worktree" { return #"{"worktrees":[]}"# }
            return #"{"root":"/Projects/api","base":"abc","files":[{"path":"pagination.swift","added":2,"deleted":0,"binary":false,"untracked":false}],"patch":"@@ -0,0 +1,2 @@\n+let page = readEvents(after: cursor, limit: requestedLimit, includeArchived: false)\n+return page","truncated":false}"#
        }
        let changes = WorkspaceChangesModel(command: { changesCommand }, runner: PreviewRunner())
        changes.open(directory: "/Projects/api")
        changes.diffFontSize = 16; changes.wrapDiffLines = true
        try await render(WorkspaceChangesView(model: changes), name: "changes-wrapped-large", width: 720, height: 450, dark: true,
                         expected: ["requestedLimit", "includeArchived", "return page"])
    }
}


extension WorkspaceWorkflowPreviewTests {
    func testClarityHistoryRunsAndFeedbackCaptures() async throws {
        let amx = command { args in
            if args[0] == "plans" {
                return #"{"plan_dir":"/plans/api","plan_title":"API deployment review","counts":{"complete":8,"pending":2},"sessions":[{"id":"9","status":"pending","path":"/plans/api/session-9.md"}],"runner_state":{"run_id":"run-one","paused":true,"reason":"verification-failed","run_started_at":"2026-10-17T12:00:00Z","run_finished_at":""},"verification":[{"event":"verification_check_finished","at":"2026-10-17T12:04:59Z","session_id":"9","name":"Unit tests","status":"failed","returncode":1},{"event":"verification_finished","at":"2026-10-17T12:05:00Z","session_id":"9","passed":false,"check_count":3,"passed_count":2}],"verification_truncated":false}"#
            }
            if args[1] == "show" {
                return #"{"session":{"id":"one","harness":"claude","project":"API deployment review","cwd":"/Projects/api"},"messages_total":2,"messages_start":0,"messages":[{"role":"user","text":"Review the existing workflow."},{"role":"assistant","text":"Keep reading positions stable. Make each action clear and easy to reach.\n\n- Preserve selection.\n- Keep the answer controls visible."}]}"#
            }
            return #"{"sessions":[{"id":"one","harness":"claude","project":"API deployment review","profile":"production-primary","cwd":"/Projects/api","snippet":"Review the existing workflow."}]}"#
        }
        let history = WorkspaceHistoryModel(command: { amx }, runner: PreviewRunner())
        let preferences = WorkspaceReadingPreferences(); preferences.proseSize = 24
        history.search()
        let service = PreviewASL()
        var row = ASLRow()
        row.rowID = "one"; row.runID = "run-one"; row.planDir = "/plans/api"
        row.primaryState = "paused"; row.pauseReason = "verification-failed"; row.sessionID = "9"
        row.actions = [ASLMethod.runResume, ASLMethod.runAbort, ASLMethod.runStopAfterCurrent]
        row.actionToken = "fixture-only"
        service.result.snapshot = ASLSnapshot(rows: [row])
        let runs = WorkspaceRunsModel(service: service, command: { amx }, runner: PreviewRunner())
        let plan = WorkspaceRunPlanModel(command: { amx }, runner: PreviewRunner())
        runs.refresh()
        let feedback = WorkspaceFeedbackHistory()
        feedback.record(.init(target: "API · claude:deployment-review", message: "Resume outcome unconfirmed.", nextStep: "Inspect the original conversation before trying again."))
        feedback.record(.init(target: "/Projects/platform-services/worktrees/deployment-review", message: "Landing was refused because the checkout changed.", nextStep: "Refresh the original checkout and review its changes."))
        for dark in [false, true] {
            try await render(WorkspaceFeedbackHistoryView(history: feedback), name: "clarity-feedback-\(dark)", width: 420, height: 392, dark: dark,
                             expected: ["Action feedback", "Landing was refused", "Refresh", "Dismiss"])
            history.layout.collapsed = false
            try await render(WorkspaceHistoryView(preferences: preferences, model: history), name: "clarity-history-\(dark)", width: 720, height: 450, dark: dark,
                             expected: ["Resume", "Fork", "Keep reading positions stable"])
            history.layout.collapsed = true
            try await render(WorkspaceHistoryView(preferences: preferences, model: history), name: "clarity-history-collapsed-\(dark)", width: 720, height: 450, dark: dark,
                             expected: ["Resume", "Fork", "Keep reading positions stable"])
            try await render(WorkspaceRunsView(model: runs, plan: plan), name: "clarity-runs-\(dark)", width: 780, height: 500, dark: dark,
                             expected: ["8 of 10 sessions complete", "Unit tests: failed", "Verification failed", "Inspect Failed Check", "Incomplete", "Resume", "Abort", "Run details", "Recorded verification"])
            runs.inspectFailedCheck()
            runs.layout.collapsed = true
            try await render(WorkspaceRunsView(model: runs, plan: plan), name: "usability-inspected-check-\(dark)", width: 780, height: 500, dark: dark,
                             expected: ["Recorded outcomes only", "Unit tests: failed", "Session 9", "Open Session"])
            runs.layout.collapsed = false
            runs.select(runs.selection)
            try await render(WorkspaceStatus(loading: true, retained: true, refreshedAt: Date().addingTimeInterval(-125),
                                            feedback: WorkspaceFeedback(target: "API deployment review", message: "Read failed. Previous content is still available."),
                                            recover: {}).padding(16).frame(maxHeight: .infinity, alignment: .top), name: "clarity-stale-\(dark)", width: 720, height: 160, dark: dark,
                             expected: ["Previous results", "2m", "Refresh"])
        }
    }
}

extension WorkspaceWorkflowPreviewTests {
    func testCodeBlocksKeepCopyBesideCodeAndPreferencesInReaderToolbar() async throws {
        let preferences = WorkspaceReadingPreferences()
        preferences.wrapLines = true
        let text = "## Validate a page\n```swift\nlet page = readEvents(limit: 50)\n```\nThen check the response.\n```sh\ncurl -s localhost:8080/events\n```\nRecord the result.\n```json\n{\"count\": 50, \"next_cursor\": \"next-page\"}\n```"
        for dark in [false, true] {
            try await render(VStack(alignment: .leading, spacing: 8) {
                HStack { Text("Conversation History").font(.headline); Spacer(); WorkspaceReadingMenu(preferences: preferences) }
                ScrollView { WorkspaceTranscriptText(text: text, terms: [], preferences: preferences).padding(8) }
            }.padding(12), name: "usability-code-\(dark)", width: 720, height: 450, dark: dark,
                expected: ["Validate a page", "Copy code", "readEvents", "Record the result", "next_cursor"])
        }
    }
}


extension WorkspaceWorkflowPreviewTests {
    func testEventLocationAndIndependentReadDetails() async throws {
        let amx = command { args in
            if args[1] == "show" {
                return #"{"session":{"id":"one","harness":"claude","project":"API event review"},"messages_total":400,"messages_start":10,"focus_message":12,"messages":[{"role":"user","text":"Earlier question"},{"role":"assistant","text":"Earlier response"},{"role":"assistant","text":"At the selected event: deployment checks failed. Inspect the unit tests before resuming.","timestamp":"2026-10-17T12:00:00Z"},{"role":"user","text":"Later discussion"}]}"#
            }
            return #"{"sessions":[{"id":"one","harness":"claude","project":"API event review","cwd":"/Projects/api"}]}"#
        }
        let history = WorkspaceHistoryModel(command: { amx }, runner: PreviewRunner())
        let reads: [WorkspaceReadObservation] = [
            .init(title: "Search", retained: true, refreshedAt: Date(timeIntervalSince1970: 1792238400),
                  feedback: .init(target: "Search", message: "Archive read failed.", nextStep: "Check the source and refresh."), retry: {}),
            .init(title: "Transcript", refreshedAt: Date(timeIntervalSince1970: 1792238460), retry: {})
        ]
        for dark in [false, true] {
            history.open(project: "API", conversation: ConversationIdentity(encoded: "claude:one"), eventTime: "2026-10-17T12:00:00Z")
            try await render(WorkspaceHistoryView(model: history), name: "event-location-\(dark)", width: 720, height: 450, dark: dark,
                             expected: ["At the selected event", "Resume", "Updated"])
            try await render(WorkspaceReadDetails(reads: reads), name: "read-details-\(dark)", width: 380, height: 332, dark: dark,
                             expected: ["Search", "Transcript", "Previous results", "Archive read failed", "Refresh transcript"])
        }
    }
}
