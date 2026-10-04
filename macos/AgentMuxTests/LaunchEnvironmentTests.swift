import XCTest
@testable import AgentMux

final class LaunchEnvironmentTests: XCTestCase {
    func testExtractReportIgnoresProfileNoiseAndKeepsNewlines() {
        let b = LaunchEnvironment.beginMarker
        let e = LaunchEnvironment.endMarker
        let output = "welcome!\n\(b)PATH=/a:/b\0AMX_CONFIG=/c.toml\0MULTI=one\ntwo\0EMPTY=\0junk\0\(e)bye\n"
        XCTAssertEqual(LaunchEnvironment.extractReport(from: output, begin: b, end: e),
                       ["PATH": "/a:/b", "AMX_CONFIG": "/c.toml", "MULTI": "one\ntwo"])
        XCTAssertNil(LaunchEnvironment.extractReport(from: "no markers", begin: b, end: e))
        XCTAssertNil(LaunchEnvironment.extractReport(from: "\(e)\(b)", begin: b, end: e))
    }

    func testResolveForwardsXDGAndAMXFromTheShellOverTheProcess() {
        let process = [
            "HOME": "/Users/me", "PATH": "/usr/bin", "AMX_DEBUG": "1", "XDG_CONFIG_HOME": "/old",
            "EDITOR": "vi", "KITTY_LISTEN_ON": "unix:/tmp/k",
        ]
        let reported = [
            "PATH": "/opt/homebrew/bin:/usr/bin", "XDG_CONFIG_HOME": "/Users/me/.config", "AMX_CONFIG": "/c.toml",
            "XDG_": "prefix only", "AMX_EMPTY": "", "TERM": "xterm",
        ]
        let environment = LaunchEnvironment.resolve(processEnvironment: process, reported: reported)
        XCTAssertEqual(environment.home, "/Users/me")
        XCTAssertEqual(environment.path, "/opt/homebrew/bin:/usr/bin")
        XCTAssertEqual(environment.forwarded, [
            "XDG_CONFIG_HOME": "/Users/me/.config", "AMX_CONFIG": "/c.toml", "AMX_DEBUG": "1",
        ])
        XCTAssertEqual(environment.commandEnvironment, [
            "XDG_CONFIG_HOME": "/Users/me/.config", "AMX_CONFIG": "/c.toml", "AMX_DEBUG": "1",
            "HOME": "/Users/me", "PATH": "/opt/homebrew/bin:/usr/bin",
        ])
    }

    func testResolveWithoutAShellFallsBackToTheProcess() {
        let environment = LaunchEnvironment.resolve(processEnvironment: ["HOME": "/h", "PATH": "/x"], reported: nil)
        XCTAssertEqual(environment, LaunchEnvironment(home: "/h", path: "/x"))
        XCTAssertEqual(LaunchEnvironment.resolve(processEnvironment: ["HOME": "/h"], reported: nil).path,
                       "/usr/bin:/bin:/usr/sbin:/sbin")
    }

    func testReportScriptRunsInARealShell() throws {
        let report = try XCTUnwrap(LaunchEnvironment.loginShellEnvironment(shell: "/bin/sh", interactive: false))
        XCTAssertFalse(report["PATH", default: ""].isEmpty)
    }
}
