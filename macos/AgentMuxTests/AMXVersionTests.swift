import XCTest
@testable import AgentMux

final class AMXVersionTests: XCTestCase {
    let full = "amx version 0.8.0 (revision 52189e529adf-dirty, built 2026-10-03T19:39:22Z, go1.27.1, sha256:ccdd2b5266db)\n"

    func testParsesEveryPart() throws {
        let version = try XCTUnwrap(AMXVersion.parse(full))
        XCTAssertEqual(version.version, "0.8.0")
        XCTAssertEqual(version.revision, "52189e529adf")
        XCTAssertTrue(version.dirty)
        XCTAssertEqual(version.built, ISO8601DateFormatter().date(from: "2026-10-03T19:39:22Z"))
        XCTAssertEqual(version.goVersion, "go1.27.1")
        XCTAssertEqual(version.sha256Prefix, "ccdd2b5266db")
        XCTAssertEqual(version.line, String(full.dropLast()))
        XCTAssertEqual(version.shortDescription, "0.8.0 (52189e529adf-dirty)")
    }

    func testCleanBuildHasNoDirtyMarker() throws {
        let version = try XCTUnwrap(AMXVersion.parse("amx version 1.2.3 (revision abcdef123456, built 2026-01-01T00:00:00Z)"))
        XCTAssertEqual(version.revision, "abcdef123456")
        XCTAssertFalse(version.dirty)
        XCTAssertNil(version.goVersion)
        XCTAssertNil(version.sha256Prefix)
    }

    func testUnstampedBuildHasNoRevisionOrTime() throws {
        // `go test` or a build outside a checkout: buildinfo prints "unknown".
        let version = try XCTUnwrap(AMXVersion.parse("amx version dev (revision unknown, go1.27.1)"))
        XCTAssertEqual(version.version, "dev")
        XCTAssertNil(version.revision)
        XCTAssertNil(version.built)
        XCTAssertEqual(version.shortDescription, "dev (unknown)")
    }

    func testVersionWithoutParentheses() throws {
        let version = try XCTUnwrap(AMXVersion.parse("amx version 0.1.0"))
        XCTAssertEqual(version.version, "0.1.0")
        XCTAssertNil(version.revision)
    }

    func testRejectsOtherOutput() {
        XCTAssertNil(AMXVersion.parse(""))
        XCTAssertNil(AMXVersion.parse("tt version 1.0.0 (commit: x, built: y)"))
        XCTAssertNil(AMXVersion.parse("Error: unknown flag: --version"))
        XCTAssertNil(AMXVersion.parse("amx version  (revision x)"))
    }

    func testOnlyTheFirstLineCounts() throws {
        let version = try XCTUnwrap(AMXVersion.parse("amx version 0.8.0 (revision aaaaaaaaaaaa)\nwarning: something"))
        XCTAssertEqual(version.revision, "aaaaaaaaaaaa")
    }

    func testMatchesDaemonByHashPrefix() throws {
        let version = try XCTUnwrap(AMXVersion.parse(full))
        XCTAssertTrue(version.matches(DaemonBuild(version: "0.8.0", binaryHash: "CCDD2B5266DB3846a7eb")))
        XCTAssertFalse(version.matches(DaemonBuild(version: "0.8.0", revision: "52189e529adfe3f2", dirty: true, binaryHash: "ffff")),
                       "a differing hash decides even when the revision agrees")
    }

    func testMatchesDaemonByRevisionWithoutHash() throws {
        let version = try XCTUnwrap(AMXVersion.parse("amx version 0.8.0 (revision 52189e529adf-dirty)"))
        XCTAssertTrue(version.matches(DaemonBuild(version: "0.8.0", revision: "52189e529adfe3f24cb1", dirty: true, binaryHash: "abc")))
        XCTAssertFalse(version.matches(DaemonBuild(version: "0.8.0", revision: "52189e529adfe3f24cb1", dirty: false)))
        XCTAssertFalse(version.matches(DaemonBuild(version: "0.7.0", revision: "52189e529adfe3f24cb1", dirty: true)))
        XCTAssertFalse(version.matches(DaemonBuild(version: "0.8.0", revision: "ffff", dirty: true)))
    }

    func testReadRunsTheBinary() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("amx-version-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }
        let script = dir.appendingPathComponent("amx")
        try "#!/bin/sh\necho 'amx version 0.9.0 (revision 0123456789ab)'\n".write(to: script, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: script.path)
        XCTAssertEqual(AMXVersion.read(binary: script.path)?.version, "0.9.0")
        XCTAssertNil(AMXVersion.read(binary: dir.appendingPathComponent("missing").path))
    }
}
