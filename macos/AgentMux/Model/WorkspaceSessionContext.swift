import Foundation

/// The short orientation above a session. Uses the same observed facts as
/// the inspector; a quiet feed never implies that an agent finished.
struct WorkspaceSessionContext: Equatable {
    let key: AttentionCycler.Key
    let title: String
    let location: String
    let status: String
    let conversation: ConversationIdentity?
    let revision: String
    let current: Bool

    init(key: AttentionCycler.Key, row: WorkspaceRow?, session: CompanionSession?,
         content: WorkspacePaneContent, current: Bool) {
        self.key = key
        title = row?.displayName ?? "Session ended"
        location = [row?.name.isEmpty == false ? row?.project : nil, row?.branch]
            .compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " / ")
        conversation = row?.conversation
        revision = String(session?.presentationChangedAtMS ?? 0) + (session?.iconState ?? "")
        self.current = current
        switch content {
        case .detached: status = "Detached · agent keeps running"
        case .closing: status = "Ending session…"
        case .gone: status = "Session ended"
        case .restoring: status = "Checking session…"
        case .rendererFailed: status = "Terminal display unavailable"
        case .unavailable: status = "Terminal unavailable"
        default:
            let observed = session.map {
                WorkspaceWording.status($0.reason.isEmpty ? WorkspaceInspector.word(for: $0.iconState) : $0.reason)
            } ?? "Status unavailable"
            status = current ? observed : "Last known: \(observed) · reconnecting"
        }
    }
}
