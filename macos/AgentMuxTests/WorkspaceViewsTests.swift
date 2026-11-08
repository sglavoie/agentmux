import AppKit
import XCTest
@testable import AgentMux

/// The workspace's utility views as data: what a forwarded request becomes,
/// what each view runs, and that none of it is a session.
final class WorkspaceViewsModelTests: XCTestCase {
    private typealias F = WorkspaceFixture

    func testTerminalNamesPreserveIdentityDefaultsAndSavedLayouts() throws {
        var views = WorkspaceViews()
        let first = views.openTerminal(beside: F.runtimeID(1))
        let second = views.openTerminal(beside: F.runtimeID(1))
        views.name(second.id, "  build logs 🛠 \n")
        let named = try XCTUnwrap(views.view(second.id))
        XCTAssertEqual(named.displayName, "build logs 🛠")
        XCTAssertEqual(named.id, second.id)
        XCTAssertEqual(named.arguments, second.arguments)
        XCTAssertEqual(views.view(first.id), first)
        XCTAssertEqual(views.open(second), named, "opening an existing view preserves its name")

        let layout = WorkspaceRestoration.Layout(active: .utility(named), other: nil, activeOnRight: false)
        let saved = try JSONEncoder().encode(layout)
        XCTAssertEqual(try JSONDecoder().decode(WorkspaceRestoration.Layout.self, from: saved), layout)
        let legacy = Data(#"{"kind":"shell","target":"/work/api","title":"Shell 2","ordinal":2}"#.utf8)
        let decoded = try JSONDecoder().decode(WorkspaceUtilityView.self, from: legacy)
        XCTAssertNil(decoded.name)
        XCTAssertEqual(decoded.displayName, "Shell 2")

        views.name(second.id, " \n ")
        XCTAssertEqual(views.view(second.id), second, "clearing restores the original numbered label")
        views.name(WorkspaceUtilityView.dashboard.id, "hidden")
        XCTAssertEqual(views.view(WorkspaceUtilityView.dashboard.id), .dashboard)
    }

    /// A shell titles itself with its directory at its prompt and with the
    /// command while one runs: only the command is worth a line.
    func testAShellsPromptTitleSaysNothing() {
        let home = NSHomeDirectory()
        let shell = WorkspaceUtilityView(kind: .shell, target: home + "/api", title: "Shell")
        XCTAssertEqual(shell.programTitle("me@host:~/api", directory: nil), "", "before OSC 7, where it was opened")
        XCTAssertEqual(shell.programTitle("~/api", directory: nil), "")
        XCTAssertEqual(shell.programTitle(home + "/api", directory: nil), "")
        XCTAssertEqual(shell.programTitle("me@host:/tmp", directory: "/tmp"), "", "where it went")
        XCTAssertEqual(shell.programTitle("me@host:/tmp", directory: "file://host/tmp/"), "")
        XCTAssertEqual(shell.programTitle("me@host:~/api", directory: "/tmp"), "", "not yet where it went")
        XCTAssertEqual(shell.programTitle("vim ~/api", directory: home + "/api"), "vim ~/api")
        XCTAssertEqual(shell.programTitle("make test", directory: nil), "make test")
        XCTAssertNil(shell.programTitle("", directory: "/tmp"), "a cleared title")

        let terminal = WorkspaceUtilityView(kind: .terminal, target: F.runtimeID(1), title: "Terminal")
        XCTAssertEqual(terminal.programTitle("me@host:/tmp", directory: nil), "", "its directory is unknown")
        XCTAssertEqual(terminal.programTitle("/usr/bin/top", directory: nil), "/usr/bin/top", "a command run by its path")
        XCTAssertEqual(terminal.programTitle("ssh me@host:22", directory: nil), "ssh me@host:22")
        XCTAssertEqual(terminal.detail(session: "release notes", program: nil), "", "untitled yet")
        XCTAssertEqual(terminal.programTitle("me@host:/tmp", directory: "/tmp"), "")
        XCTAssertEqual(terminal.detail(session: "release notes", program: ""), "")
        XCTAssertEqual(shell.detail(session: nil, program: ""), "")
    }

    func testDecodesAForwardedViewFrame() throws {
        let line = Data(#"{"v":1,"seq":9,"type":"workspace_view","data":{"kind":"session","runtime_session_id":"\#(F.runtimeID(3))","await":true}}"#.utf8)
        XCTAssertEqual(
            SubscriptionClient.decodeFrame(line),
            .workspaceView(seq: 9, WorkspaceViewRequest(kind: "session", runtimeSessionID: F.runtimeID(3), awaited: true))
        )
        let shell = Data(#"{"v":1,"seq":10,"type":"workspace_view","data":{"kind":"shell","directory":"/work/api","title":"api","later":1}}"#.utf8)
        XCTAssertEqual(
            SubscriptionClient.decodeFrame(shell),
            .workspaceView(seq: 10, WorkspaceViewRequest(kind: "shell", directory: "/work/api", title: "api")),
            "a field this app does not know is ignored"
        )
        // A daemon that predates the feature never sends the frame; one that
        // sends a shape this app cannot read must not end the stream.
        let raised = Data(#"{"v":1,"seq":12,"type":"workspace_view","data":{"kind":"session","runtime_session_id":"\#(F.runtimeID(3))","await":true,"raise":true}}"#.utf8)
        XCTAssertEqual(
            SubscriptionClient.decodeFrame(raised),
            .workspaceView(seq: 12, WorkspaceViewRequest(kind: "session", runtimeSessionID: F.runtimeID(3), awaited: true, raise: true))
        )

        let empty = Data(#"{"v":1,"seq":11,"type":"workspace_view","data":{}}"#.utf8)
        XCTAssertEqual(SubscriptionClient.decodeFrame(empty), .workspaceView(seq: 11, WorkspaceViewRequest()))
    }

    func testTheAppAsksForWorkspaceViews() throws {
        let data = try JSONEncoder().encode(SubscribeParams())
        let params = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        XCTAssertEqual(params["features"] as? [String], ["runtime_sessions", "workspace_views"])
    }

    func testEveryViewRunsAmxWithAKindAndATargetOnly() {
        XCTAssertEqual(WorkspaceUtilityView.dashboard.arguments, ["workspace-view", "dashboard"])
        XCTAssertEqual(WorkspaceUtilityView.overview.arguments, ["workspace-view", "overview"])
        let shell = WorkspaceUtilityView.from(WorkspaceViewRequest(kind: "shell", directory: "/work/api; rm -rf /"))
        XCTAssertEqual(shell?.arguments, ["workspace-view", "shell", "/work/api; rm -rf /"],
                       "the target is one argument, whatever it holds")
        let editor = WorkspaceUtilityView.from(WorkspaceViewRequest(kind: "editor", path: "/work/api/main.go", title: "main.go"))
        XCTAssertEqual(editor?.arguments, ["workspace-view", "edit", "/work/api/main.go"])
        XCTAssertEqual(editor?.title, "main.go")
        let pager = WorkspaceUtilityView.from(WorkspaceViewRequest(kind: "pager", runtimeSessionID: F.runtimeID(2), title: "api"))
        XCTAssertEqual(pager?.arguments, ["workspace-view", "pager", F.runtimeID(2)])
        var views = WorkspaceViews()
        let terminal = views.openTerminal(beside: F.runtimeID(2))
        XCTAssertEqual(terminal.arguments, ["workspace-view", "session-shell", F.runtimeID(2)])
        XCTAssertNil(WorkspaceUtilityView.from(WorkspaceViewRequest(kind: "terminal", runtimeSessionID: F.runtimeID(2))),
                     "a terminal beside a session is the operator's to open, not a request's")
        let history = WorkspaceUtilityView.history(ConversationIdentity(harness: "claude", sessionID: "0b6f-1c2e"))
        XCTAssertEqual(history.arguments, ["workspace-view", "history", "--", "claude:0b6f-1c2e"])
        XCTAssertFalse(history.isPermanent || history.isOverlay)
        XCTAssertNil(history.aboutRuntimeSessionID, "it is about a conversation, not a session that is running")
        XCTAssertNil(WorkspaceUtilityView.from(WorkspaceViewRequest(kind: "history", title: "claude:0b6f-1c2e")),
                     "Past Sessions on a conversation is the operator's to open, not a request's")
        XCTAssertNil(WorkspaceRestoration.Place(encoded: "view:\(history.id)"), "and it is not brought back at launch")
    }

    /// Only a view whose program may hold the operator's work asks before it
    /// is closed: a shell, an editor, a pager, and a terminal beside a
    /// session. Every kind is named, so a new one has to decide.
    func testOnlyShellsEditorsAndPagersAskBeforeClosing() {
        let expected: [WorkspaceUtilityView.Kind: Bool] = [
            .dashboard: false, .overview: false, .shell: true, .editor: true, .pager: true,
            .terminal: true, .history: false, .keybindings: false, .iconPicker: false, .colorPicker: false,
        ]
        for (kind, asks) in expected {
            let view = WorkspaceUtilityView(kind: kind, target: "x", title: "X")
            XCTAssertEqual(view.asksBeforeClosing, asks, kind.rawValue)
        }
        XCTAssertFalse(WorkspaceUtilityView.dashboard.asksBeforeClosing)
        XCTAssertFalse(WorkspaceUtilityView.overview.asksBeforeClosing)
    }

    func testTheQuitQuestionNamesEveryView() {
        XCTAssertEqual(WorkspaceViews.quitQuestion(["Shell"]), "A process is still running in Shell. Quit anyway?")
        XCTAssertEqual(WorkspaceViews.quitQuestion(["Shell", "main.go"]),
                       "Processes are still running in Shell and main.go. Quit anyway?")
        XCTAssertEqual(WorkspaceViews.quitQuestion(["Shell", "main.go", "api"]),
                       "Processes are still running in Shell, main.go and api. Quit anyway?")
    }

    func testTheQuitQuestionNamesARepeatedTitleOnce() {
        XCTAssertEqual(WorkspaceViews.quitQuestion(Array(repeating: "Terminal", count: 7)),
                       "Processes are still running in Terminal (7). Quit anyway?")
        XCTAssertEqual(WorkspaceViews.quitQuestion(["Terminal", "main.go", "Terminal"]),
                       "Processes are still running in Terminal (2) and main.go. Quit anyway?")
    }

    func testTheKeybindingReferenceAndThePickersAreTheOperatorsToOpen() {
        XCTAssertEqual(WorkspaceUtilityView.keybindings.arguments, ["workspace-view", "keybindings"])
        let icon = WorkspaceUtilityView.picker(.iconPicker, runtimeSessionID: F.runtimeID(2))
        XCTAssertEqual(icon?.arguments, ["workspace-view", "icon-picker", F.runtimeID(2)])
        XCTAssertEqual(icon?.aboutRuntimeSessionID, F.runtimeID(2))
        let color = WorkspaceUtilityView.picker(.colorPicker, runtimeSessionID: F.runtimeID(2))
        XCTAssertEqual(color?.arguments, ["workspace-view", "color-picker", F.runtimeID(2)])
        XCTAssertNil(color?.besideRuntimeSessionID, "a picker is not listed under its session")
        XCTAssertNil(WorkspaceUtilityView.picker(.shell, runtimeSessionID: F.runtimeID(2)))
        for kind in ["keybindings", "icon-picker", "color-picker"] {
            XCTAssertNil(WorkspaceUtilityView.from(WorkspaceViewRequest(kind: kind, runtimeSessionID: F.runtimeID(2))),
                         "\(kind) is a key the operator presses, never a request")
        }
    }

    func testARequestThatNamesNoUsableTargetIsNotAView() {
        XCTAssertNil(WorkspaceUtilityView.from(WorkspaceViewRequest(kind: "session", runtimeSessionID: F.runtimeID(1))),
                     "a session is shown by its own native view, never as a utility")
        XCTAssertNil(WorkspaceUtilityView.from(WorkspaceViewRequest(kind: "exec", path: "/bin/sh")))
        XCTAssertNil(WorkspaceUtilityView.from(WorkspaceViewRequest(kind: "shell", directory: "work/api")))
        XCTAssertNil(WorkspaceUtilityView.from(WorkspaceViewRequest(kind: "shell")))
        XCTAssertNil(WorkspaceUtilityView.from(WorkspaceViewRequest(kind: "editor", path: "")))
        XCTAssertNil(WorkspaceUtilityView.from(WorkspaceViewRequest(kind: "pager", runtimeSessionID: "%4")))
        XCTAssertNil(WorkspaceUtilityView.from(WorkspaceViewRequest(kind: "pager", runtimeSessionID: "rs-XYZ")))
    }

    func testTheDashboardAndOverviewAreAlwaysListedAndOpenedViewsAreUnique() {
        var views = WorkspaceViews()
        XCTAssertEqual(views.all, [.dashboard, .overview])
        let shell = WorkspaceUtilityView(kind: .shell, target: "/work/api", title: "api")
        views.open(shell)
        views.open(WorkspaceUtilityView(kind: .shell, target: "/work/api", title: "another name"))
        views.open(WorkspaceUtilityView(kind: .editor, target: "/work/api", title: "api"))
        views.open(.dashboard)
        XCTAssertEqual(views.all.map(\.id), ["dashboard", "overview", "shell:/work/api", "editor:/work/api"])
        XCTAssertEqual(views.view("shell:/work/api")?.title, "api", "the view already open is the one kept")

        views.close("shell:/work/api")
        views.close("dashboard")
        XCTAssertEqual(views.all.map(\.id), ["dashboard", "overview", "editor:/work/api"], "the Dashboard cannot be unlisted")
    }

    /// New Shell opens one more shell each time; a surface asking for the
    /// shell in a directory still gets the one listed there.
    func testNewShellOpensAnotherShellEachTime() {
        var views = WorkspaceViews()
        let first = views.open(views.nextShell(in: "/home"))
        let second = views.open(views.nextShell(in: "/home"))
        let third = views.open(views.nextShell(in: "/home"))
        XCTAssertEqual([first, second, third].map(\.id), ["shell:/home", "shell:/home#2", "shell:/home#3"])
        XCTAssertEqual([first, second, third].map(\.title), ["Shell", "Shell 2", "Shell 3"])
        XCTAssertEqual(views.nextShell(in: "/work").id, "shell:/work", "another directory starts again")

        let requested = views.open(WorkspaceUtilityView(kind: .shell, target: "/home", title: "Shell"))
        XCTAssertEqual(requested, first, "a request for the shell there reuses the first")
        views.close(first.id)
        XCTAssertEqual(views.nextShell(in: "/home").id, "shell:/home#4", "a number is not reused while a later one is open")
    }

    func testATerminalMovesOnlyAmongTheOnesBesideItsSession() {
        var views = WorkspaceViews()
        let (a, b) = (F.runtimeID(1), F.runtimeID(2))
        let a1 = views.openTerminal(beside: a).id
        let b1 = views.openTerminal(beside: b).id
        let a2 = views.openTerminal(beside: a).id
        let a3 = views.openTerminal(beside: a).id

        views.moveTerminal(a3, before: a1)
        XCTAssertEqual(views.terminals(beside: a).map(\.id), [a3, a1, a2])
        views.moveTerminal(a3, before: nil)
        XCTAssertEqual(views.terminals(beside: a).map(\.id), [a1, a2, a3], "nil is after the last of them")
        views.moveTerminal(a1, before: a3)
        XCTAssertEqual(views.terminals(beside: a).map(\.id), [a2, a1, a3])

        let before = views
        views.moveTerminal(a1, before: b1)
        views.moveTerminal("dashboard", before: a1)
        views.moveTerminal(a1, before: a1)
        XCTAssertEqual(views, before, "a terminal never leaves its session, and no other view moves")
        XCTAssertEqual(views.terminals(beside: b).map(\.id), [b1])
    }

    func testALaunchGoesToTmuxAndReportsItsRuntimeSession() {
        let project = ProjectEntry(profile: "work", title: "API")
        XCTAssertEqual(ProjectCatalog.workspaceLaunchArguments(for: project),
                       ["project", "launch", "--title", "API", "--profile", "work", "--backend", "tmux"])
        let stdout = Data(#"{"schema_version":1,"data":{"changed":true,"message":"launched session","backend":"tmux","runtime_session_id":"\#(F.runtimeID(7))"}}"#.utf8)
        guard case let .success(result) = AMXEnvelope.decode(ProjectLaunchResult.self, stdout: stdout, status: 0) else {
            return XCTFail("the tmux launch payload did not decode")
        }
        XCTAssertEqual(result.runtimeSessionID, F.runtimeID(7))
    }
}
