import AppKit
import XCTest
@testable import AgentMux

/// Where the workspace was, as it is kept between launches.
final class WorkspaceRestorationTests: XCTestCase {
    private typealias F = WorkspaceFixture
    private typealias Place = WorkspaceRestoration.Place

    func testPlacesRoundTripThroughTheirPreferenceString() {
        let places: [Place] = [
            .session(runtimeSessionID: F.runtimeID(7)),
            .kitty(socket: "unix:/tmp/kitty-1", windowID: 42),
            .kitty(socket: "", windowID: 3),
            .view(id: WorkspaceUtilityView.dashboard.id),
            .view(id: WorkspaceUtilityView.overview.id),
        ]
        for place in places {
            XCTAssertEqual(Place(encoded: place.encoded), place, place.encoded)
        }
        XCTAssertEqual(Place.session(runtimeSessionID: F.runtimeID(7)).sessionKey, .embedded(F.runtimeID(7)))
        XCTAssertNil(Place.view(id: "dashboard").sessionKey)
    }

    func testAStringThatIsNotAPlaceRestoresNothing() {
        for junk in ["", "session", "session:", "session:rs-1", "session:not-an-id", "kitty:0:sock", "kitty:x:sock", "kitty:7",
                     "view:shell:/tmp", "view:pager:" + F.runtimeID(1), "launch:api", "session:\(F.runtimeID(1)); rm"] {
            XCTAssertNil(Place(encoded: junk), junk)
        }
    }

    func testTheWindowAndThePlaceAreKeptInDefaults() throws {
        let suite = "amx.tests.restoration.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }

        XCTAssertEqual(WorkspaceRestoration.load(from: defaults), WorkspaceRestoration(windowOpen: false, place: nil))
        WorkspaceRestoration.saveWindowOpen(true, to: defaults)
        WorkspaceRestoration.savePlace(.session(runtimeSessionID: F.runtimeID(2)), to: defaults)
        XCTAssertEqual(WorkspaceRestoration.load(from: defaults),
                       WorkspaceRestoration(windowOpen: true, place: .session(runtimeSessionID: F.runtimeID(2))))
        XCTAssertEqual(defaults.string(forKey: WorkspaceRestoration.placeKey), "session:" + F.runtimeID(2))

        WorkspaceRestoration.saveSidebarCollapsed(true, to: defaults)
        XCTAssertTrue(WorkspaceRestoration.load(from: defaults).sidebarCollapsed)
        WorkspaceRestoration.saveFullScreen(true, to: defaults)
        XCTAssertTrue(WorkspaceRestoration.load(from: defaults).fullScreen)
        WorkspaceRestoration.saveFullScreen(false, to: defaults)

        WorkspaceRestoration.savePlace(nil, to: defaults)
        WorkspaceRestoration.saveWindowOpen(false, to: defaults)
        WorkspaceRestoration.saveSidebarCollapsed(false, to: defaults)
        XCTAssertEqual(WorkspaceRestoration.load(from: defaults), WorkspaceRestoration())

        defaults.set("session:garbage", forKey: WorkspaceRestoration.placeKey)
        XCTAssertNil(WorkspaceRestoration.load(from: defaults).place, "a damaged preference restores nothing")
    }

    func testTheSidebarAndInspectorWidthsAreKeptInDefaults() throws {
        let suite = "amx.tests.restoration.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }

        XCTAssertNil(WorkspaceRestoration.load(from: defaults).sidebarWidth, "nothing saved: the window's default")
        XCTAssertNil(WorkspaceRestoration.load(from: defaults).inspectorWidth)
        WorkspaceRestoration.saveWidths(sidebar: 300, inspector: nil, to: defaults)
        var loaded = WorkspaceRestoration.load(from: defaults)
        XCTAssertEqual(loaded.sidebarWidth, 300)
        XCTAssertNil(loaded.inspectorWidth)
        WorkspaceRestoration.saveWidths(sidebar: nil, inspector: 280, to: defaults)
        loaded = WorkspaceRestoration.load(from: defaults)
        XCTAssertEqual(loaded.sidebarWidth, 300, "a hidden sidebar keeps the width it had")
        XCTAssertEqual(loaded.inspectorWidth, 280)

        defaults.set("wide", forKey: WorkspaceRestoration.sidebarWidthKey)
        XCTAssertNil(WorkspaceRestoration.load(from: defaults).sidebarWidth, "a damaged preference restores nothing")
    }

    func testTheConversationShownIsKeptInDefaults() throws {
        let suite = "amx.tests.restoration.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }

        let conversation = ConversationIdentity(harness: "claude", sessionID: "0b6f:1c2e")
        WorkspaceRestoration.saveConversation(conversation, to: defaults)
        XCTAssertEqual(defaults.string(forKey: WorkspaceRestoration.conversationKey), "claude:0b6f:1c2e")
        XCTAssertEqual(WorkspaceRestoration.load(from: defaults).conversation, conversation, "only the first colon divides it")
        WorkspaceRestoration.saveConversation(nil, to: defaults)
        XCTAssertNil(WorkspaceRestoration.load(from: defaults).conversation)

        // Anything that would not reach amx as one argument naming one
        // conversation is not one.
        for damaged in ["claude", "claude:", ":0b6f", "--help:0b6f", "claude:0b6f 1c2e", "claude:0b6f\n"] {
            XCTAssertNil(ConversationIdentity(encoded: damaged), damaged)
        }
    }

    func testAnAttachCheckSaysWhetherTheAgentRunsExitedOrWasLost() {
        func failure(_ code: String, _ message: String = "why") -> AMXCommandResult<AttachCheck> {
            .failure(.command(AMXJSONError(code: code, message: message), status: 1))
        }
        XCTAssertEqual(WorkspaceSessionStatus.from(.success(AttachCheck(clients: 0))), .live(clients: 0))
        XCTAssertEqual(WorkspaceSessionStatus.from(.success(AttachCheck(clients: 1))), .live(clients: 1))
        XCTAssertEqual(WorkspaceSessionStatus.from(failure("runtime_session_exited")), .exited("why"))
        XCTAssertEqual(WorkspaceSessionStatus.from(failure("runtime_session_lost")), .lost("why"))
        XCTAssertEqual(WorkspaceSessionStatus.from(failure("runtime_session_ended")), .ended("why"))
        XCTAssertEqual(WorkspaceSessionStatus.from(failure("not_found")), .unknownSession("why"))
        XCTAssertEqual(WorkspaceSessionStatus.from(failure("registry_unavailable")), .unknown("why"))
        XCTAssertEqual(WorkspaceSessionStatus.from(.failure(.undecodable(status: 2, detail: "boom"))), .unknown("boom"))

        let decoded = AMXEnvelope.decode(
            AttachCheck.self,
            stdout: Data(#"{"schema_version":1,"data":{"runtime_session_id":"rs-1","backend":"tmux","state":"live","pane":"%0","pid":7,"clients":1,"command":["tmux"]}}"#.utf8),
            status: 0
        )
        XCTAssertEqual(decoded.value, AttachCheck(runtimeSessionID: "rs-1", state: "live", clients: 1))
        // An amx from before the count answers without it: nobody is assumed attached.
        let older = AMXEnvelope.decode(AttachCheck.self, stdout: Data(#"{"schema_version":1,"data":{"runtime_session_id":"rs-1","state":"live"}}"#.utf8), status: 0)
        XCTAssertEqual(older.value?.clients, 0)
    }

    func testAnEndedSessionIsNotSaidToBeClosedAndAnUnknownOneIsNotSaidToBeEnded() throws {
        let ended = try XCTUnwrap(WorkspaceSessionStatus.ended("").notice(for: "api — work"))
        XCTAssertEqual(ended.message, "api — work was ended. Nothing was restarted.")
        let unknown = try XCTUnwrap(WorkspaceSessionStatus.unknownSession("").notice(for: "api — work", resumable: true))
        XCTAssertEqual(unknown.title, "No longer known")
        XCTAssertFalse(unknown.message.contains("ended"), "nobody is known to have ended it")
        XCTAssertTrue(unknown.message.contains("Past Sessions"))
    }

    func testExitedAndLostAreSaidDifferentlyAndNeitherPromisesARestart() throws {
        let exited = try XCTUnwrap(WorkspaceSessionStatus.exited("agent exited (status 1)").notice(for: "api — work"))
        let lost = try XCTUnwrap(WorkspaceSessionStatus.lost("backend generation changed").notice(for: "api — work"))
        XCTAssertEqual(exited.title, "Agent exited")
        XCTAssertEqual(lost.title, "Agent lost")
        XCTAssertNotEqual(exited.message, lost.message)
        for notice in [exited, lost] {
            XCTAssertTrue(notice.message.contains("api — work"))
            XCTAssertTrue(notice.message.contains("It was not restarted"), notice.message)
        }
        XCTAssertTrue(exited.message.contains("agent exited (status 1)"))
        for notice in [exited, lost] {
            XCTAssertFalse(notice.message.contains("press c"), "nothing is promised without a conversation")
        }
        let resumable = try XCTUnwrap(WorkspaceSessionStatus.lost("").notice(for: "api — work", resumable: true))
        XCTAssertTrue(resumable.message.contains("It was not restarted"))
        XCTAssertTrue(resumable.message.contains("press c"))
        XCTAssertNil(WorkspaceSessionStatus.live(clients: 0).notice(for: "x"))
        XCTAssertNil(WorkspaceSessionStatus.unknown("x").notice(for: "x"))
    }
}

/// The workspace coming back after the app, the daemon, or an agent went
/// away: what it shows, what it attaches, and that it starts nothing.
@MainActor
final class WorkspaceRecoveryTests: XCTestCase {
    private typealias F = WorkspaceFixture
    private var app: FakeWorkspaceApp!
    private var workspace: WorkspaceWindowController!

    private let sessions = [
        F.tmux(1, project: "api", profile: "work"),
        F.tmux(2, project: "api", profile: "personal"),
        F.kitty(4, project: "web"),
    ]

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

    private func row(_ project: String, _ label: String) throws -> WorkspaceRow {
        try XCTUnwrap(workspace.state.rows.first { $0.project == project && $0.label == label })
    }

    // MARK: Where the pane is

    func testEveryMoveOfThePaneIsReportedOnce() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        workspace.activate(try row("api", "work"))
        workspace.select(try row("web", "work").key)
        workspace.showView(WorkspaceUtilityView.overview.id)
        XCTAssertEqual(app.places, [
            .session(runtimeSessionID: F.runtimeID(1)),
            .kitty(socket: "unix:/tmp/kitty-1", windowID: 4),
            .view(id: "overview"),
        ])

        // A shell is a program, not a place to come back to: the place stays.
        workspace.open(WorkspaceUtilityView(kind: .shell, target: "/tmp", title: "Shell"))
        XCTAssertEqual(app.places.count, 3)

        // A daemon that goes away moves nothing.
        workspace.activate(try row("api", "personal"))
        app.store.apply(.disconnected(.notRunning, retryIn: 0.5))
        app.store.apply(.subscribed(SubscriptionAck()))
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(app.places.last, .session(runtimeSessionID: F.runtimeID(2)))
        XCTAssertEqual(app.places.count, 4)
    }

    // MARK: Coming back after the app went away

    func testRestoringASessionWaitsForTheDaemonThenReattachesTheSameSession() throws {
        // The app starts before the daemon answers.
        workspace.restore(.session(runtimeSessionID: F.runtimeID(2)))
        XCTAssertEqual(workspace.content, .restoring(runtimeSessionID: F.runtimeID(2)))
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "Restoring")
        XCTAssertTrue(workspace.placeholder.messageLabel.stringValue.contains("Nothing is started"))
        XCTAssertEqual(app.asked, [], "nothing is asked before the daemon lists the session")
        XCTAssertEqual(app.made, [])

        app.store.apply(.disconnected(.notRunning, retryIn: 0.5))
        XCTAssertEqual(workspace.content, .restoring(runtimeSessionID: F.runtimeID(2)))
        XCTAssertEqual(app.places, [], "a pending restore keeps the saved place")

        app.store.apply(.subscribed(SubscriptionAck()))
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(app.asked, [F.runtimeID(2)])
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(2)))
        XCTAssertEqual(app.made, [F.runtimeID(2)], "only the session the window was on is attached")
        XCTAssertNil(workspace.restoring)
        XCTAssertEqual(app.places, [], "it is where it was: nothing to save")

        // Later snapshots do not ask again.
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(app.asked, [F.runtimeID(2)])
    }

    func testRestoringOffersWhatBringsADaemonBack() throws {
        workspace.restore(.session(runtimeSessionID: F.runtimeID(2)))
        app.store.apply(.disconnected(.notRunning, retryIn: 0.5))
        XCTAssertEqual(workspace.content, .restoring(runtimeSessionID: F.runtimeID(2)))
        XCTAssertEqual(workspace.placeholder.button.title, "Start Daemon")
        XCTAssertTrue(workspace.placeholder.messageLabel.stringValue.contains("once the daemon lists it"))
        workspace.placeholder.press()
        XCTAssertEqual(app.daemonStarts, 1)
        XCTAssertEqual(workspace.content, .restoring(runtimeSessionID: F.runtimeID(2)), "starting the daemon keeps the restore")

        app.store.apply(.disconnected(.unsupported, retryIn: 0.5))
        XCTAssertEqual(workspace.placeholder.button.title, "Restart Daemon")
    }

    func testTheSessionIsAskedAboutOnceWhileTheAnswerIsPending() throws {
        app.holdsAnswers = true
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.restore(.session(runtimeSessionID: F.runtimeID(1)))
        app.store.apply(snapshot: F.snapshot(sessions))
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(app.asked, [F.runtimeID(1)])
        XCTAssertEqual(workspace.content, .restoring(runtimeSessionID: F.runtimeID(1)))
        XCTAssertTrue(workspace.placeholder.messageLabel.stringValue.contains("Checking"))
        XCTAssertEqual(app.made, [])
        app.answer()
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)))
    }

    func testRestoringNeverTakesASessionFromATerminalThatAttachedMeanwhile() throws {
        app.statuses[F.runtimeID(1)] = .live(clients: 1)
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.restore(.session(runtimeSessionID: F.runtimeID(1)))
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
        XCTAssertEqual(workspace.content, .detached(runtimeSessionID: F.runtimeID(1)))
        XCTAssertEqual(app.made, [], "the other terminal keeps the session")

        // Taking it back is the operator's decision.
        workspace.placeholder.press()
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)))
        XCTAssertEqual(app.made, [F.runtimeID(1)])
    }

    func testASessionThatCannotBeCheckedIsShownButNotAttached() throws {
        app.statuses[F.runtimeID(1)] = .unknown("amx did not answer")
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.restore(.session(runtimeSessionID: F.runtimeID(1)))
        XCTAssertEqual(workspace.content, .detached(runtimeSessionID: F.runtimeID(1)))
        XCTAssertEqual(app.made, [])
    }

    func testTheOperatorGoingElsewhereEndsARestore() throws {
        app.holdsAnswers = true
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.restore(.session(runtimeSessionID: F.runtimeID(1)))
        workspace.activate(try row("api", "personal"))
        app.answer()
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)), "a late answer moves nothing")
        XCTAssertEqual(app.made, [F.runtimeID(2)])
        XCTAssertNil(workspace.restoring)
    }

    func testAnAbsentAgentIsReportedAndNeverRelaunched() throws {
        // The session the window was on is not among the live ones.
        app.statuses[F.runtimeID(9)] = .lost("runtime session was lost (backend generation changed)")
        workspace.restore(.session(runtimeSessionID: F.runtimeID(9)))
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(app.asked, [F.runtimeID(9)])
        XCTAssertEqual(workspace.content, .gone(runtimeSessionID: F.runtimeID(9), .lost("runtime session was lost (backend generation changed)")))
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "Agent lost")
        XCTAssertTrue(workspace.placeholder.messageLabel.stringValue.contains("It was not restarted"))
        XCTAssertNil(workspace.state.selection, "no other session is picked instead")
        XCTAssertEqual(app.made, [], "no terminal is made for an agent that is not there")
        XCTAssertEqual(app.madeViews, [], "and no program is started on its behalf")
        XCTAssertEqual(app.places, [nil], "a session that is gone is not where the next launch returns")

        // The one thing offered is the Dashboard, where starting is explicit.
        XCTAssertEqual(workspace.placeholder.button.title, "Open Dashboard")
        workspace.placeholder.press()
        XCTAssertEqual(workspace.viewSelection, WorkspaceUtilityView.dashboard.id)
        XCTAssertNil(workspace.gone)
    }

    func testOpeningTheWindowOnNothingInParticularKeepsARestoreOrALostAgent() throws {
        // Dock reopen, Window ▸ Workspace, ⌘⇧L, and Open Workspace all land
        // on the Dashboard only when the pane has no place.
        app.holdsAnswers = true
        workspace.restore(.session(runtimeSessionID: F.runtimeID(2)))
        XCTAssertTrue(workspace.hasPlace)
        workspace.showDashboardUnlessPlaced()
        XCTAssertEqual(workspace.content, .restoring(runtimeSessionID: F.runtimeID(2)))
        XCTAssertNil(workspace.viewSelection)
        app.store.apply(snapshot: F.snapshot(sessions))
        app.answer()
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(2)), "the restore went on")

        app.holdsAnswers = false
        app.statuses[F.runtimeID(9)] = .lost("runtime session was lost (backend generation changed)")
        workspace.restore(.session(runtimeSessionID: F.runtimeID(9)))
        XCTAssertNotNil(workspace.gone)
        workspace.showDashboardUnlessPlaced()
        XCTAssertEqual(workspace.content, .gone(runtimeSessionID: F.runtimeID(9), .lost("runtime session was lost (backend generation changed)")))
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "Agent lost")
        XCTAssertNil(workspace.viewSelection)

        // A closed window forgets both, so it opens again on the Dashboard.
        workspace.window.close()
        XCTAssertFalse(workspace.hasPlace)
        workspace.openForTesting()
        workspace.showDashboardUnlessPlaced()
        XCTAssertEqual(workspace.viewSelection, WorkspaceUtilityView.dashboard.id)
    }

    func testAStoppedAgentWithAConversationOffersItInPastSessions() throws {
        let conversation = ConversationIdentity(harness: "claude", sessionID: "c-1")
        app.store.apply(snapshot: F.snapshot([F.tmux(1, project: "api", conversation: "c-1"), sessions[1]]))
        workspace.activate(try row("api", "work"))
        XCTAssertEqual(app.conversations, [conversation], "the conversation shown is kept for the next launch")
        app.statuses[F.runtimeID(1)] = .exited("agent exited (status 1)")
        app.store.apply(snapshot: F.snapshot([sessions[1]]))

        XCTAssertEqual(workspace.gone?.conversation, conversation)
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "Agent exited")
        let message = workspace.placeholder.messageLabel.stringValue
        XCTAssertTrue(message.contains("It was not restarted"), message)
        XCTAssertTrue(message.contains("press c"), message)
        XCTAssertEqual(workspace.placeholder.button.title, "Resume from Past Sessions")
        XCTAssertEqual(app.conversations.last, .some(nil), "a session that is gone is not where the next launch returns")

        workspace.placeholder.press()
        let history = WorkspaceUtilityView.history(conversation)
        XCTAssertEqual(workspace.viewSelection, history.id)
        XCTAssertEqual(app.madeViews, [history.id], "the list is opened and nothing else")
        XCTAssertEqual(app.made, [F.runtimeID(1)], "no terminal is attached and no agent started for it")
        XCTAssertNil(workspace.gone)
    }

    func testASessionGoneAtLaunchIsResumedFromTheConversationSavedForIt() throws {
        let saved = ConversationIdentity(harness: "codex", sessionID: "c-9")
        app.statuses[F.runtimeID(9)] = .lost("runtime session was lost (backend generation changed)")
        workspace.restore(.session(runtimeSessionID: F.runtimeID(9)), conversation: saved)
        XCTAssertEqual(app.conversations, [], "a pending restore keeps the saved conversation")
        app.store.apply(snapshot: F.snapshot(sessions))

        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "Agent lost")
        XCTAssertEqual(workspace.placeholder.button.title, "Resume from Past Sessions")
        workspace.placeholder.press()
        XCTAssertEqual(app.madeViews, ["history:codex:c-9"])
        XCTAssertEqual(app.made, [], "nothing is attached or started")
    }

    func testTheConversationFollowsThePaneAndOutlivesARowThatStopsNamingIt() throws {
        app.store.apply(snapshot: F.snapshot([F.tmux(1, project: "api", conversation: "c-1"), sessions[1], sessions[2]]))
        workspace.activate(try row("api", "work"))
        // Between two conversations the agent names none; the last one stays.
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(app.conversations, [ConversationIdentity(harness: "claude", sessionID: "c-1")])
        workspace.activate(try row("api", "personal"))
        XCTAssertEqual(app.conversations.last, .some(nil), "another session's conversation is never kept for this one")
    }

    func testRestoringAViewShowsItAndAKittySessionIsSelectedNotRaised() throws {
        workspace.restore(.view(id: "overview"))
        XCTAssertEqual(workspace.content, .view(id: "overview"))
        XCTAssertEqual(app.madeViews, ["overview"])

        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.restore(.kitty(socket: "unix:/tmp/kitty-1", windowID: 4))
        XCTAssertEqual(workspace.content, .external(try row("web", "work").key))
        XCTAssertEqual(app.external, [], "coming back must not pull Kitty forward")
        XCTAssertEqual(app.asked, [])

        // A Kitty window that is gone restores nothing and reports nothing.
        workspace.restore(.kitty(socket: "unix:/tmp/kitty-1", windowID: 99))
        XCTAssertEqual(workspace.content, .empty)
        XCTAssertNil(workspace.gone)
    }

    // MARK: An agent that stops while the window is on it

    func testAnAgentThatExitsIsSaidToHaveExited() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        app.holdsAnswers = true
        app.statuses[F.runtimeID(1)] = .exited("the agent of runtime session is no longer running: agent exited (status 1)")

        app.store.apply(snapshot: F.snapshot([sessions[1], sessions[2]]))
        XCTAssertEqual(app.closed, [F.runtimeID(1)], "its terminal is let go")
        XCTAssertEqual(workspace.content, .gone(runtimeSessionID: F.runtimeID(1), nil))
        XCTAssertTrue(workspace.placeholder.messageLabel.stringValue.contains("api — work"))
        XCTAssertTrue(workspace.placeholder.messageLabel.stringValue.contains("Checking"))

        app.answer()
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "Agent exited")
        XCTAssertTrue(workspace.placeholder.messageLabel.stringValue.contains("agent exited (status 1)"))
        XCTAssertEqual(app.made, [F.runtimeID(1)], "nothing was attached or started after it exited")
        XCTAssertEqual(app.places.last, .some(nil))

        // It stays said across snapshots, and the daemon going away, until
        // the operator moves on.
        app.store.apply(snapshot: F.snapshot([sessions[1], sessions[2]]))
        app.store.apply(.disconnected(.bye("shutdown"), retryIn: 0.5))
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "Agent exited")
        XCTAssertEqual(app.asked, [F.runtimeID(1)])
        workspace.activate(try row("api", "personal"))
        XCTAssertNil(workspace.gone)
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(2)))
    }

    func testALostAgentIsSaidToBeLostAfterItsTerminalEnded() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        let terminal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))

        // The server dies: the attachment ends first, then the daemon drops
        // the session.
        workspace.terminalEnded(terminal)
        XCTAssertEqual(workspace.content, .detached(runtimeSessionID: F.runtimeID(1)))
        app.statuses[F.runtimeID(1)] = .lost("runtime session was lost (backend generation changed)")
        app.store.apply(snapshot: F.snapshot([sessions[1], sessions[2]]))
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "Agent lost")
        XCTAssertEqual(app.made, [F.runtimeID(1)])
    }

    func testAKittySessionThatClosesLeavesThePaneEmpty() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.select(try row("web", "work").key)
        app.store.apply(snapshot: F.snapshot([sessions[0], sessions[1]]))
        XCTAssertEqual(workspace.content, .empty)
        XCTAssertEqual(app.asked, [], "amx is asked only about sessions the workspace embeds")
    }

    func testClosingTheWindowForgetsWhatThePaneWasSaying() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        app.statuses[F.runtimeID(1)] = .exited("agent exited (status 0)")
        app.store.apply(snapshot: F.snapshot([sessions[1], sessions[2]]))
        XCTAssertNotNil(workspace.gone)
        workspace.window.close()
        XCTAssertNil(workspace.gone)
        XCTAssertEqual(workspace.content, .empty)
    }
}
