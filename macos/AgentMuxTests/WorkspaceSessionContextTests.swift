import AppKit
import ScreenCaptureKit
import XCTest
@testable import AgentMux

private struct ContextRecapRunner: ASLWorkRunner {
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { then(work()) }
}

@MainActor
final class WorkspaceSessionContextTests: XCTestCase {
    private typealias F = WorkspaceFixture
    private var app: FakeWorkspaceApp!
    private var workspace: WorkspaceWindowController!

    override func setUp() async throws {
        app = FakeWorkspaceApp()
        workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        app.store.onChange = { [weak self] in self?.workspace.refresh() }
        workspace.openForTesting()
    }

    override func tearDown() async throws {
        workspace.window.close()
        workspace = nil
        app = nil
    }

    private func session(_ n: Int = 1, icon: String = "key") -> CompanionSession {
        var session = F.tmux(n, project: "api-service", icon: icon, attention: icon == "key", conversation: "conversation-\(n)")
        session.name = n == 1 ? "Fix OAuth callback" : "Review test coverage"
        session.branch = "fix/oauth-callback"
        session.reason = icon == "key" ? "command_approval" : ""
        return session
    }

    func testHeaderKeepsTerminalSizeAndFocusAcrossStateAndFeedChanges() throws {
        var live = session()
        app.store.apply(snapshot: F.snapshot([live]))
        workspace.select(.embedded(F.runtimeID(1)), focus: true)
        workspace.window.contentView?.layoutSubtreeIfNeeded()
        let terminal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))
        let bounds = terminal.bounds
        let strip = workspace.paneLayout.active.sessionContext
        XCTAssertEqual(strip.context?.title, "Fix OAuth callback")
        XCTAssertEqual(strip.context?.location, "api-service / fix/oauth-callback")
        XCTAssertEqual(strip.status.stringValue, "Needs permission")
        XCTAssertFalse(strip.question.isHidden)
        strip.question.performClick(nil)
        XCTAssertEqual(app.answers, [.prompt(runtimeSessionID: F.runtimeID(1))])
        XCTAssertTrue(workspace.window.firstResponder === terminal)

        live.iconState = "bolt"; live.reason = ""; live.attention = false
        app.store.apply(snapshot: F.snapshot([live]))
        workspace.window.contentView?.layoutSubtreeIfNeeded()
        XCTAssertEqual(terminal.bounds, bounds)
        XCTAssertTrue(strip.question.isHidden)
        strip.onQuestion?() // A previously queued click must recheck live state.
        XCTAssertEqual(app.answers.count, 1)
        XCTAssertTrue(workspace.window.firstResponder === terminal)

        app.store.apply(.disconnected(.heartbeatLost, retryIn: 0.5))
        XCTAssertEqual(strip.status.stringValue, "Last known: Working · reconnecting")
        XCTAssertTrue(strip.question.isHidden)
        workspace.window.contentView?.layoutSubtreeIfNeeded()
        XCTAssertEqual(terminal.bounds, bounds)
    }

    func testSplitContextsFollowTheirSessionAndUtilityHidesContext() {
        app.store.apply(snapshot: F.snapshot([session(), session(2, icon: "stop")]))
        workspace.select(.embedded(F.runtimeID(1)), focus: true)
        workspace.split(with: .embedded(F.runtimeID(2)))
        XCTAssertEqual(workspace.paneLayout.active.sessionContext.context?.key, .embedded(F.runtimeID(2)))
        XCTAssertEqual(workspace.paneLayout.other.sessionContext.context?.key, .embedded(F.runtimeID(1)))
        workspace.paneLayout.other.sessionContext.question.performClick(nil)
        XCTAssertEqual(app.answers, [.prompt(runtimeSessionID: F.runtimeID(1))])
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
        workspace.focusOtherPane()
        XCTAssertEqual(workspace.paneLayout.active.sessionContext.context?.key, .embedded(F.runtimeID(1)))
        workspace.collapseOtherPane()
        XCTAssertNil(workspace.paneLayout.other.sessionContext.context)
        workspace.showView(WorkspaceUtilityView.dashboard.id)
        XCTAssertNil(workspace.paneLayout.active.sessionContext.context)
    }

    func testDetachedContextDoesNotClaimAgentStoppedOrReattachIt() {
        app.store.apply(snapshot: F.snapshot([session()]))
        workspace.select(.embedded(F.runtimeID(1)))
        workspace.closeShown()
        XCTAssertEqual(workspace.paneLayout.active.sessionContext.status.stringValue, "Detached · agent keeps running")
        XCTAssertEqual(app.made, [F.runtimeID(1)])
    }

    func testOutcomeReadsOnlySavedDataAndSwitchingDiscardsIt() {
        var calls: [[String]] = []
        var command = AMXCommand(binary: URL(fileURLWithPath: "/fixture/amx"),
                                 launchEnvironment: LaunchEnvironment(home: "/fixture", path: "/bin"))
        command.runner = { _, args, _, _ in
            calls.append(args)
            return .finished(.init(status: 0, stdout: Data(#"{"schema_version":1,"data":{"conversation":"claude:conversation-1","status":"missing"}}"#.utf8), stderr: Data()))
        }
        let model = WorkspaceRecapModel(command: { command }, runner: ContextRecapRunner(), automaticallyGenerate: false)
        model.schedule = { _, _ in XCTFail("Reading a missing saved recap must not schedule generation") }
        let strip = WorkspaceSessionContextView(recap: model)
        strip.frame = NSRect(x: 0, y: 0, width: 440, height: 24)
        let window = NSWindow(contentRect: strip.frame, styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.contentView = strip
        defer { window.close() }
        let live = session()
        let context = WorkspaceSessionContext(key: .embedded(F.runtimeID(1)), row: WorkspaceSidebar.row(live, now: Date(), glyphs: .fallback),
                                              session: live, content: .terminal(runtimeSessionID: F.runtimeID(1)), current: true)
        strip.show(context, canAnswer: true)
        XCTAssertTrue(calls.isEmpty, "navigation alone starts no recap work")
        window.makeKeyAndOrderFront(nil)
        strip.layoutSubtreeIfNeeded()
        strip.showOutcome()
        XCTAssertEqual(calls, [["session", "recap", "--cached", "--json", "--", "claude:conversation-1"]])
        XCTAssertEqual(model.report?.status, "missing")
        XCTAssertFalse(model.canGenerate)
        strip.show(nil, canAnswer: false)
        XCTAssertNil(model.report)
        XCTAssertNil(model.conversation)
    }

    func testWarmWorkspaceResponsivenessAndPreviews() async throws {
        // Synthetic terminals isolate native navigation/layout cost from CLI
        // startup and Ghostty rendering. All 30 views are warmed before timing.
        app.factory = { _ in ContextPreviewTerminal() }
        let fleet = (1...30).map { n -> CompanionSession in
            var value = session(n, icon: n % 5 == 0 ? "key" : n % 3 == 0 ? "stop" : "bolt")
            value.project = n % 2 == 0 ? "website" : "api-service"
            return value
        }
        app.store.apply(snapshot: F.snapshot(fleet))
        for n in 1...30 { workspace.select(.embedded(F.runtimeID(n)), focus: true) }
        var switches: [Double] = []
        var palette: [Double] = []
        for n in 0..<100 {
            let start = ProcessInfo.processInfo.systemUptime
            workspace.select(.embedded(F.runtimeID(n % 30 + 1)), focus: true)
            workspace.window.contentView?.layoutSubtreeIfNeeded()
            switches.append((ProcessInfo.processInfo.systemUptime - start) * 1000)
            let opened = ProcessInfo.processInfo.systemUptime
            workspace.toggleSwitcher(actions: true)
            workspace.window.contentView?.layoutSubtreeIfNeeded()
            palette.append((ProcessInfo.processInfo.systemUptime - opened) * 1000)
            workspace.closeSwitcher(restoring: true)
        }
        XCTAssertEqual(app.made.count, 30)
        let directory = URL(fileURLWithPath: "/tmp/agentmux-context-previews", isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        func summary(_ samples: [Double]) -> [String: Double] {
            let sorted = samples.sorted()
            return ["median_ms": sorted[sorted.count / 2], "p95_ms": sorted[Int(Double(sorted.count - 1) * 0.95)], "max_ms": sorted.last!]
        }
        let results = ["warm_session_switch": summary(switches), "command_palette": summary(palette)]
        try JSONSerialization.data(withJSONObject: results, options: [.prettyPrinted, .sortedKeys])
            .write(to: directory.appendingPathComponent("responsiveness.json"))
        print("Workspace responsiveness (synthetic terminals, 30 sessions, 100 samples): \(results)")

        var saved = AMXCommand(binary: URL(fileURLWithPath: "/fixture/amx"), launchEnvironment: LaunchEnvironment(home: "/fixture", path: "/bin"))
        saved.runner = { _, args, _, _ in
            let data: [String: Any] = ["schema_version": 1, "data": ["conversation": args.last!, "status": "ready", "recap": [
                "goal": ["text": "Fix the OAuth callback", "messages": [0]],
                "latest": ["text": "Added callback validation and regression coverage", "messages": [4]],
                "outstanding": ["text": "Run the test suite", "messages": [4]],
                "generated_at": "2026-11-06T10:00:00Z", "profile": "work", "model": "fixture", "partial": false]]]
            return .finished(.init(status: 0, stdout: try! JSONSerialization.data(withJSONObject: data), stderr: Data()))
        }
        app.recapCommand = saved
        workspace.welcome.schedule = { _, _ in }
        workspace.welcome.answerSent()

        app.store.apply(snapshot: F.snapshot([session(), session(2, icon: "stop"), session(3, icon: "bolt"), session(4, icon: "")]))
        workspace.select(.embedded(F.runtimeID(1)), focus: true)
        guard #available(macOS 14.4, *) else { return }
        for (name, appearance, width) in [("light", NSAppearance.Name.aqua, CGFloat(1000)), ("dark", .darkAqua, 1000), ("narrow", .aqua, 640)] {
            workspace.window.appearance = NSAppearance(named: appearance)
            workspace.window.setContentSize(NSSize(width: width, height: 800))
            workspace.window.makeKeyAndOrderFront(nil)
            workspace.window.contentView?.layoutSubtreeIfNeeded()
            workspace.window.displayIfNeeded()
            // State-change feedback is transient; capture the settled surface.
            try await Task.sleep(for: .seconds(2))
            let content = try await SCShareableContent.currentProcess
            let target = try XCTUnwrap(content.windows.first { $0.windowID == CGWindowID(workspace.window.windowNumber) })
            let filter = SCContentFilter(desktopIndependentWindow: target)
            let configuration = SCStreamConfiguration()
            configuration.width = Int(filter.contentRect.width * CGFloat(filter.pointPixelScale))
            configuration.height = Int(filter.contentRect.height * CGFloat(filter.pointPixelScale))
            configuration.showsCursor = false
            let capture = try await SCScreenshotManager.captureImage(contentFilter: filter, configuration: configuration)
            let png = try XCTUnwrap(NSBitmapImageRep(cgImage: capture).representation(using: .png, properties: [:]))
            try png.write(to: directory.appendingPathComponent(name + ".png"))
            let attachment = XCTAttachment(data: png, uniformTypeIdentifier: "public.png")
            attachment.name = "workspace-context-" + name
            attachment.lifetime = .keepAlways
            add(attachment)
        }
    }
}

/// Draw the synthetic transcript directly so appearance changes exercise the
/// native chrome without depending on layer-backed label redraw timing.
private final class ContextPreviewTerminal: NSView {
    override var acceptsFirstResponder: Bool { true }
    override var isFlipped: Bool { true }
    override func draw(_ dirtyRect: NSRect) {
        NSColor(srgbRed: 0.10, green: 0.12, blue: 0.15, alpha: 1).setFill()
        bounds.fill()
        let text = "Scripted preview · no live agents\n\n› Fix the OAuth callback\n\n✓ Updated callback validation\n✓ Added regression coverage\n\nPermission requested: run the test suite"
        (text as NSString).draw(in: bounds.insetBy(dx: 20, dy: 24), withAttributes: [
            .font: NSFont.monospacedSystemFont(ofSize: 13, weight: .regular),
            .foregroundColor: NSColor.white,
        ])
    }
}
