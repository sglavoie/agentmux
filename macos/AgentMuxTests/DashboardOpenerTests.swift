import XCTest
@testable import AgentMux

final class DashboardOpenerTests: XCTestCase {
    private func unavailable() -> AMXCommandResult<OpenDashboardResult> {
        .failure(.command(AMXJSONError(code: "kitty_unavailable", message: "no kitty at that socket"), status: 1))
    }

    func testTriesTheNextSocketOnlyWhenKittyIsUnavailable() {
        var tried: [String] = []
        let outcome = DashboardOpener.open(candidates: ["a", "b", "c"]) { socket in
            tried.append(socket)
            return socket == "a" ? self.unavailable() : .success(OpenDashboardResult(windowID: 4, tabID: 2, opened: true))
        }
        XCTAssertEqual(outcome, .opened(OpenDashboardResult(windowID: 4, tabID: 2, opened: true), socket: "b"))
        XCTAssertEqual(tried, ["a", "b"])
    }

    func testOtherFailuresStop() {
        var tried: [String] = []
        let outcome = DashboardOpener.open(candidates: ["a", "b"]) { socket in
            tried.append(socket)
            return .failure(.command(AMXJSONError(code: "config_invalid", message: "bad config"), status: 1))
        }
        XCTAssertEqual(outcome, .failed("bad config"))
        XCTAssertEqual(tried, ["a"])
        XCTAssertEqual(DashboardOpener.open(candidates: ["a"]) { _ in self.unavailable() }, .failed("no kitty at that socket"))
        XCTAssertEqual(DashboardOpener.open(candidates: []) { _ in self.unavailable() }, .noSocket)
    }

    func testCandidatesPreferTheOverrideThenTheRunningDashboard() {
        var snapshot = CompanionSnapshot()
        snapshot.kitty.socket = "unix:/tmp/kitty-1"
        snapshot.dashboard.open = true
        snapshot.dashboard.kittySocket = "unix:/tmp/kitty-2"
        XCTAssertEqual(
            DashboardOpener.candidates(override: "unix:/tmp/mine", snapshot: snapshot, kittyPIDs: [3], fileExists: { _ in true }),
            ["unix:/tmp/mine", "unix:/tmp/kitty-2", "unix:/tmp/kitty-1", "unix:/tmp/kitty-3"]
        )
        snapshot.dashboard.open = false
        XCTAssertEqual(
            DashboardOpener.candidates(override: "", snapshot: snapshot, kittyPIDs: [], fileExists: { _ in true }),
            ["unix:/tmp/kitty-1"]
        )
    }

    func testDecodesTheCommandData() {
        let stdout = Data(#"{"schema_version":1,"data":{"window_id":12,"tab_id":3,"opened":false}}"#.utf8)
        XCTAssertEqual(AMXEnvelope.decode(OpenDashboardResult.self, stdout: stdout, status: 0),
                       .success(OpenDashboardResult(windowID: 12, tabID: 3, opened: false)))
    }

    func testOpensKittyOnlyWhenOneRuns() {
        let kitty = KittyEndpoint.bundleIdentifier
        XCTAssertTrue(KittyActivator.shouldOpen(running: [.init(pid: 1, bundleIdentifier: kitty), .init(pid: 2, bundleIdentifier: "x")]))
        XCTAssertFalse(KittyActivator.shouldOpen(running: [.init(pid: 1, bundleIdentifier: kitty), .init(pid: 2, bundleIdentifier: kitty)]))
        XCTAssertFalse(KittyActivator.shouldOpen(running: []))
    }
}
