import AppKit
import XCTest
@testable import AgentMux

/// The workspace's right sidebar: what it lists as data, and what showing
/// it, turning it, and clicking in it do to the window.
@MainActor
final class WorkspaceInspectorTests: XCTestCase {
    private typealias F = WorkspaceFixture
    private var app: FakeWorkspaceApp!
    private var workspace: WorkspaceWindowController!
    private let now = Date(timeIntervalSince1970: 600)

    private let sessions = [
        F.tmux(1, project: "api", profile: "work"),
        F.tmux(2, project: "api", profile: "personal", icon: "stop", attention: true, changedAtMS: 300_000),
        F.kitty(4, project: "web", icon: "key", attention: true, changedAtMS: 60_000),
    ]

    override func setUp() async throws {
        app = FakeWorkspaceApp()
        workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback, now: { [now] in now })
        app.store.onChange = { [weak self] in self?.workspace.refresh() }
        workspace.openForTesting()
    }

    override func tearDown() async throws {
        workspace.window.close()
        workspace = nil
        app = nil
    }

    // MARK: Data

    func testWaitingListsTheSessionsThatWaitOldestFirst() {
        let waiting = WorkspaceInspector.waiting(sessions, snapshotSocket: "unix:/tmp/kitty-1", now: now, glyphs: .fallback)
        XCTAssertEqual(waiting.map(\.title), ["web — work", "api — personal"], "the one that waited longest comes first")
        XCTAssertEqual(waiting.map(\.detail), ["needs permission · 9m · Kitty", "stopped · 5m"])
        XCTAssertEqual(waiting.map(\.glyph), ["🔑", "🛑"])
        XCTAssertEqual(waiting.map(\.key), [
            AttentionCycler.Key(socket: "unix:/tmp/kitty-1", windowID: 4), .embedded(F.runtimeID(2)),
        ])
    }

    func testAWaitingSessionSaysItsReasonAndGoesByItsName() {
        var session = sessions[1]
        session.reason = "permission"
        session.name = "migrations"
        let waiting = WorkspaceInspector.waiting([session], now: now, glyphs: .fallback)
        XCTAssertEqual(waiting.map(\.title), ["migrations"])
        XCTAssertEqual(waiting.map(\.detail), ["permission · 5m"])
        XCTAssertTrue(waiting[0].toolTip.hasPrefix("api — personal"), "the name does not hide where the session is")
    }

    func testAnInspectionLeavesOutWhatIsNotKnown() {
        let idle = WorkspaceInspector.inspection(sessions[0], now: now, glyphs: .fallback)
        XCTAssertEqual(idle.title, "api — work")
        XCTAssertTrue(idle.embedded)
        XCTAssertEqual(idle.sections.map(\.title), ["Status", "Identity"], "a session with no branch has no Git section")
        XCTAssertEqual(idle.sections[0].fields, [.init(label: "State", value: "idle")])
        XCTAssertEqual(idle.sections[1].fields, [
            .init(label: "Project", value: "api"),
            .init(label: "Profile", value: "work"),
            .init(label: "Harness", value: "claude"),
            .init(label: "Runtime", value: F.runtimeID(1), copies: true),
            .init(label: "Terminal", value: "tmux %1"),
        ])
    }

    func testAnInspectionSaysEverythingTheSnapshotCarries() {
        var session = sessions[1]
        session.iconState = "bolt"
        session.reason = "tool"
        session.busySinceAt = 480
        session.lastEventAt = 590
        session.muted = true
        session.looping = true
        session.branch = "main"
        session.dirty = true
        session.aslRunID = "run-7"
        session.conversation = ConversationIdentity(harness: "claude", sessionID: "c-1")
        let inspection = WorkspaceInspector.inspection(session, now: now, glyphs: .fallback)
        XCTAssertTrue(inspection.muted)
        XCTAssertEqual(inspection.sections[0].fields, [
            .init(label: "State", value: "working"),
            .init(label: "Reason", value: "tool"),
            .init(label: "Since", value: "5m"),
            .init(label: "Working for", value: "2m"),
            .init(label: "Last event", value: "<1m"),
            .init(label: "Flags", value: "waiting on you, muted, looping"),
        ])
        XCTAssertTrue(inspection.sections[1].fields.contains(.init(label: "Conversation", value: "c-1", copies: true)))
        XCTAssertTrue(inspection.sections[1].fields.contains(.init(label: "ASL run", value: "run-7", copies: true)))
        XCTAssertEqual(inspection.sections[2], .init(title: "Git", fields: [
            .init(label: "Branch", value: "main", copies: true),
            .init(label: "Changes", value: "uncommitted"),
        ]))

        let kitty = WorkspaceInspector.inspection(sessions[2], snapshotSocket: "unix:/tmp/kitty-1", now: now, glyphs: .fallback)
        XCTAssertFalse(kitty.embedded)
        XCTAssertTrue(kitty.sections[1].fields.contains(.init(label: "Terminal", value: "window 4")))
    }

    func testTheInspectorAndItsTabAreKeptInDefaults() throws {
        let suite = "amx.tests.inspector.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }

        XCTAssertFalse(WorkspaceRestoration.load(from: defaults).inspectorShown, "hidden until it is asked for")
        XCTAssertEqual(WorkspaceRestoration.load(from: defaults).inspectorTab, .waiting)
        WorkspaceRestoration.saveInspector(shown: true, tab: .session, to: defaults)
        XCTAssertTrue(WorkspaceRestoration.load(from: defaults).inspectorShown)
        XCTAssertEqual(WorkspaceRestoration.load(from: defaults).inspectorTab, .session)
        defaults.set("garbage", forKey: WorkspaceRestoration.inspectorTabKey)
        XCTAssertEqual(WorkspaceRestoration.load(from: defaults).inspectorTab, .waiting)
    }

    // MARK: Window

    /// Hidden, the inspector takes no room; shown, the pane gives up its
    /// width to it and the terminal keeps the keyboard.
    func testTheInspectorIsHiddenUntilAskedForAndTakesNoKeyboard() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.select(.embedded(F.runtimeID(1)), focus: true)
        let terminal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))
        workspace.window.layoutIfNeeded()
        XCTAssertTrue(workspace.isInspectorCollapsed)
        XCTAssertTrue(workspace.inspector.isHiddenOrHasHiddenAncestor)
        let wide = terminal.frame.width

        workspace.inspectorToggle.performClick(nil)
        workspace.window.layoutIfNeeded()
        XCTAssertFalse(workspace.isInspectorCollapsed)
        XCTAssertFalse(workspace.inspector.isHiddenOrHasHiddenAncestor)
        XCTAssertLessThan(terminal.frame.width, wide)
        XCTAssertTrue(workspace.window.firstResponder === terminal, "showing it does not take the keyboard")
        XCTAssertFalse(workspace.isSidebarCollapsed, "the two sidebars are hidden and shown apart")

        workspace.toggleInspector()
        workspace.window.layoutIfNeeded()
        XCTAssertTrue(workspace.isInspectorCollapsed)
        XCTAssertEqual(terminal.frame.width, wide)
        XCTAssertEqual(app.inspectorChanges.map(\.collapsed), [false, true])
        XCTAssertEqual(app.sidebarCollapses, [])
    }

    func testTheWaitingTabGoesToTheSessionThatIsClicked() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.setInspectorCollapsed(false)
        XCTAssertEqual(workspace.inspector.tabs.label(forSegment: 0), "Waiting 2")
        let lines = workspace.inspector.lines.compactMap { $0 as? WorkspaceWaitingRowView }
        XCTAssertEqual(lines.map(\.titleLabel.stringValue), ["🔑 web — work", "🛑 api — personal"])

        lines[1].press()
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(2)))
        XCTAssertTrue(workspace.window.firstResponder === workspace.terminalView(for: F.runtimeID(2)),
                      "as a click on its line in the session list does")

        lines[0].press()
        XCTAssertEqual(app.external.map(\.project), ["web"], "a Kitty session comes forward in Kitty")

        var settled = sessions
        settled[1].attention = false
        settled[2].attention = false
        app.store.apply(snapshot: F.snapshot(settled))
        XCTAssertEqual(workspace.inspector.tabs.label(forSegment: 0), "Waiting")
        XCTAssertEqual((workspace.inspector.lines.first as? NSTextField)?.stringValue, WorkspaceInspectorView.nothingWaiting)
    }

    func testTheSessionTabFollowsThePaneAndItsButtonsAskTheApp() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.setInspectorTab(.session)
        XCTAssertEqual(app.inspectorChanges.map(\.tab), [.session])
        XCTAssertNil(workspace.inspector.model.inspection, "the Dashboard is no session")
        XCTAssertEqual((workspace.inspector.lines.first as? NSTextField)?.stringValue, WorkspaceInspectorView.noSession)
        XCTAssertEqual(workspace.inspector.actionButtons, [])

        workspace.select(.embedded(F.runtimeID(2)))
        XCTAssertEqual(workspace.inspector.model.inspection?.title, "api — personal")
        var buttons = workspace.inspector.actionButtons
        XCTAssertEqual(buttons.map(\.title), ["Name…", "Color…", "Icon…", "Mute", "Clear State", "Mark Active"])
        XCTAssertTrue(buttons.allSatisfy(\.isEnabled))
        buttons[3].performClick(nil)
        buttons[0].performClick(nil)
        XCTAssertEqual(app.inspectorActions, [.mute, .name])

        // A terminal beside a session is about that session.
        workspace.openTerminalBesideSession()
        XCTAssertEqual(workspace.inspector.model.inspection?.title, "api — personal")

        workspace.select(AttentionCycler.Key(socket: "unix:/tmp/kitty-1", windowID: 4))
        buttons = workspace.inspector.actionButtons
        XCTAssertEqual(buttons.map(\.isEnabled), [false, false, false, true, true, true],
                       "a Kitty session is named, colored, and given its icon in Kitty")
    }

    func testAnIdentifierIsCopiedByAClickOnItsLine() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.setInspectorTab(.session)
        workspace.select(.embedded(F.runtimeID(1)))
        var copied: [String] = []
        workspace.inspector.onCopy = { copied.append($0) }
        let fields = workspace.inspector.lines.compactMap { $0 as? WorkspaceInspectorFieldView }
        try XCTUnwrap(fields.first { $0.field.label == "Profile" }).copyValue()
        XCTAssertEqual(copied, [], "only an identifier is copied")
        let runtime = try XCTUnwrap(fields.first { $0.field.label == "Runtime" })
        runtime.copyValue()
        XCTAssertEqual(copied, [F.runtimeID(1)])
        XCTAssertEqual(runtime.valueLabel.stringValue, WorkspaceInspectorFieldView.copied)
    }
}
