import AppKit
import XCTest
@testable import AgentMux

@MainActor
final class WorkspacePanesTests: XCTestCase {
    private typealias F = WorkspaceFixture
    private var app: FakeWorkspaceApp!
    private var workspace: WorkspaceWindowController!
    private var surfaces: [String: NSView] = [:]
    private let one = F.runtimeID(1)
    private let two = F.runtimeID(2)

    override func setUp() async throws {
        app = FakeWorkspaceApp()
        app.factory = { [weak self] id in
            let view = FocusableView()
            self?.surfaces[id] = view
            return view
        }
        workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        app.store.onChange = { [weak self] in self?.workspace.refresh() }
        workspace.openForTesting()
        app.store.apply(snapshot: F.snapshot([F.tmux(1, project: "api"), F.tmux(2, project: "web")]))
        workspace.select(.embedded(one), focus: true)
    }

    override func tearDown() async throws {
        workspace.window.close()
        workspace = nil
        app = nil
        surfaces.removeAll()
    }

    func testSplitFocusSwapAndSelectReuseAttachments() throws {
        XCTAssertTrue(workspace.split(with: .embedded(two)))
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: two))
        XCTAssertEqual(workspace.otherContent, .terminal(runtimeSessionID: one))
        XCTAssertTrue(workspace.activeOnRight)
        let first = try XCTUnwrap(surfaces[one])
        let second = try XCTUnwrap(surfaces[two])
        XCTAssertFalse(first.isHiddenOrHasHiddenAncestor)
        XCTAssertFalse(second.isHiddenOrHasHiddenAncestor)
        workspace.window.contentView?.layoutSubtreeIfNeeded()
        XCTAssertGreaterThan(first.bounds.width, 100)
        XCTAssertGreaterThan(second.bounds.height, 100)
        XCTAssertFalse(first.superview === second.superview)
        XCTAssertTrue(workspace.focusOtherPane())
        XCTAssertEqual(workspace.state.selection, .embedded(one))
        XCTAssertTrue(workspace.window.firstResponder === first)
        XCTAssertFalse(workspace.activeOnRight)
        workspace.swapPanes()
        XCTAssertTrue(workspace.activeOnRight)
        XCTAssertEqual(workspace.state.selection, .embedded(one))
        // Picking the already-visible session focuses its pane, without a
        // duplicate view or attachment and without blanking the other pane.
        workspace.select(.embedded(two), focus: true)
        XCTAssertEqual(workspace.otherContent, .terminal(runtimeSessionID: one))
        XCTAssertEqual(app.made, [one, two])
        XCTAssertEqual(app.closed, [])
    }

    func testFocusingViaHeadingPreservesPhysicalPositionAndDivider() throws {
        workspace.split(with: .embedded(two))
        workspace.window.contentView?.layoutSubtreeIfNeeded()
        let first = try XCTUnwrap(surfaces[one])
        let second = try XCTUnwrap(surfaces[two])
        let host = try XCTUnwrap(first.superview?.superview as? WorkspacePaneLayout.Host)
        let layout = try XCTUnwrap(host.superview as? WorkspacePaneLayout)
        layout.setPosition(510, ofDividerAt: 0)
        workspace.window.contentView?.layoutSubtreeIfNeeded()
        let beforeFirst = first.convert(first.bounds, to: nil)
        let beforeSecond = second.convert(second.bounds, to: nil)
        host.heading.performClick(nil)
        workspace.window.contentView?.layoutSubtreeIfNeeded()
        XCTAssertEqual(workspace.state.selection, .embedded(one))
        XCTAssertEqual(first.convert(first.bounds, to: nil), beforeFirst)
        XCTAssertEqual(second.convert(second.bounds, to: nil), beforeSecond)
        XCTAssertEqual(app.made, [one, two])
    }

    func testCollapseHidesWithoutEndingAndResplitReuses() {
        workspace.split(with: .embedded(two))
        workspace.collapseOtherPane()
        XCTAssertFalse(workspace.isSplit)
        XCTAssertTrue(surfaces[one]!.isHidden)
        XCTAssertFalse(surfaces[two]!.isHidden)
        XCTAssertEqual(app.closed, [])
        workspace.split(with: .embedded(one))
        XCTAssertEqual(app.made, [one, two])
        XCTAssertEqual(app.ended, [])
    }

    func testDetachedInactivePaneNeverReattachesOnRefreshOrFocus() {
        workspace.split(with: .embedded(two))
        workspace.terminalEnded(surfaces[one]!)
        XCTAssertEqual(workspace.otherContent, .detached(runtimeSessionID: one))
        workspace.refresh()
        workspace.focusOtherPane()
        XCTAssertEqual(workspace.content, .detached(runtimeSessionID: one))
        XCTAssertEqual(app.made, [one, two])
        XCTAssertEqual(app.closed, [one])
    }

    func testLostInactiveSessionDoesNotDisturbActivePane() {
        workspace.split(with: .embedded(two))
        app.store.apply(snapshot: F.snapshot([F.tmux(2, project: "web")]))
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: two))
        XCTAssertEqual(workspace.otherContent, .gone(runtimeSessionID: one, nil))
        workspace.refresh()
        XCTAssertEqual(app.made, [one, two])
        XCTAssertEqual(app.closed, [one])
    }

    func testSplitShellKeepsAgentVisibleAndShellSurvivesCollapse() {
        XCTAssertTrue(workspace.splitWithShell())
        let shell = workspace.viewSelection!
        XCTAssertEqual(workspace.content, .view(id: shell))
        XCTAssertEqual(workspace.otherContent, .terminal(runtimeSessionID: one))
        workspace.focusOtherPane()
        workspace.collapseOtherPane()
        XCTAssertEqual(app.closedViews, [])
        workspace.showView(shell)
        XCTAssertEqual(app.madeViews.filter { $0 == shell }.count, 1)
    }

    func testClosingInactiveShellCollapsesItWithoutChangingAgent() {
        workspace.splitWithShell()
        let shell = workspace.viewSelection!
        workspace.focusOtherPane()
        workspace.closeView(shell)
        XCTAssertFalse(workspace.isSplit)
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: one))
        XCTAssertEqual(app.closedViews, [shell])
        XCTAssertEqual(app.ended, [])
    }

    func testReviewFocusAndCloseRetainsAgentAndProtectsPendingLanding() {
        let review = FocusableView()
        var landing = true
        var closes = 0
        XCTAssertTrue(workspace.splitWithReview(review, canClose: { !landing }, onClose: { closes += 1 }))
        XCTAssertEqual(workspace.content, .review)
        XCTAssertTrue(workspace.window.firstResponder === review)
        XCTAssertFalse(workspace.windowShouldClose(workspace.window))
        workspace.closeShown()
        XCTAssertTrue(workspace.isSplit)
        XCTAssertEqual(closes, 0)
        landing = false
        workspace.closeShown()
        XCTAssertEqual(closes, 1)
        XCTAssertFalse(workspace.isSplit)
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: one))
        XCTAssertEqual(app.closed, [])
    }

    func testHiddenReviewCanBeFocusedWhileAnOperationIsPending() {
        let review = FocusableView()
        workspace.splitWithReview(review, canClose: { false }, onClose: {})
        workspace.showView(WorkspaceUtilityView.dashboard.id)
        XCTAssertTrue(review.isHidden)
        XCTAssertTrue(workspace.hasReview)
        XCTAssertTrue(workspace.focusReview())
        XCTAssertEqual(workspace.content, .review)
        XCTAssertFalse(review.isHidden)
        XCTAssertEqual(workspace.otherContent, .terminal(runtimeSessionID: one))
        XCTAssertTrue(workspace.window.firstResponder === review)
    }

    func testClosingReviewWorkspaceRetainsAgentForSafeReopen() {
        workspace.splitWithReview(FocusableView(), canClose: { true }, onClose: {})
        XCTAssertEqual(app.places.last!, .session(runtimeSessionID: one))
        workspace.window.close()
        XCTAssertEqual(workspace.state.selection, .embedded(one))
        app.statuses[one] = .live(clients: 1)
        workspace.openForTesting()
        XCTAssertFalse(workspace.isSplit)
        XCTAssertEqual(workspace.content, .detached(runtimeSessionID: one))
        XCTAssertEqual(app.made, [one], "reopening must not steal an attachment")
    }

    func testDisconnectedFeedKeepsBothPanesWithoutNewAttachments() {
        workspace.split(with: .embedded(two))
        app.store.apply(.disconnected(.heartbeatLost, retryIn: 0.5))
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: two))
        XCTAssertEqual(workspace.otherContent, .terminal(runtimeSessionID: one))
        XCTAssertFalse(surfaces[one]!.isHiddenOrHasHiddenAncestor)
        XCTAssertFalse(surfaces[two]!.isHiddenOrHasHiddenAncestor)
        workspace.focusOtherPane()
        XCTAssertEqual(app.made, [one, two])
    }

    func testWindowCloseDetachesBothAgentsWithoutEndingEither() {
        workspace.split(with: .embedded(two))
        workspace.window.close()
        XCTAssertEqual(Set(app.closed), Set([one, two]))
        XCTAssertEqual(app.ended, [])
        XCTAssertFalse(workspace.isSplit)
    }

    func testFailedInactiveAttachmentIsNotRetriedByRefresh() {
        app.problem = "renderer unavailable"
        workspace.split(with: .embedded(two))
        workspace.focusOtherPane()
        app.problem = nil
        workspace.refresh()
        XCTAssertEqual(workspace.otherContent, .unavailable(runtimeSessionID: two, "renderer unavailable"))
        XCTAssertEqual(app.made, [one])
    }
}
