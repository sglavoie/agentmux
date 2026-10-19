import Foundation

/// Read-only context. Tokens fetched here are discarded; the answer panels
/// authorize independently when the operator chooses Answer.
enum WorkspaceWaitingContext {
    enum Result: Equatable {
        case loaded(SecretText, warning: SecretText? = nil)
        case failed(SecretText)
        case unavailable(SecretText)
    }

    struct Reading: Equatable {
        var content: SecretText?
        var loading = false
        var refreshedAt: Date?
        var problem: SecretText?
        var unavailable = false
        var failed = false

        mutating func begin() { loading = true; problem = nil; unavailable = false; failed = false }
        mutating func finish(_ result: Result, at: Date = .now) {
            loading = false
            switch result {
            case let .loaded(text, warning):
                content = text; refreshedAt = at; problem = warning; unavailable = false; failed = false
            case let .failed(error): problem = error; failed = true
            case let .unavailable(reason):
                content = nil; refreshedAt = nil; problem = reason; unavailable = true; failed = false
            }
        }
    }

    static func read(_ row: WorkspaceWaitingRow, asl: ASLService,
                     look: (String) -> AMXCommandResult<PromptLook>) -> Result {
        if case let .asl(target)? = row.answer {
            switch asl.snapshot() {
            case let .failure(error): return .failed(SecretText(ASLRefusal.text(error)))
            case let .success(result):
                guard result.status.available, !result.status.snapshotStale, result.meta?.stale != true,
                      let run = target.row(in: result.snapshot?.rows ?? []),
                      let question = run.question, !question.questionID.isEmpty else {
                    return .unavailable(SecretText("This question is no longer available. Update the waiting list."))
                }
                switch asl.question(planDir: run.planDir, questionID: question.questionID) {
                case let .failure(error): return .failed(SecretText(ASLRefusal.text(error)))
                case let .success(body):
                    guard body.runID == run.runID, body.questionID == question.questionID else {
                        return .unavailable(SecretText("The question changed while loading. Update the waiting list."))
                    }
                    return .loaded(body.body)
                }
            }
        }
        guard !row.key.runtimeSessionID.isEmpty else {
            return .unavailable(SecretText("Open this session in Kitty to read its current output."))
        }
        switch look(row.key.runtimeSessionID) {
        case let .failure(error): return .failed(SecretText(PromptAnswerState.text(error)))
        case let .partial(screen, status):
            return .loaded(SecretText(screen.lines.joined(separator: "\n")), warning: SecretText("Only partial output was read (exit \(status)). Refresh to try again."))
        case let .success(screen):
            return screen.lines.isEmpty ? .unavailable(SecretText("No screen output is available. Refresh to check again."))
                : .loaded(SecretText(screen.lines.joined(separator: "\n")))
        }
    }
}
