import XCTest
@testable import AgentMux

final class MenuBuilderTests: XCTestCase {
    private let now = Date(timeIntervalSince1970: 1_000_000)

    private func session(_ id: Int, _ icon: String, attention: Bool = false, project: String = "", changedSecondsAgo: Int? = nil) -> CompanionSession {
        var s = CompanionSession(windowID: id)
        s.iconState = icon
        s.attention = attention
        s.project = project.isEmpty ? "p\(id)" : project
        s.harness = "claude"
        if let ago = changedSecondsAgo {
            s.presentationChangedAtMS = Int64((now.timeIntervalSince1970 - Double(ago)) * 1000)
        }
        return s
    }

    private func input(_ sessions: [CompanionSession], feed: FeedState = .live) -> MenuBuilder.Input {
        var snapshot = CompanionSnapshot()
        snapshot.daemon.ready = true
        snapshot.sessions = sessions
        return MenuBuilder.Input(feed: feed, daemonStatus: .ready(nil), sessions: sessions, snapshot: snapshot,
                                 notice: nil, now: now, glyphs: .fallback)
    }

    func testRowFormat() {
        var s = session(1, "key", attention: true, project: "api", changedSecondsAgo: 245)
        s.reason = "permission"
        let row = MenuBuilder.row(s, showReason: true, input: input([s]))
        XCTAssertEqual(row.title, "🔑 api · claude · 4m · permission")
        XCTAssertEqual(row.windowID, 1)

        s.muted = true
        s.iconState = ""
        XCTAssertEqual(MenuBuilder.row(s, showReason: false, input: input([s])).title, "api · claude · 4m 🔇")

        var unnamed = CompanionSession(windowID: 9)
        unnamed.baseTitle = "W:x"
        XCTAssertEqual(MenuBuilder.name(of: unnamed), "W:x")
        XCTAssertEqual(MenuBuilder.name(of: CompanionSession(windowID: 9)), "window 9")
    }

    func testAges() {
        XCTAssertEqual(MenuBuilder.age(since: now.addingTimeInterval(-5), now: now), "<1m")
        XCTAssertEqual(MenuBuilder.age(since: now.addingTimeInterval(-240), now: now), "4m")
        XCTAssertEqual(MenuBuilder.age(since: now.addingTimeInterval(-7300), now: now), "2h")
        XCTAssertEqual(MenuBuilder.age(since: now.addingTimeInterval(-3 * 86400), now: now), "3d")
        XCTAssertEqual(MenuBuilder.age(since: now.addingTimeInterval(60), now: now), "<1m", "a clock skew is not negative")
    }

    func testSectionsFromASnapshot() {
        let sessions = [
            session(1, "bolt"),
            session(2, "stop", attention: true, changedSecondsAgo: 60),
            session(3, ""),
            session(4, "key", attention: true, changedSecondsAgo: 600),
            session(5, "subagent"),
        ]
        let entries = MenuBuilder.entries(input(sessions))
        XCTAssertEqual(entries.first, .info("Daemon: ready"))
        let headers = entries.compactMap { if case let .header(t) = $0 { return t } else { return nil } }
        XCTAssertEqual(headers, ["Needs attention", "Working", "Idle"])

        func ids(after header: String) -> [Int] {
            guard let start = entries.firstIndex(of: .header(header)) else { return [] }
            return entries[(start + 1)...].prefix { if case .session = $0 { return true } else { return false } }
                .compactMap { if case let .session(row) = $0 { return row.windowID } else { return nil } }
        }
        // Longest-waiting attention row first.
        XCTAssertEqual(ids(after: "Needs attention"), [4, 2])
        XCTAssertEqual(ids(after: "Working"), [1, 5])
        XCTAssertEqual(ids(after: "Idle"), [3])
    }

    func testManyIdleRowsCollapseIntoASubmenu() {
        let ten = (1...10).map { session($0, "") }
        XCTAssertTrue(MenuBuilder.entries(input(ten)).contains(.header("Idle")))

        let eleven = (1...11).map { session($0, "") }
        let entries = MenuBuilder.entries(input(eleven))
        XCTAssertFalse(entries.contains(.header("Idle")))
        guard case let .submenu(title, rows)? = entries.first(where: { if case .submenu = $0 { return true } else { return false } }) else {
            return XCTFail("no submenu in \(entries)")
        }
        XCTAssertEqual(title, "Idle (11)")
        XCTAssertEqual(rows.count, 11)
    }

    func testEmptyAndTruncated() {
        XCTAssertEqual(MenuBuilder.entries(input([])), [.info("Daemon: ready"), .separator, .info("No live sessions")])
        var truncated = input([session(1, "")])
        truncated.snapshot?.truncated = true
        XCTAssertEqual(MenuBuilder.entries(truncated).last, .info("More sessions than the daemon lists"))
    }

    func testFeedStates() {
        XCTAssertEqual(MenuBuilder.entries(input([], feed: .notRunning)),
                       [.info("Daemon: not running"), .action(.startDaemon, "Start Daemon")])
        var old = input([], feed: .daemonTooOld)
        old.daemonStatus = .ready(DaemonBuild(version: "0.7.0", revision: "abc"))
        XCTAssertEqual(MenuBuilder.entries(old),
                       [.info("Daemon: ready · 0.7.0 (abc)"), .info("Daemon too old — run amx daemon restart")])
        XCTAssertEqual(MenuBuilder.entries(input([session(1, "")], feed: .reconnecting("no heartbeat"))),
                       [.info("Daemon: reconnecting · no heartbeat")], "no sessions shown from a feed that is not live")
        XCTAssertEqual(MenuBuilder.entries(input([], feed: .connecting)), [.info("Daemon: connecting…")])
    }

    func testNoticeAndMissingListenOn() {
        var value = input([session(1, "")])
        value.notice = "That session has closed."
        value.kittyReachable = false
        let entries = MenuBuilder.entries(value)
        XCTAssertEqual(entries[1], .info("That session has closed."))
        XCTAssertEqual(entries[2], .info(KittyEndpoint.listenOnMissing))
    }

    func testMenuItemsCarryTheirRows() {
        let rows = (1...11).map { MenuSessionRow(title: "r\($0)", windowID: $0, kittySocket: "", toolTip: "") }
        let entries: [MenuEntry] = [.info("x"), .header("H"), .session(rows[0]), .submenu("Idle (11)", rows),
                                    .action(.startDaemon, "Start Daemon"), .separator]
        let target = NSObject()
        let items = MenuBuilder.items(for: entries, target: target, sessionAction: #selector(NSObject.description), actionSelector: #selector(NSObject.hash))
        XCTAssertEqual(items.count, 6)
        XCTAssertFalse(items[0].isEnabled)
        XCTAssertTrue(items[1].isSectionHeader)
        XCTAssertEqual((items[2].representedObject as? MenuBuilder.RowBox)?.row, rows[0])
        XCTAssertEqual(items[3].submenu?.items.count, 11)
        XCTAssertEqual((items[4].representedObject as? MenuBuilder.ActionBox)?.action, .startDaemon)
        XCTAssertTrue(items[5].isSeparatorItem)
    }

    // MARK: Title

    func testTitleStyles() {
        let attention = CompanionAttention(total: 3, stop: 1, key: 1, error: 0, question: 1, working: 2)
        XCTAssertEqual(StatusTitle.text(for: attention, style: .glyphs, glyphs: .fallback), "⚡2 🛑1 🔑1 ❓1")
        XCTAssertEqual(StatusTitle.text(for: attention, style: .glyphs, showWorking: false, glyphs: .fallback), "🛑1 🔑1 ❓1")
        XCTAssertEqual(StatusTitle.text(for: attention, style: .compact, glyphs: .fallback), "3")
        XCTAssertEqual(StatusTitle.text(for: attention, style: .iconOnly, glyphs: .fallback), "")
        XCTAssertEqual(StatusTitle.text(for: CompanionAttention(), style: .glyphs, glyphs: .fallback), "", "zero counts are not shown")
        XCTAssertEqual(StatusTitle.text(for: CompanionAttention(), style: .compact, glyphs: .fallback), "")
        XCTAssertEqual(StatusTitle.text(for: CompanionAttention(total: 1, error: 1), glyphs: .fallback), "❗1")
    }

    func testBundledGlyphsMatchThePalette() {
        XCTAssertNotNil(Bundle.main.url(forResource: "icons", withExtension: "json"), "icons.json is not bundled")
        XCTAssertEqual(Glyphs.bundled, Glyphs.fallback, "the fallback drifted from internal/palette/icons.json")
        XCTAssertNil(Glyphs(json: Data("{}".utf8)))
        XCTAssertEqual(Glyphs.fallback.glyph(for: "nope"), "")
    }

    // MARK: Kitty

    func testEndpointCandidates() {
        let exists: (String) -> Bool = { $0 == "/tmp/kitty-20" || $0 == "/tmp/kitty-10" }
        XCTAssertEqual(KittyEndpoint.candidates(rowSocket: "unix:/tmp/kitty-20", snapshotSocket: "unix:/tmp/kitty-10",
                                                kittyPIDs: [30, 20, 10], fileExists: exists),
                       ["unix:/tmp/kitty-20", "unix:/tmp/kitty-10"])
        XCTAssertEqual(KittyEndpoint.candidates(rowSocket: "", snapshotSocket: "", kittyPIDs: [30, 20, 10], fileExists: exists),
                       ["unix:/tmp/kitty-10", "unix:/tmp/kitty-20"])
        XCTAssertEqual(KittyEndpoint.candidates(rowSocket: " ", snapshotSocket: "", kittyPIDs: [30], fileExists: exists), [])
    }

    func testSocketPIDs() {
        XCTAssertEqual(KittyEndpoint.pid(fromSocket: "unix:/tmp/kitty-1234"), 1234)
        XCTAssertEqual(KittyEndpoint.pid(fromSocket: "/tmp/kitty-5"), 5)
        XCTAssertNil(KittyEndpoint.pid(fromSocket: "unix:/tmp/kitty"))
        XCTAssertNil(KittyEndpoint.pid(fromSocket: "unix:/tmp/kitty-abc"))
        XCTAssertNil(KittyEndpoint.pid(fromSocket: "unix:/tmp/kitty-"))
    }

    func testActivatorPicksTheInstanceTheSocketNames() {
        let kitty = KittyEndpoint.bundleIdentifier
        let running = [
            KittyActivator.RunningKitty(pid: 10, bundleIdentifier: kitty),
            KittyActivator.RunningKitty(pid: 20, bundleIdentifier: kitty),
            KittyActivator.RunningKitty(pid: 30, bundleIdentifier: "other"),
        ]
        XCTAssertEqual(KittyActivator.target(socket: "unix:/tmp/kitty-20", running: running), 20)
        XCTAssertEqual(KittyActivator.target(socket: "unix:/tmp/kitty-30", running: running), 10, "pid 30 is not Kitty")
        XCTAssertEqual(KittyActivator.target(socket: "unix:/tmp/mykitty", running: running), 10)
        XCTAssertNil(KittyActivator.target(socket: "unix:/tmp/kitty-20", running: []))
    }
}
