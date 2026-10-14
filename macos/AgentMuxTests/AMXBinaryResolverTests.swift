import XCTest
@testable import AgentMux

final class AMXBinaryResolverTests: XCTestCase {
    private var root: URL!
    private var home: URL!
    private var resources: URL!
    private var pathA: URL!
    private var pathB: URL!

    override func setUpWithError() throws {
        root = FileManager.default.temporaryDirectory.appendingPathComponent("amx-resolve-\(UUID().uuidString)")
        home = root.appendingPathComponent("home")
        resources = root.appendingPathComponent("Agent Mux.app/Contents/Resources")
        pathA = root.appendingPathComponent("path-a")
        pathB = root.appendingPathComponent("path-b")
        for dir in [home!, resources!, pathA!, pathB!] {
            try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        }
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: root)
    }

    @discardableResult
    private func makeBinary(_ url: URL, executable: Bool = true) throws -> URL {
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try "#!/bin/sh\n".write(to: url, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: executable ? 0o755 : 0o644], ofItemAtPath: url.path)
        return url
    }

    private var bundled: URL { resources.appendingPathComponent("bin/amx") }
    private var localBin: URL { home.appendingPathComponent(".local/bin/amx") }

    private struct Facts {
        var versions: [URL: String] = [:]
        var hashes: [URL: String] = [:]
        var modified: [URL: Date] = [:]
    }

    private func resolver(override: String? = nil, daemon: DaemonBuild? = nil, facts: Facts = Facts()) -> AMXBinaryResolver {
        AMXBinaryResolver(
            overridePath: override,
            bundleResourcesURL: resources,
            homeDirectory: home,
            searchPath: "\(pathA.path)::\(pathB.path)",
            daemonBuild: daemon,
            identity: { path in facts.versions[URL(fileURLWithPath: path)].flatMap(AMXVersion.parse) },
            contentHash: { path in facts.hashes[URL(fileURLWithPath: path)] },
            modificationDate: { path in facts.modified[URL(fileURLWithPath: path)] }
        )
    }

    private func line(_ revision: String, built: String = "2026-10-03T12:00:00Z", dirty: Bool = false, sha: String? = nil) -> String {
        var parts = ["revision \(revision)\(dirty ? "-dirty" : "")", "built \(built)", "go1.27.1"]
        if let sha { parts.append("sha256:\(sha)") }
        return "amx version 0.8.0 (\(parts.joined(separator: ", ")))"
    }

    // MARK: Search order

    func testOverrideWinsOverEverything() throws {
        let custom = try makeBinary(root.appendingPathComponent("custom/amx"))
        try makeBinary(bundled)
        try makeBinary(localBin)
        try makeBinary(pathA.appendingPathComponent("amx"))
        XCTAssertEqual(try resolver(override: custom.path).resolve().get(), .init(url: custom, source: .userDefaultsOverride))
    }

    func testOverrideExpandsTilde() throws {
        try makeBinary(localBin)
        XCTAssertEqual(try resolver(override: "~/.local/bin/amx").resolve().get(), .init(url: localBin, source: .userDefaultsOverride))
    }

    func testBlankOverrideIsUnset() throws {
        try makeBinary(bundled)
        XCTAssertEqual(try resolver(override: "  ").resolve().get().source, .bundled)
    }

    func testInvalidOverrideFailsClosed() throws {
        try makeBinary(bundled)
        let missing = root.appendingPathComponent("nope/amx").path
        guard case .failure(let error) = resolver(override: missing).resolve() else {
            return XCTFail("an unusable override must not fall back")
        }
        XCTAssertEqual(error.invalidOverride, missing)
        XCTAssertEqual(error.tried, [missing])
        XCTAssertTrue(error.message.contains("defaults delete io.github.sglavoie.agentmux amxBinaryPath"))
        XCTAssertEqual(error.menuMessage, "The amx binary override is not an executable — fix it in Settings › amx binary")
        XCTAssertFalse(error.menuMessage.contains("\n"))
    }

    func testBundledBeatsLocalBinAndPathWhenNothingIsKnown() throws {
        try makeBinary(bundled)
        try makeBinary(localBin)
        try makeBinary(pathA.appendingPathComponent("amx"))
        XCTAssertEqual(try resolver().resolve().get(), .init(url: bundled, source: .bundled))
    }

    func testLocalBinBeatsPath() throws {
        try makeBinary(localBin)
        try makeBinary(pathA.appendingPathComponent("amx"))
        XCTAssertEqual(try resolver().resolve().get(), .init(url: localBin, source: .localBin))
    }

    func testPathInOrderSkippingNonExecutablesAndDirectories() throws {
        try makeBinary(pathA.appendingPathComponent("amx"), executable: false)
        try FileManager.default.createDirectory(at: localBin, withIntermediateDirectories: true)
        let second = try makeBinary(pathB.appendingPathComponent("amx"))
        XCTAssertEqual(try resolver().resolve().get(), .init(url: second, source: .path))
    }

    func testNotFoundListsEveryCandidateOnce() throws {
        var other = resolver()
        other.searchPath = "\(pathA.path):\(pathA.path)"
        guard case .failure(let error) = other.resolve() else { return XCTFail("nothing is installed") }
        XCTAssertNil(error.invalidOverride)
        XCTAssertEqual(error.tried, [bundled.path, localBin.path, pathA.appendingPathComponent("amx").path])
        XCTAssertTrue(error.message.contains("just install"))
        XCTAssertEqual(error.menuMessage, "amx not found — set it in Settings › amx binary")
        XCTAssertFalse(error.menuMessage.contains("\n"))
        XCTAssertFalse(error.menuMessage.contains(bundled.path), "the tried paths stay in Settings and the log")
    }

    // MARK: Bundled against ~/.local/bin

    func testLocalBinMatchingTheDaemonWins() throws {
        try makeBinary(bundled)
        try makeBinary(localBin)
        var facts = Facts()
        // The local copy is even older than the bundled one; matching the
        // daemon still decides.
        facts.versions[localBin] = line("aaaaaaaaaaaa", built: "2026-01-01T00:00:00Z", sha: "111111111111")
        facts.versions[bundled] = line("bbbbbbbbbbbb", built: "2026-06-01T00:00:00Z", sha: "222222222222")
        let daemon = DaemonBuild(version: "0.8.0", revision: "aaaaaaaaaaaa00", binaryHash: "111111111111ffff")
        XCTAssertEqual(try resolver(daemon: daemon, facts: facts).resolve().get(), .init(url: localBin, source: .localBinMatchingDaemon))
    }

    func testBundledMatchingTheDaemonIsKeptOverAnOlderLocalBin() throws {
        try makeBinary(bundled)
        try makeBinary(localBin)
        var facts = Facts()
        facts.versions[localBin] = line("aaaaaaaaaaaa", built: "2026-01-01T00:00:00Z", sha: "111111111111")
        facts.versions[bundled] = line("bbbbbbbbbbbb", built: "2026-06-01T00:00:00Z", sha: "222222222222")
        let daemon = DaemonBuild(version: "0.8.0", revision: "bbbbbbbbbbbb", binaryHash: "222222222222ffff")
        XCTAssertEqual(try resolver(daemon: daemon, facts: facts).resolve().get().source, .bundled)
    }

    func testNewerLocalBinWins() throws {
        try makeBinary(bundled)
        try makeBinary(localBin)
        var facts = Facts()
        facts.versions[localBin] = line("aaaaaaaaaaaa", built: "2026-06-01T00:00:00Z")
        facts.versions[bundled] = line("bbbbbbbbbbbb", built: "2026-01-01T00:00:00Z")
        XCTAssertEqual(try resolver(facts: facts).resolve().get(), .init(url: localBin, source: .newerLocalBin))
    }

    func testOlderLocalBinLoses() throws {
        try makeBinary(bundled)
        try makeBinary(localBin)
        var facts = Facts()
        facts.versions[localBin] = line("aaaaaaaaaaaa", built: "2026-01-01T00:00:00Z")
        facts.versions[bundled] = line("bbbbbbbbbbbb", built: "2026-06-01T00:00:00Z")
        // A newer file and a different hash do not overturn an older commit.
        facts.hashes = [localBin: "1", bundled: "2"]
        facts.modified = [localBin: Date(timeIntervalSince1970: 200), bundled: Date(timeIntervalSince1970: 100)]
        XCTAssertEqual(try resolver(facts: facts).resolve().get().source, .bundled)
    }

    func testTieGoesToADifferentNewerLocalFile() throws {
        try makeBinary(bundled)
        try makeBinary(localBin)
        var facts = Facts()
        // Two dirty builds of one commit share `built`.
        facts.versions[localBin] = line("aaaaaaaaaaaa", dirty: true)
        facts.versions[bundled] = line("aaaaaaaaaaaa", dirty: true)
        facts.hashes = [localBin: "1", bundled: "2"]
        facts.modified = [localBin: Date(timeIntervalSince1970: 200), bundled: Date(timeIntervalSince1970: 100)]
        XCTAssertEqual(try resolver(facts: facts).resolve().get(), .init(url: localBin, source: .newerLocalBin))
    }

    func testTieKeepsBundledWhenTheContentIsTheSame() throws {
        try makeBinary(bundled)
        try makeBinary(localBin)
        var facts = Facts()
        facts.versions[localBin] = line("aaaaaaaaaaaa", dirty: true)
        facts.versions[bundled] = line("aaaaaaaaaaaa", dirty: true)
        facts.hashes = [localBin: "same", bundled: "same"]
        facts.modified = [localBin: Date(timeIntervalSince1970: 200), bundled: Date(timeIntervalSince1970: 100)]
        XCTAssertEqual(try resolver(facts: facts).resolve().get().source, .bundled)
    }

    func testTieKeepsBundledWhenTheLocalFileIsOlder() throws {
        try makeBinary(bundled)
        try makeBinary(localBin)
        var facts = Facts()
        facts.versions[localBin] = line("aaaaaaaaaaaa", dirty: true)
        facts.versions[bundled] = line("aaaaaaaaaaaa", dirty: true)
        facts.hashes = [localBin: "1", bundled: "2"]
        facts.modified = [localBin: Date(timeIntervalSince1970: 100), bundled: Date(timeIntervalSince1970: 200)]
        XCTAssertEqual(try resolver(facts: facts).resolve().get().source, .bundled)
    }

    func testTieBetweenOneCleanBuildGoesToLocalEvenWhenBundledIsNewer() throws {
        try makeBinary(bundled)
        try makeBinary(localBin)
        var facts = Facts()
        // `sign.sh` re-signs the bundled copy, so the hash and the file date
        // both differ from the local copy of the very same build.
        facts.versions[localBin] = line("aaaaaaaaaaaa", sha: "111111111111")
        facts.versions[bundled] = line("aaaaaaaaaaaa", sha: "222222222222")
        facts.hashes = [localBin: "1", bundled: "2"]
        facts.modified = [localBin: Date(timeIntervalSince1970: 100), bundled: Date(timeIntervalSince1970: 200)]
        XCTAssertEqual(try resolver(facts: facts).resolve().get(), .init(url: localBin, source: .sameBuildLocalBin))
    }

    func testTieBetweenDifferentCleanRevisionsKeepsBundled() throws {
        try makeBinary(bundled)
        try makeBinary(localBin)
        var facts = Facts()
        facts.versions[localBin] = line("aaaaaaaaaaaa")
        facts.versions[bundled] = line("bbbbbbbbbbbb")
        facts.hashes = [localBin: "1", bundled: "2"]
        facts.modified = [localBin: Date(timeIntervalSince1970: 100), bundled: Date(timeIntervalSince1970: 200)]
        XCTAssertEqual(try resolver(facts: facts).resolve().get().source, .bundled)
    }

    func testUnknownLocalIdentityNeverDisplacesBundled() throws {
        try makeBinary(bundled)
        try makeBinary(localBin)
        var facts = Facts()
        facts.versions[bundled] = line("bbbbbbbbbbbb", built: "2026-01-01T00:00:00Z")
        facts.hashes = [localBin: "1", bundled: "2"]
        facts.modified = [localBin: Date(timeIntervalSince1970: 200), bundled: Date(timeIntervalSince1970: 100)]
        XCTAssertEqual(try resolver(daemon: DaemonBuild(version: "0.8.0"), facts: facts).resolve().get().source, .bundled)
    }

    func testOneSidedBuildTimeKeepsBundled() throws {
        try makeBinary(bundled)
        try makeBinary(localBin)
        var facts = Facts()
        facts.versions[localBin] = "amx version 0.8.0 (revision unknown)"
        facts.versions[bundled] = line("bbbbbbbbbbbb")
        facts.hashes = [localBin: "1", bundled: "2"]
        facts.modified = [localBin: Date(timeIntervalSince1970: 200), bundled: Date(timeIntervalSince1970: 100)]
        XCTAssertEqual(try resolver(facts: facts).resolve().get().source, .bundled)
    }

    func testSHA256OfAFile() throws {
        let file = root.appendingPathComponent("hash-me")
        try Data("abc".utf8).write(to: file)
        XCTAssertEqual(AMXBinaryResolver.sha256(file.path), "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
        XCTAssertNil(AMXBinaryResolver.sha256(root.appendingPathComponent("missing").path))
    }
}
