import Foundation
import Combine

/// An explicit recovery of one recorded conversation. Opening this model only
/// reads; submission rechecks its source and delegates to History's focus/resume.
@MainActor
final class WorkspaceResumeModel: ObservableObject {
    let identity: ConversationIdentity
    @Published private(set) var conversation: RecentConversation?
    @Published private(set) var reading = false
    @Published private(set) var acting = false
    @Published private(set) var error: String?
    let feedbackHistory: WorkspaceFeedbackHistory
    var onResume: ((RecentConversation, @escaping (String?) -> Void) -> Void)?
    var onFinished: (() -> Void)?
    private let command: () -> AMXCommand?
    private let runner: ASLWorkRunner
    private var generation = 0

    init(identity: ConversationIdentity, command: @escaping () -> AMXCommand?,
         runner: ASLWorkRunner = QueueWorkRunner.concurrent(), feedbackHistory: WorkspaceFeedbackHistory? = nil) {
        self.identity = identity; self.command = command; self.runner = runner; self.feedbackHistory = feedbackHistory ?? WorkspaceFeedbackHistory()
    }

    var canResume: Bool { conversation != nil && !reading && !acting && error == nil }

    func load() {
        guard !acting, !reading else { return }
        read { [weak self] value in self?.conversation = value }
    }

    private func read(_ done: @escaping (RecentConversation) -> Void) {
        generation += 1
        let current = generation
        guard let command = command() else { error = AMXWording.current; return }
        reading = true; error = nil
        let args = ["session", "show", "--last", "1", "--json", "--", identity.encoded]
        runner.run({ command.json(WorkspaceTranscript.self, args) }) { [weak self] result in
            guard let self, self.generation == current else { return }
            self.reading = false
            switch result {
            case let .failure(error): self.error = error.message
            case .partial: self.error = "The conversation could only be read partially. Refresh before resuming."
            case let .success(value):
                guard value.session.identity == self.identity else {
                    self.error = "The recorded conversation did not match this session. Nothing was resumed."
                    return
                }
                done(value.session)
            }
        }
    }

    func resume() {
        guard canResume, let shown = conversation, let onResume else { return }
        read { [weak self] current in
            guard let self else { return }
            guard current.profile == shown.profile && current.cwd == shown.cwd && current.harness == shown.harness else {
                self.conversation = current
                self.error = "The conversation's profile or checkout changed. Review the updated details, then refresh before resuming."
                return
            }
            self.acting = true
            onResume(current) { [weak self] problem in
                guard let self else { return }
                self.acting = false
                let issue = WorkspaceFeedbackIssue(operation: .resume, target: [self.identity.encoded])
                if let problem {
                    self.error = problem
                    self.feedbackHistory.record(.init(target: self.identity.encoded, message: problem,
                        nextStep: "Open the conversation to check whether it is already running.", recovery: .transcript, issue: issue),
                        destination: .conversation(self.identity.encoded), available: { [weak self] in self?.acting == false },
                        recover: { [weak self] in self?.load() })
                } else {
                    self.feedbackHistory.resolve(issue, evidence: "The conversation opened successfully.")
                    self.onFinished?()
                }
            }
        }
    }

    func close() { generation += 1 }
}
