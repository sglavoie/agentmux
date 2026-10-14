import Foundation

/// What an answer panel's notice is, so it is shown as what it is: only a
/// failure is an error.
enum PanelNoticeKind: Equatable {
    /// What happened, or what the panel is waiting for.
    case info
    /// A question back to the operator, answered by doing the same again.
    case confirm
    /// Something went wrong.
    case error
}
