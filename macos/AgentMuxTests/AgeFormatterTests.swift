import XCTest
@testable import AgentMux

final class AgeFormatterTests: XCTestCase {
    private let now = Date(timeIntervalSince1970: 1_800_000_000)

    private func age(_ seconds: TimeInterval, _ style: AgeFormatter.Style, seconds counted: Bool = false) -> String {
        AgeFormatter.string(since: now.addingTimeInterval(-seconds), now: now, style: style, seconds: counted)
    }

    func testCompactMatchesTheTUIsCompactAge() {
        XCTAssertEqual(age(5, .compact), "now")
        XCTAssertEqual(age(240, .compact), "4m")
        XCTAssertEqual(age(7300, .compact), "2h")
        XCTAssertEqual(age(3 * 86400, .compact), "3d")
        XCTAssertEqual(age(-60, .compact), "now", "a clock skew is not negative")
    }

    func testSentenceRollsOverToDays() {
        XCTAssertEqual(age(5, .sentence), "just now")
        XCTAssertEqual(age(60, .sentence), "1m ago")
        XCTAssertEqual(age(240, .sentence), "4m ago")
        XCTAssertEqual(age(7300, .sentence), "2h ago")
        XCTAssertEqual(age(72 * 3600, .sentence), "3d ago", "an old refresh reads in days, not 72h")
        XCTAssertEqual(age(-60, .sentence), "just now", "a clock skew is not negative")
    }

    func testCompactMatchesSentence() {
        for seconds: TimeInterval in [5, 240, 7300, 72 * 3600] {
            let compact = age(seconds, .compact)
            let sentence = age(seconds, .sentence)
            XCTAssertTrue(compact == "now" ? sentence == "just now" : sentence == compact + " ago", "\(compact) / \(sentence)")
        }
    }

    func testSecondsAreCountedOnlyWhenAsked() {
        XCTAssertEqual(age(12, .compact, seconds: true), "12s")
        XCTAssertEqual(age(12, .sentence, seconds: true), "12s ago")
        XCTAssertEqual(age(0, .sentence, seconds: true), "0s ago")
        XCTAssertEqual(age(125, .sentence, seconds: true), "2m ago")
    }
}
