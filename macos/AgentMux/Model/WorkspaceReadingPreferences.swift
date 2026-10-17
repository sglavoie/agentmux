import Foundation
import Combine

/// Code blocks and checkout diffs share reading preferences. The existing saved
/// diff appearance is retained, so upgrading also preserves the reader's choice.
@MainActor
final class WorkspaceReadingPreferences: ObservableObject {
    static let shared = WorkspaceReadingPreferences(defaults: .standard)
    private let state: WorkspaceReviewState
    @Published var fontSize: Double { didSet { save() } }
    @Published var wrapLines: Bool { didSet { save() } }

    init(defaults: UserDefaults? = nil) {
        state = WorkspaceReviewState(defaults: defaults)
        let saved = state.diffAppearance
        fontSize = saved.fontSize
        wrapLines = saved.wrapLines
    }
    var effectiveFontSize: Double { fontSize.isFinite ? min(24, max(10, fontSize)) : 12 }
    private func save() {
        state.diffAppearance = .init(fontSize: effectiveFontSize, wrapLines: wrapLines)
    }
}
