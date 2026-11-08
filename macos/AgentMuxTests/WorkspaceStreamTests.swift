import AppKit
import GhosttyKit
import ScreenCaptureKit
import XCTest
@testable import AgentMux

/// The whole path a snapshot travels — socket, subscription, store, sidebar —
/// with real Ghostty terminals in the pane, across a daemon that drops the
/// stream and comes back.
@MainActor
final class WorkspaceStreamTests: XCTestCase {
    private typealias F = WorkspaceFixture
    private var directory: URL!
    private var server: FakeStreamServer?
    private var client: SubscriptionClient?
    private var ghostty: Ghostty.App!
    private var app: FakeWorkspaceApp!
    private var workspace: WorkspaceWindowController!

    override func setUp() async throws {
        directory = URL(fileURLWithPath: "/tmp/amx-\(UUID().uuidString.prefix(8))")
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        ghostty = Ghostty.App { Ghostty.Config(userConfig: nil, overrides: Ghostty.ConfigOverrides()) }
        XCTAssertEqual(ghostty.readiness, .ready)
        app = FakeWorkspaceApp()
        workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        app.store.onChange = { [weak self] in self?.workspace.refresh() }
    }

    override func tearDown() async throws {
        client?.stop()
        server?.shutdown()
        workspace.window.close()
        ghostty.shutdown()
        try? FileManager.default.removeItem(at: directory)
    }

    /// A terminal whose program records its pid and then waits, as an
    /// attached agent would.
    private func terminal(_ id: String) -> NSView? {
        let out = directory.appendingPathComponent("\(id).pid").path
        let view = ghostty.makeSurface(Ghostty.SurfaceConfiguration(
            program: URL(fileURLWithPath: "/bin/sh"),
            arguments: ["-c", #"echo $$ > "$OUT.tmp" && mv "$OUT.tmp" "$OUT"; exec cat"#],
            environment: ["OUT": out]
        ))
        view?.delegate = workspace
        return view
    }

    private func pid(_ id: String) -> pid_t? {
        let url = directory.appendingPathComponent("\(id).pid")
        ghosttyWait("\(id) to start") { FileManager.default.fileExists(atPath: url.path) }
        return (try? String(contentsOf: url, encoding: .utf8)).flatMap { pid_t($0.trimmingCharacters(in: .whitespacesAndNewlines)) }
    }

    private func alive(_ pid: pid_t) -> Bool { kill(pid, 0) == 0 }

    func testWarmSessionSwitchResponsivenessWithGhostty() throws {
        app.factory = { [unowned self] id in self.terminal(id) }
        workspace.openForTesting()
        app.store.apply(snapshot: F.snapshot((1...4).map { F.tmux($0, project: "fixture", icon: "bolt") }))
        var processes: [pid_t] = []
        for n in 1...4 {
            let id = F.runtimeID(n)
            workspace.select(.embedded(id), focus: true)
            processes.append(try XCTUnwrap(pid(id)))
        }
        var samples: [Double] = []
        for n in 0..<100 {
            let id = F.runtimeID(n % 4 + 1)
            let start = ProcessInfo.processInfo.systemUptime
            workspace.select(.embedded(id), focus: true)
            workspace.window.contentView?.layoutSubtreeIfNeeded()
            samples.append((ProcessInfo.processInfo.systemUptime - start) * 1000)
            XCTAssertTrue(workspace.window.firstResponder === workspace.terminalView(for: id))
        }
        XCTAssertEqual(app.made.count, 4, "switching reuses each live terminal")
        XCTAssertTrue(processes.allSatisfy(alive))
        samples.sort()
        let report = ["median_ms": samples[50], "p95_ms": samples[94], "max_ms": samples[99]]
        let directory = URL(fileURLWithPath: "/tmp/agentmux-context-previews", isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try JSONSerialization.data(withJSONObject: report, options: [.prettyPrinted, .sortedKeys])
            .write(to: directory.appendingPathComponent("ghostty-responsiveness.json"))
        print("Warm Ghostty switch (4 terminals, 100 samples, controller + layout, excluding GPU presentation): \(report)")
    }

    func testSidebarAndTerminalsStayConsistentAcrossAReconnect() throws {
        let sessions = [
            F.tmux(1, project: "api", profile: "work"),
            F.tmux(2, project: "api", profile: "personal", icon: "stop", attention: true, changedAtMS: 2000),
            F.tmux(4, project: "web", icon: "key", attention: true, changedAtMS: 1000),
        ]
        let subscribe = Locked<String?>(nil)
        let drop = DispatchSemaphore(value: 0)
        let comeBack = DispatchSemaphore(value: 0)
        let finish = DispatchSemaphore(value: 0)
        server = try FakeStreamServer(directory: directory, scripts: [
            { conn in
                subscribe.set(conn.readLine())
                conn.write(F.ack)
                conn.write(F.frame(seq: 1, sessions))
                drop.wait()
                conn.close()
            },
            { conn in
                _ = conn.readLine()
                comeBack.wait()
                conn.write(F.ack)
                conn.write(F.frame(seq: 1, sessions))
                finish.wait()
                conn.write(F.frame(seq: 2, [sessions[0], sessions[2]]))
                Thread.sleep(forTimeInterval: 2)
                conn.close()
            },
        ])
        defer { [drop, comeBack, finish].forEach { $0.signal() } }

        app.factory = { [unowned self] id in self.terminal(id) }
        workspace.openForTesting()
        let store = app.store
        let client = SubscriptionClient(
            socketPath: try XCTUnwrap(server).path,
            backoff: ReconnectBackoff(initial: 0.05, maximum: 0.2),
            request: { SubscribeParams(clientVersion: "test") },
            onEvent: { event in DispatchQueue.main.async { MainActor.assumeIsolated { store.apply(event) } } }
        )
        self.client = client
        client.start()

        ghosttyWait("the first snapshot") { self.workspace.state.rows.count == 3 }
        XCTAssertTrue(try XCTUnwrap(subscribe.get()).contains(#""features":["runtime_sessions","workspace_views"]"#))
        XCTAssertTrue(workspace.state.current)

        let (work, personal) = (F.runtimeID(1), F.runtimeID(2))
        workspace.activate(try XCTUnwrap(workspace.state.row(.embedded(personal))))
        workspace.activate(try XCTUnwrap(workspace.state.row(.embedded(work))))
        let surface = try XCTUnwrap(workspace.terminalView(for: work) as? Ghostty.SurfaceView)
        let hidden = try XCTUnwrap(workspace.terminalView(for: personal) as? Ghostty.SurfaceView)
        let workPID = try XCTUnwrap(pid(work))
        let personalPID = try XCTUnwrap(pid(personal))
        XCTAssertTrue(workspace.window.firstResponder === surface)
        XCTAssertEqual([surface.isHidden, hidden.isHidden], [false, true])
        let items = workspace.items

        // The daemon drops the stream.
        drop.signal()
        ghosttyWait("the feed to drop") { self.app.store.feed != .live }
        XCTAssertFalse(workspace.state.current)
        XCTAssertEqual(workspace.items, items)
        XCTAssertEqual(workspace.state.selection, .embedded(work))
        XCTAssertFalse(workspace.feedLabel.isHidden)
        XCTAssertTrue(workspace.terminalView(for: work) === surface)
        XCTAssertNotNil(surface.surface, "the terminal is still open")
        XCTAssertTrue(workspace.window.firstResponder === surface, "and still has the keyboard")
        XCTAssertTrue(alive(workPID) && alive(personalPID), "both programs are still running")

        // It comes back and lists the same sessions.
        comeBack.signal()
        ghosttyWait("the feed to return") { self.workspace.state.current }
        XCTAssertEqual(workspace.items, items)
        XCTAssertEqual(workspace.state.selection, .embedded(work))
        XCTAssertTrue(workspace.terminalView(for: work) === surface)
        XCTAssertTrue(workspace.terminalView(for: personal) === hidden)
        XCTAssertTrue(workspace.window.firstResponder === surface)
        XCTAssertEqual(app.made.count, 2, "nothing was attached a second time")
        XCTAssertTrue(alive(workPID) && alive(personalPID))
        XCTAssertTrue(workspace.feedLabel.isHidden)

        // Then one session ends: only its terminal goes.
        finish.signal()
        ghosttyWait("the ended session to leave") { self.workspace.state.rows.count == 2 }
        XCTAssertEqual(app.closed, [personal])
        XCTAssertNil(hidden.surface)
        ghosttyWait("its program to end") { !self.alive(personalPID) }
        XCTAssertTrue(alive(workPID))
        XCTAssertTrue(workspace.window.firstResponder === surface)

        // The program in the shown terminal exits: the view detaches itself.
        kill(workPID, SIGTERM)
        ghosttyWait("the workspace to notice") { self.workspace.content == .detached(runtimeSessionID: work) }
        XCTAssertNil(surface.surface)
        XCTAssertEqual(workspace.state.selection, .embedded(work), "the session is still listed and selected")
    }
}
