import XCTest
@testable import AgentMux

private struct StoriesImmediateRunner: ASLWorkRunner {
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { then(work()) }
}

/// Reads now, but delivers completions in any order to simulate overlapping subprocesses.
private final class StoriesQueuedRunner: ASLWorkRunner {
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) {
        let result = work()
        pending.append { then(result) }
    }
    func release(_ index: Int = 0) { pending.remove(at: index)() }
}

@MainActor
final class WorkspaceStoriesTests: XCTestCase {
    private var waiting: CompanionSession {
        var session = WorkspaceFixture.tmux(1, project: "api", harness: "codex", icon: "key", changedAtMS: 123)
        session.reason = "command_approval"
        return session
    }
    private var key: AttentionCycler.Key { AttentionCycler.key(waiting) }

    private func look(_ summary: SecretText) -> AMXCommandResult<PromptLook> {
        var look = PromptLook(screen: "blocked", lines: ["raw screen must never become a story"])
        look.prompt = .init(summary: summary)
        return .success(look)
    }

    private func transcript(_ identity: ConversationIdentity, messages: [WorkspaceTranscript.Message]) -> WorkspaceTranscript {
        WorkspaceTranscript(session: RecentConversation(id: identity.sessionID, harness: identity.harness),
                            messages: messages, total: messages.count, start: 0,
                            matchMessages: [], matchTerms: [], focusMessage: nil)
    }

    func testPromptSummaryIsFetchedOnceAndKeptRedacted() {
        var reads = 0
        let model = WorkspaceStories(look: { id in
            XCTAssertEqual(id, self.key.runtimeSessionID)
            reads += 1
            return self.look("Allow creating notes?")
        }, runner: StoriesImmediateRunner())
        var changed: [AttentionCycler.Key] = []
        model.onChange = { changed.append($0) }
        model.update([waiting], live: true)
        model.update([waiting], live: true)
        XCTAssertEqual(reads, 1)
        XCTAssertEqual(changed, [key])
        XCTAssertEqual(model.text(for: key), "Allow creating notes?")
        var dumpText = ""
        dump(model, to: &dumpText)
        XCTAssertFalse(dumpText.contains("Allow creating notes?"))
    }

    func testLastAssistantMessageUsesCleanedFirstNonemptyLine() {
        var session = waiting
        session.iconState = "stop"
        session.conversation = .init(harness: "codex", sessionID: "conversation")
        let model = WorkspaceStories(lastMessages: { identity in
            .success(self.transcript(identity, messages: [
                .init(role: "assistant", text: "Older message", timestamp: nil),
                .init(role: "assistant", text: "\n  \n ## > ** - Tests\t passed   today\nOther details", timestamp: nil),
                .init(role: "user", text: "Thanks", timestamp: nil),
            ]))
        }, runner: StoriesImmediateRunner())
        model.update([session], live: true)
        XCTAssertEqual(model.text(for: key), "Tests passed today")
    }

    func testLastMessageClampsCharactersAndRejectsMissingOrMismatchedEvidence() {
        var session = waiting
        session.iconState = "stop"
        session.conversation = .init(harness: "codex", sessionID: "conversation")
        let longText = String(repeating: "👩🏽‍💻", count: 145)
        let model = WorkspaceStories(lastMessages: { identity in
            .success(self.transcript(identity, messages: [.init(role: "assistant", text: SecretText(longText), timestamp: nil)]))
        }, runner: StoriesImmediateRunner())
        model.update([session], live: true)
        XCTAssertEqual(model.text(for: key), String(longText.prefix(139)) + "…")
        XCTAssertEqual(model.text(for: key).count, 140)
        model.lastMessages = { _ in
            .success(self.transcript(.init(harness: "codex", sessionID: "wrong"), messages: [.init(role: "assistant", text: "Wrong story", timestamp: nil)]))
        }
        session.presentationChangedAtMS += 1
        model.update([session], live: true)
        XCTAssertEqual(model.text(for: key), "")
        model.lastMessages = { identity in
            .success(self.transcript(identity, messages: [.init(role: "user", text: "Only user text", timestamp: nil)]))
        }
        session.presentationChangedAtMS += 1
        model.update([session], live: true)
        XCTAssertEqual(model.text(for: key), "")
    }

    func testAbsentPromptAndFailedReadsCacheEmptyWithoutRawFallback() {
        for failed in [false, true] {
            var reads = 0
            let model = WorkspaceStories(look: { _ in
                reads += 1
                return failed ? .failure(.launchFailed(binary: "amx", reason: "unavailable"))
                    : .success(PromptLook(screen: "blocked", lines: ["Do not show raw lines"]))
            }, runner: StoriesImmediateRunner())
            model.update([waiting], live: true)
            model.update([waiting], live: true)
            XCTAssertEqual(model.text(for: key), "")
            XCTAssertEqual(reads, 1)
            var changed = waiting
            changed.presentationChangedAtMS += 1
            model.update([changed], live: true)
            XCTAssertEqual(reads, 2)
        }
    }

    func testOfflineAndUnsupportedStatesDoNotRead() {
        var reads = 0
        let model = WorkspaceStories(look: { _ in reads += 1; return self.look("Question") },
                                     lastMessages: { _ in XCTFail("No transcript source"); return .failure(.launchFailed(binary: "amx", reason: "unexpected")) },
                                     runner: StoriesImmediateRunner())
        model.update([waiting], live: false)
        XCTAssertEqual(reads, 0)
        for icon in ["bolt", "subagent", "compact", "", "error", "stop"] {
            var session = waiting
            session.iconState = icon
            model.update([session], live: true)
        }
        var missingRuntime = waiting
        missingRuntime.runtime = nil
        model.update([missingRuntime], live: true)
        XCTAssertEqual(reads, 0)
        model.update([waiting], live: true)
        XCTAssertEqual(reads, 1)
    }

    func testLeavingOrLosingSourceRemovesStoryAndNotifiesImmediately() {
        for gone in [false, true] {
            let model = WorkspaceStories(look: { _ in self.look("Question") }, runner: StoriesImmediateRunner())
            model.update([waiting], live: true)
            var changed: [AttentionCycler.Key] = []
            model.onChange = { changed.append($0) }
            var working = waiting
            working.iconState = "bolt"
            model.update(gone ? [] : [working], live: false)
            XCTAssertEqual(model.text(for: key), "")
            XCTAssertEqual(changed, [key])
        }
    }

    func testCodexReasonOnlyChangeClearsDisplayedStoryBeforeFetchingOnce() {
        let runner = StoriesQueuedRunner()
        var reads = 0
        let model = WorkspaceStories(look: { _ in reads += 1; return self.look(SecretText("Question \(reads)")) }, runner: runner)
        model.update([waiting], live: true)
        runner.release()
        var changed = waiting
        changed.reason = "permission_grant"
        var notices: [String] = []
        model.onChange = { notices.append(model.text(for: $0)) }
        model.update([changed], live: true)
        XCTAssertEqual(model.text(for: key), "")
        XCTAssertEqual(notices, [""])
        model.update([changed], live: true)
        XCTAssertEqual(reads, 2)
        XCTAssertEqual(runner.pending.count, 1)
        runner.release()
        XCTAssertEqual(model.text(for: key), "Question 2")
        XCTAssertEqual(notices, ["", "Question 2"])
        model.update([changed], live: true)
        XCTAssertEqual(reads, 2)
    }

    func testObsoleteCompletionsCannotOverwriteOrClearNewPendingRequestInEitherOrder() {
        for newFirst in [false, true] {
            let runner = StoriesQueuedRunner()
            var reads = 0
            let model = WorkspaceStories(look: { _ in reads += 1; return self.look(SecretText("Question \(reads)")) }, runner: runner)
            model.update([waiting], live: true)
            var changed = waiting
            changed.reason = "permission_grant"
            model.update([changed], live: true)
            XCTAssertEqual(reads, 2)
            runner.release(newFirst ? 1 : 0)
            XCTAssertEqual(model.text(for: key), newFirst ? "Question 2" : "")
            model.update([changed], live: true)
            XCTAssertEqual(reads, 2, "old completion must not clear the new in-flight marker")
            runner.release()
            XCTAssertEqual(model.text(for: key), "Question 2")
            model.update([changed], live: true)
            XCTAssertEqual(reads, 2)
        }
    }

    func testConversationIdentityChangeInvalidatesWithOtherRevisionFieldsUnchanged() {
        let runner = StoriesQueuedRunner()
        var session = waiting
        session.iconState = "stop"
        session.conversation = .init(harness: "codex", sessionID: "first")
        var reads = 0
        let model = WorkspaceStories(lastMessages: { identity in
            reads += 1
            return .success(self.transcript(identity, messages: [.init(role: "assistant", text: SecretText(identity.sessionID), timestamp: nil)]))
        }, runner: runner)
        model.update([session], live: true)
        runner.release()
        XCTAssertEqual(model.text(for: key), "first")
        session.conversation = .init(harness: "codex", sessionID: "second")
        model.update([session], live: true)
        XCTAssertEqual(model.text(for: key), "")
        model.update([session], live: true)
        XCTAssertEqual(reads, 2)
        runner.release()
        XCTAssertEqual(model.text(for: key), "second")
    }

    func testRemovedSessionCannotBeRestoredByPendingCompletionEvenAfterReturning() {
        let runner = StoriesQueuedRunner()
        var reads = 0
        let model = WorkspaceStories(look: { _ in reads += 1; return self.look(SecretText("Question \(reads)")) }, runner: runner)
        model.update([waiting], live: true)
        model.update([], live: true)
        model.update([waiting], live: true)
        runner.release()
        XCTAssertEqual(model.text(for: key), "")
        model.update([waiting], live: true)
        XCTAssertEqual(reads, 2)
        runner.release()
        XCTAssertEqual(model.text(for: key), "Question 2")
    }
}
