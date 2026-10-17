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
        // The sidebar promotes the permission request above a seen completion.
        XCTAssertEqual(ids(after: "Needs attention"), [4, 2])
        XCTAssertEqual(ids(after: "Working"), [1, 5])
        XCTAssertEqual(ids(after: "Idle"), [3])
    }

    func testNeedsAttentionFollowsTheNextAttentionCycle() {
        var embedded = session(6, "key", attention: true, project: "api", changedSecondsAgo: 900)
        embedded.runtime = CompanionRuntime(sessionID: "rs-6", backend: "tmux", pane: "%6")
        let sessions = [
            session(1, "stop", attention: true, project: "web", changedSecondsAgo: 30),
            session(2, "key", attention: true, project: "api", changedSecondsAgo: 60),
            session(3, "bolt", project: "api"),
            session(4, "question", attention: true, project: "api", changedSecondsAgo: 600),
            session(5, "error", attention: true, project: "Docs", changedSecondsAgo: 10),
            embedded,
        ]
        var order = WorkspaceOrder()
        order.set([.embedded("rs-6"), AttentionCycler.Key(socket: "", windowID: 4),
                   AttentionCycler.Key(socket: "", windowID: 3), AttentionCycler.Key(socket: "", windowID: 2)], for: "api")
        var menuInput = input(sessions)
        menuInput.order = order
        let entries = MenuBuilder.entries(menuInput)
        guard let start = entries.firstIndex(of: .header("Needs attention")) else { return XCTFail("no Needs attention") }
        let listed = entries[(start + 1)...].prefix { if case .session = $0 { return true } else { return false } }
            .compactMap { if case let .session(row) = $0 { return row.windowID } else { return nil } }
        let cycle = AttentionCycler.ordered(sessions, order: order).map(\.windowID)
        XCTAssertEqual(listed, cycle)
        XCTAssertEqual(listed, [6, 4, 2, 5, 1], "the dragged api order, then Docs and web by name")

        var cycler = AttentionCycler()
        let presses = (0..<5).compactMap { _ in cycler.advance(in: sessions, order: order)?.windowID }
        XCTAssertEqual(presses, listed, "⌃⌥⌘N visits them in the order the menu lists them")
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

    /// A second start or restart could stop the daemon the first just
    /// brought up, so both are greyed out, with what is under way, while one
    /// runs: in the feed lines, the notice's offer, and Notifications.
    func testDaemonActionsAreGreyedOutWhileOneRuns() {
        var down = input([], feed: .notRunning)
        down.daemonBusy = DaemonStart.startingNotice
        XCTAssertEqual(MenuBuilder.entries(down),
                       [.info("Daemon: not running"), .unavailable("Start Daemon", reason: DaemonStart.startingNotice)])
        var old = input([], feed: .daemonTooOld)
        old.daemonBusy = DaemonStart.restartingNotice
        XCTAssertEqual(MenuBuilder.entries(old).last, .unavailable("Restart Daemon", reason: DaemonStart.restartingNotice))
        var noticed = input([])
        noticed.notice = MenuBuilder.snoozeUnsupported
        noticed.daemonBusy = DaemonStart.restartingNotice
        XCTAssertTrue(MenuBuilder.entries(noticed).contains(.unavailable("Restart Daemon", reason: DaemonStart.restartingNotice)))
        var actions = MenuBuilder.ActionsInput()
        actions.notification = CompanionNotification()
        actions.snoozeSupported = false
        actions.daemonBusy = DaemonStart.restartingNotice
        XCTAssertEqual(MenuBuilder.notificationEntries(actions),
                       [.info(MenuBuilder.snoozeUnsupported), .unavailable("Restart Daemon", reason: DaemonStart.restartingNotice)])
    }

    func testFeedStates() {
        XCTAssertEqual(MenuBuilder.entries(input([], feed: .notRunning)),
                       [.info("Daemon: not running"), .action(.startDaemon, "Start Daemon")])
        var old = input([], feed: .daemonTooOld)
        old.daemonStatus = .ready(DaemonBuild(version: "0.7.0", revision: "abc"))
        XCTAssertEqual(MenuBuilder.entries(old),
                       [.info("Daemon: ready · 0.7.0 (abc)"), .info("Daemon too old for this app"),
                        .action(.restartDaemon, "Restart Daemon")])
        XCTAssertEqual(MenuBuilder.entries(input([session(1, "")], feed: .reconnecting("no heartbeat"))),
                       [.info("Daemon: reconnecting · no heartbeat")], "no sessions shown from a feed that is not live")
        XCTAssertEqual(MenuBuilder.entries(input([], feed: .reconnecting(DaemonPhrase.notAnswering))),
                       [.info("Daemon: reconnecting · \(DaemonPhrase.notAnswering)"), .action(.restartDaemon, "Restart Daemon")],
                       "a daemon stuck not answering can be restarted from the menu")
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

    func testANoticeNamingRestartDaemonOffersIt() {
        var value = input([])
        value.notice = "Daemon too old to focus sessions — \(MenuBuilder.restartHint)"
        XCTAssertEqual(Array(MenuBuilder.entries(value)[1...2]),
                       [.info(value.notice!), .action(.restartDaemon, "Restart Daemon")])
        value.notice = MenuBuilder.snoozeUnsupported
        XCTAssertTrue(MenuBuilder.entries(value).contains(.action(.restartDaemon, "Restart Daemon")))
        value.notice = "That session has closed."
        XCTAssertFalse(MenuBuilder.entries(value).contains(.action(.restartDaemon, "Restart Daemon")))

        var old = input([], feed: .daemonTooOld)
        old.notice = MenuBuilder.snoozeUnsupported
        XCTAssertEqual(MenuBuilder.entries(old).filter { $0 == .action(.restartDaemon, "Restart Daemon") }.count, 1,
                       "offered once when the feed already offers it")
    }

    func testMenuItemsCarryTheirRows() {
        let rows = (1...11).map { MenuSessionRow(title: "r\($0)", windowID: $0, kittySocket: "", toolTip: "") }
        let entries: [MenuEntry] = [.info("x"), .header("H"), .session(rows[0]), .submenu("Idle (11)", rows),
                                    .action(.startDaemon, "Start Daemon"), .separator, .action(.restartDaemon, "Restart Daemon")]
        let target = NSObject()
        let items = MenuBuilder.items(for: entries, target: target, sessionAction: #selector(NSObject.description), actionSelector: #selector(NSObject.hash))
        XCTAssertEqual(items.count, 7)
        XCTAssertFalse(items[0].isEnabled)
        XCTAssertTrue(items[1].isSectionHeader)
        XCTAssertEqual((items[2].representedObject as? MenuBuilder.RowBox)?.row, rows[0])
        XCTAssertEqual(items[3].submenu?.items.count, 11)
        XCTAssertEqual((items[4].representedObject as? MenuBuilder.ActionBox)?.action, .startDaemon)
        XCTAssertTrue(items[5].isSeparatorItem)
        XCTAssertEqual((items[6].representedObject as? MenuBuilder.ActionBox)?.action, .restartDaemon)
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

    func testALongOrManyLinedMessageIsCutWithTheWholeAsTooltip() {
        XCTAssertTrue(MenuBuilder.menuLine("Kitty is not running.") == ("Kitty is not running.", nil))
        let multi = MenuBuilder.menuLine("amx exited with status 2\nerror: config.toml line 4\nmore")
        XCTAssertEqual(multi.title, "amx exited with status 2 …")
        XCTAssertEqual(multi.toolTip, "amx exited with status 2\nerror: config.toml line 4\nmore")
        let long = String(repeating: "word ", count: 60)
        let cut = MenuBuilder.menuLine(long)
        XCTAssertLessThanOrEqual(cut.title.count, MenuBuilder.menuLineLimit + 1)
        XCTAssertTrue(cut.title.hasSuffix("word…"))
        XCTAssertEqual(cut.toolTip, long.trimmingCharacters(in: .whitespaces))

        let items = MenuBuilder.items(for: [.info(long)], target: NSObject(), sessionAction: #selector(NSObject.description), actionSelector: #selector(NSObject.hash))
        XCTAssertEqual(items.first?.title, cut.title)
        XCTAssertEqual(items.first?.toolTip, cut.toolTip)
    }
}
