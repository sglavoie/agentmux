import XCTest
@testable import AgentMux

final class WorkspaceReviewStateTests: XCTestCase {
    private var suite = ""
    private var defaults: UserDefaults!

    override func setUpWithError() throws {
        suite = "amx.tests.reviewstate.\(UUID())"
        defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
    }

    override func tearDown() {
        defaults.removePersistentDomain(forName: suite)
    }

    func testFullStoreEvictsTheEntrySavedLongestAgoNotTheAlphabeticallyFirst() {
        var clock = Date(timeIntervalSince1970: 1_000)
        let state = WorkspaceReviewState(defaults: defaults, now: { clock })
        // "a…" is saved last, so the old alphabetical rule would have evicted it.
        for index in (0..<WorkspaceReviewState.entryLimit).reversed() {
            clock += 1
            state.savePosition(NSPoint(x: 1, y: Double(index + 1)), id: "a\(index)")
        }
        // Re-saving an existing entry makes it recent again.
        clock += 1
        state.savePosition(NSPoint(x: 2, y: 2), id: "a99")
        clock += 1
        state.savePosition(NSPoint(x: 3, y: 3), id: "z-new")

        XCTAssertEqual(state.position("z-new"), NSPoint(x: 3, y: 3))
        XCTAssertEqual(state.position("a99"), NSPoint(x: 2, y: 2), "re-saved entry must survive")
        XCTAssertEqual(state.position("a98"), .zero, "the entry saved longest ago is evicted")
        XCTAssertEqual(state.position("a0"), NSPoint(x: 1, y: 1), "alphabetically first entry is kept")
    }

    func testComparisonsUseTheSameLeastRecentlyUsedRule() {
        var clock = Date(timeIntervalSince1970: 50)
        let state = WorkspaceReviewState(defaults: defaults, now: { clock })
        for index in 0..<WorkspaceReviewState.entryLimit {
            clock += 1
            state.saveComparison(.init(base: "b\(index)", file: nil), target: ["--directory", "/r\(index)"])
        }
        clock += 1
        state.saveComparison(.init(base: "new", file: "f"), target: ["--directory", "/new"])
        XCTAssertNil(state.comparison(["--directory", "/r0"]))
        XCTAssertEqual(state.comparison(["--directory", "/r1"])?.base, "b1")
        XCTAssertEqual(state.comparison(["--directory", "/new"])?.file, "f")
    }

    func testUnstampedEntriesFromEarlierReleasesAreReadAndEvictedFirst() throws {
        let legacyPositions = ["old": WorkspaceReviewState.Position(x: 4, y: 5)]
        defaults.set(try JSONEncoder().encode(legacyPositions), forKey: "amxReview.positions")
        let legacyComparisons = ["--directory\u{0}/repo": WorkspaceReviewState.Comparison(base: "main", file: "a.swift")]
        defaults.set(try JSONEncoder().encode(legacyComparisons), forKey: "amxReview.comparisons")

        var clock = Date(timeIntervalSince1970: 10)
        let state = WorkspaceReviewState(defaults: defaults, now: { clock })
        XCTAssertEqual(state.position("old"), NSPoint(x: 4, y: 5))
        XCTAssertEqual(state.comparison(["--directory", "/repo"])?.base, "main")

        // The first save rewrites the store in the stamped format, keeping the old entry.
        state.savePosition(NSPoint(x: 6, y: 7), id: "a-new")
        XCTAssertEqual(state.position("old"), NSPoint(x: 4, y: 5))
        let stored = try XCTUnwrap(defaults.data(forKey: "amxReview.positions"))
        let decoded = try JSONDecoder().decode([String: WorkspaceReviewState.Stamped<WorkspaceReviewState.Position>].self, from: stored)
        XCTAssertEqual(decoded["old"]?.used, 0)
        XCTAssertEqual(decoded["a-new"]?.used, 10)

        for index in 0..<(WorkspaceReviewState.entryLimit - 1) {
            clock += 1
            state.savePosition(NSPoint(x: 1, y: 1), id: "fill\(index)")
        }
        XCTAssertEqual(state.position("old"), .zero, "a legacy entry counts as never used")
        XCTAssertEqual(state.position("a-new"), NSPoint(x: 6, y: 7))
    }

    func testUnreadableStoreStartsOverInsteadOfFailing() {
        defaults.set(Data("not json".utf8), forKey: "amxReview.positions")
        defaults.set(Data("not json".utf8), forKey: "amxReview.historyLayout")
        let state = WorkspaceReviewState(defaults: defaults)
        XCTAssertEqual(state.position("any"), .zero)
        XCTAssertFalse(state.historyLayout.collapsed)
        state.savePosition(NSPoint(x: 8, y: 9), id: "any")
        XCTAssertEqual(state.position("any"), NSPoint(x: 8, y: 9))
    }
}
