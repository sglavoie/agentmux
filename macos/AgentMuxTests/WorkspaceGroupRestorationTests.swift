import AppKit
import XCTest
@testable import AgentMux

@MainActor
final class WorkspaceGroupRestorationTests: XCTestCase {
    typealias F = WorkspaceFixture
    var app: FakeWorkspaceApp!
    var workspace: WorkspaceWindowController!

    override func setUp() {
        app = FakeWorkspaceApp()
        app.workspaceProjects = [
            ProjectEntry(profile: "work", title: "API", groups: ["work", "shared"]),
            ProjectEntry(profile: "work", title: "Garden", groups: ["personal", "shared", "All"]),
            ProjectEntry(profile: "work", title: "Offline", groups: ["empty"]),
        ]
        workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        app.store.onChange = { [weak self] in self?.workspace.refresh() }
        workspace.openForTesting()
        app.store.apply(snapshot: F.snapshot([
            F.tmux(1, project: "API", conversation: "one"), F.tmux(2, project: "API"),
            F.tmux(3, project: "Garden"), F.tmux(4, project: "Garden"),
        ]))
    }

    override func tearDown() {
        workspace.window.close()
        app.store.onChange = nil
        workspace = nil
        app = nil
    }

    func pane(_ n: Int) -> WorkspaceRestoration.Pane { .session("session:" + F.runtimeID(n)) }
    func context(_ n: Int, other: Int? = nil) -> WorkspaceRestoration.GroupContext {
        .init(layout: .init(active: pane(n), other: other.map(pane), activeOnRight: false))
    }

    func testGroupsRememberIndependentSelectionsIncludingAllAndOverlappingMembership() {
        workspace.select(.embedded(F.runtimeID(4)))
        workspace.selectGroup("work")
        workspace.select(.embedded(F.runtimeID(2)))
        workspace.selectGroup("personal")
        workspace.select(.embedded(F.runtimeID(3)))
        workspace.selectGroup("shared")
        workspace.select(.embedded(F.runtimeID(1)))
        for (group, id) in [("work", 2), ("personal", 3), ("shared", 1)] {
            workspace.selectGroup(group)
            XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(id)))
        }
        workspace.selectGroup(nil)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(4)))
        workspace.selectGroup("All")
        workspace.select(.embedded(F.runtimeID(3)))
        workspace.selectGroup("All")
        XCTAssertNil(workspace.selectedGroup)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(4)))
        XCTAssertEqual(Set(app.made).count, app.made.count, "returning to a group reuses its terminal")
        XCTAssertTrue(app.closed.isEmpty)
    }

    func testSplitLayoutsRestoreTheirActivePaneSideAndRatio() throws {
        workspace.window.setContentSize(NSSize(width: 2000, height: 900))
        workspace.selectGroup("work")
        XCTAssertTrue(workspace.split(with: .embedded(F.runtimeID(2))))
        workspace.window.layoutIfNeeded()
        workspace.paneLayout.restoreFraction(0.62)
        workspace.reportLayout()
        let work = try XCTUnwrap(workspace.groupContexts["group:work"])
        workspace.selectGroup("personal")
        XCTAssertFalse(workspace.isSplit)
        XCTAssertTrue(workspace.split(with: .embedded(F.runtimeID(4))))
        workspace.swapPanes()
        workspace.paneLayout.restoreFraction(0.4)
        workspace.reportLayout()
        let personal = try XCTUnwrap(workspace.groupContexts["group:personal"])
        let made = app.made
        for (group, saved) in [("work", work), ("personal", personal)] {
            workspace.selectGroup(group)
            XCTAssertEqual(workspace.groupContexts["group:" + group]?.layout.active, saved.layout.active)
            XCTAssertEqual(workspace.groupContexts["group:" + group]?.layout.other, saved.layout.other)
            XCTAssertEqual(workspace.activeOnRight, saved.layout.activeOnRight)
            XCTAssertEqual(try XCTUnwrap(workspace.paneLayout.fraction), try XCTUnwrap(saved.layout.fraction), accuracy: 0.02)
        }
        XCTAssertEqual(app.made, made)
        XCTAssertTrue(app.closed.isEmpty)
    }

    func testRestartRestoresSavedGroupAndLaterSwitchRestoresAnotherGroupsSplit() throws {
        let suite = "amx.tests.group-restoration.\(UUID())"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        workspace.onGroupContextsChanged = { WorkspaceRestoration.saveGroupContexts($0, to: defaults) }
        app.onGroupChanged = { WorkspaceRestoration.saveGroup($0, to: defaults) }
        workspace.window.setContentSize(NSSize(width: 2000, height: 900))
        workspace.selectGroup("work")
        workspace.split(with: .embedded(F.runtimeID(2)))
        workspace.window.layoutIfNeeded()
        workspace.paneLayout.restoreFraction(0.62)
        workspace.reportLayout()
        workspace.selectGroup("personal")
        workspace.select(.embedded(F.runtimeID(4)))
        workspace.window.close()
        let saved = WorkspaceRestoration.load(from: defaults)
        let next = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        defer { next.window.close() }
        next.window.setContentSize(NSSize(width: 2000, height: 900))
        next.restoreContext(saved)
        next.openForTesting()
        XCTAssertEqual(next.selectedGroup, "personal")
        XCTAssertEqual(next.content, .terminal(runtimeSessionID: F.runtimeID(4)))
        next.selectGroup("work")
        XCTAssertEqual(next.content, .terminal(runtimeSessionID: F.runtimeID(2)))
        XCTAssertEqual(next.otherContent, .terminal(runtimeSessionID: F.runtimeID(1)))
        XCTAssertTrue(next.activeOnRight)
        XCTAssertEqual(try XCTUnwrap(next.paneLayout.fraction), 0.62, accuracy: 0.02)
        defaults.set(Data("broken".utf8), forKey: WorkspaceRestoration.groupContextsKey)
        XCTAssertTrue(WorkspaceRestoration.load(from: defaults).groupContexts.isEmpty)
    }

    func testLegacyLayoutMigratesAndMissingSessionKeepsItsConversationWithoutRestart() {
        workspace.window.close()
        workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        var saved = WorkspaceRestoration()
        saved.selectedGroup = "work"
        saved.layout = .init(active: pane(9), other: pane(1), activeOnRight: true)
        saved.conversation = .init(harness: "claude", sessionID: "ended")
        app.statuses[F.runtimeID(9)] = .ended("")
        workspace.restoreContext(saved)
        workspace.openForTesting()
        XCTAssertEqual(workspace.gone?.runtimeSessionID, F.runtimeID(9))
        XCTAssertEqual(workspace.gone?.conversation, saved.conversation)
        workspace.selectGroup("personal")
        workspace.selectGroup("work")
        XCTAssertEqual(workspace.gone?.conversation, saved.conversation)
        XCTAssertFalse(app.made.contains(F.runtimeID(9)))
        XCTAssertTrue(app.resumeRequests.isEmpty)
    }

    func testSwitchWaitsForLiveFeedAndDoesNotStealAnExternalAttachment() {
        workspace.groupContexts["group:work"] = context(2, other: 1)
        app.statuses[F.runtimeID(1)] = .live(clients: 1)
        app.statuses[F.runtimeID(2)] = .live(clients: 1)
        app.store.apply(.disconnected(.notRunning, retryIn: 1))
        workspace.selectGroup("work")
        XCTAssertTrue(workspace.choosingGroupPlace)
        XCTAssertTrue(app.made.isEmpty)
        app.store.apply(snapshot: F.snapshot([F.tmux(1, project: "API"), F.tmux(2, project: "API")]))
        XCTAssertEqual(workspace.content, .detached(runtimeSessionID: F.runtimeID(2)))
        XCTAssertEqual(workspace.otherContent, .detached(runtimeSessionID: F.runtimeID(1)))
        XCTAssertTrue(app.made.isEmpty)
    }

    func testRapidSwitchDiscardsBothInactiveAndActiveStatusReplies() {
        for split in [false, true] {
            workspace.selectGroup("personal")
            workspace.groupContexts["group:work"] = context(2, other: split ? 1 : nil)
            app.holdsAnswers = true
            workspace.selectGroup("work")
            workspace.selectGroup("personal")
            let selection = workspace.state.selection
            app.answer()
            XCTAssertEqual(workspace.state.selection, selection)
            XCTAssertEqual(workspace.selectedGroup, "personal")
            XCTAssertFalse(workspace.isSplit)
            XCTAssertFalse(app.made.contains(F.runtimeID(2)))
            app.holdsAnswers = false
            // Return to All so the next iteration's first click selects Personal.
            workspace.selectGroup(nil)
        }
    }

    func testRemovedGroupFallsBackToAllOnlyAfterSuccessfulCatalogRead() {
        workspace.select(.embedded(F.runtimeID(4)))
        workspace.selectGroup("work")
        app.workspaceProjectsLoaded = false
        app.workspaceProjects = []
        workspace.reloadSidebar()
        XCTAssertEqual(workspace.selectedGroup, "work")
        XCTAssertNotNil(workspace.groupContexts["group:work"])
        app.workspaceProjectsLoaded = true
        workspace.reloadSidebar()
        XCTAssertNil(workspace.selectedGroup)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(4)))
        XCTAssertNil(workspace.groupContexts["group:work"])
    }

    func testExternalRevealKeepsRequestedSessionAndPreviousGroupsContext() {
        workspace.selectGroup("work")
        workspace.select(.embedded(F.runtimeID(2)))
        workspace.open(.init(kind: WorkspaceViewRequest.sessionKind, runtimeSessionID: F.runtimeID(4), awaited: true))
        XCTAssertNil(workspace.selectedGroup)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(4)))
        workspace.selectGroup("work")
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
        workspace.selectGroup(nil)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(4)))
    }

    func testLiveShellIsReusedAndAnEmptyGroupHasNoUnrelatedPane() throws {
        workspace.selectGroup("work")
        XCTAssertTrue(workspace.splitWithShell())
        let shell = try XCTUnwrap(workspace.viewSelection)
        let terminal = workspace.utilityTerminalView(for: shell)
        workspace.selectGroup("empty")
        XCTAssertEqual(workspace.content, .empty)
        XCTAssertFalse(workspace.isSplit)
        workspace.selectGroup("work")
        XCTAssertEqual(workspace.content, .view(id: shell))
        XCTAssertEqual(workspace.otherContent, .terminal(runtimeSessionID: F.runtimeID(1)))
        XCTAssertTrue(workspace.utilityTerminalView(for: shell) === terminal)
        XCTAssertFalse(workspace.endedViews.contains(shell))
        XCTAssertTrue(app.closedViews.isEmpty)
    }

    func testGroupSwitchRestoresReviewAndWaitsForCheckoutOperations() {
        workspace.selectGroup("work")
        workspace.reviewDirectory = { "/api" }
        var busy = true
        var closes = 0
        workspace.splitWithReview(FocusableView(), canClose: { !busy }, onClose: { closes += 1 })
        workspace.selectGroup("personal")
        XCTAssertEqual(workspace.selectedGroup, "work")
        XCTAssertEqual(closes, 0)
        busy = false
        workspace.selectGroup("personal")
        XCTAssertEqual(closes, 1)
        workspace.makeRestoredReview = { path in
            XCTAssertEqual(path, "/api")
            return .init(view: FocusableView(), canClose: { true }, close: {}, directory: { path })
        }
        workspace.selectGroup("work")
        XCTAssertEqual(workspace.content, .review)
        XCTAssertEqual(workspace.otherContent, .terminal(runtimeSessionID: F.runtimeID(1)))
    }

    func testEmptyGroupSurvivesRestartWithoutFallingBackToTheGlobalLayout() {
        workspace.selectGroup("work")
        workspace.selectGroup("empty")
        var saved = WorkspaceRestoration()
        saved.selectedGroup = "empty"
        saved.layout = .init(active: pane(2), activeOnRight: false)
        saved.groupContexts = workspace.groupContexts
        let next = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        defer { next.window.close() }
        next.restoreContext(saved)
        next.openForTesting()
        next.showDashboardUnlessPlaced()
        XCTAssertEqual(next.selectedGroup, "empty")
        XCTAssertEqual(next.content, .empty)
        XCTAssertNil(next.state.selection)
        XCTAssertTrue(app.madeViews.isEmpty)
    }

    func testCatalogArrivalCompletesGroupSwitchWithoutAnotherSessionUpdate() {
        workspace.groupContexts["group:work"] = context(2)
        app.workspaceProjectsLoaded = false
        workspace.selectGroup("work")
        XCTAssertTrue(workspace.choosingGroupPlace)
        app.workspaceProjectsLoaded = true
        workspace.reloadSidebar()
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
    }

    func testRemovedStartupGroupRestoresAllAndDoesNotReturnToRemovedLayout() {
        workspace.window.close()
        workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        var saved = WorkspaceRestoration()
        saved.selectedGroup = "removed"
        saved.groupContexts = ["all": context(4), "group:removed": context(1)]
        workspace.restoreContext(saved)
        workspace.openForTesting()
        XCTAssertNil(workspace.selectedGroup)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(4)))
        XCTAssertNil(workspace.groupContexts["group:removed"])
    }

    func testWindowReopeningRestoresSplitAndChecksBothSessionOwners() {
        workspace.selectGroup("work")
        workspace.split(with: .embedded(F.runtimeID(2)))
        workspace.window.close()
        app.statuses[F.runtimeID(1)] = .live(clients: 1)
        app.statuses[F.runtimeID(2)] = .live(clients: 1)
        workspace.openForTesting()
        XCTAssertEqual(workspace.content, .detached(runtimeSessionID: F.runtimeID(2)))
        XCTAssertEqual(workspace.otherContent, .detached(runtimeSessionID: F.runtimeID(1)))
        XCTAssertEqual(app.made, [F.runtimeID(1), F.runtimeID(2)])
    }

    func testShellInSavedGroupIsNotRestartedByANewWindowController() throws {
        workspace.selectGroup("work")
        workspace.splitWithShell()
        let shell = try XCTUnwrap(workspace.viewSelection)
        var saved = WorkspaceRestoration()
        saved.selectedGroup = "work"
        saved.groupContexts = workspace.groupContexts
        let madeViews = app.madeViews
        let next = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        defer { next.window.close() }
        next.restoreContext(saved)
        next.openForTesting()
        XCTAssertEqual(next.content, .viewEnded(id: shell))
        XCTAssertEqual(app.madeViews, madeViews)
    }

    func testSessionMovedOutOfGroupFallsBackToAnInScopeSession() {
        workspace.groupContexts["group:work"] = context(4)
        workspace.selectGroup("work")
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
        XCTAssertEqual(workspace.groupContexts["group:work"]?.layout.active, pane(1))
    }

    func testSavedGroupNeverBorrowsAnotherGroupsLegacyConversation() {
        workspace.window.close()
        workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        var saved = WorkspaceRestoration()
        saved.selectedGroup = "work"
        saved.groupContexts = ["group:work": context(9)]
        saved.conversation = .init(harness: "claude", sessionID: "unrelated")
        app.statuses[F.runtimeID(9)] = .ended("")
        workspace.restoreContext(saved)
        workspace.openForTesting()
        XCTAssertEqual(workspace.gone?.runtimeSessionID, F.runtimeID(9))
        XCTAssertNil(workspace.gone?.conversation)
        XCTAssertNil(workspace.groupContexts["group:work"]?.conversation)
    }

    func testClickingAllWhileAlreadySelectedKeepsItsButtonAndContext() throws {
        workspace.select(.embedded(F.runtimeID(4)))
        let all = try XCTUnwrap(workspace.groupFilter.pills.first)
        all.performClick(nil)
        XCTAssertEqual(all.state, .on)
        XCTAssertNil(workspace.selectedGroup)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(4)))
    }

    func testNewLaunchCancelsPendingGroupRestoreBeforeItsSessionIsListed() {
        workspace.groupContexts["group:work"] = context(2, other: 1)
        app.holdsAnswers = true
        workspace.selectGroup("work")
        workspace.open(.init(kind: WorkspaceViewRequest.sessionKind, runtimeSessionID: F.runtimeID(9), awaited: true))
        app.answer()
        app.answer()
        XCTAssertEqual(workspace.awaited?.runtimeSessionID, F.runtimeID(9))
        app.store.apply(snapshot: F.snapshot([F.tmux(1, project: "API"), F.tmux(2, project: "API"), F.tmux(9, project: "Garden")]))
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(9)))
        XCTAssertNil(workspace.selectedGroup)
        XCTAssertFalse(app.made.contains(F.runtimeID(2)))
    }
}
