import AppKit

/// One terminal the workspace embeds: the view, and how to let go of it.
/// Closing only detaches; the session lives on in its backend.
struct WorkspaceTerminal {
    let view: NSView
    let close: () -> Void
    /// Whether closing it now would end a program that should be asked
    /// about first (`ghostty_surface_needs_confirm_quit`).
    var needsConfirm: () -> Bool = { false }
    /// The font size it shows now, zoom included; nil when unknown.
    var fontSize: () -> Float? = { nil }
}

/// Why a terminal could not be shown.
struct WorkspaceTerminalProblem: Error, Equatable {
    let message: String
}

/// What the workspace asks of the app.
@MainActor
protocol WorkspaceDelegate: AnyObject {
    /// The live sessions and the feed that delivers them.
    var store: SessionStore { get }
    /// A terminal attached to a runtime session, at `fontSize` points when
    /// one is given, else the app configuration's.
    func makeTerminal(runtimeSessionID: String, fontSize: Float?) -> Result<WorkspaceTerminal, WorkspaceTerminalProblem>
    /// A terminal running a utility view: never an attachment, never a
    /// session.
    func makeUtilityTerminal(_ view: WorkspaceUtilityView) -> Result<WorkspaceTerminal, WorkspaceTerminalProblem>
    /// Focuses a session that is a window of another app and brings that app
    /// forward.
    func focusExternally(_ row: WorkspaceRow)
    /// The window was shown or closed.
    func workspaceVisibilityChanged(_ visible: Bool)
    /// The pane moved to another session or view, or to none.
    func workspacePlaceChanged(_ place: WorkspaceRestoration.Place?)
    /// The conversation of the session the pane is on became known, changed,
    /// or the pane left that session.
    func workspaceConversationChanged(_ conversation: ConversationIdentity?)
    /// The sidebar was hidden or shown.
    func workspaceSidebarCollapsedChanged(_ collapsed: Bool)
    /// The open window entered or left full screen.
    func workspaceFullScreenChanged(_ fullScreen: Bool)
    /// The inspector was hidden or shown, or turned to another tab.
    func workspaceInspectorChanged(collapsed: Bool, tab: WorkspaceInspectorTab)
    /// The sidebar or the inspector was given another width. Each is nil
    /// while it is hidden: hiding one keeps the width it had.
    func workspaceLayoutChanged(sidebarWidth: CGFloat?, inspectorWidth: CGFloat?)
    /// Does what one of the inspector's buttons asks for the session the
    /// pane is on.
    func workspaceInspectorAction(_ action: WorkspaceInspectorAction)
    /// Mutes a listed session, or unmutes it: the right-click menu's Mute,
    /// for a line that may not be the one the pane is on.
    func workspaceToggleMute(_ row: WorkspaceRow)
    /// Answer… on a waiting session of the inspector: opens the panel that
    /// answers it. The pane stays where it is.
    func workspaceAnswer(_ answer: WorkspaceWaitingAnswer)
    /// A project's sessions were dragged into another order.
    func workspaceOrderChanged(_ order: WorkspaceOrder)
    /// Asks whether a session's agent is running and whether a terminal is
    /// attached to it. It only asks: nothing is attached and nothing started.
    /// The answer arrives on the main actor, possibly before this returns.
    func sessionStatus(runtimeSessionID: String, completion: @escaping @MainActor (WorkspaceSessionStatus) -> Void)
    /// Ends a session's agent and the tmux session that holds it. The answer
    /// is nil once it is closed, or why it could not be, and arrives on the
    /// main actor, possibly before this returns.
    func closeSession(runtimeSessionID: String, completion: @escaping @MainActor (String?) -> Void)
    /// Gives a session a name, or with an empty one removes the name it has.
    /// The answer is nil once it is written, or why it could not be, and
    /// arrives on the main actor, possibly before this returns.
    func nameSession(runtimeSessionID: String, name: String, completion: @escaping @MainActor (String?) -> Void)
    /// Runs `amx daemon start`, because the operator asked for it from the
    /// pane or the sidebar. It starts no agent.
    func workspaceStartDaemon()
    /// Runs `amx daemon restart`, because the operator asked for it from the
    /// pane or the sidebar while the daemon is too old for the app. It ends
    /// no agent.
    func workspaceRestartDaemon()
    /// A program in one of the workspace's terminals asked for a desktop
    /// notification (OSC 9 or 777). `onScreen` is set when the terminal it
    /// came from is in the pane of the key window.
    func workspaceTerminalNotification(_ notice: TerminalNotice, onScreen: Bool)
    /// The pane shows the terminal of a session or view: it moved there, or
    /// the window became key on it. Its terminal notices are now read.
    func workspaceShowing(_ origin: TerminalNotice.Origin)
    var workspaceCommands: [WorkspaceSwitcher.Entry] { get }
    var workspaceLaunchDescription: String { get }
    var workspaceProjectGuidance: String { get }
    func workspaceLaunchProjects(from view: NSView)
    func workspacePerform(_ command: WorkspaceCommand)
    func workspaceContext(_ row: WorkspaceWaitingRow, completion: @escaping (WorkspaceWaitingContext.Result) -> Void)
    func workspaceSearchConversation()
    /// Opens Settings on the amx binary, from an error that says amx was not
    /// found.
    func workspaceOpenAMXSettings()
    /// Whether the daemon is known to be down, so an error can offer Start
    /// Daemon.
    var workspaceDaemonIsDown: Bool { get }
}

extension WorkspaceDelegate {
    var workspaceCommands: [WorkspaceSwitcher.Entry] { [] }
    var workspaceLaunchDescription: String { "Launch in Workspace (tmux)" }
    var workspaceProjectGuidance: String { "Choose a configured project to start, or return to a past conversation." }
    func workspaceLaunchProjects(from view: NSView) {}
    func workspacePerform(_ command: WorkspaceCommand) {}
    func workspaceSearchConversation() {}
    func workspaceOpenAMXSettings() {}
    var workspaceDaemonIsDown: Bool { false }
    func workspaceContext(_ row: WorkspaceWaitingRow, completion: @escaping (WorkspaceWaitingContext.Result) -> Void) {
        completion(.unavailable(SecretText("Context is unavailable.")))
    }
}

extension WorkspaceDelegate where Self: AppDelegate {
    /// The app restarts the daemon from the workspace as it does from the
    /// menu bar's Restart Daemon, which is the same command and notices.
    func workspaceRestartDaemon() {
        perform(.restartDaemon)
    }
}
