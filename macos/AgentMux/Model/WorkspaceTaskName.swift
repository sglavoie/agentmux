import Foundation
import Combine

@MainActor
final class WorkspaceTaskName: ObservableObject {
    @Published var name: String
    @Published private(set) var suggestion: String
    @Published private(set) var loading = false
    private let row: WorkspaceRow
    private let command: () -> AMXCommand?
    private let runner: ASLWorkRunner
    private var generation = 0
    init(row: WorkspaceRow, command: @escaping () -> AMXCommand?, runner: ASLWorkRunner = QueueWorkRunner.concurrent()) {
        self.row = row; self.name = row.name; self.command = command; self.runner = runner
        suggestion = Self.branchSuggestion(row.branch)
    }
    nonisolated static func concise(_ text: String) -> String {
        let line = text.components(separatedBy: .newlines).first(where: { !$0.trimmingCharacters(in: .whitespaces).isEmpty }) ?? ""
        return String(line.split(whereSeparator: \.isWhitespace).joined(separator: " ").prefix(80))
    }
    nonisolated static func branchSuggestion(_ branch: String) -> String {
        guard !branch.isEmpty, !["main", "master", "develop", "HEAD"].contains(branch) else { return "" }
        let words = branch.replacingOccurrences(of: "/", with: " ").replacingOccurrences(of: "-", with: " ").replacingOccurrences(of: "_", with: " ")
        let title = concise(words)
        return title.prefix(1).uppercased() + title.dropFirst()
    }
    func load() {
        guard let identity = row.conversation, let command = command() else { return }
        generation += 1
        let request = generation
        loading = true
        runner.run({ command.json(WorkspaceTranscript.self, ["session", "show", "--last", "1", "--json", "--", identity.encoded]) }) { [weak self] result in
            guard let self, self.generation == request else { return }
            self.loading = false
            if case let .success(transcript) = result, transcript.session.identity == identity {
                let title = Self.concise(transcript.session.title.isEmpty ? transcript.session.snippet : transcript.session.title)
                if !title.isEmpty { self.suggestion = title }
            }
        }
    }
    func close() { generation += 1; loading = false }
}
