import AppKit
import GhosttyKit
import XCTest
@testable import AgentMux

/// A utility view's find bar, scrollbar, and progress, and the selection
/// pasteboard: the model under them and what the workspace does with what
/// its terminals report.
@MainActor
final class WorkspaceTerminalOverlaysTests: XCTestCase {
    private typealias F = WorkspaceFixture
    private var app: FakeWorkspaceApp!
    private var workspace: WorkspaceWindowController!
    private var performed: [String] = []
    private var performedOn: [NSView] = []

    override func setUp() async throws {
        app = FakeWorkspaceApp()
        workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        app.store.onChange = { [weak self] in self?.workspace.refresh() }
        workspace.openForTesting()
        performed = []
        performedOn = []
        workspace.performTerminalAction = { [unowned self] view, action in
            self.performed.append(action)
            self.performedOn.append(view)
            return true
        }
        app.store.apply(snapshot: F.snapshot([F.tmux(1, project: "api", profile: "work")]))
    }

    override func tearDown() async throws {
        workspace.window.close()
        workspace = nil
        app = nil
    }

    private var dashboard: String { WorkspaceUtilityView.dashboard.id }

    func testServerFindOffersConversationSearchOnlyForItsOriginalSession() throws {
        app.store.apply(snapshot: F.snapshot([F.tmux(1, project: "api", conversation: "one"), F.tmux(2, project: "web", conversation: "two")]))
        workspace.activate(try XCTUnwrap(workspace.state.rows.first))
        let original = try XCTUnwrap(workspace.shownTerminalView)
        workspace.window.makeFirstResponder(original)
        workspace.showConversationSearchHint(for: original)
        XCTAssertFalse(workspace.conversationSearchButton.isHidden)
        workspace.conversationSearchButton.performClick(nil)
        XCTAssertEqual(app.conversationSearchRequests, 1)
        XCTAssertTrue(workspace.window.firstResponder === original)
        workspace.activate(try XCTUnwrap(workspace.state.rows.last))
        XCTAssertTrue(workspace.conversationSearchButton.isHidden)
        workspace.conversationSearchButton.performClick(nil)
        XCTAssertEqual(app.conversationSearchRequests, 1, "an old hint cannot search a different session")
        workspace.showConversationSearchHint(for: original)
        XCTAssertTrue(workspace.conversationSearchButton.isHidden)
    }

    private func viewCell(_ id: String) throws -> WorkspaceRowCellView {
        let index = try XCTUnwrap(workspace.items.firstIndex { item in
            if case let .view(view) = item { return view.id == id }
            return false
        })
        return try XCTUnwrap(workspace.tableView(workspace.sidebar, viewFor: nil, row: index) as? WorkspaceRowCellView)
    }

    // MARK: Model

    func testASearchFollowsWhatGhosttyReports() {
        typealias S = Ghostty.Search
        XCTAssertNil(S.applying(.total(3), to: nil), "a count with no search open starts none")
        var search = S.applying(.started(needle: ""), to: nil)
        XCTAssertEqual(search, S())
        search = S.applying(.started(needle: "error"), to: search)
        XCTAssertEqual(search?.needle, "error", "a search from a selection brings its needle")
        search?.needle = "warn"
        search = S.applying(.started(needle: ""), to: search)
        XCTAssertEqual(search?.needle, "warn", "asked for again, the needle typed stays")
        search = S.applying(.total(12), to: search)
        search = S.applying(.selected(2), to: search)
        XCTAssertEqual(search, S(needle: "warn", total: 12, selected: 2))
        XCTAssertNil(S.applying(.ended, to: search))
    }

    func testTheFindBarCountsMatchesAndWaitsOnShortNeedles() {
        typealias B = WorkspaceFindBar
        XCTAssertEqual(B.countText(Ghostty.Search()), "")
        XCTAssertEqual(B.countText(Ghostty.Search(needle: "a", total: nil)), "", "still counting")
        XCTAssertEqual(B.countText(Ghostty.Search(needle: "a", total: 0)), "No matches")
        XCTAssertEqual(B.countText(Ghostty.Search(needle: "a", total: 1)), "1 match")
        XCTAssertEqual(B.countText(Ghostty.Search(needle: "a", total: 7)), "7 matches")
        XCTAssertEqual(B.countText(Ghostty.Search(needle: "a", total: 7, selected: 0)), "1 of 7")
        XCTAssertNil(B.delay(for: ""), "clearing the field is sent at once")
        XCTAssertNotNil(B.delay(for: "ab"))
        XCTAssertNil(B.delay(for: "abc"))
    }

    func testTheFindBarFollowsLightAndDark() throws {
        let bar = WorkspaceFindBar()
        XCTAssertTrue(bar.wantsUpdateLayer)
        func colors(_ name: NSAppearance.Name) throws -> (CGColor?, CGColor?, CGColor, CGColor) {
            let appearance = try XCTUnwrap(NSAppearance(named: name))
            bar.appearance = appearance
            bar.updateLayer()
            var expected: (CGColor, CGColor)!
            appearance.performAsCurrentDrawingAppearance {
                expected = (NSColor.windowBackgroundColor.cgColor, NSColor.separatorColor.cgColor)
            }
            return (bar.layer?.backgroundColor, bar.layer?.borderColor, expected.0, expected.1)
        }
        let light = try colors(.aqua)
        XCTAssertEqual(light.0, light.2)
        XCTAssertEqual(light.1, light.3)
        let dark = try colors(.darkAqua)
        XCTAssertEqual(dark.0, dark.2)
        XCTAssertEqual(dark.1, dark.3)
        XCTAssertNotEqual(light.0, dark.0, "the background changes with the appearance")
    }

    func testOnlyTheScrollbarsKnobTakesThePointer() {
        let host = NSView(frame: NSRect(x: 0, y: 0, width: 400, height: 200))
        let bar = WorkspaceScrollbarView(frame: NSRect(x: 390, y: 0, width: 10, height: 200))
        host.addSubview(bar)
        bar.scrollbar = Ghostty.Scrollbar(total: 124, offset: 0, len: 24)
        let knob = WorkspaceScrollbarView.knobRect(bar.scrollbar!, in: bar.bounds)
        XCTAssertTrue(bar.hitTest(NSPoint(x: 395, y: knob.midY)) === bar, "the knob is taken")
        XCTAssertNil(bar.hitTest(NSPoint(x: 395, y: 5)), "the track is the terminal's")
        bar.scrollbar = Ghostty.Scrollbar(total: 24, offset: 0, len: 24)
        XCTAssertNil(bar.hitTest(NSPoint(x: 395, y: 195)), "nothing to scroll, nothing taken")
    }

    func testTheScrollbarMapsRowsToTheKnobAndBack() {
        let bottom = Ghostty.Scrollbar(total: 124, offset: 100, len: 24)
        XCTAssertTrue(bottom.scrolls)
        XCTAssertEqual(bottom.position, 1)
        XCTAssertEqual(bottom.knobProportion, 24.0 / 124.0, accuracy: 0.0001)
        XCTAssertEqual(bottom.row(at: 0), 0)
        XCTAssertEqual(bottom.row(at: 0.5), 50)
        XCTAssertEqual(bottom.row(at: 2), 100, "clamped to the last viewport")
        XCTAssertFalse(Ghostty.Scrollbar(total: 24, offset: 0, len: 24).scrolls, "no history, no scrollbar")

        let bounds = NSRect(x: 0, y: 0, width: 10, height: 200)
        let top = Ghostty.Scrollbar(total: 124, offset: 0, len: 24)
        let topKnob = WorkspaceScrollbarView.knobRect(top, in: bounds)
        XCTAssertEqual(topKnob.maxY, bounds.maxY, "the top of history at the top")
        XCTAssertEqual(WorkspaceScrollbarView.knobRect(bottom, in: bounds).minY, 0, accuracy: 0.001)
        XCTAssertEqual(WorkspaceScrollbarView.knobRect(Ghostty.Scrollbar(total: 100_000, offset: 0, len: 24), in: bounds).height,
                       WorkspaceScrollbarView.minimumKnob, "a long history keeps a knob that can be taken")
        XCTAssertEqual(WorkspaceScrollbarView.position(knobMidY: topKnob.midY, knobHeight: topKnob.height, in: bounds), 0,
                       accuracy: 0.001)
        XCTAssertEqual(WorkspaceScrollbarView.position(knobMidY: -50, knobHeight: topKnob.height, in: bounds), 1)
    }

    func testProgressReportsReadGhosttysStatesAndNameThemOnTheLine() {
        typealias P = Ghostty.Progress
        XCTAssertNil(P(ghostty_action_progress_report_s(state: GHOSTTY_PROGRESS_STATE_REMOVE, progress: 10)))
        XCTAssertEqual(P(ghostty_action_progress_report_s(state: GHOSTTY_PROGRESS_STATE_SET, progress: 45)),
                       P(state: .normal, percent: 45))
        XCTAssertEqual(P(ghostty_action_progress_report_s(state: GHOSTTY_PROGRESS_STATE_INDETERMINATE, progress: -1)),
                       P(state: .indeterminate, percent: nil))
        XCTAssertEqual(P(ghostty_action_progress_report_s(state: GHOSTTY_PROGRESS_STATE_ERROR, progress: 80))?.state, .error)
        XCTAssertEqual(P(ghostty_action_progress_report_s(state: GHOSTTY_PROGRESS_STATE_PAUSE, progress: -1))?.state, .paused)

        typealias C = WorkspaceRowCellView
        XCTAssertEqual(C.progressText(P(state: .normal, percent: 45)), "45%")
        XCTAssertEqual(C.progressText(P(state: .normal, percent: nil)), "in progress")
        XCTAssertEqual(C.progressText(P(state: .indeterminate, percent: nil)), "in progress")
        XCTAssertEqual(C.progressText(P(state: .paused, percent: 30)), "paused at 30%")
        XCTAssertEqual(C.progressText(P(state: .error, percent: nil)), "failed")
    }

    /// As upstream Ghostty.app: a selection goes to a private pasteboard,
    /// never the general one, so selecting text leaves what ⌘C copied.
    func testTheSelectionClipboardIsAPrivatePasteboard() {
        XCTAssertEqual(Ghostty.App.pasteboard(for: GHOSTTY_CLIPBOARD_STANDARD), .general)
        let selection = Ghostty.App.pasteboard(for: GHOSTTY_CLIPBOARD_SELECTION)
        XCTAssertNotNil(selection)
        XCTAssertNotEqual(selection, .general)
        XCTAssertEqual(selection?.name.rawValue, "io.github.sglavoie.agentmux.selection")
    }

    // MARK: Workspace

    func testAUtilityViewsSearchOpensTheFindBarAndDrivesGhostty() throws {
        XCTAssertTrue(workspace.showView(dashboard))
        let terminal = try XCTUnwrap(workspace.utilityTerminalView(for: dashboard))
        XCTAssertTrue(workspace.findBar.isHidden)

        workspace.terminalSearch(terminal, .started(needle: ""))
        XCTAssertFalse(workspace.findBar.isHidden)
        XCTAssertEqual(workspace.searches[dashboard], Ghostty.Search())

        workspace.findBarNeedleChanged("error")
        workspace.findBarNavigate(next: true)
        workspace.findBarNavigate(next: false)
        XCTAssertEqual(performed, ["search:error", "navigate_search:next", "navigate_search:previous"])
        workspace.terminalSearch(terminal, .total(3))
        workspace.terminalSearch(terminal, .selected(0))
        XCTAssertEqual(workspace.findBar.countLabel.stringValue, "1 of 3")

        // Another view leaves the bar behind; coming back brings it.
        workspace.activate(try XCTUnwrap(workspace.state.rows.first))
        XCTAssertTrue(workspace.findBar.isHidden, "a session's search is the server's")
        XCTAssertTrue(workspace.showView(dashboard))
        XCTAssertFalse(workspace.findBar.isHidden)
        XCTAssertEqual(workspace.findBar.field.stringValue, "error")

        performed = []
        workspace.closeFindBar()
        XCTAssertEqual(performed, ["end_search"])
        XCTAssertTrue(workspace.findBar.isHidden)
        XCTAssertNil(workspace.searches[dashboard])
        workspace.findBarNavigate(next: true)
        XCTAssertEqual(performed, ["end_search"], "no search, nothing to go to")
    }

    func testSeededUtilitySearchRunsWithoutEditing() throws {
        XCTAssertTrue(workspace.showView(dashboard))
        let terminal = try XCTUnwrap(workspace.utilityTerminalView(for: dashboard))
        performed = []
        performedOn = []

        // ⌘E names the selection; nothing is typed, yet it is searched for.
        workspace.terminalSearch(terminal, .started(needle: "error"))
        XCTAssertEqual(performed, ["search:error"])
        XCTAssertTrue(performedOn.first === terminal)
        XCTAssertEqual(workspace.findBar.field.stringValue, "error")
        XCTAssertEqual(workspace.searches[dashboard]?.needle, "error")

        // Asked again with nothing selected, the search stays as it was.
        workspace.terminalSearch(terminal, .started(needle: ""))
        XCTAssertEqual(performed, ["search:error"])

        // Another selection replaces the active query, and a short needle
        // still being typed never overtakes it.
        workspace.findBar.needleChanged("ab")
        workspace.terminalSearch(terminal, .total(2))
        workspace.terminalSearch(terminal, .started(needle: "warn"))
        waitOutTheFindDelay()
        XCTAssertEqual(performed, ["search:error", "search:warn"])
        XCTAssertTrue(performedOn.allSatisfy { $0 === terminal })
        XCTAssertEqual(workspace.searches[dashboard]?.needle, "warn")
        XCTAssertEqual(workspace.findBar.field.stringValue, "warn")
    }

    /// Lets the find bar's short-needle delay run out.
    private func waitOutTheFindDelay() {
        let delivered = expectation(description: "the find delay ran out")
        DispatchQueue.main.asyncAfter(deadline: .now() + (WorkspaceFindBar.delay(for: "ab") ?? 0) + 0.2) {
            delivered.fulfill()
        }
        wait(for: [delivered], timeout: 5)
    }

    func testPendingFindNeedleCannotCrossUtilityViews() throws {
        let overview = WorkspaceUtilityView.overview.id
        XCTAssertTrue(workspace.showView(overview))
        let other = try XCTUnwrap(workspace.utilityTerminalView(for: overview))
        workspace.terminalSearch(other, .started(needle: "warn"))
        XCTAssertTrue(workspace.showView(dashboard))
        let terminal = try XCTUnwrap(workspace.utilityTerminalView(for: dashboard))
        workspace.terminalSearch(terminal, .started(needle: ""))
        performed = []
        performedOn = []

        workspace.findBar.needleChanged("ab")
        XCTAssertEqual(performed, [], "a short needle waits")
        XCTAssertTrue(workspace.showView(overview))
        waitOutTheFindDelay()

        XCTAssertEqual(performed, [], "typed in one view, it reaches no other")
        XCTAssertFalse(performedOn.contains { $0 === other })
        XCTAssertEqual(workspace.searches[overview]?.needle, "warn")
        XCTAssertEqual(workspace.searches[dashboard]?.needle, "")
    }

    func testPendingFindNeedleCannotReachReopenedSearch() throws {
        XCTAssertTrue(workspace.showView(dashboard))
        let terminal = try XCTUnwrap(workspace.utilityTerminalView(for: dashboard))
        workspace.terminalSearch(terminal, .started(needle: ""))

        workspace.findBar.needleChanged("ab")
        workspace.closeFindBar()
        workspace.terminalSearch(terminal, .started(needle: ""))
        performed = []
        performedOn = []
        waitOutTheFindDelay()
        XCTAssertEqual(performed, [], "typed into a search since closed, it reaches no new one")
        XCTAssertEqual(workspace.searches[dashboard]?.needle, "")

        // Typing into the search open now still waits, then is sent.
        workspace.findBar.needleChanged("cd")
        XCTAssertEqual(performed, [])
        workspace.terminalSearch(terminal, .total(4))
        waitOutTheFindDelay()
        XCTAssertEqual(performed, ["search:cd"])
        XCTAssertTrue(performedOn.first === terminal)
        XCTAssertEqual(workspace.searches[dashboard]?.needle, "cd")
    }

    func testOnlyAUtilityViewShowsAScrollbar() throws {
        workspace.activate(try XCTUnwrap(workspace.state.rows.first))
        let session = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))
        let history = Ghostty.Scrollbar(total: 500, offset: 100, len: 40)
        workspace.terminalScrollbar(session, history)
        XCTAssertTrue(workspace.scrollbarView.isHidden, "a session's history is the server's")

        XCTAssertTrue(workspace.showView(dashboard))
        let terminal = try XCTUnwrap(workspace.utilityTerminalView(for: dashboard))
        workspace.terminalScrollbar(terminal, Ghostty.Scrollbar(total: 40, offset: 0, len: 40))
        XCTAssertTrue(workspace.scrollbarView.isHidden, "nothing scrolled off yet")
        workspace.terminalScrollbar(terminal, history)
        XCTAssertFalse(workspace.scrollbarView.isHidden)
        XCTAssertTrue(workspace.scrollbarView.terminal === terminal)

        workspace.scrollbarMoved(toRow: 7)
        XCTAssertEqual(performed, ["scroll_to_row:7"])

        workspace.activate(try XCTUnwrap(workspace.state.rows.first))
        XCTAssertTrue(workspace.scrollbarView.isHidden)
    }

    func testProgressShowsOnAViewsLineAndIsNeverAttention() throws {
        XCTAssertTrue(workspace.showView(dashboard))
        let terminal = try XCTUnwrap(workspace.utilityTerminalView(for: dashboard))
        workspace.activate(try XCTUnwrap(workspace.state.rows.first))
        let attentionBefore = workspace.state.rows.map(\.attention)

        workspace.terminalProgress(terminal, Ghostty.Progress(state: .normal, percent: 45))
        let cell = try viewCell(dashboard)
        XCTAssertTrue(cell.detailLabel.stringValue.hasPrefix("45%"), cell.detailLabel.stringValue)
        XCTAssertTrue(try XCTUnwrap(cell.accessibilityLabel()).contains("45%"))
        XCTAssertEqual(workspace.ringingViews, [], "progress rings nothing")
        XCTAssertEqual(workspace.state.rows.map(\.attention), attentionBefore)

        workspace.terminalProgress(terminal, nil)
        XCTAssertFalse(try viewCell(dashboard).detailLabel.stringValue.contains("%"))

        let session = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))
        workspace.terminalProgress(session, Ghostty.Progress(state: .normal, percent: 10))
        XCTAssertTrue(workspace.progresses.isEmpty, "only a utility view's line shows progress")
    }
}
