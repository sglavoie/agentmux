import AppKit
import XCTest
@testable import AgentMux

/// The Go to… list as data: what it lists and what a query keeps.
final class WorkspaceSwitcherTests: XCTestCase {
    private typealias F = WorkspaceFixture

    private func entries(_ sessions: [CompanionSession], views: [WorkspaceUtilityView] = WorkspaceViews().all) -> [WorkspaceSwitcher.Entry] {
        let rows = WorkspaceSidebar.groups(sessions, snapshotSocket: "unix:/tmp/kitty-1", glyphs: .fallback).flatMap(\.rows)
        return WorkspaceSwitcher.entries(rows: rows, views: views)
    }

    private let sessions = [
        F.tmux(1, project: "api", profile: "work"),
        F.tmux(2, project: "api", profile: "personal", harness: "codex"),
        F.kitty(4, project: "web"),
    ]

    func testListsTheSessionsInSidebarOrderThenTheViews() {
        let listed = entries(sessions)
        XCTAssertEqual(listed.map(\.title), ["api — personal", "api — work", "web — work", "Dashboard", "Overview"])
        XCTAssertEqual(listed[0].place, .session(.embedded(F.runtimeID(2))))
        XCTAssertEqual(listed[3].place, .view(WorkspaceUtilityView.dashboard.id))
        XCTAssertTrue(listed[2].detail.hasSuffix("Kitty"), "a Kitty session says where it runs")
    }

    func testAnOverlayViewIsNotAPlaceToGoTo() throws {
        var views = WorkspaceViews()
        views.open(.keybindings)
        views.open(try XCTUnwrap(WorkspaceUtilityView.picker(.iconPicker, runtimeSessionID: F.runtimeID(1))))
        views.open(WorkspaceUtilityView(kind: .shell, target: "/work/api", title: "api"))
        XCTAssertEqual(entries([], views: views.all).map(\.title), ["Dashboard", "Overview", "api"])
    }

    func testAQueryKeepsWhatHoldsEveryWord() {
        let listed = entries(sessions)
        XCTAssertEqual(WorkspaceSwitcher.matches("", in: listed), listed, "nothing typed keeps everything")
        XCTAssertEqual(WorkspaceSwitcher.matches("  ", in: listed), listed)
        XCTAssertEqual(WorkspaceSwitcher.matches("API", in: listed).map(\.title), ["api — personal", "api — work"])
        XCTAssertEqual(WorkspaceSwitcher.matches("work api", in: listed).map(\.title), ["api — work"], "the words need not be in order")
        XCTAssertEqual(WorkspaceSwitcher.matches("codex", in: listed).map(\.title), ["api — personal"], "a harness is searched too")
        XCTAssertEqual(WorkspaceSwitcher.matches("kitty", in: listed).map(\.title), ["web — work"])
        XCTAssertEqual(WorkspaceSwitcher.matches("nothing here", in: listed), [])
    }

    func testATitleThatStartsWithTheQueryComesFirst() {
        // "over" is in the Dashboard's detail and starts the Overview's title.
        let views = [
            WorkspaceUtilityView(kind: .shell, target: "/work/overlay", title: "shell"),
            WorkspaceUtilityView.overview,
        ]
        XCTAssertEqual(WorkspaceSwitcher.matches("over", in: entries([], views: views)).map(\.title), ["Overview", "shell"])
    }

    func testANamedSessionGoesByItsNameAndIsStillFoundByItsProject() throws {
        var named = F.tmux(1, project: "api", profile: "work")
        named.name = "release notes"
        let entry = try XCTUnwrap(entries([named]).first)
        XCTAssertEqual(entry.title, "release notes")
        XCTAssertTrue(entry.detail.hasPrefix("api — work"))
        XCTAssertEqual(WorkspaceSwitcher.matches("api", in: [entry]), [entry])
        XCTAssertEqual(WorkspaceSwitcher.matches("release", in: [entry]), [entry])
    }
}

/// The Go to… list in the workspace window: it takes the keyboard while it is
/// up, goes where a click on the sidebar would, and starts nothing.
@MainActor
final class WorkspaceSwitcherWindowTests: XCTestCase {
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
        app.store.apply(snapshot: F.snapshot(sessions))
    }

    override func tearDown() async throws {
        workspace.window.close()
        workspace = nil
        app = nil
    }

    private var typingInTheList: Bool {
        (workspace.window.firstResponder as? NSTextView)?.delegate === workspace.switcher.field
    }

    func testTheListTakesTheKeyboardAndGivesItBackWhenDismissed() throws {
        workspace.focus(runtimeSessionID: F.runtimeID(1))
        let terminal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))
        XCTAssertTrue(workspace.switcher.isHidden, "hidden until asked for")

        workspace.toggleSwitcher()
        XCTAssertTrue(workspace.isSwitching)
        XCTAssertTrue(typingInTheList)
        XCTAssertEqual(workspace.switcher.shown.count, 5, "three sessions and the two views")
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)), "the pane stays where it was")

        workspace.switcher.cancel()
        XCTAssertFalse(workspace.isSwitching)
        XCTAssertTrue(workspace.window.firstResponder === terminal, "Escape gives the keyboard back")

        workspace.toggleSwitcher()
        workspace.toggleSwitcher()
        XCTAssertFalse(workspace.isSwitching, "the key that brings it up puts it away")
        XCTAssertTrue(workspace.window.firstResponder === terminal)
        XCTAssertEqual(app.made, [F.runtimeID(1)], "looking at the list attaches nothing")
    }

    func testReturnGoesToTheSessionAsAClickWould() throws {
        workspace.focus(runtimeSessionID: F.runtimeID(1))
        workspace.toggleSwitcher()
        workspace.switcher.enter("personal")
        XCTAssertEqual(workspace.switcher.shown.map(\.title), ["api — personal"])

        workspace.switcher.choose()
        XCTAssertFalse(workspace.isSwitching)
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(2)))
        XCTAssertTrue(workspace.window.firstResponder === workspace.terminalView(for: F.runtimeID(2)))
        XCTAssertEqual(app.made, [F.runtimeID(1), F.runtimeID(2)], "only an attachment, as a click makes")
        XCTAssertEqual(app.madeViews, [])
    }

    func testTheArrowsMoveThroughWhatMatches() {
        workspace.toggleSwitcher()
        workspace.switcher.enter("api")
        XCTAssertEqual(workspace.switcher.selected?.title, "api — personal", "the first match is selected")
        workspace.switcher.move(1)
        XCTAssertEqual(workspace.switcher.selected?.title, "api — work")
        workspace.switcher.move(1)
        XCTAssertEqual(workspace.switcher.selected?.title, "api — work", "it stops at the end")
        workspace.switcher.move(-5)
        XCTAssertEqual(workspace.switcher.selected?.title, "api — personal")

        workspace.switcher.enter("nothing here")
        XCTAssertNil(workspace.switcher.selected)
        workspace.switcher.choose()
        XCTAssertTrue(workspace.isSwitching, "Return on nothing goes nowhere")
    }

    func testAKittySessionComesForwardInKitty() {
        workspace.toggleSwitcher()
        workspace.switcher.enter("web")
        workspace.switcher.choose()
        XCTAssertEqual(app.external.map(\.target), [.kitty(socket: "", windowID: 4)])
        XCTAssertEqual(app.made, [], "a Kitty session has no terminal here")
        XCTAssertFalse(typingInTheList, "the hidden field does not keep the keyboard")
    }

    func testAViewIsShownAndGivenTheKeyboard() {
        workspace.toggleSwitcher()
        workspace.switcher.enter("dash")
        workspace.switcher.choose()
        XCTAssertEqual(workspace.viewSelection, WorkspaceUtilityView.dashboard.id)
        XCTAssertTrue(workspace.window.firstResponder === workspace.utilityTerminalView(for: WorkspaceUtilityView.dashboard.id))
        XCTAssertEqual(app.made, [], "no session was attached or started")
    }

    func testASessionThatEndsLeavesTheListWhileItIsUp() {
        workspace.toggleSwitcher()
        workspace.switcher.enter("api")
        workspace.switcher.move(1)
        app.store.apply(snapshot: F.snapshot([sessions[0], sessions[2]]))
        XCTAssertEqual(workspace.switcher.shown.map(\.title), ["api — work"], "what is typed is kept")
        XCTAssertEqual(workspace.switcher.selected?.title, "api — work", "and so is the place that was selected")
        XCTAssertTrue(typingInTheList)
    }

    func testAClickElsewherePutsTheListAwayWithoutTakingTheKeyboardBack() throws {
        workspace.focus(runtimeSessionID: F.runtimeID(1))
        workspace.toggleSwitcher()
        // What a click on the sidebar does to the field that was being typed in.
        workspace.focusSidebar()
        XCTAssertFalse(workspace.isSwitching)
        XCTAssertTrue(workspace.window.firstResponder === workspace.sidebar)
    }

    func testClosingTheWindowPutsTheListAway() {
        workspace.toggleSwitcher()
        workspace.window.close()
        XCTAssertFalse(workspace.isSwitching)
        workspace.toggleSwitcher()
        XCTAssertFalse(workspace.isSwitching, "a closed workspace has no list")
    }
}
