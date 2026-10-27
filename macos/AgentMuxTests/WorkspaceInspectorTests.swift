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

    private var sessions: [CompanionSession] {
        var rows = [
            F.tmux(1, project: "api", profile: "work"),
            F.tmux(2, project: "api", profile: "personal", icon: "stop", attention: true, changedAtMS: 300_000),
            F.kitty(4, project: "web", icon: "key", attention: true, changedAtMS: 60_000),
        ]
        rows[1].unseen = true
        return rows
    }

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

    func testWaitingListsTheSessionsThatWaitInSidebarOrder() {
        // The permission request waits under Waiting, above the turn that
        // completed under Done, though it has waited the least.
        var listed = sessions
        listed[2].presentationChangedAtMS = 540_000
        let waiting = WorkspaceInspector.waiting(listed, snapshotSocket: "unix:/tmp/kitty-1", now: now, glyphs: .fallback)
        XCTAssertEqual(waiting.map(\.title), ["web — work", "api — personal"], "not the one that waited longest first")
        XCTAssertEqual(waiting.map(\.detail), ["Needs permission · 1m · Kitty", "Turn complete · 5m"])
        XCTAssertEqual(waiting.map(\.glyph), ["🔑", "✓"])
        XCTAssertEqual(waiting.map(\.key), [
            AttentionCycler.Key(socket: "unix:/tmp/kitty-1", windowID: 4), .embedded(F.runtimeID(2)),
        ])
    }

    func testAWaitingSessionSaysItsReasonAndGoesByItsName() {
        var session = sessions[1]
        session.reason = "permission"
        session.name = "migrations"
        let waiting = WorkspaceInspector.waiting([session], now: now, glyphs: .fallback)
        XCTAssertEqual(waiting.map(\.title), ["api — migrations"])
        XCTAssertEqual(waiting.map(\.detail), ["Needs permission · 5m"])
        XCTAssertTrue(waiting[0].toolTip.hasPrefix("api — personal"), "the name does not hide where the session is")
    }

    func testAWaitingSessionIsAnsweredThroughItsRunElseItsPromptElseNotFromHere() {
        var looping = F.tmux(3, project: "loop", icon: "question", attention: true)
        looping.aslRunID = "run-3"
        let waiting = [
            F.tmux(1, project: "api", icon: "key", attention: true),
            F.tmux(2, project: "docs", icon: "stop", attention: true),
            looping,
            F.kitty(4, project: "web", icon: "key", attention: true),
            F.kitty(5, project: "zed", icon: "question", attention: true),
            F.tmux(6, project: "ops", icon: "error", attention: true),
        ]
        var tmuxRun = CompanionASLWaiting()
        tmuxRun.runID = "run-3"
        tmuxRun.planDir = "/plans/loop"
        var kittyRun = CompanionASLWaiting()
        kittyRun.runID = "run-5"
        kittyRun.planDir = "/plans/zed"
        kittyRun.windowID = 5

        let rows = WorkspaceInspector.waiting(waiting, asl: [tmuxRun, kittyRun], snapshotSocket: "unix:/tmp/kitty-1", now: now, glyphs: .fallback)
        let answers = Dictionary(uniqueKeysWithValues: rows.map { ($0.title.components(separatedBy: " — ")[0], $0.answer) })
        XCTAssertEqual(answers["api"], .some(.prompt(runtimeSessionID: F.runtimeID(1))), "a prompt in a session the workspace embeds")
        XCTAssertEqual(answers["docs"], .some(nil), "a stopped agent asks nothing")
        XCTAssertEqual(answers["ops"], .some(nil))
        XCTAssertEqual(answers["loop"], .some(.asl(ASLAnswerTarget(runID: "run-3", planDir: "/plans/loop"))),
                       "a supervisor question is the run's mailbox to answer, never the pane's")
        XCTAssertEqual(answers["web"], .some(nil), "a Kitty session's prompt is answered in Kitty")
        XCTAssertEqual(answers["zed"], .some(.asl(ASLAnswerTarget(runID: "run-5", planDir: "/plans/zed", windowID: 5))),
                       "a run is answered whichever terminal it is in")
        XCTAssertEqual(WorkspaceInspector.waiting(waiting, now: now, glyphs: .fallback).compactMap(\.answer),
                       [.prompt(runtimeSessionID: F.runtimeID(1)), .prompt(runtimeSessionID: F.runtimeID(3))],
                       "with no waiting run known, only prompts are offered")
    }

    func testInboxFiltersAndOldestOrderDoNotChangeSidebarOrder() {
        var listed = sessions
        listed[2].presentationChangedAtMS = 540_000
        let rows = WorkspaceInspector.waiting(listed, now: now, glyphs: .fallback)
        XCTAssertEqual(rows.map(\.category), [.permission, .completed])
        XCTAssertEqual(WorkspaceWaitingFilter.all.apply(rows, oldestFirst: true).map(\.key), [rows[0].key, rows[1].key],
                       "a permission still leads a completion")
        XCTAssertEqual(WorkspaceWaitingFilter.permission.apply(rows, oldestFirst: false), [rows[0]])
        XCTAssertEqual(WorkspaceWaitingFilter.completed.apply(rows, oldestFirst: false), [rows[1]])
        app.store.apply(snapshot: F.snapshot(listed))
        let before = workspace.state.rows
        workspace.inspector.setWaitingFilter(.permission, oldestFirst: true)
        XCTAssertEqual(workspace.inspector.visibleWaiting.count, 1)
        XCTAssertEqual(workspace.state.rows, before)
    }

    func testLateContextIsDiscardedWhenThePromptChangesOrFeedIsLost() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let inspector = workspace.inspector
        var finish: ((WorkspaceWaitingContext.Result) -> Void)?
        inspector.onContext = { _, completion in finish = completion }
        inspector.toggleContext(try XCTUnwrap(inspector.model.waiting.first))
        XCTAssertEqual(inspector.lines.compactMap { $0 as? WorkspaceWaitingRowView }.first { $0.row.key == inspector.model.waiting.first?.key }?.contextStatus.stringValue, "Loading context…")
        var changed = sessions
        changed[2].iconState = "question"
        app.store.apply(snapshot: F.snapshot(changed))
        finish?(.loaded(SecretText("old prompt")))
        XCTAssertFalse(inspector.lines.compactMap { ($0 as? WorkspaceWaitingRowView)?.contextLabel.stringValue }.contains("old prompt"))
        inspector.toggleContext(try XCTUnwrap(inspector.model.waiting.first))
        var stale = inspector.model
        stale.current = false
        stale.feedNotice = "Reconnecting…"
        inspector.show(stale)
        finish?(.loaded(SecretText("late response")))
        XCTAssertFalse(inspector.lines.compactMap { ($0 as? WorkspaceWaitingRowView)?.contextLabel.stringValue }.contains("late response"))
    }

    func testAnotherQuestionOnTheSameRunInvalidatesContext() {
        var session = F.tmux(1, project: "api", icon: "question", attention: true)
        session.aslRunID = "run-one"
        var run = CompanionASLWaiting()
        run.runID = "run-one"
        run.planDir = "/plans/api"
        run.latestQuestionID = "q-one"
        run.unanswered = 1
        let first = WorkspaceInspector.waiting([session], asl: [run], now: now)[0]
        run.latestQuestionID = "q-two"
        let second = WorkspaceInspector.waiting([session], asl: [run], now: now)[0]
        XCTAssertEqual(first.answer, second.answer)
        XCTAssertFalse(first.sameContext(as: second))
    }

    func testNextWaitingUsesTheFilteredOrderWithoutSendingAnAnswer() {
        app.store.apply(snapshot: F.snapshot(sessions))
        let inspector = workspace.inspector
        inspector.setWaitingFilter(.all, oldestFirst: true)
        var visited: [AttentionCycler.Key] = []
        inspector.onWaiting = { visited.append($0) }
        inspector.onAnswer = { _ in XCTFail("Navigation must not answer a question") }
        inspector.nextWaiting()
        inspector.nextWaiting()
        inspector.nextWaiting()
        let order = inspector.visibleWaiting.map(\.key)
        XCTAssertEqual(visited, [order[0], order[1], order[0]])
    }

    func testAnInspectionLeavesOutWhatIsNotKnown() {
        let idle = WorkspaceInspector.inspection(sessions[0], now: now, glyphs: .fallback)
        XCTAssertEqual(idle.title, "api — work")
        XCTAssertTrue(idle.embedded)
        XCTAssertEqual(idle.sections.map(\.title), ["Status", "Identity"], "a session with no branch has no Git section")
        XCTAssertEqual(idle.sections[0].fields, [.init(label: "State", value: "idle"), .init(label: "Evidence", value: "Nothing has reported it yet")])
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
            .init(label: "Reason", value: "Tool"),
            .init(label: "Evidence", value: "Nothing has reported it yet"),
            .init(label: "Since", value: "5m"),
            .init(label: "Working for", value: "2m"),
            .init(label: "Last event", value: "now"),
            .init(label: "Flags", value: "waiting on you, unseen, muted, looping"),
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
        XCTAssertEqual(lines.map(\.titleLabel.stringValue), ["🔑 web — work", "✓ api — personal"])

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

    func testAnswerOnAWaitingRowAsksTheAppAndLeavesThePaneAndTheKeyboard() throws {
        var asking = sessions
        asking[1] = F.tmux(2, project: "api", profile: "personal", icon: "key", attention: true, changedAtMS: 300_000)
        asking.append(F.tmux(3, project: "docs", icon: "stop", attention: true))
        app.store.apply(snapshot: F.snapshot(asking))
        workspace.select(.embedded(F.runtimeID(1)), focus: true)
        let terminal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))
        workspace.setInspectorCollapsed(false)
        workspace.window.layoutIfNeeded()

        let lines = workspace.inspector.lines.compactMap { $0 as? WorkspaceWaitingRowView }
        XCTAssertEqual(lines.map(\.titleLabel.stringValue), ["🔑 api — personal", "🔑 web — work", "✓ docs — work"])
        XCTAssertEqual(lines.map { $0.answerButton?.title }, ["Answer…", nil, nil], "only what can be answered from here has the button")

        let button = try XCTUnwrap(lines[0].answerButton)
        XCTAssertTrue(button.refusesFirstResponder)
        let onButton = button.convert(NSPoint(x: button.bounds.midX, y: button.bounds.midY), to: lines[0].superview)
        XCTAssertTrue(lines[0].hitTest(onButton)?.isDescendant(of: button) == true, "the button takes its own clicks")
        let beside = lines[0].convert(NSPoint(x: 6, y: lines[0].bounds.midY), to: lines[0].superview)
        XCTAssertTrue(lines[0].hitTest(beside) === lines[0], "the rest of the line is still one target")

        XCTAssertTrue(lines[0].isAccessibilityElement(), "the line is said as one")
        XCTAssertEqual(lines[0].accessibilityRole(), .button)
        XCTAssertFalse(lines[0].titleLabel.isAccessibilityElement())
        XCTAssertFalse(lines[0].detailLabel.isAccessibilityElement())
        let children = (lines[0].accessibilityChildren() ?? []).compactMap { $0 as? NSAccessibilityProtocol }
        XCTAssertEqual(children.map { $0.accessibilityRole() }, [.button, .button], "answer and context remain accessible")
        XCTAssertEqual(children.first?.accessibilityLabel(), "Answer api — personal")
        XCTAssertEqual(lines[1].accessibilityChildren()?.count, 1, "context remains available without an answer")
        XCTAssertEqual(button.accessibilityLabel(), "Answer api — personal")

        button.performClick(nil)
        XCTAssertEqual(app.answers, [.prompt(runtimeSessionID: F.runtimeID(2))])
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)), "the pane stays where it is")
        XCTAssertTrue(workspace.window.firstResponder === terminal, "and the terminal keeps the keyboard")
        XCTAssertEqual(app.made, [F.runtimeID(1)], "nothing is attached to answer")

        lines[0].press()
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(2)), "a click on the line still goes there")
        XCTAssertEqual(app.answers.count, 1)
    }

    func testAWaitingRunIsAnsweredFromItsSessionsRow() throws {
        var snapshot = F.snapshot(sessions)
        var run = CompanionASLWaiting()
        run.runID = "run-2"
        run.planDir = "/plans/api"
        run.runtimeSessionID = F.runtimeID(2)
        snapshot.asl.waiting = [run]
        app.store.apply(snapshot: snapshot)
        workspace.setInspectorCollapsed(false)

        let lines = workspace.inspector.lines.compactMap { $0 as? WorkspaceWaitingRowView }
        try XCTUnwrap(lines.first { $0.row.key == .embedded(F.runtimeID(2)) }?.answerButton).performClick(nil)
        XCTAssertEqual(app.answers, [.asl(ASLAnswerTarget(runID: "run-2", planDir: "/plans/api"))])
        XCTAssertNil(lines.first { $0.row.key.windowID == 4 }?.answerButton, "the Kitty session next to it has no run and no button")
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
        XCTAssertEqual(buttons.map(\.title), ["Name…", "Color…", "Icon…", "Mute", "Clear Markers", "Mark Active", "Changes…"])
        XCTAssertTrue(buttons.allSatisfy(\.isEnabled))
        buttons[3].performClick(nil)
        buttons[0].performClick(nil)
        XCTAssertEqual(app.inspectorActions, [.mute, .name])

        // A terminal beside a session is about that session.
        workspace.openTerminalBesideSession()
        XCTAssertEqual(workspace.inspector.model.inspection?.title, "api — personal")

        workspace.select(AttentionCycler.Key(socket: "unix:/tmp/kitty-1", windowID: 4))
        buttons = workspace.inspector.actionButtons
        XCTAssertEqual(buttons.map(\.isEnabled), [false, false, false, true, true, true, true],
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

    func testAnIdentifierIsAButtonVoiceOverCanPress() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.setInspectorTab(.session)
        workspace.select(.embedded(F.runtimeID(1)))
        var copied: [String] = []
        workspace.inspector.onCopy = { copied.append($0) }
        let fields = workspace.inspector.lines.compactMap { $0 as? WorkspaceInspectorFieldView }
        let profile = try XCTUnwrap(fields.first { $0.field.label == "Profile" })
        XCTAssertTrue(profile.isAccessibilityElement())
        XCTAssertEqual(profile.accessibilityRole(), .staticText)
        XCTAssertFalse(profile.accessibilityPerformPress(), "only an identifier is copied")
        let runtime = try XCTUnwrap(fields.first { $0.field.label == "Runtime" })
        XCTAssertTrue(runtime.isAccessibilityElement())
        XCTAssertEqual(runtime.accessibilityRole(), .button)
        XCTAssertEqual(runtime.accessibilityLabel(), "Runtime: \(F.runtimeID(1))")
        XCTAssertFalse(runtime.valueLabel.isAccessibilityElement(), "the line is said once")
        XCTAssertTrue(runtime.accessibilityPerformPress())
        XCTAssertEqual(copied, [F.runtimeID(1)])
        XCTAssertEqual(runtime.valueLabel.stringValue, WorkspaceInspectorFieldView.copied)
    }

    // MARK: Updating in place

    func testLinesAreTheSameLinesWhenOnlyWhatTheySayChanged() {
        typealias Line = WorkspaceInspectorLine
        let reason = WorkspaceInspection.Field(label: "Reason", value: "permission")
        let row = WorkspaceWaitingRow(key: .embedded(F.runtimeID(1)), title: "api", detail: "stopped · 1m", glyph: "", toolTip: "")
        let later = WorkspaceWaitingRow(key: row.key, title: row.title, detail: "stopped · 2m", glyph: "", toolTip: "")
        XCTAssertTrue(Line.sameLines(
            [.title("a", toolTip: "a"), .heading("Status"), .field(reason), .waiting(row), .actions(muted: false, embedded: true)],
            [.title("b", toolTip: "b"), .heading("Status"), .field(WorkspaceInspection.Field(label: "Reason", value: "idle")),
             .waiting(later), .actions(muted: true, embedded: false)]
        ))
        XCTAssertFalse(Line.sameLines([.heading("Status")], [.heading("Git")]))
        XCTAssertFalse(Line.sameLines([.field(reason)], [.field(WorkspaceInspection.Field(label: "Since", value: "1m"))]))
        XCTAssertFalse(Line.sameLines([.waiting(row)], [.waiting(WorkspaceWaitingRow(
            key: .embedded(F.runtimeID(2)), title: "api", detail: "", glyph: "", toolTip: ""))]))
        var answerable = row
        answerable.answer = .prompt(runtimeSessionID: F.runtimeID(1))
        XCTAssertFalse(Line.sameLines([.waiting(row)], [.waiting(answerable)]), "an Answer… button comes or goes")
        XCTAssertFalse(Line.sameLines([.note("a")], [.note("a"), .note("b")]))
    }

    func testTheSessionTabSaysWhatIsWrongWithTheFeedWhenItHasNoSession() {
        var model = WorkspaceInspectorView.Model(tab: .session)
        XCTAssertEqual(model.lines, [.note(WorkspaceInspectorView.noSession)])
        model.feedNotice = "Daemon not running"
        XCTAssertEqual(model.lines, [.note("Daemon not running")])
        model.tab = .waiting
        XCTAssertEqual(model.lines, [.note("Daemon not running")])
    }

    func testTheSessionTabSaysTheFeedIsDownWhileThePaneIsOnASession() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.setInspectorTab(.session)
        workspace.select(.embedded(F.runtimeID(1)))
        XCTAssertNotNil(workspace.inspector.model.inspection)
        app.store.apply(.disconnected(.notRunning, retryIn: 0.5))
        XCTAssertNil(workspace.inspector.model.inspection)
        XCTAssertEqual((workspace.inspector.lines.first as? NSTextField)?.stringValue, "Daemon not running")
    }

    func testHeldLinesAreDimmedButNotTheNoteSayingWhy() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.setInspectorTab(.waiting)
        app.store.apply(.disconnected(.heartbeatLost, retryIn: 0.5))
        let model = workspace.inspector.model
        XCTAssertFalse(model.current)
        XCTAssertFalse(model.waiting.isEmpty, "the waiting rows are held")
        guard case .note = try XCTUnwrap(model.lines.first) else { return XCTFail("no note above the held lines") }
        XCTAssertEqual(workspace.inspector.alphaValue, 1, "the tabs are not dimmed")
        XCTAssertEqual(workspace.inspector.lines.map(\.alphaValue), [1] + Array(repeating: 0.5, count: workspace.inspector.lines.count - 1))
    }

    func testACopiedIdentifierStaysCopiedWhileTheSessionChanges() throws {
        var changed = sessions
        changed[1].reason = "permission"
        app.store.apply(snapshot: F.snapshot(changed))
        workspace.setInspectorTab(.session)
        workspace.select(.embedded(F.runtimeID(2)))
        workspace.inspector.onCopy = { _ in }
        let before = workspace.inspector.lines
        let fields = before.compactMap { $0 as? WorkspaceInspectorFieldView }
        let runtime = try XCTUnwrap(fields.first { $0.field.label == "Runtime" })
        runtime.copyValue()

        changed[1].reason = "done"
        app.store.apply(snapshot: F.snapshot(changed))
        let after = workspace.inspector.lines
        XCTAssertEqual(after.count, before.count)
        XCTAssertTrue(zip(before, after).allSatisfy { $0 === $1 }, "the same lines, updated where they are")
        XCTAssertEqual(runtime.valueLabel.stringValue, WorkspaceInspectorFieldView.copied)
        let reason = try XCTUnwrap(fields.first { $0.field.label == "Reason" })
        XCTAssertEqual(reason.valueLabel.stringValue, "Turn complete")

        // A line that comes or goes builds the lines again.
        changed[1].branch = "main"
        app.store.apply(snapshot: F.snapshot(changed))
        XCTAssertFalse(workspace.inspector.lines.first === before.first)
    }

    func testAHoveredWaitingLineKeepsItsTintWhileItsAgeChanges() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.setInspectorCollapsed(false)
        let line = try XCTUnwrap(workspace.inspector.lines.compactMap { $0 as? WorkspaceWaitingRowView }.first { $0.row.key == .embedded(F.runtimeID(2)) })
        line.isHovered = true
        var changed = sessions
        changed[1].reason = "permission"
        app.store.apply(snapshot: F.snapshot(changed))
        XCTAssertTrue(workspace.inspector.lines.contains { $0 === line })
        XCTAssertEqual(line.detailLabel.stringValue, "Needs permission · 5m")
        XCTAssertEqual(line.fillColor, WorkspaceWaitingRowView.hoveredFill)
        line.press()
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
    }
}
