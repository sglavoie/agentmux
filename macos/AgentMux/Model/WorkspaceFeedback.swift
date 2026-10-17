import Foundation

/// Presentation of an outcome, captured with the target the operation used.
/// Recovery only reads state; it never repeats a launch, landing, or run control.
struct WorkspaceFeedback: Equatable {
    enum Kind { case information, success, warning, error }
    enum Recovery {
        case refresh, transcript, receipt, readiness, runs
        var title: String {
            switch self {
            case .refresh: return "Refresh"
            case .transcript: return "Reload Transcript"
            case .receipt: return "Retry Receipt"
            case .readiness: return "Check Readiness"
            case .runs: return "Show Runs"
            }
        }
    }
    var target: String
    var message: String
    var kind: Kind = .error
    var nextStep: String = ""
    var recovery: Recovery? = .refresh
}
