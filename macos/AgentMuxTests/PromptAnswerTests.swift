import SwiftUI
import XCTest
@testable import AgentMux

/// Runs the work and its continuation at once, on the calling thread.
private struct ImmediateRunner: ASLWorkRunner {
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { then(work()) }
}

/// Holds work until the test releases it, to stand in for a slow amx.
private final class HeldRunner: ASLWorkRunner {
    private var held: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) {
        held.append { then(work()) }
    }
    func release() {
        let pending = held
        held = []
        pending.forEach { $0() }
    }
}

private final class FakePromptService: PromptAnswerService {
    var looks: [AMXCommandResult<PromptLook>] = []
    var presses: [AMXCommandResult<PromptSent>] = []
    private(set) var calls: [[String]] = []

    func look(runtimeSessionID: String) -> AMXCommandResult<PromptLook> {
        calls.append(PromptLook.arguments(runtimeSessionID: runtimeSessionID))
        return looks.count > 1 ? looks.removeFirst() : looks.first ?? .success(PromptLook())
    }

    func press(arguments: [String]) -> AMXCommandResult<PromptSent> {
        calls.append(arguments)
        return presses.isEmpty ? .success(PromptSent(delivery: "accepted")) : presses.removeFirst()
    }
}

private func refusal(_ code: String, _ message: String = "") -> AMXCommandError {
    .command(AMXJSONError(code: code, message: message), status: 1)
}

@MainActor
final class PromptAnswerTests: XCTestCase {
    private let id = "rs-0123456789abcdef0123456789abcdef"
    private var target: PromptAnswerTarget { PromptAnswerTarget(runtimeSessionID: id) }
    private let asking = PromptLook(project: "api", screen: "blocked", lines: ["Do you want to proceed?", "❯ 1. Yes", "  2. No"])
    private let two = PromptKey.digits[1]

    private func flow(_ service: FakePromptService, runner: ASLWorkRunner = ImmediateRunner()) -> (PromptAnswerFlow, () -> [TimeInterval]) {
        var pauses: [TimeInterval] = []
        let flow = PromptAnswerFlow(service: service, runner: runner, pause: { pauses.append($0) })
        return (flow, { pauses })
    }

    // MARK: Wire

    func testLookAndSentDecodeFromTheSendEnvelope() throws {
        let look = AMXEnvelope.decode(PromptLook.self, stdout: Data("""
        {"schema_version":1,"data":{"runtime_session_id":"\(id)","backend":"tmux","project":"api","screen":"blocked","lines":["a","b"],"later":1}}
        """.utf8), status: 0)
        XCTAssertEqual(look.value, PromptLook(project: "api", screen: "blocked", lines: ["a", "b"]))
        XCTAssertEqual(look.value?.asking, true)
        let sent = AMXEnvelope.decode(PromptSent.self, stdout: Data("""
        {"schema_version":1,"data":{"runtime_session_id":"\(id)","operation_id":"send-1","input":"keys","screen":"blocked","delivery":"accepted"}}
        """.utf8), status: 0)
        XCTAssertEqual(sent.value, PromptSent(delivery: "accepted"))
        XCTAssertEqual(PromptLook.arguments(runtimeSessionID: id), ["send", id, "--look"])
    }

    func testEveryKeyIsOneAmxSendTakes() {
        // backend.KeyNames, less the three the panel has no use for.
        let names = Set(["enter", "escape", "tab", "shift-tab", "up", "down", "left", "right", "space", "y", "n"] + (0...9).map(String.init))
        for key in PromptKey.all {
            XCTAssertTrue(names.contains(key.name), key.name)
        }
        XCTAssertEqual(Set(PromptKey.all.map(\.name)).count, PromptKey.all.count)
        XCTAssertEqual(PromptKey.typed("2")?.name, "2")
        XCTAssertEqual(PromptKey.typed("y")?.name, "y")
        XCTAssertEqual(PromptKey.typed("\r")?.name, "enter")
        XCTAssertEqual(PromptKey.typed("\u{F700}")?.name, "up", "↑ arrives as NSUpArrowFunctionKey")
        XCTAssertEqual(PromptKey.typed("\u{F701}")?.name, "down", "↓ arrives as NSDownArrowFunctionKey")
        XCTAssertNil(PromptKey.typed("\u{F702}"), "← is not a key the panel forwards")
        XCTAssertNil(PromptKey.typed("x"))
        XCTAssertNil(PromptKey.typed("\u{1b}"), "Escape closes the panel; the session's is the button")
    }

    // MARK: Rules

    func testNoKeyBeforeTheScreenHasBeenSeen() {
        var (state, effect) = PromptAnswerState.open(target)
        XCTAssertEqual(effect, .look(seq: 1, settle: false))
        XCTAssertEqual(state.phase, .loading)
        XCTAssertFalse(state.canPress)
        XCTAssertNil(state.press(two), "a key pressed before the look would be a blind approval")

        state.looked(seq: 1, .success(asking))
        XCTAssertEqual(state.phase, .open)
        XCTAssertEqual(state.title, "api")
        XCTAssertTrue(state.canPress)
        XCTAssertEqual(state.press(two), .press(seq: 2, arguments: ["send", id, "--key", "2"]))
        XCTAssertFalse(state.canPress, "one key at a time")
        XCTAssertNil(state.press(two))
    }

    func testNoKeyWhereNothingIsAsked() {
        for screen in ["working", "idle", "unknown", ""] {
            var (state, _) = PromptAnswerState.open(target)
            state.looked(seq: 1, .success(PromptLook(project: "api", screen: screen, lines: ["❯"])))
            XCTAssertEqual(state.phase, .open, screen)
            XCTAssertFalse(state.canPress, screen)
            XCTAssertNil(state.press(two), screen)
            XCTAssertFalse(state.caption.isEmpty, screen)
        }
    }

    func testAPressedKeyIsFollowedByALookAtWhatItLedTo() {
        var (state, _) = PromptAnswerState.open(target)
        state.looked(seq: 1, .success(asking))
        _ = state.press(two)
        XCTAssertEqual(state.pressed(seq: 2, .success(PromptSent(delivery: "accepted"))), .look(seq: 3, settle: true))
        XCTAssertEqual(state.notice, "Sent.")
        XCTAssertEqual(state.noticeKind, .info, "a key that was sent is no error")
        XCTAssertFalse(state.canPress, "not until the screen has been read again")
        state.looked(seq: 3, .success(PromptLook(project: "api", screen: "working", lines: ["⏺ Working"])))
        XCTAssertFalse(state.canPress)
        XCTAssertEqual(state.look.lines, ["⏺ Working"])
    }

    func testARunsSessionAsksForTheSameKeyTwice() {
        var (state, _) = PromptAnswerState.open(target)
        state.looked(seq: 1, .success(asking))
        _ = state.press(two)
        XCTAssertNil(state.pressed(seq: 2, .failure(refusal("confirmation_required"))))
        XCTAssertTrue(state.notice.contains("again to confirm"))
        XCTAssertEqual(state.noticeKind, .confirm)
        XCTAssertTrue(state.canPress)
        XCTAssertEqual(state.press(two), .press(seq: 3, arguments: ["send", id, "--key", "2", "--yes"]))

        // A different key is a different decision, and is asked about afresh.
        _ = state.pressed(seq: 3, .failure(refusal("confirmation_required")))
        XCTAssertEqual(state.press(PromptKey.digits[0]), .press(seq: 4, arguments: ["send", id, "--key", "1"]))
    }

    func testRefusalsAreWordedForThePanel() {
        var (state, _) = PromptAnswerState.open(target)
        state.looked(seq: 1, .success(asking))
        _ = state.press(two)
        XCTAssertEqual(state.pressed(seq: 2, .failure(refusal("no_prompt_showing", "pass --any-screen to press them anyway"))),
                       .look(seq: 3, settle: true))
        XCTAssertFalse(state.notice.contains("--any-screen"), "the panel has no flags")
        XCTAssertTrue(state.notice.contains("not asking"))
        XCTAssertEqual(state.noticeKind, .error)
        for code in ["screen_unreadable", "asl_question_open", "unsupported_backend", "not_found", "runtime_session_exited"] {
            XCTAssertFalse(PromptAnswerState.text(refusal(code, "raw --flag text")).contains("--"), code)
        }
        XCTAssertEqual(PromptAnswerState.text(refusal("something_new", "its own sentence")), "its own sentence")
    }

    func testAFirstLookThatFailsRefusesThePanel() {
        var (state, _) = PromptAnswerState.open(target)
        state.looked(seq: 1, .failure(refusal("runtime_session_exited")))
        XCTAssertEqual(state.phase, .refused("This session's agent is no longer running."))
        XCTAssertNil(state.press(two))
        XCTAssertTrue(state.canLookAgain, "a failed first look can be tried again")

        // Looking again waits as the first look did, and can open the panel.
        XCTAssertEqual(state.refresh(), .look(seq: 2, settle: false))
        XCTAssertEqual(state.phase, .loading)
        XCTAssertFalse(state.canLookAgain)
        XCTAssertNil(state.refresh(), "one look at a time")
        state.looked(seq: 2, .success(asking))
        XCTAssertEqual(state.phase, .open)
        XCTAssertTrue(state.canPress)
    }

    func testALookAgainClearsWhatAnEarlierKeySaid() {
        var (state, _) = PromptAnswerState.open(target)
        state.looked(seq: 1, .success(asking))
        _ = state.press(two)
        XCTAssertEqual(state.pressed(seq: 2, .failure(refusal("no_prompt_showing"))), .look(seq: 3, settle: true))
        state.looked(seq: 3, .success(asking))
        XCTAssertEqual(state.notice, "The session is not asking anything now, so the key was not pressed.",
                       "the look after a key keeps that key's notice")

        _ = state.refresh()
        state.looked(seq: 4, .failure(refusal("screen_unreadable")))
        XCTAssertFalse(state.notice.isEmpty, "a look that fails after opening says why")
        XCTAssertEqual(state.noticeKind, .error)

        _ = state.refresh()
        state.looked(seq: 5, .success(asking))
        XCTAssertEqual(state.notice, "", "a look the operator asked for shows the screen as it is now")
    }

    func testALookAgainAsksForAConfirmationAfresh() {
        var (state, _) = PromptAnswerState.open(target)
        state.looked(seq: 1, .success(asking))
        _ = state.press(two)
        XCTAssertNil(state.pressed(seq: 2, .failure(refusal("confirmation_required"))))
        _ = state.refresh()
        state.looked(seq: 3, .success(asking))
        XCTAssertEqual(state.notice, "")
        XCTAssertEqual(state.press(two), .press(seq: 4, arguments: ["send", id, "--key", "2"]),
                       "the warning is gone, so the key is not confirmed")
    }

    func testModifiedKeysAreLeftToTheMenus() {
        XCTAssertEqual(PromptAnswerView.key("2", modifiers: []), two)
        XCTAssertEqual(PromptAnswerView.key("y", modifiers: .shift), PromptKey.answers[0])
        XCTAssertEqual(PromptAnswerView.key("\r", modifiers: []), PromptKey.navigation[2])
        // Arrow keys carry the function and numeric-pad flags; they are not shortcuts.
        XCTAssertEqual(PromptAnswerView.key("\u{F700}", modifiers: [.function, .numericPad]), PromptKey.navigation[0])
        XCTAssertEqual(PromptAnswerView.key("\u{F701}", modifiers: [.function, .numericPad]), PromptKey.navigation[1])
        XCTAssertNil(PromptAnswerView.key("\u{F700}", modifiers: [.command, .function, .numericPad]))
        for modifiers: EventModifiers in [.command, .control, .option, [.command, .shift]] {
            XCTAssertNil(PromptAnswerView.key("2", modifiers: modifiers))
            XCTAssertNil(PromptAnswerView.key("n", modifiers: modifiers))
        }
        XCTAssertNil(PromptAnswerView.key("", modifiers: []))
    }

    func testAnswersForAnEarlierRequestAreDropped() {
        var (state, _) = PromptAnswerState.open(target)
        state.looked(seq: 1, .success(asking))
        _ = state.press(two)
        let before = state
        state.looked(seq: 1, .success(PromptLook(project: "stale", screen: "idle")))
        XCTAssertNil(state.pressed(seq: 1, .success(PromptSent())))
        XCTAssertEqual(state, before)
    }

    // MARK: Flow

    func testFlowLooksThenPressesThenLooksAgainAfterSettling() {
        let service = FakePromptService()
        service.looks = [.success(asking), .success(PromptLook(project: "api", screen: "working", lines: ["⏺"]))]
        let (flow, pauses) = flow(service)

        flow.open(target)
        XCTAssertEqual(service.calls, [["send", id, "--look"]])
        XCTAssertEqual(flow.state?.canPress, true)
        XCTAssertEqual(pauses(), [], "the first look does not wait")

        flow.press(two)
        XCTAssertEqual(service.calls, [["send", id, "--look"], ["send", id, "--key", "2"], ["send", id, "--look"]])
        XCTAssertEqual(pauses(), [PromptAnswerFlow.settle])
        XCTAssertEqual(flow.state?.look.screen, "working")
        XCTAssertEqual(flow.state?.canPress, false)

        // Nothing is asked now, so a key goes nowhere.
        flow.press(two)
        XCTAssertEqual(service.calls.count, 3)
    }

    func testReopeningTheSameSessionLooksAgainAndAnotherSessionStartsOver() {
        let service = FakePromptService()
        service.looks = [.success(asking)]
        let (flow, _) = flow(service)
        flow.open(target)
        flow.open(target)
        XCTAssertEqual(service.calls.count, 2)
        XCTAssertEqual(flow.state?.phase, .open)
        XCTAssertEqual(flow.shown, 2, "each opening gives the panel's keys the keyboard again")

        let other = PromptAnswerTarget(runtimeSessionID: "rs-ffffffffffffffffffffffffffffffff")
        flow.open(other)
        XCTAssertEqual(flow.state?.target, other)
        XCTAssertEqual(service.calls.last, ["send", other.runtimeSessionID, "--look"])
    }

    func testReopeningASessionWhoseFirstLookFailedStartsOver() {
        let service = FakePromptService()
        service.looks = [.failure(refusal("screen_unreadable")), .success(asking)]
        let (flow, _) = flow(service)
        flow.open(target)
        XCTAssertEqual(flow.state?.isRefused, true)
        flow.open(target)
        XCTAssertEqual(service.calls.count, 2)
        XCTAssertEqual(flow.state?.phase, .open)
        XCTAssertEqual(flow.state?.canPress, true)
    }

    func testAnAnswerThatOutlivesThePanelIsDropped() {
        let service = FakePromptService()
        service.looks = [.success(asking)]
        let runner = HeldRunner()
        let (flow, _) = flow(service, runner: runner)
        flow.open(target)
        flow.cancel()
        runner.release()
        XCTAssertNil(flow.state)

        // And one for the panel as it was before it was reopened.
        flow.open(target)
        let firstSeq = flow.state?.seq
        flow.cancel()
        flow.open(target)
        XCTAssertNotEqual(flow.state?.seq, firstSeq, "a reopened panel never reuses a sequence")
        runner.release()
        XCTAssertEqual(flow.state?.phase, .open)
    }

    func testWithNoAmxThePanelSaysSo() {
        let service = AMXPromptAnswerService(command: { nil }, problem: { "amx was not found" })
        guard case let .failure(error) = service.look(runtimeSessionID: id) else { return XCTFail("looked with no amx") }
        XCTAssertEqual(error.message, "amx was not found")
        guard case .failure = service.press(arguments: ["send", id, "--key", "1"]) else { return XCTFail("pressed with no amx") }
    }
}
