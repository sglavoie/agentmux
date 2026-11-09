import XCTest
@testable import AgentMux

extension WorkspaceSidebarTests {
    func testNewProjectsKeepAlphabeticalPositionsInEveryLayout() {
        let initial = [F.tmux(1, project: "🧠 BrainNotes"), F.tmux(2, project: "Katana"),
                       F.tmux(3, project: "sis"), F.tmux(4, project: "↑ ZoomFlow")]
        var order = WorkspaceOrder()
        order.setFlat(initial.map(AttentionCycler.key))
        let added = [F.tmux(5, project: "🐱 amx"), F.tmux(6, project: "📝 Docket"),
                     F.tmux(7, project: "zzz")]
        for grouping in WorkspaceSidebarGrouping.allCases {
            var state = WorkspaceState()
            state.apply(WorkspaceSidebar.groups(initial, order: order, grouping: grouping))
            for snapshot in [initial + added, Array((initial + added).reversed())] {
                state.apply(WorkspaceSidebar.groups(snapshot, order: order, grouping: grouping))
                XCTAssertEqual(state.rows.map(\.key), [5, 1, 6, 2, 3, 4, 7].map { .embedded(F.runtimeID($0)) },
                               "new projects sort by name in \(grouping), independently of snapshot order")
            }
        }
    }

    func testNewCompactRowsKeepDefaultSlotsWithoutLosingSavedRelativeOrder() {
        let sessions = [F.tmux(1, project: "api", profile: "a"), F.tmux(2, project: "api", profile: "c"),
                        F.tmux(3, project: "web"), F.tmux(4, project: "api", profile: "b"),
                        F.tmux(5, project: "amx")]
        var order = WorkspaceOrder()
        // Include an ended session and a duplicate to exercise persisted preferences.
        order.setFlat([3, 9, 2, 1, 2].map { .embedded(F.runtimeID($0)) })
        let rows = WorkspaceSidebar.groups(sessions, order: order, grouping: .compact).flatMap(\.rows)
        XCTAssertEqual(rows.map(\.key), [5, 3, 4, 2, 1].map { .embedded(F.runtimeID($0)) })
        XCTAssertEqual(rows.filter { [1, 2, 3].map { AttentionCycler.Key.embedded(F.runtimeID($0)) }.contains($0.key) }.map(\.key),
                       [3, 2, 1].map { .embedded(F.runtimeID($0)) }, "saved sessions keep their relative order")
        XCTAssertEqual(order.flat, [3, 9, 2, 1, 2].map(F.runtimeID), "rendering does not rewrite saved preferences")
    }

    func testStableProjectsLiftPinsAndRestoreTheirSavedPlacesOnUnpin() throws {
        var sessions = [F.tmux(1, project: "api", profile: "a", icon: "bolt"),
                        F.tmux(2, project: "api", profile: "b", icon: "key", attention: true),
                        F.tmux(3, project: "web", icon: "stop", attention: true)]
        sessions[1].pinned = true
        var order = WorkspaceOrder()
        order.set([2, 1].map { .embedded(F.runtimeID($0)) }, for: "api")
        order.setCollapsed(project: "api", pinned: false, true)
        var state = WorkspaceState()
        state.apply(WorkspaceSidebar.groups(sessions, order: order, grouping: .project))
        XCTAssertEqual(state.groups.map(\.title), ["Pinned", "api", "web"])
        XCTAssertEqual(state.rows.map(\.key), [2, 1, 3].map { .embedded(F.runtimeID($0)) })
        XCTAssertEqual(state.groups.first?.attention, 1)
        XCTAssertEqual(state.groups.first?.collapsed, false)
        XCTAssertEqual(state.groups[1].collapsed, true)
        XCTAssertEqual(state.sectionCounts.map(\.count), [1, 1, 1])
        XCTAssertEqual(Set(state.rows.map(\.key)).count, sessions.count)
        let keys = state.rows.map(\.key)
        sessions[0].iconState = "error"
        sessions[0].attention = true
        sessions[1].iconState = "stop"
        sessions[1].pinned = false
        state.apply(WorkspaceSidebar.groups(sessions, order: order, grouping: .project))
        XCTAssertEqual(state.rows.map(\.key), keys, "unpinning restores the saved project order")
        XCTAssertEqual(state.groups.first?.attention, 2)
    }

    func testPinsLeadEveryLayoutAndSortAndUnpinRestoresManualOrder() throws {
        var sessions = [F.tmux(1, project: "api", icon: "key", attention: true),
                        F.tmux(2, project: "api", icon: "bolt"),
                        F.tmux(3, project: "web", icon: "stop"),
                        F.tmux(4, project: "web", icon: "key", attention: true)]
        sessions[1].branch = "feature"
        sessions[2].branch = "main"
        var order = WorkspaceOrder()
        let keys = (1...4).map { AttentionCycler.Key.embedded(F.runtimeID($0)) }
        order.set([keys[0], keys[1]], for: "api")
        order.set([keys[3], keys[2]], for: "web")
        order.setFlat([keys[0], keys[3], keys[1], keys[2]])
        order.setPinned([keys[2], keys[1]])
        order.visit(keys[1])
        order.visit(keys[0])
        order.visit(keys[3])
        for grouping in WorkspaceSidebarGrouping.allCases {
            let original = WorkspaceSidebar.groups(sessions, order: order, grouping: grouping).flatMap(\.rows).map(\.key)
            var pinned = sessions
            pinned[1].pinned = true
            pinned[2].pinned = true
            for sorting in WorkspaceSidebarSort.allCases {
                var state = WorkspaceState()
                state.apply(WorkspaceSidebar.groups(pinned, order: order, grouping: grouping, sorting: sorting))
                let expectedPins = sorting == .manual ? [keys[2], keys[1]] : [keys[1], keys[2]]
                XCTAssertEqual(Array(state.rows.prefix(2)).map(\.key), expectedPins, "\(grouping) / \(sorting)")
                XCTAssertEqual(Set(state.rows.map(\.key)), Set(keys))
                XCTAssertEqual(state.rows.count, keys.count, "pins must not be duplicated in projects")
                XCTAssertEqual(state.sectionCounts.reduce(0) { $0 + $1.count }, keys.count)
                XCTAssertEqual(WorkspaceSidebar.ordered(pinned, order: order, grouping: grouping, sorting: sorting).map(AttentionCycler.key), state.rows.map(\.key))
                if grouping == .compact {
                    XCTAssertTrue(state.groups.allSatisfy { $0.isFlat && !$0.canCollapse })
                }
                state.apply(WorkspaceSidebar.groups(sessions, order: order, grouping: grouping))
                XCTAssertEqual(state.rows.map(\.key), original, "unpin restores \(grouping)'s order")
            }
        }
    }

    func testBranchesOmitRedundantNestingAndScopeCollapseToTheirProject() throws {
        var sessions = [F.tmux(1, project: "api"), F.tmux(2, project: "api", icon: "key", attention: true),
                        F.tmux(3, project: "api"), F.tmux(4, project: "web"), F.tmux(5, project: "web")]
        sessions[0].branch = "main"
        sessions[1].branch = "feature/login"
        sessions[3].branch = "main"
        sessions[4].branch = "main"
        var order = WorkspaceOrder()
        var state = WorkspaceState()
        state.apply(WorkspaceSidebar.groups(sessions, order: order, grouping: .projectThenBranch))
        XCTAssertEqual(state.groups.map(\.title), ["api", "No branch", "feature/login", "main", "web"])
        XCTAssertEqual(state.groups.last?.rows.count, 2, "single-branch projects have no branch heading")
        XCTAssertEqual(state.groups.first?.attention, 1)
        XCTAssertEqual(state.rows(under: state.groups[0]).count, 3)
        let branch = try XCTUnwrap(state.groups.first { $0.branch == "main" })
        order.setCollapsed(branch, true)
        order.setCollapsed(project: "api", pinned: false, true)
        state.apply(WorkspaceSidebar.groups(sessions, order: order, grouping: .projectThenBranch))
        XCTAssertEqual(state.listedGroups.map(\.title), ["api", "web"])
        XCTAssertEqual(state.rows.count, 5)
        XCTAssertFalse(try XCTUnwrap(state.groups.last).collapsed)
        XCTAssertNotEqual(WorkspaceOrder.groupToken(branch), WorkspaceOrder.groupToken(WorkspaceGroup(project: "web", rows: [], branch: "main")))
        XCTAssertNotEqual(WorkspaceOrder.groupToken(WorkspaceGroup(project: "a:b", rows: [], branch: "c")),
                          WorkspaceOrder.groupToken(WorkspaceGroup(project: "a", rows: [], branch: "b:c")))
        sessions[0].branch = "feature/login"
        sessions[2].branch = "feature/login"
        state.apply(WorkspaceSidebar.groups(sessions, order: order, grouping: .projectThenBranch))
        XCTAssertEqual(state.groups.map(\.title), ["api", "web"])
        XCTAssertEqual(state.groups[0].rows.count, 3, "rows survive the removal of a redundant branch level")
    }

    func testFlatOrderAndVisitOrderPersistIndependentlyAndForgetEndedSessions() throws {
        let sessions = [F.tmux(1, project: "api", icon: "key", attention: true),
                        F.tmux(2, project: "web", icon: "key", attention: true), F.tmux(3, project: "api")]
        var order = WorkspaceOrder()
        let keys = [2, 3, 1].map { AttentionCycler.Key.embedded(F.runtimeID($0)) }
        order.setFlat(keys)
        order.visit(keys[1])
        order.visit(keys[2])
        order.visit(keys[1])
        order.setCollapsed(project: "api", pinned: false, true)
        let suite = "SidebarOrder-\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suite)!
        defer { defaults.removePersistentDomain(forName: suite) }
        order.save(to: defaults)
        XCTAssertEqual(WorkspaceOrder.load(from: defaults), order)
        var state = WorkspaceState()
        state.apply(WorkspaceSidebar.groups(sessions, order: order, grouping: .compact))
        XCTAssertEqual(state.groups.count, 1)
        XCTAssertFalse(state.groups[0].canCollapse)
        XCTAssertEqual(state.rows.map(\.key), keys)
        state.arrange(order, grouping: .compact, sorting: .recentlyVisited)
        XCTAssertEqual(state.rows.map(\.key), [keys[1], keys[2], keys[0]])
        XCTAssertEqual(WorkspaceInspector.waiting(sessions, order: order, grouping: .compact, sorting: .recentlyVisited).map(\.key), [keys[2], keys[0]])
        var cycle = AttentionCycler()
        XCTAssertEqual(cycle.advance(in: sessions, order: order, grouping: .compact, sorting: .recentlyVisited).map(AttentionCycler.key), keys[2])
        state.arrange(order, grouping: .compact)
        XCTAssertEqual(state.rows.map(\.key), keys)
        order.forget(notAmong: Array(state.rows.prefix(1)))
        XCTAssertEqual(order.flat, [F.runtimeID(2)])
        XCTAssertTrue(order.visited.isEmpty)
    }

    func testProjectFirstLiftsPinsAndScopesCollapse() throws {
        var sessions = [
            F.tmux(1, project: "web", icon: "key", attention: true),
            F.tmux(2, project: "api", icon: "bolt"),
            F.tmux(3, project: "api", icon: "stop"),
            F.tmux(4, project: "web", icon: "bolt"),
            F.tmux(5, project: "Working"),
        ]
        sessions[2].pinned = true
        var order = WorkspaceOrder()
        var state = WorkspaceState()
        func built() -> [WorkspaceGroup] {
            WorkspaceSidebar.groups(sessions, order: order, grouping: .projectThenState)
        }
        state.apply(built())
        XCTAssertEqual(state.groups.map(\.title), ["Pinned", "api", "Working", "web", "Waiting", "Working", "Working", "Idle"])
        XCTAssertEqual(state.rows.map(\.key), [3, 2, 1, 4, 5].map { .embedded(F.runtimeID($0)) })
        XCTAssertEqual(Set(state.rows.map(\.key)).count, sessions.count)
        XCTAssertTrue(state.groups[0].pinned)
        XCTAssertFalse(state.groups.contains { $0.needsAttention })
        XCTAssertEqual(state.sectionCounts.map(\.count), [1, 1, 2, 1])
        let api = try XCTUnwrap(state.groups.first { $0.isProjectHeading && $0.project == "api" })
        XCTAssertEqual(state.rows(under: api).count, 1)
        let working = try XCTUnwrap(state.groups.first { $0.isNestedState && $0.project == "api" && $0.stateSection == .working })
        order.setCollapsed(working, true)
        state.apply(built())
        XCTAssertEqual(state.groups.filter { $0.isNestedState && $0.stateSection == .working }.map(\.collapsed), [true, false])
        order.setCollapsed(api, true)
        state.apply(built())
        XCTAssertEqual(state.listedGroups.map(\.title), ["Pinned", "api", "web", "Waiting", "Working", "Working", "Idle"])
        XCTAssertEqual(state.rows.count, sessions.count)
        XCTAssertEqual(state.rows(under: working).map(\.key), [.embedded(F.runtimeID(2))])
        XCTAssertTrue(state.select(.embedded(F.runtimeID(2))))
        state.apply(nil)
        state.arrange(order, grouping: .stateThenProject)
        XCTAssertFalse(state.current)
        XCTAssertEqual(state.selection, .embedded(F.runtimeID(2)))
        state.arrange(order, grouping: .projectThenState)
        XCTAssertEqual(state.groups, built())

        let defaults = UserDefaults(suiteName: "WorkspaceGrouping-\(UUID().uuidString)")!
        order.save(to: defaults)
        XCTAssertEqual(WorkspaceOrder.load(from: defaults), order)
    }

    func testStateSectionsListTheProjectsOfTheirSessionsByName() throws {
        let sessions = [
            F.tmux(1, project: "web", icon: "bolt"),
            F.tmux(2, project: "api", profile: "personal", icon: "stop"),
            F.tmux(3, project: "API tools", icon: "bolt"),
            F.tmux(4, project: "docs"),
            // A warning waits on the operator without being promoted.
            F.tmux(5, project: "api", profile: "other", icon: "warning"),
            F.tmux(6, project: "api", profile: "work", icon: "robot"),
        ]
        func lines(_ groups: [WorkspaceGroup]) -> [String] {
            groups.map { $0.isSectionHeading ? "\($0.project) (\($0.sessions))\($0.collapsed ? "+" : "")" : $0.project }
        }
        var order = WorkspaceOrder()
        func built() -> [WorkspaceGroup] {
            WorkspaceSidebar.groups(sessions, now: now, glyphs: .fallback, order: order)
        }
        XCTAssertEqual(lines(built()), ["Waiting (1)", "api", "Done (1)", "api", "Working (3)", "api", "API tools", "web",
                                        "Idle (1)", "docs"],
                       "most urgent first, a project in every state its sessions are in, projects by name")
        XCTAssertEqual(built().filter { !$0.isSectionHeading }.map(\.stateSection),
                       [.waiting, .done, .working, .working, .working, .idle])
        XCTAssertTrue(built().filter(\.isSectionHeading).allSatisfy(\.rows.isEmpty), "a heading holds no row")

        order.setCollapsed(.working, true)
        var state = WorkspaceState()
        state.apply(built())
        XCTAssertEqual(lines(state.listedGroups), ["Waiting (1)", "api", "Done (1)", "api", "Working (3)+", "Idle (1)", "docs"])
        XCTAssertEqual(state.rows.count, sessions.count, "a collapsed section keeps its rows")
        let working = try XCTUnwrap(state.groups.first { $0.isSectionHeading && $0.stateSection == .working })
        XCTAssertEqual(state.rows(under: working).map(\.key), [.embedded(F.runtimeID(6)), .embedded(F.runtimeID(3)), .embedded(F.runtimeID(1))])
        XCTAssertEqual(order.collapsed, [WorkspaceOrder.groupToken(section: .working)], "not the project called Working")

        order.setCollapsed(project: "api", pinned: false, true)
        XCTAssertEqual(built().filter { $0.project == "api" }.map(\.collapsed), [true, true, true],
                       "a project collapses in every section at once")
    }

    func testProjectsSortByNameNotByTheIconTheyLeadWith() {
        let names = ["🎵 muse", "🐱 amx", "👶 Lyzma", "🔍 LogScanner", "🧠 BrainNotes", "🧰 dev-helpers", "api", "★"]
        let built = groups(names.enumerated().map { F.tmux($0 + 1, project: $1) })
        XCTAssertEqual(
            built.map(\.project),
            ["★", "🐱 amx", "api", "🧠 BrainNotes", "🧰 dev-helpers", "🔍 LogScanner", "👶 Lyzma", "🎵 muse"],
            "a name that is all symbols sorts as written"
        )
    }

    func testTwoTmuxSessionsOfOneProjectAndProfileStayApart() {
        let built = groups([F.tmux(2, project: "api"), F.tmux(1, project: "api")])
        XCTAssertEqual(built.count, 1)
        XCTAssertEqual(built[0].rows.map(\.key), [.embedded(F.runtimeID(1)), .embedded(F.runtimeID(2))])
    }

    func testADraggedOrderHoldsAndNewSessionsFollowIt() throws {
        let sessions = [F.tmux(1, project: "api"), F.tmux(2, project: "api"), F.tmux(4, project: "api"), F.tmux(3, project: "web")]
        let fourth: AttentionCycler.Key = .embedded(F.runtimeID(4))
        var order = WorkspaceOrder()
        order.set([fourth, .embedded(F.runtimeID(2)), .embedded(F.runtimeID(1))], for: "api")
        func keys(_ sessions: [CompanionSession], _ project: String = "api") -> [AttentionCycler.Key] {
            WorkspaceSidebar.groups(sessions, now: now, glyphs: .fallback, order: order)
                .first { $0.project == project }?.rows.map(\.key) ?? []
        }
        XCTAssertEqual(keys(sessions), [fourth, .embedded(F.runtimeID(2)), .embedded(F.runtimeID(1))])
        XCTAssertEqual(keys(sessions, "web"), [.embedded(F.runtimeID(3))], "a project nobody dragged in sorts as it did")
        XCTAssertEqual(keys(sessions + [F.tmux(5, project: "api", profile: "alpha")]).last, .embedded(F.runtimeID(5)),
                       "a session launched since follows the dragged ones")
        XCTAssertEqual(keys([sessions[0], sessions[2]]), [fourth, .embedded(F.runtimeID(1))], "one that ended leaves no gap")

        var state = WorkspaceState()
        state.apply(groups(sessions))
        state.arrange(order)
        XCTAssertEqual(state.groups.first { $0.project == "api" }?.rows.map(\.key), keys(sessions),
                       "held rows are arranged without a snapshot")

        let suite = "amx.tests.order.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        XCTAssertEqual(WorkspaceOrder.load(from: defaults), WorkspaceOrder())
        order.save(to: defaults)
        XCTAssertEqual(WorkspaceOrder.load(from: defaults), order)
    }

    func testPinnedSessionsLeaveTheirProjectsForAGroupListedFirst() throws {
        var sessions = [F.tmux(1, project: "api"), F.tmux(2, project: "api", profile: "personal"), F.tmux(4, project: "web"),
                        F.tmux(3, project: "Pinned")]
        let fourth: AttentionCycler.Key = .embedded(F.runtimeID(4))
        var order = WorkspaceOrder()
        func built(_ sessions: [CompanionSession]) -> [WorkspaceGroup] {
            projects(WorkspaceSidebar.groups(sessions, now: now, glyphs: .fallback, order: order))
        }
        XCTAssertEqual(built(sessions).map(\.pinned), [false, false, false], "nothing is pinned until asked")
        order.setPinned(.embedded(F.runtimeID(2)), true)
        XCTAssertEqual(built(sessions).map(\.pinned), [false, false, false], "a place among the pinned is not a pin")

        // The pin is the session's (the ★ in its title); the order is where
        // it was pinned or dragged to here.
        sessions[2].pinned = true
        sessions[0].pinned = true
        order.setPinned(fourth, true)
        order.setPinned(.embedded(F.runtimeID(1)), true)
        var groups = built(sessions)
        XCTAssertEqual(groups.map(\.project), ["Pinned", "api", "Pinned"], "a project of that name is still a project")
        XCTAssertEqual(groups.map(\.pinned), [true, false, false])
        XCTAssertEqual(groups[0].rows.map(\.key), [fourth, .embedded(F.runtimeID(1))], "in the order they were pinned")
        XCTAssertEqual(groups[0].rows.map(\.project), ["web", "api"], "a pinned row keeps its project")
        XCTAssertEqual(groups[0].rows.map(\.pinned), [true, true])
        XCTAssertEqual(groups[1].rows.map(\.key), [.embedded(F.runtimeID(2))])
        XCTAssertEqual(WorkspaceSidebar.ordered(sessions, order: order).first?.testID, 4,
                       "what follows sidebar order starts with the pinned")

        // Pinned in the dashboard, and unpinned there: the pins follow the
        // sessions, and one placed nowhere here follows the ones that are.
        var elsewhere = sessions
        elsewhere[2].pinned = false
        elsewhere[3].pinned = true
        XCTAssertEqual(built(elsewhere)[0].rows.map(\.key), [.embedded(F.runtimeID(1)), .embedded(F.runtimeID(3))])

        order.setPinned([.embedded(F.runtimeID(1)), fourth])
        XCTAssertEqual(built(sessions)[0].rows.map(\.key), [.embedded(F.runtimeID(1)), fourth])
        XCTAssertEqual(built([sessions[1], sessions[2]])[0].rows.map(\.key), [fourth], "one that ended leaves no gap")
        XCTAssertFalse(built([sessions[1]])[0].pinned, "and no heading when none is listed")

        // Held rows are arranged again without a snapshot, and keep their pins.
        var state = WorkspaceState()
        state.apply(built(sessions))
        order.setPinned([fourth, .embedded(F.runtimeID(1))])
        state.arrange(order)
        XCTAssertEqual(projects(state.groups), built(sessions))
        groups = state.groups
        XCTAssertEqual(groups[0].rows.map(\.key), [fourth, .embedded(F.runtimeID(1))])

        order.forgetPins(notAmong: [fourth])
        XCTAssertEqual(order.pinned, [WorkspaceOrder.token(fourth)], "the place of a session that ended is forgotten")

        let suite = "amx.tests.pins.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        order.save(to: defaults)
        XCTAssertEqual(WorkspaceOrder.load(from: defaults), order)
    }

    /// Pins this app kept by itself, before pins were the sessions' own,
    /// are pinned once after upgrading: the ones listed and not yet pinned,
    /// in the order they were pinned.
    func testPinsKeptBeforeSharingAreTheOnesToPin() {
        var sessions = [F.tmux(1, project: "api"), F.tmux(2, project: "api", profile: "personal"), F.tmux(4, project: "web")]
        sessions[0].pinned = true
        let fourth: AttentionCycler.Key = .embedded(F.runtimeID(4))
        var order = WorkspaceOrder()
        order.setPinned(fourth, true)
        order.setPinned(.embedded(F.runtimeID(9)), true)
        order.setPinned(.embedded(F.runtimeID(1)), true)
        order.setPinned(.embedded(F.runtimeID(2)), true)
        let rows = WorkspaceSidebar.rows(sessions)
        XCTAssertEqual(order.unsharedPins(rows).map(\.key), [fourth, .embedded(F.runtimeID(2))],
                       "neither the session that ended nor the one already pinned")
        XCTAssertTrue(WorkspaceOrder().unsharedPins(rows).isEmpty)
    }

    func testCollapsedGroupsAreKeptAndStillCountWhatWaits() throws {
        var sessions = [F.tmux(1, project: "api"), F.tmux(2, project: "api", profile: "personal", attention: true),
                        F.tmux(3, project: "Pinned")]
        var order = WorkspaceOrder()
        func built() -> [WorkspaceGroup] {
            projects(WorkspaceSidebar.groups(sessions, now: now, glyphs: .fallback, order: order))
        }
        XCTAssertEqual(built().map(\.collapsed), [false, false], "nothing is collapsed until asked")

        order.setCollapsed(project: "api", pinned: false, true)
        sessions[0].pinned = true
        var groups = built()
        XCTAssertEqual(groups.map(\.project), ["Pinned", "api", "Pinned"])
        XCTAssertEqual(groups.map(\.collapsed), [false, true, false], "the pinned group is not the project called Pinned")
        XCTAssertEqual(groups[1].rows.count, 1, "a collapsed group keeps its rows")
        XCTAssertEqual(groups[1].attention, 1)

        order.setCollapsed(project: "Pinned", pinned: true, true)
        XCTAssertEqual(built().map(\.collapsed), [true, true, false])
        order.forget(notAmong: [])
        XCTAssertTrue(order.isCollapsed(project: "api", pinned: false), "a project with nothing listed stays collapsed")

        let suite = "amx.tests.collapsed.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        order.save(to: defaults)
        XCTAssertEqual(WorkspaceOrder.load(from: defaults), order)
        XCTAssertEqual(defaults.stringArray(forKey: WorkspaceOrder.collapsedDefaultsKey), ["pinned", "project:api"])

        order.setCollapsed(project: "api", pinned: false, false)
        groups = built()
        XCTAssertEqual(groups.map(\.collapsed), [true, false, false])
    }

    func testForgettingSessionsThatEndedPrunesTheDraggedOrders() {
        let sessions = [F.tmux(1, project: "api"), F.tmux(2, project: "api", profile: "personal"), F.tmux(4, project: "web"),
                        F.tmux(3, project: "docs")]
        let fourth: AttentionCycler.Key = .embedded(F.runtimeID(4))
        var order = WorkspaceOrder()
        order.set([.embedded(F.runtimeID(2)), .embedded(F.runtimeID(1))], for: "api")
        order.set([fourth], for: "web")
        order.set([.embedded(F.runtimeID(3))], for: "docs")
        order.set([.embedded(F.runtimeID(9))], for: "gone")
        order.setPinned(.embedded(F.runtimeID(3)), true)
        func rows(_ sessions: [CompanionSession]) -> [WorkspaceRow] {
            var state = WorkspaceState()
            state.apply(WorkspaceSidebar.groups(sessions, now: now, glyphs: .fallback, order: order))
            return state.rows
        }

        var pruned = order
        pruned.forget(notAmong: rows(sessions))
        XCTAssertEqual(pruned.projects.keys.sorted(), ["api", "docs", "web"], "a project nobody lists is forgotten")
        XCTAssertEqual(pruned.projects["docs"], [WorkspaceOrder.token(.embedded(F.runtimeID(3)))], "a pinned row keeps its project")
        XCTAssertEqual(pruned.pinned, order.pinned)

        pruned.forget(notAmong: rows([sessions[1], sessions[2]]))
        XCTAssertEqual(pruned.projects, ["api": [WorkspaceOrder.token(.embedded(F.runtimeID(2)))], "web": [WorkspaceOrder.token(fourth)]],
                       "an ended session leaves its project's order, and a project with none left is forgotten")
        XCTAssertEqual(pruned.pinned, [])

        var unchanged = pruned
        unchanged.forget(notAmong: rows(sessions))
        XCTAssertEqual(unchanged, pruned, "what is listed is kept")
    }
}
