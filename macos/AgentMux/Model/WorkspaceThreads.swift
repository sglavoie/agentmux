import Foundation
import Combine

struct ConversationLink: Decodable, Equatable, Identifiable {
    let id: String
    let source: ConversationIdentity
    let target: ConversationIdentity?
    let targetKey: String
    let kind: String
    let artifactPath: String
    let createdAt: String
    enum CodingKeys: String, CodingKey {
        case id, source, target, kind
        case targetKey = "target_key", artifactPath = "artifact_path", createdAt = "created_at"
    }
}

struct ConversationThreadGroup: Identifiable {
    let id: String
    let conversations: [RecentConversation]

    /// Connected components include paths through conversations outside the
    /// current list. Same session IDs from different harnesses remain distinct.
    static func group(_ conversations: [RecentConversation], links: [ConversationLink]) -> [Self] {
        var remaining = conversations
        var groups: [Self] = []
        while let first = remaining.first {
            var members: Set<String> = [first.selector]
            var changed = true
            while changed {
                changed = false
                for link in links {
                    guard members.contains(link.source.encoded) || link.target.map({ members.contains($0.encoded) }) == true else { continue }
                    if members.insert(link.source.encoded).inserted { changed = true }
                    if let target = link.target, members.insert(target.encoded).inserted { changed = true }
                }
            }
            let grouped = remaining.filter { members.contains($0.selector) }
            let byIdentity = Dictionary(uniqueKeysWithValues: grouped.map { ($0.selector, $0) })
            var visited: Set<String> = [], ordered: [RecentConversation] = []
            func visit(_ identity: String) {
                guard visited.insert(identity).inserted else { return }
                for link in links where link.target?.encoded == identity { visit(link.source.encoded) }
                if let item = byIdentity[identity] { ordered.append(item) }
            }
            grouped.forEach { visit($0.selector) }
            remaining.removeAll { members.contains($0.selector) }
            groups.append(Self(id: members.sorted().first ?? first.selector, conversations: ordered))
        }
        return groups
    }
}

@MainActor
final class WorkspaceThreadsModel: ObservableObject {
    struct Report: Decodable { let threadLinks: [ConversationLink]; enum CodingKeys: String, CodingKey { case threadLinks = "thread_links" } }
    @Published private(set) var links: [ConversationLink] = []
    @Published private(set) var error: String?
    @Published private(set) var loading = false
    @Published private(set) var conversation: ConversationIdentity?
    @Published private(set) var targetKey = ""
    var command: () -> AMXCommand?
    private let runner: ASLWorkRunner
    private var generation = 0
    private var revision = ""

    init(command: @escaping () -> AMXCommand?, runner: ASLWorkRunner = QueueWorkRunner.concurrent()) {
        self.command = command; self.runner = runner
    }

    func select(_ identity: ConversationIdentity?, targetKey: String = "", revision: String = "") {
        guard identity != conversation || targetKey != self.targetKey || revision != self.revision else { return }
        generation += 1; loading = false; error = nil; links = []
        conversation = identity; self.targetKey = targetKey; self.revision = revision
        guard identity != nil || !targetKey.isEmpty else { return }
        refresh()
    }

    func refresh() {
        guard !loading, conversation != nil || !targetKey.isEmpty else { return }
        guard let command = command() else { error = AMXWording.current; return }
        generation += 1
        let request = generation
        loading = true; error = nil
        runner.run({ command.json(Report.self, ["session", "threads"]) }) { [weak self] result in
            guard let self, self.generation == request else { return }
            self.loading = false
            switch result {
            case let .success(report): self.links = report.threadLinks
            case .partial: self.error = "Conversation links are incomplete. Refresh to try again."
            case let .failure(error): self.error = error.message
            }
        }
    }

    func close() { generation += 1; loading = false; links = []; error = nil; conversation = nil; targetKey = ""; revision = "" }
}
