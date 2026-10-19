/// What either pane of the workspace shows. The controller chooses a state;
/// the state supplies the passive pane's explanation and notice origin.
enum WorkspacePaneContent: Equatable {
    /// Nothing is selected.
    case empty
    case review
    case terminal(runtimeSessionID: String)
    /// The selected session is a Kitty window.
    case external(AttentionCycler.Key)
    /// Its terminal ended while its session is still listed: it was attached
    /// elsewhere, or detached with F12.
    case detached(runtimeSessionID: String)
    case unavailable(runtimeSessionID: String, String)
    /// Its terminal reported that its renderer failed.
    case rendererFailed(runtimeSessionID: String)
    /// Its agent was asked to end.
    case closing(runtimeSessionID: String)
    /// The daemon has not established whether this restored session runs yet.
    case restoring(runtimeSessionID: String)
    /// The session is no longer running; nil status means the reason is still
    /// being checked.
    case gone(runtimeSessionID: String, WorkspaceSessionStatus?)
    /// A utility view's terminal.
    case view(id: String)
    /// The Dashboard or Overview after its program exited.
    case viewEnded(id: String)
    case viewUnavailable(id: String, String)
    /// A utility view's renderer failed.
    case viewRendererFailed(id: String)

    var otherPaneExplanation: String {
        switch self {
        case let .unavailable(_, reason), let .viewUnavailable(_, reason): return reason
        case .detached: return "Detached. Focus this pane to reconnect explicitly."
        case .rendererFailed, .viewRendererFailed: return "The terminal stopped drawing. Focus this pane for recovery options."
        case .gone: return "This session is no longer running."
        case .closing: return "Ending the session…"
        case .viewEnded: return "This program closed. Focus this pane to reopen it."
        default: return "Focus this pane to see its available actions."
        }
    }

    var terminalNoticeOrigin: TerminalNotice.Origin? {
        switch self {
        case let .terminal(id): return .session(runtimeSessionID: id)
        case let .view(id): return .view(id: id)
        default: return nil
        }
    }
}
