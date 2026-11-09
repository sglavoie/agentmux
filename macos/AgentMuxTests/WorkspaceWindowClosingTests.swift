import AppKit
import GhosttyKit
import ScreenCaptureKit
import XCTest
@testable import AgentMux

extension WorkspaceWindowTests {
    func testALineDrawnAgainIsHoveredWhereThePointerIs() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.window.layoutIfNeeded()
        let first = try index(of: .embedded(F.runtimeID(2)))
        let second = try index(of: .embedded(F.runtimeID(1)))
        let over = try cell(.embedded(F.runtimeID(2)))
        let other = try cell(.embedded(F.runtimeID(1)))
        other.isHovered = true
        let point = over.convert(NSPoint(x: over.bounds.midX, y: over.bounds.midY), to: nil)
        workspace.syncHover(at: point, lines: IndexSet([first, second]))
        XCTAssertTrue(over.isHovered)
        XCTAssertFalse(other.isHovered)
        workspace.syncHover(at: NSPoint(x: -100, y: -100), lines: IndexSet([first, second]))
        XCTAssertFalse(over.isHovered, "a pointer outside the list hovers nothing")
    }

    func testAHalfOpenFeedKeepsItsSnapshotAndTheWorkspace() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        app.store.apply(.disconnected(.heartbeatLost, retryIn: 0.5))
        XCTAssertFalse(workspace.state.current)
        XCTAssertEqual(workspace.feedLabel.stringValue, "Reconnecting… (the daemon is not answering)")
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)))
        app.store.apply(.subscribed(SubscriptionAck()))
        XCTAssertTrue(workspace.state.current, "the kept snapshot is the daemon's last word until the next one")
        XCTAssertEqual(app.closed, [])
    }

    func testOnlyALiveSnapshotRetiresASession() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "personal"))
        workspace.activate(try row("api", "work"))

        // The unselected session ended: its terminal goes, nothing else moves.
        app.store.apply(snapshot: F.snapshot([sessions[0], sessions[2]]))
        XCTAssertEqual(app.closed, [F.runtimeID(2)])
        XCTAssertEqual(workspace.attachedRuntimeSessionIDs, [F.runtimeID(1)])
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)))

        // The selected one ended: nothing is selected, and no other session
        // is picked for the operator. The pane says what became of it.
        app.statuses[F.runtimeID(1)] = .ended("runtime session is ended")
        app.store.apply(snapshot: F.snapshot([sessions[2]]))
        XCTAssertEqual(app.closed, [F.runtimeID(2), F.runtimeID(1)])
        XCTAssertNil(workspace.state.selection)
        XCTAssertEqual(workspace.sidebar.selectedRow, -1)
        XCTAssertEqual(workspace.content, .gone(runtimeSessionID: F.runtimeID(1), .ended("runtime session is ended")))
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "Session ended")
        XCTAssertTrue(workspace.window.firstResponder === workspace.sidebar)
        XCTAssertEqual(workspace.window.title, "Agent Mux")
    }

    func testAnEndedAttachmentIsNotRemadeUntilAskedFor() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        let terminal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))

        // `amx attach` ended: another view took the session, or F12.
        workspace.terminalEnded(terminal)
        XCTAssertEqual(app.closed, [F.runtimeID(1)])
        XCTAssertEqual(workspace.content, .detached(runtimeSessionID: F.runtimeID(1)))
        XCTAssertEqual(workspace.placeholder.titleLabel.stringValue, "Detached")
        XCTAssertNil(terminal.superview)

        // Snapshots keep arriving and do not take the session back.
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        XCTAssertEqual(app.made, [F.runtimeID(1)], "attaching again would take it from whoever has it")

        workspace.placeholder.press()
        XCTAssertEqual(app.made, [F.runtimeID(1), F.runtimeID(1)])
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)))
        XCTAssertTrue(workspace.window.firstResponder === workspace.terminalView(for: F.runtimeID(1)))
    }

    func cell(_ key: AttentionCycler.Key) throws -> WorkspaceRowCellView {
        try XCTUnwrap(workspace.sidebar.view(atColumn: 0, row: try index(of: key), makeIfNecessary: true) as? WorkspaceRowCellView)
    }

    func testEndSessionCanSkipConfirmationAndRestoreItImmediately() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let busy = try row("api", "personal")
        workspace.activate(busy)
        app.holdsCloses = true
        var asked: [WorkspaceRow] = []
        workspace.confirmClose = { row, answer in asked.append(row); answer(false) }

        workspace.confirmBeforeClosing = false
        workspace.closeSession(busy.key)
        XCTAssertTrue(asked.isEmpty)
        XCTAssertEqual(app.ended, [F.runtimeID(2)])
        XCTAssertEqual(app.closed, [F.runtimeID(2)])
        workspace.closeSession(busy.key)
        XCTAssertEqual(app.ended, [F.runtimeID(2)], "an in-flight close cannot be duplicated")

        workspace.confirmBeforeClosing = true
        let idle = try row("api", "work")
        workspace.closeSession(idle.key)
        XCTAssertEqual(asked, [idle])
        XCTAssertEqual(app.ended, [F.runtimeID(2)], "cancelling still keeps the agent running")
    }

    func testExplicitEndSessionAsksEvenForAnIdleSession() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let work = try row("api", "work")
        workspace.activate(work)
        app.holdsCloses = true
        var asked: [(WorkspaceRow, @MainActor (Bool) -> Void)] = []
        workspace.confirmClose = { row, answer in asked.append((row, answer)) }

        XCTAssertFalse(try cell(work.key).closeButton.isHidden)
        XCTAssertFalse(try cell(try row("web", "work").key).closeButton.isHidden)
        workspace.closeSession(work.key)
        XCTAssertEqual(asked.map(\.0), [work], "an idle agent is ended only once the operator agrees")
        XCTAssertEqual(app.ended, [], "nothing ends before the answer")
        asked[0].1(true)
        XCTAssertEqual(app.ended, [F.runtimeID(1)])
        XCTAssertEqual(app.closed, [F.runtimeID(1)], "its terminal is let go at once")
        XCTAssertEqual(workspace.content, .closing(runtimeSessionID: F.runtimeID(1)))
        XCTAssertTrue(try cell(work.key).closeButton.isHidden)
        XCTAssertEqual(try cell(work.key).detailLabel.stringValue, "ending…")
        XCTAssertTrue(try XCTUnwrap(cell(work.key).accessibilityLabel()).contains(", ending session"),
                      "VoiceOver hears what the line shows")

        // A second click, and snapshots that still list it, change nothing.
        workspace.closeSession(work.key)
        app.store.apply(snapshot: F.snapshot(sessions))
        XCTAssertEqual(asked.count, 1, "a session being closed is not asked about again")
        XCTAssertEqual(app.ended, [F.runtimeID(1)])
        XCTAssertEqual(app.made, [F.runtimeID(1)], "a session being closed is not attached again")

        // The daemon drops the row before amx answers: nothing is reported
        // as gone, and the pane ends up on the Dashboard.
        app.store.apply(snapshot: F.snapshot(Array(sessions.dropFirst())))
        XCTAssertEqual(workspace.content, .empty)
        XCTAssertEqual(app.asked, [], "a session the operator closed is not asked about")
        app.finishClosing()
        XCTAssertEqual(workspace.content, .view(id: "dashboard"))
        XCTAssertEqual(workspace.state.rows.count, 2)
    }

    func testClosingASessionThatIsBusyAsksFirst() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        let busy = try row("api", "personal")
        var asked: [(WorkspaceRow, @MainActor (Bool) -> Void)] = []
        workspace.confirmClose = { row, answer in asked.append((row, answer)) }

        workspace.closeSession(busy.key)
        XCTAssertEqual(asked.map(\.0), [busy])
        XCTAssertEqual(app.ended, [], "nothing ends before the answer")
        asked[0].1(false)
        XCTAssertEqual(app.ended, [])
        XCTAssertFalse(try cell(busy.key).closeButton.isHidden)

        workspace.closeSession(busy.key)
        asked[1].1(true)
        XCTAssertEqual(app.ended, [F.runtimeID(2)])
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)), "the pane was elsewhere and stays there")
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
    }

    func testClosingAnAttachmentKeepsTheAgentAndCanReattach() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let busy = try row("api", "personal")
        workspace.activate(busy, reattaching: true)
        workspace.confirmClose = { _, _ in XCTFail("detaching must not ask to end the agent") }
        XCTAssertTrue(workspace.closeShown())
        XCTAssertEqual(app.ended, [])
        XCTAssertEqual(app.closed, [F.runtimeID(2)])
        XCTAssertEqual(workspace.content, .detached(runtimeSessionID: F.runtimeID(2)))
        XCTAssertNotNil(workspace.state.row(busy.key))
        workspace.activate(busy, reattaching: true)
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(2)))
    }

    func testSessionRowCloseButtonEndsItsOwnSessionThroughConfirmation() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        let busy = try row("api", "personal")
        var asked: [(WorkspaceRow, @MainActor (Bool) -> Void)] = []
        workspace.confirmClose = { row, answer in asked.append((row, answer)) }

        let button = try cell(busy.key).closeButton
        XCTAssertEqual(button.toolTip, "End \(busy.displayName)…")
        XCTAssertEqual(button.accessibilityLabel(), "End session \(busy.displayName)")
        button.performClick(nil)
        XCTAssertEqual(asked.map(\.0), [busy])
        XCTAssertEqual(app.ended, [], "clicking does not end the agent before confirmation")
        asked[0].1(false)
        XCTAssertEqual(app.ended, [], "cancelling keeps the session running")

        try cell(busy.key).closeButton.performClick(nil)
        XCTAssertEqual(asked.map(\.0), [busy, busy])
        asked[1].1(true)
        XCTAssertEqual(app.ended, [F.runtimeID(2)], "the row under the pointer is ended")
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)),
                       "ending another row does not change the shown session")
    }

    func testASessionThatCouldNotBeClosedComesBack() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let work = try row("api", "work")
        workspace.activate(work)
        app.closeProblem = "tmux backend unavailable"
        var reported: [String] = []
        workspace.reportCloseProblem = { name, message in reported.append("\(name): \(message)") }
        workspace.confirmClose = { _, answer in answer(true) }

        workspace.closeSession(work.key)
        XCTAssertEqual(reported, ["api — work: tmux backend unavailable"])
        XCTAssertEqual(app.made, [F.runtimeID(1), F.runtimeID(1)], "its terminal is attached again")
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)))
        XCTAssertFalse(try cell(work.key).closeButton.isHidden)
    }

    func testTheCloseButtonIsDrawnOnlyUnderThePointerWithARoomyTarget() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let line = try cell(try row("api", "work").key)
        XCTAssertFalse(line.closeButton.isHidden, "the line can be closed")
        XCTAssertFalse(line.showsCloseButton, "but the × is not drawn until the pointer is over it")
        XCTAssertEqual(line.closeButton.alphaValue, 0)
        line.isHovered = true
        XCTAssertTrue(line.showsCloseButton)
        XCTAssertEqual(line.closeButton.alphaValue, 1)
        line.isHovered = false
        XCTAssertFalse(line.showsCloseButton)
        XCTAssertFalse(try cell(try row("web", "work").key).showsCloseButton, "the terminal surface has none to draw")

        line.layoutSubtreeIfNeeded()
        XCTAssertGreaterThanOrEqual(line.closeButton.frame.width, 18)
        XCTAssertGreaterThanOrEqual(line.closeButton.frame.height, 18)
    }

    func testTheCloseItemSaysWhatItCloses() throws {
        XCTAssertEqual(workspace.closeShownTitle, "Close")
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.activate(try row("api", "work"))
        XCTAssertEqual(workspace.closeShownTitle, "Detach Terminal")
        workspace.select(try row("web", "work").key)
        XCTAssertEqual(workspace.closeShownTitle, "Detach Terminal")
        workspace.showView(WorkspaceUtilityView.dashboard.id)
        XCTAssertEqual(workspace.closeShownTitle, "Close View")
    }

    func testHandoffTargetsClickedConversationAndRetainsSourceNavigation() throws {
        var fourth = F.tmux(4, project: "api")
        fourth.conversation = .init(harness: "copilot", sessionID: "fourth-source")
        let initial = [F.tmux(1, project: "api", conversation: "first"), F.tmux(2, project: "api", conversation: "second"), F.tmux(3, project: "api"), fourth]
        app.store.apply(snapshot: F.snapshot(initial))
        workspace.select(.embedded(F.runtimeID(1)))
        let menu = workspace.contextMenuItems(forRow: try index(of: .embedded(F.runtimeID(2))))
        let handoff = try XCTUnwrap(menu.first { $0.title == WorkspaceHandoffModel.title })
        XCTAssertTrue(handoff.isEnabled)
        XCTAssertTrue(NSApp.sendAction(try XCTUnwrap(handoff.action), to: handoff.target, from: handoff))
        XCTAssertEqual(app.handoffRequests.map(\.conversation), [ConversationIdentity(harness: "claude", sessionID: "second")])
        let withoutIdentity = workspace.contextMenuItems(forRow: try index(of: .embedded(F.runtimeID(3))))
        XCTAssertFalse(try XCTUnwrap(withoutIdentity.first { $0.title == WorkspaceHandoffModel.title }).isEnabled)
        let fourthKey = AttentionCycler.key(fourth)
        XCTAssertFalse(workspace.contextMenuItems(forRow: try index(of: fourthKey)).isEmpty)
        let source = ConversationIdentity(harness: "codex", sessionID: "original")
        app.handoffSource = source
        workspace.setInspectorTab(.session)
        workspace.reloadInspector()
        XCTAssertEqual(workspace.inspector.model.sourceConversation, source)
        workspace.inspector.onSource?(source)
        XCTAssertEqual(app.openedHandoffSources, [source])
        app.store.apply(snapshot: F.snapshot([initial[0]]))
        XCTAssertTrue(NSApp.sendAction(try XCTUnwrap(handoff.action), to: handoff.target, from: handoff))
        XCTAssertEqual(app.handoffRequests.count, 1, "an ended target cannot be handed off from a stale menu")
    }

    func testNewSessionMenuTargetsClickedRowAndLaunchFocusWaitsForSnapshot() throws {
        let initial = [F.tmux(1, project: "api"), F.tmux(2, project: "api"), F.tmux(3, project: "api")]
        app.store.apply(snapshot: F.snapshot(initial))
        let parent = AttentionCycler.Key.embedded(F.runtimeID(2))
        workspace.select(.embedded(F.runtimeID(1)))
        let menu = workspace.contextMenuItems(forRow: try index(of: parent))
        let item = try XCTUnwrap(menu.first { $0.title == WorkspaceWindowController.newSessionTitle })
        _ = try XCTUnwrap(item.target).perform(try XCTUnwrap(item.action), with: item)
        XCTAssertEqual(app.newSessionRequests.map(\.key), [parent])
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)), "opening the dialog preserves the shown session")
        workspace.openNewSession(F.runtimeID(4), below: parent)
        XCTAssertEqual(workspace.awaited?.runtimeSessionID, F.runtimeID(4))
        app.store.apply(snapshot: F.snapshot(initial + [F.tmux(4, project: "api", profile: "codex", harness: "codex")]))
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(4)))
        XCTAssertEqual(workspace.order.projects["api"], [F.runtimeID(1), F.runtimeID(2), F.runtimeID(4), F.runtimeID(3)])
        XCTAssertNil(workspace.awaited)
        XCTAssertNil(workspace.newSessionBelow)
        app.store.apply(snapshot: F.snapshot(initial + [F.tmux(4, project: "api", profile: "codex", harness: "codex")]))
        XCTAssertEqual(workspace.order.projects["api"], [F.runtimeID(1), F.runtimeID(2), F.runtimeID(4), F.runtimeID(3)])
    }

    func testTheSessionMenuFollowsTheSessions() throws {
        XCTAssertNil(workspace.sessionName(at: 0), "nothing listed, nothing to go to")
        XCTAssertEqual(WorkspaceWindowController.sessionItemTitle(at: 6, name: nil), "Session 7")
        XCTAssertEqual(workspace.pinShownTitle, "Pin Session")
        XCTAssertNil(workspace.shownSessionRow, "so the pin item is greyed out")
        XCTAssertFalse(workspace.canOpenTerminalBesideSession)
        XCTAssertFalse(workspace.openTerminalBesideSession(), "refused with nothing shown")

        app.store.apply(snapshot: F.snapshot(sessions))
        let rows = workspace.state.rows
        XCTAssertEqual(rows.count, 3)
        for (index, row) in rows.enumerated() {
            XCTAssertEqual(workspace.sessionName(at: index), row.displayName)
        }
        for index in rows.count..<WorkspaceMenu.sessionKeys.count {
            XCTAssertNil(workspace.sessionName(at: index), "⌘\(WorkspaceMenu.sessionKeys[index]) has no session to go to")
        }
        XCTAssertEqual(WorkspaceWindowController.sessionItemTitle(at: 0, name: rows[0].displayName),
                       "Session 1: \(rows[0].displayName)")

        let work = try row("api", "work")
        workspace.activate(work)
        XCTAssertEqual(workspace.pinShownTitle, "Pin Session")
        XCTAssertTrue(workspace.canOpenTerminalBesideSession)
        XCTAssertTrue(workspace.togglePinOfShownSession())
        XCTAssertEqual(workspace.pinShownTitle, "Unpin Session")
        XCTAssertEqual(workspace.sessionName(at: 0), work.displayName, "a pinned session is listed first")

        workspace.select(try row("web", "work").key)
        XCTAssertNotNil(workspace.shownSessionRow)
        XCTAssertEqual(workspace.pinShownTitle, "Pin Session")
        XCTAssertTrue(workspace.canOpenTerminalBesideSession)
    }

    /// Reattach is offered only on a detached session's line, and reattaches
    /// it even when the pane is somewhere else.
    func testDetachedLinesOfferReattach() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        let work = try row("api", "work")
        let other = try row("web", "work")
        workspace.activate(work)
        XCTAssertFalse(workspace.contextMenuItems(forRow: try index(of: work.key)).map(\.title).contains("Reattach"))
        workspace.terminalEnded(try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1))))
        XCTAssertEqual(workspace.content, .detached(runtimeSessionID: F.runtimeID(1)))
        workspace.select(other.key)
        let items = workspace.contextMenuItems(forRow: try index(of: work.key))
        XCTAssertEqual(items.map(\.title), ["Peek at Session", "Name Session…", "Color…", "Icon…", "Mute", "Pin to Top", "", "Reattach", "New Terminal Below",
                                            "New Session Below…", "Continue with Another Agent…", "Copy Runtime Session ID", "", "End Session…", "", "Mark Accounted For", "Edit Session Note…", "Undo Annotation"])
        let reattach = try XCTUnwrap(items.first { $0.title == "Reattach" })
        _ = try XCTUnwrap(reattach.target).perform(try XCTUnwrap(reattach.action), with: reattach)
        XCTAssertEqual(workspace.state.selection, work.key)
        XCTAssertEqual(app.made, [F.runtimeID(1), F.runtimeID(4), F.runtimeID(1)])
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)))
        XCTAssertFalse(workspace.contextMenuItems(forRow: try index(of: work.key)).map(\.title).contains("Reattach"))
    }
}
