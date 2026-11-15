import Foundation

/// One line of story per live session, read on demand and kept in memory.
@MainActor
final class WorkspaceStories {
    enum Source: Equatable {
        case prompt(runtimeSessionID: String)
        case lastMessage(ConversationIdentity)
    }

    private struct Revision: Equatable {
        let presentationChangedAtMS: Int64
        let iconState: String
        let reason: String
        let source: Source
    }

    private struct Request: Equatable {
        let revision: Revision
        // Also reject a completion if a session left and returned to the same revision.
        let id = UUID()
    }

    var look: (String) -> AMXCommandResult<PromptLook>
    var lastMessages: (ConversationIdentity) -> AMXCommandResult<WorkspaceTranscript>
    var onChange: (AttentionCycler.Key) -> Void = { _ in }
    private let runner: ASLWorkRunner
    private var revisions: [AttentionCycler.Key: Revision] = [:]
    private var stories: [AttentionCycler.Key: SecretText] = [:]
    private var inFlight: [AttentionCycler.Key: Request] = [:]

    init(look: @escaping (String) -> AMXCommandResult<PromptLook> = { _ in
        .failure(.launchFailed(binary: "amx", reason: AMXWording.resolving))
    }, lastMessages: @escaping (ConversationIdentity) -> AMXCommandResult<WorkspaceTranscript> = { _ in
        .failure(.launchFailed(binary: "amx", reason: AMXWording.resolving))
    }, runner: ASLWorkRunner = QueueWorkRunner.concurrent()) {
        self.look = look; self.lastMessages = lastMessages; self.runner = runner
    }

    func text(for key: AttentionCycler.Key) -> String { stories[key]?.reveal() ?? "" }

    func update(_ sessions: [CompanionSession], live: Bool) {
        var latest: [AttentionCycler.Key: Revision] = [:]
        for session in sessions {
            guard let source = Self.source(session) else { continue }
            latest[AttentionCycler.key(session)] = Revision(
                presentationChangedAtMS: session.presentationChangedAtMS,
                iconState: session.iconState, reason: session.reason, source: source)
        }
        let invalidated = revisions.keys.filter { revisions[$0] != latest[$0] }
        revisions = latest
        for key in invalidated {
            let old = stories.removeValue(forKey: key)
            inFlight.removeValue(forKey: key)
            if let old, !old.isEmpty { onChange(key) }
        }
        guard live else { return }
        for (key, revision) in latest {
            guard stories[key] == nil, inFlight[key]?.revision != revision else { continue }
            let request = Request(revision: revision)
            inFlight[key] = request
            // Capture the readers on the main actor, before dispatching work.
            let look = self.look, lastMessages = self.lastMessages
            runner.run({
                switch revision.source {
                case let .prompt(id):
                    return look(id).value?.prompt?.summary ?? SecretText()
                case let .lastMessage(identity):
                    guard let transcript = lastMessages(identity).value,
                          transcript.session.identity == identity,
                          let message = transcript.messages.last(where: { $0.role == "assistant" }) else {
                        return SecretText()
                    }
                    return Self.firstLine(message.text)
                }
            }) { [weak self] story in
                guard let self, self.revisions[key] == revision, self.inFlight[key] == request else { return }
                self.inFlight.removeValue(forKey: key)
                self.stories[key] = story
                self.onChange(key)
            }
        }
    }

    private static func source(_ session: CompanionSession) -> Source? {
        if ["key", "question"].contains(session.iconState), let id = session.embeddedRuntimeSessionID {
            return .prompt(runtimeSessionID: id)
        }
        if session.iconState == "stop", let identity = session.conversation, identity.isUsable {
            return .lastMessage(identity)
        }
        return nil
    }

    nonisolated private static func firstLine(_ text: SecretText) -> SecretText {
        text.map { value in
            let line = value.components(separatedBy: .newlines).first { !$0.trimmingCharacters(in: .whitespaces).isEmpty } ?? ""
            let collapsed = line.split(whereSeparator: \.isWhitespace).joined(separator: " ")
            let cleaned = String(collapsed.drop(while: { "#*-> ".contains($0) }))
            return cleaned.count > 140 ? String(cleaned.prefix(139)) + "…" : cleaned
        }
    }
}
