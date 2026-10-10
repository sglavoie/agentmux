import AppKit
import XCTest
@testable import AgentMux

/// Sessions as a v2 snapshot lists them, for the workspace tests.
enum WorkspaceFixture {
    /// `rs-` and 32 hex digits ending in `n`.
    static func runtimeID(_ n: Int) -> String {
        let hex = String(n, radix: 16)
        return "rs-" + String(repeating: "0", count: 32 - hex.count) + hex
    }

    /// A session on the private tmux server: no window, a runtime identity.
    static func tmux(_ n: Int, project: String, profile: String = "work", harness: String = "claude",
                     icon: String = "", attention: Bool = false, changedAtMS: Int64 = 0) -> CompanionSession {
        var session = CompanionSession()
        session.project = project
        session.profile = profile
        session.harness = harness
        session.iconState = icon
        session.attention = attention
        session.presentationChangedAtMS = changedAtMS
        session.runtime = CompanionRuntime(sessionID: runtimeID(n), backend: "tmux", pane: "%\(n)")
        return session
    }

    static func kitty(_ windowID: Int, project: String, profile: String = "work", harness: String = "claude",
                      socket: String = "", icon: String = "", attention: Bool = false, changedAtMS: Int64 = 0) -> CompanionSession {
        var session = CompanionSession(windowID: windowID)
        session.kittySocket = socket
        session.project = project
        session.profile = profile
        session.harness = harness
        session.iconState = icon
        session.attention = attention
        session.presentationChangedAtMS = changedAtMS
        return session
    }

    static func snapshot(_ sessions: [CompanionSession], socket: String = "unix:/tmp/kitty-1") -> CompanionSnapshot {
        var snapshot = CompanionSnapshot()
        snapshot.v = 2
        snapshot.daemon.ready = true
        snapshot.kitty.socket = socket
        snapshot.sessions = sessions
        return snapshot
    }

    /// One session as the daemon writes it in a v2 snapshot.
    static func json(_ session: CompanionSession) -> String {
        var fields = [
            #""harness":"\#(session.harness)""#, #""profile":"\#(session.profile)""#, #""project":"\#(session.project)""#,
            #""icon_state":"\#(session.iconState)""#, #""attention":\#(session.attention)"#,
            #""presentation_changed_at_ms":\#(session.presentationChangedAtMS)"#,
        ]
        if session.windowID > 0 {
            fields.append(#""window_id":\#(session.windowID)"#)
            fields.append(#""kitty_socket":"\#(session.kittySocket)""#)
        }
        if let runtime = session.runtime {
            fields.append(#""runtime":{"session_id":"\#(runtime.sessionID)","backend":"\#(runtime.backend)","instance":"/tmp/amx-tmux.sock","generation":"g1","pane":"\#(runtime.pane)"}"#)
        }
        return "{" + fields.joined(separator: ",") + "}"
    }

    /// A v2 snapshot frame listing `sessions`.
    static func frame(seq: Int, _ sessions: [CompanionSession]) -> String {
        #"{"v":1,"seq":\#(seq),"type":"snapshot","data":{"v":2,"daemon":{"pid":7,"ready":true},"kitty":{"ready":true,"socket":"unix:/tmp/kitty-1"},"notification":{},"dashboard":{"open":false},"attention":{},"asl":{},"sessions":[\#(sessions.map(json).joined(separator: ","))]}}"#
    }

    /// The ack of a daemon that granted `runtime_sessions`.
    static let ack = #"{"ok":true,"subscription":{"protocol":1,"server_protocol_max":1,"subscriber_id":"sub-1","heartbeat_ms":15000,"features":["snapshot","heartbeat","notifications","runtime_sessions"]},"ready":true}"#
}

/// A stand-in terminal that can take the keyboard.
final class FocusableView: NSView {
    override var acceptsFirstResponder: Bool { true }
}

/// The app as the workspace sees it, recording what it was asked for.
@MainActor
final class FakeWorkspaceApp: WorkspaceDelegate {
    let store = SessionStore()
    private(set) var made: [String] = []
    private(set) var closed: [String] = []
    private(set) var external: [WorkspaceRow] = []
    private(set) var visibility: [Bool] = []
    /// Set to refuse every terminal with this message.
    var problem: String?
    /// Makes the view for a terminal; a `FocusableView` when nil.
    var factory: ((String) -> NSView?)?

    func makeTerminal(runtimeSessionID: String) -> Result<WorkspaceTerminal, WorkspaceTerminalProblem> {
        if let problem { return .failure(WorkspaceTerminalProblem(message: problem)) }
        guard let view = factory.map({ $0(runtimeSessionID) }) ?? FocusableView() else {
            return .failure(WorkspaceTerminalProblem(message: "no view"))
        }
        made.append(runtimeSessionID)
        return .success(WorkspaceTerminal(view: view) { [weak self] in
            self?.closed.append(runtimeSessionID)
            (view as? Ghostty.SurfaceView)?.close()
        })
    }

    /// The utility views a terminal was made for, by id, and those let go.
    private(set) var madeViews: [String] = []
    private(set) var closedViews: [String] = []
    /// Set to refuse every utility terminal with this message.
    var viewProblem: String?

    func makeUtilityTerminal(_ view: WorkspaceUtilityView) -> Result<WorkspaceTerminal, WorkspaceTerminalProblem> {
        if let viewProblem { return .failure(WorkspaceTerminalProblem(message: viewProblem)) }
        let terminal = FocusableView()
        madeViews.append(view.id)
        return .success(WorkspaceTerminal(view: terminal) { [weak self] in self?.closedViews.append(view.id) })
    }

    func focusExternally(_ row: WorkspaceRow) { external.append(row) }
    func workspaceVisibilityChanged(_ visible: Bool) { visibility.append(visible) }

    /// Every place the workspace reported moving to, in order.
    private(set) var places: [WorkspaceRestoration.Place?] = []
    func workspacePlaceChanged(_ place: WorkspaceRestoration.Place?) { places.append(place) }

    /// What amx says about each session; one that is not named is running
    /// with no terminal attached.
    var statuses: [String: WorkspaceSessionStatus] = [:]
    /// The sessions amx was asked about, in order.
    private(set) var asked: [String] = []
    /// Set to hold every answer until `answer()` is called.
    var holdsAnswers = false
    private var held: [() -> Void] = []

    func sessionStatus(runtimeSessionID: String, completion: @escaping @MainActor (WorkspaceSessionStatus) -> Void) {
        asked.append(runtimeSessionID)
        let status = statuses[runtimeSessionID] ?? .live(clients: 0)
        if holdsAnswers {
            held.append { completion(status) }
        } else {
            completion(status)
        }
    }

    /// Delivers the held answers, oldest first.
    func answer() {
        let pending = held
        held = []
        pending.forEach { $0() }
    }
}
