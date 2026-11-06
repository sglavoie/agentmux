import AppKit
import XCTest
import ScreenCaptureKit
@testable import AgentMux

@MainActor
final class SessionReviewTests: XCTestCase {
    private typealias F = WorkspaceFixture

    func testWireDefaultsAndPrivateAnnotationRequest() throws {
        let old = try JSONDecoder().decode(CompanionSession.self, from: Data("{}".utf8))
        XCTAssertEqual(old.review, SessionReview())
        let json = #"{"review":{"epoch":"event-2","accounted":true,"reviewed":true,"note":"Parked","project_note":"Implementation running"}}"#
        let current = try JSONDecoder().decode(CompanionSession.self, from: Data(json.utf8))
        XCTAssertTrue(current.review.accounted)
        XCTAssertEqual(current.review.projectNote, "Implementation running")
        var sent: [String: Any] = [:]
        let client = DaemonClient(socketPath: "/tmp/fixture.sock", roundTrip: { _, data, _ in
            sent = try JSONSerialization.jsonObject(with: data) as! [String: Any]
            return Data(#"{"ok":true,"review":{"undo_token":"receipt","count":1}}"#.utf8)
        })
        let result = try client.sessionReview(.init(targets: [F.runtimeID(1): "event-2"], note: "Parked")).get()
        XCTAssertEqual(result.undoToken, "receipt")
        XCTAssertEqual(sent["type"] as? String, "session_review")
        let review = try XCTUnwrap(sent["review"] as? [String: Any])
        XCTAssertNil(review["accounted"], "saving a note never acknowledges the session")
        XCTAssertEqual(review["targets"] as? [String: String], [F.runtimeID(1): "event-2"])
    }

    func testProjectBadgeTracksNewSessionsAndRetainsNotes() throws {
        var working = F.tmux(1, project: "Example", icon: "bolt", attention: false)
        working.review.accounted = true; working.review.reviewed = true
        working.review.projectNote = "Implementation running; reviewer parked."
        var idle = F.tmux(2, project: "Example", icon: "", attention: false)
        idle.review = working.review
        func group(_ sessions: [CompanionSession]) throws -> WorkspaceGroup {
            try XCTUnwrap(WorkspaceSidebar.groups(sessions, grouping: .project).first)
        }
        let accounted = try group([working, idle])
        XCTAssertEqual(accounted.reviewLabel, "✓ Accounted for")
        XCTAssertEqual(accounted.projectNote, working.review.projectNote)
        idle.review.accounted = false
        XCTAssertEqual(try group([working, idle]).reviewLabel, "1 needs review")
        idle.review = SessionReview()
        XCTAssertEqual(try group([working, idle]).reviewLabel, "1 needs review", "new members are unacknowledged")
        let row = WorkspaceSidebar.row(working, now: Date(), glyphs: .fallback)
        XCTAssertTrue(row.detail.contains("Accounted for"))
        XCTAssertTrue(row.toolTip.contains("reviewer parked"))
    }

    func testAcknowledgementLeavesQuestionsAndErrorsVisible() {
        for icon in ["key", "question", "error", "stop"] {
            var session = F.tmux(1, project: "Example", icon: icon)
            session.unseen = true; session.review.accounted = true; session.review.reviewed = true
            XCTAssertEqual(session.needsAttention, icon != "stop", icon)
        }
    }

    func testContextMenuCapturesProjectAndDoesNotMoveSelection() async throws {
        let app = FakeWorkspaceApp()
        let workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        app.store.onChange = { [weak workspace] in workspace?.refresh() }
        workspace.openForTesting()
        defer { workspace.window.close() }
        var first = F.tmux(1, project: "Example", icon: "bolt", attention: false)
        first.review.epoch = "first"
        var second = F.tmux(2, project: "Example", icon: "", attention: false)
        second.review.epoch = "second"
        app.store.apply(snapshot: F.snapshot([first, second]))
        workspace.select(.embedded(F.runtimeID(1)), focus: true)
        let originalFocus = workspace.window.firstResponder
        let captured = expectation(description: "annotation reached service")
        workspace.sessionReviewService = { request in
            XCTAssertEqual(request.targets, [F.runtimeID(1): "first", F.runtimeID(2): "second"])
            XCTAssertEqual(request.project, "Example")
            XCTAssertEqual(request.accounted, true)
            captured.fulfill()
            return .success(SessionReviewResult(undoToken: "undo", count: 2))
        }
        let menu = workspace.reviewMenuItems(rows: workspace.state.rows, project: "Example")
        first.review.epoch = "new attention"
        app.store.apply(snapshot: F.snapshot([first, second, F.tmux(3, project: "Example")]))
        let mark = try XCTUnwrap(menu.first { $0.title == "Mark Accounted For" })
        _ = try XCTUnwrap(mark.target).perform(try XCTUnwrap(mark.action), with: mark)
        await fulfillment(of: [captured], timeout: 3)
        for _ in 0..<50 where workspace.sessionReviewBusy { try await Task.sleep(for: .milliseconds(10)) }
        XCTAssertEqual(workspace.sessionReviewUndo, "undo")
        XCTAssertEqual(workspace.state.selection, .embedded(F.runtimeID(1)))
        XCTAssertTrue(workspace.window.firstResponder === originalFocus)
        app.store.apply(.disconnected(.heartbeatLost, retryIn: 1))
        XCTAssertFalse(workspace.reviewMenuItems(rows: workspace.state.rows).first { $0.title == "Mark Accounted For" }!.isEnabled)
    }

    func testFailedSaveKeepsReceiptAndSavedNotes() async throws {
        let app = FakeWorkspaceApp()
        let workspace = WorkspaceWindowController(delegate: app, glyphs: .fallback)
        app.store.onChange = { [weak workspace] in workspace?.refresh() }
        workspace.openForTesting()
        defer { workspace.window.close() }
        var session = F.tmux(1, project: "Example", icon: "bolt", attention: false)
        session.review.note = "Prior note"
        app.store.apply(snapshot: F.snapshot([session]))
        workspace.sessionReviewUndo = "prior-receipt"
        workspace.sessionReviewService = { _ in
            .failure(NSError(domain: "Fixture", code: 1, userInfo: [NSLocalizedDescriptionKey: "Disk full"]))
        }
        let finished = expectation(description: "failed save")
        workspace.saveSessionReview(.init(targets: [F.runtimeID(1): ""], note: "New note")) { problem in
            XCTAssertEqual(problem, "Disk full")
            finished.fulfill()
        }
        await fulfillment(of: [finished], timeout: 3)
        XCTAssertEqual(workspace.sessionReviewUndo, "prior-receipt")
        XCTAssertEqual(workspace.state.rows.first?.review.note, "Prior note")
        XCTAssertFalse(workspace.sessionReviewBusy)
    }

    func testProjectReviewVisuals() async throws {
        guard #available(macOS 14.4, *) else { throw XCTSkip("Own-window captures require macOS 14.4") }
        for dark in [false, true] {
            var first = F.tmux(1, project: "Example", icon: "bolt", attention: false)
            first.review.accounted = true; first.review.reviewed = true
            first.review.projectNote = "Implementation running; reviewer parked."
            var second = F.tmux(2, project: "Example", icon: "", attention: false)
            second.review = first.review
            let groups = WorkspaceSidebar.groups([first, second], grouping: .project)
            let header = WorkspaceGroupCellView(); header.show(try XCTUnwrap(groups.first))
            let rows = WorkspaceSidebar.rows([first, second])
            let firstCell = WorkspaceRowCellView(); firstCell.show(rows[0], muteGlyph: "")
            let secondCell = WorkspaceRowCellView(); secondCell.show(rows[1], muteGlyph: "")
            let note = NSTextField(wrappingLabelWithString: first.review.projectNote)
            let stack = NSStackView(views: [header, firstCell, secondCell, note])
            stack.orientation = .vertical; stack.alignment = .leading; stack.spacing = 8
            stack.edgeInsets = NSEdgeInsets(top: 16, left: 16, bottom: 16, right: 16)
            for view in [header, firstCell, secondCell] { view.widthAnchor.constraint(equalToConstant: 468).isActive = true }
            header.heightAnchor.constraint(equalToConstant: 28).isActive = true
            firstCell.heightAnchor.constraint(equalToConstant: 56).isActive = true
            secondCell.heightAnchor.constraint(equalToConstant: 56).isActive = true
            let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 500, height: 230), styleMask: [.titled], backing: .buffered, defer: false)
            window.isReleasedWhenClosed = false; window.contentView = stack
            window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
            window.orderFront(nil); window.layoutIfNeeded()
            defer { window.close() }
            try await Task.sleep(for: .milliseconds(150))
            let content = try await SCShareableContent.currentProcess
            let target = try XCTUnwrap(content.windows.first { $0.windowID == CGWindowID(window.windowNumber) })
            let filter = SCContentFilter(desktopIndependentWindow: target)
            let config = SCStreamConfiguration()
            config.width = Int(filter.contentRect.width * CGFloat(filter.pointPixelScale))
            config.height = Int(filter.contentRect.height * CGFloat(filter.pointPixelScale))
            config.ignoreShadowsSingleWindow = true; config.showsCursor = false
            let capture = try await SCScreenshotManager.captureImage(contentFilter: filter, configuration: config)
            let png = try XCTUnwrap(NSBitmapImageRep(cgImage: capture).representation(using: .png, properties: [:]))
            let path = URL(fileURLWithPath: "/tmp/agentmux-review-\(dark ? "dark" : "light").png")
            try png.write(to: path)
            XCTAssertTrue(header.countLabel.stringValue.contains("Accounted for"))
        }
    }
}
