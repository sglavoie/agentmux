import Foundation

/// Read-only context. Tokens fetched here are discarded; the answer panels
/// authorize independently when the operator chooses Answer.
enum WorkspaceWaitingContext {
    static func read(_ row: WorkspaceWaitingRow, asl: ASLService,
                     look: (String) -> AMXCommandResult<PromptLook>) -> SecretText {
        if case let .asl(target)? = row.answer {
            switch asl.snapshot() {
            case let .failure(error): return SecretText(ASLRefusal.text(error))
            case let .success(result):
                guard result.status.available, !result.status.snapshotStale, result.meta?.stale != true,
                      let run = target.row(in: result.snapshot?.rows ?? []),
                      let question = run.question, !question.questionID.isEmpty else {
                    return SecretText("This question is no longer available. Refresh the waiting list.")
                }
                switch asl.question(planDir: run.planDir, questionID: question.questionID) {
                case let .failure(error): return SecretText(ASLRefusal.text(error))
                case let .success(body):
                    guard body.runID == run.runID, body.questionID == question.questionID else {
                        return SecretText("The question changed while loading. Open its context again.")
                    }
                    return body.body
                }
            }
        }
        guard !row.key.runtimeSessionID.isEmpty else {
            return SecretText("Open this session in Kitty to read its current output.")
        }
        switch look(row.key.runtimeSessionID) {
        case let .failure(error): return SecretText(PromptAnswerState.text(error))
        case let .success(screen), let .partial(screen, _):
            return SecretText(screen.lines.isEmpty ? "No screen output is available." : screen.lines.joined(separator: "\n"))
        }
    }
}
