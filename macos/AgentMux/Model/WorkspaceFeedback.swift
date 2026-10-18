import Foundation
import Combine

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

/// A bounded, in-memory record of outcomes. It stores no action tokens or
/// executable commands; each recovery captures a read-only destination (or the
/// existing pending receipt) and never repeats the original mutation.
@MainActor
final class WorkspaceFeedbackHistory: ObservableObject {
    struct Entry: Identifiable {
        let id = UUID()
        let occurred: Date
        let feedback: WorkspaceFeedback
        let available: () -> Bool
        let recover: () -> Void
    }
    @Published private(set) var entries: [Entry] = []
    static let limit = 30

    func record(_ feedback: WorkspaceFeedback, at: Date = Date(), available: @escaping () -> Bool = { true }, recover: @escaping () -> Void = {}) {
        entries.insert(Entry(occurred: at, feedback: feedback, available: available, recover: recover), at: 0)
        if entries.count > Self.limit { entries.removeLast(entries.count - Self.limit) }
    }
    func dismiss(_ id: UUID) { entries.removeAll { $0.id == id } }
}
