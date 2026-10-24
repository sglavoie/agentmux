import AppKit
import XCTest
import ScreenCaptureKit
@testable import AgentMux

@MainActor
final class WorkspaceConsistencyPolishTests: XCTestCase {
    func testDuplicateSessionsUseTheSameIdentityInSidebarSwitcherAndWaiting() throws {
        var sessions = (1...3).map { WorkspaceFixture.tmux($0, project: "api", icon: "question", attention: true) }
        sessions[0].branch = "fix/auth"
        sessions[1].branch = "main"
        sessions[2].branch = "main"
        let rows = WorkspaceSidebar.rows(sessions)
        XCTAssertEqual(rows[0].qualifier, "fix/auth")
        XCTAssertEqual(Set(rows.map(\.displayName)).count, 3)
        XCTAssertTrue(rows[1].qualifier.hasPrefix("#"))
        let reversed = WorkspaceSidebar.rows(sessions.reversed())
        for row in rows {
            XCTAssertEqual(reversed.first { $0.key == row.key }?.displayName, row.displayName)
            let cell = WorkspaceRowCellView()
            cell.show(row, muteGlyph: "")
            XCTAssertTrue(cell.titleLabel.stringValue.contains(row.qualifier))
            XCTAssertTrue(cell.accessibilityLabel()?.contains(row.qualifier) == true)
        }
        let switcher = WorkspaceSwitcher.entries(rows: rows, views: [])
        let waiting = WorkspaceInspector.waiting(sessions)
        XCTAssertEqual(Set(switcher.map(\.title)), Set(rows.map(\.displayName)))
        XCTAssertEqual(Set(waiting.map(\.title)), Set(rows.map(\.displayName)))
        XCTAssertTrue(rows.allSatisfy { $0.detail.hasPrefix("Waiting for your answer") })
    }

    func testLongSharedIdentitySuffixesAndKittyInstancesRemainDistinct() {
        var one = WorkspaceFixture.tmux(1, project: "api")
        var two = one
        one.runtime?.sessionID = "rs-10000000000000000000000000000001"
        two.runtime?.sessionID = "rs-20000000000000000000000000000001"
        let sessions = [one, two, WorkspaceFixture.kitty(1, project: "api", socket: "/tmp/one"), WorkspaceFixture.kitty(1, project: "api", socket: "/tmp/two")]
        XCTAssertEqual(Set(WorkspaceSidebar.rows(sessions).map(\.displayName)).count, 4)
        XCTAssertEqual(WorkspaceSidebar.rows([one])[0].displayName, "api — work")
        let kitty = WorkspaceSidebar.rows((1...3).map { WorkspaceFixture.kitty($0, project: "api", socket: "/a/very/long/shared/kitty/socket") })
        XCTAssertEqual(Set(kitty.map(\.qualifier)).count, 3)
        XCTAssertTrue(kitty.allSatisfy { $0.qualifier.count <= 8 }, "the common socket path must not consume the row")
    }

    func testResolutionIsScopedPersistsAndRecurrenceReopensTheNotice() throws {
        let suite = "amx.polish.\(UUID())"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let history = WorkspaceFeedbackHistory(defaults: defaults)
        let issue = WorkspaceFeedbackIssue(operation: .readiness, target: ["/plan", "work"])
        let failure = WorkspaceFeedback(target: "/plan", message: "Not ready", issue: issue)
        history.record(failure)
        history.record(.init(target: "/plan", message: "Unrelated success", kind: .success))
        history.resolve(.init(operation: .readiness, target: ["/other", "work"]), evidence: "Ready")
        history.resolve(.init(operation: .resume, target: issue.target), evidence: "Opened")
        XCTAssertTrue(history.entries.last?.unresolved == true)
        let at = Date(timeIntervalSince1970: 123)
        history.resolve(issue, evidence: "Fresh readiness check passed", at: at)
        let reopened = WorkspaceFeedbackHistory(defaults: defaults)
        XCTAssertEqual(reopened.entries.count, 1)
        XCTAssertEqual(reopened.entries[0].resolvedAt, at)
        XCTAssertEqual(reopened.entries[0].resolution, "Fresh readiness check passed")
        XCTAssertFalse(reopened.entries[0].unresolved)
        reopened.record(failure)
        XCTAssertNil(reopened.entries[0].resolvedAt)
        XCTAssertEqual(reopened.entries[0].occurrences, 2)
        XCTAssertTrue(reopened.entries[0].unresolved)
    }

    func testConfirmationMigrationAndLivePolicyFile() throws {
        let suite = "amx.polish.\(UUID())"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        XCTAssertEqual(AppPreferences.load(from: defaults).confirmationPolicy, .followCLI)
        defaults.set(false, forKey: AppPreferences.Key.confirmBeforeClosing)
        var preferences = AppPreferences.load(from: defaults)
        XCTAssertEqual(preferences.confirmationPolicy, .never)
        preferences.confirmationPolicy = .followCLI
        preferences.save(to: defaults)
        XCTAssertEqual(AppPreferences.load(from: defaults).confirmationPolicy, .followCLI)
        XCTAssertNil(defaults.object(forKey: AppPreferences.Key.confirmBeforeClosing))
        let file = WorkspaceConfirmationFile()
        try file.write(.always)
        XCTAssertEqual(try String(contentsOf: file.url, encoding: .utf8), "always")
        try file.write(.never)
        XCTAssertEqual(try String(contentsOf: file.url, encoding: .utf8), "never")
    }

    func testAdvancedConversationArgumentsPreserveQueryAsData() {
        var view = WorkspaceUtilityView(kind: .history, target: "codex:chosen", title: "Conversation Actions")
        view.historyQuery = "--flag \"two words\" $(literal)"
        XCTAssertEqual(view.arguments, ["workspace-view", "history", "--query", "--flag \"two words\" $(literal)", "--", "codex:chosen"])
        let other = WorkspaceUtilityView(kind: .history, target: "codex:chosen", title: "Conversation Actions")
        XCTAssertNotEqual(view.id, other.id, "a newly transferred query must not reuse a terminal showing an older query")
    }
}


extension WorkspaceConsistencyPolishTests {
    func testSidebarVisualReferencesInBothAppearances() async throws {
        guard #available(macOS 14.4, *) else { throw XCTSkip("Own-window capture requires macOS 14.4") }
        let app = FakeWorkspaceApp()
        let workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        defer { workspace.window.close() }
        app.store.onChange = { [weak workspace] in workspace?.refresh() }
        workspace.openForTesting()
        var sessions = (1...3).map { WorkspaceFixture.tmux($0, project: "api-service", icon: $0 == 1 ? "question" : $0 == 2 ? "bolt" : "stop", attention: $0 != 2) }
        for i in sessions.indices { sessions[i].branch = "main"; sessions[i].tabColor = ["#a9c5ff", "#f5d678", "#c4a9eb"][i] }
        app.store.apply(snapshot: WorkspaceFixture.snapshot(sessions))
        workspace.select(.embedded(WorkspaceFixture.runtimeID(2)))
        workspace.window.setContentSize(NSSize(width: 1000, height: 640))
        workspace.window.orderFront(nil)
        let directory = URL(fileURLWithPath: "/tmp/agentmux-polish-previews")
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        for dark in [false, true] {
            workspace.window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
            workspace.window.contentView?.layoutSubtreeIfNeeded()
            workspace.window.displayIfNeeded()
            try await Task.sleep(for: .milliseconds(450))
            let content = try await SCShareableContent.currentProcess
            let target = try XCTUnwrap(content.windows.first { $0.windowID == CGWindowID(workspace.window.windowNumber) })
            let filter = SCContentFilter(desktopIndependentWindow: target)
            let configuration = SCStreamConfiguration()
            configuration.width = Int(filter.contentRect.width * CGFloat(filter.pointPixelScale))
            configuration.height = Int(filter.contentRect.height * CGFloat(filter.pointPixelScale))
            configuration.ignoreShadowsSingleWindow = true
            configuration.showsCursor = false
            let capture = try await SCScreenshotManager.captureImage(contentFilter: filter, configuration: configuration)
            let png = try XCTUnwrap(NSBitmapImageRep(cgImage: capture).representation(using: .png, properties: [:]))
            let name = dark ? "consistent-sidebar-dark" : "consistent-sidebar-light"
            try png.write(to: directory.appendingPathComponent(name + ".png"))
            let attachment = XCTAttachment(data: png, uniformTypeIdentifier: "public.png")
            attachment.name = name; attachment.lifetime = .keepAlways; add(attachment)
            for row in workspace.state.rows {
                let index = try XCTUnwrap(workspace.items.firstIndex { if case let .row(item) = $0 { return item.key == row.key }; return false })
                let cell = try XCTUnwrap(workspace.sidebar.view(atColumn: 0, row: index, makeIfNecessary: true) as? WorkspaceRowCellView)
                XCTAssertTrue(cell.titleLabel.stringValue.contains(row.qualifier))
                XCTAssertEqual(cell.titleLabel.textColor, .labelColor)
                XCTAssertFalse(cell.detailLabel.stringValue.isEmpty)
            }
        }
    }
}
