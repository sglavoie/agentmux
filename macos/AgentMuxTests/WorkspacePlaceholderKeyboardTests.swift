import AppKit
import XCTest
@testable import AgentMux

extension WorkspaceViewsWindowTests {
    private func returnKey(code: UInt16 = 36, flags: NSEvent.ModifierFlags = []) -> NSEvent {
        let text = code == 76 ? "\u{3}" : "\r"
        return NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: flags, timestamp: 0,
            windowNumber: workspace.window.windowNumber, context: nil, characters: text,
            charactersIgnoringModifiers: text, isARepeat: false, keyCode: code)!
    }

    func testReturnAndKeypadEnterReopenClosedDashboardWithEitherSidebarState() throws {
        for collapsed in [false, true] {
            workspace.setSidebarCollapsed(collapsed)
            for code: UInt16 in [36, 76] {
                workspace.showView("dashboard")
                workspace.terminalEnded(try XCTUnwrap(workspace.shownTerminalView))
                XCTAssertEqual(workspace.content, .viewEnded(id: "dashboard"))
                let count = app.madeViews.count

                workspace.window.sendEvent(returnKey(code: code))

                XCTAssertEqual(app.madeViews.count, count + 1)
                XCTAssertEqual(workspace.content, .view(id: "dashboard"))
                XCTAssertTrue(workspace.window.firstResponder === workspace.shownTerminalView)
            }
        }
    }

    func testEnterReattachesClosedSessionAndRetriesUnavailableDashboard() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.select(.embedded(F.runtimeID(1)), focus: true)
        workspace.terminalEnded(try XCTUnwrap(workspace.shownTerminalView))
        workspace.window.sendEvent(returnKey(code: 76))
        XCTAssertEqual(workspace.content, .terminal(runtimeSessionID: F.runtimeID(1)))
        XCTAssertEqual(app.made, [F.runtimeID(1), F.runtimeID(1)])

        app.viewProblem = "Temporarily unavailable"
        workspace.showView("dashboard")
        XCTAssertEqual(workspace.placeholder.button.title, "Try Again")
        app.viewProblem = nil
        workspace.window.sendEvent(returnKey())
        XCTAssertEqual(workspace.content, .view(id: "dashboard"))
        XCTAssertEqual(app.madeViews, ["dashboard"])
    }

    func testPlaceholderReturnDoesNotClaimTextInspectorModifiedKeysOrHiddenButtons() throws {
        workspace.showView("dashboard")
        workspace.terminalEnded(try XCTUnwrap(workspace.shownTerminalView))
        workspace.focusSidebar()
        for flags: NSEvent.ModifierFlags in [.command, .control, .option, .shift] {
            XCTAssertFalse(workspace.placeholder.button.performKeyEquivalent(with: returnKey(flags: flags)))
        }

        workspace.toggleSwitcher()
        XCTAssertTrue(workspace.window.firstResponder is NSText)
        XCTAssertFalse(workspace.placeholder.button.performKeyEquivalent(with: returnKey()))
        workspace.closeSwitcher(restoring: true)
        workspace.window.makeFirstResponder(workspace.inspector)
        XCTAssertFalse(workspace.placeholder.button.performKeyEquivalent(with: returnKey()))
        XCTAssertEqual(app.madeViews, ["dashboard"])

        workspace.focusSidebar()
        workspace.placeholder.button.isEnabled = false
        XCTAssertFalse(workspace.placeholder.button.performKeyEquivalent(with: returnKey()))
        workspace.placeholder.button.isEnabled = true
        workspace.placeholder.press()
        XCTAssertFalse(workspace.placeholder.button.performKeyEquivalent(with: returnKey()))
        XCTAssertEqual(app.madeViews, ["dashboard", "dashboard"])
    }

    func testReturnInOtherLivePaneDoesNotReopenClosedDashboard() throws {
        app.store.apply(snapshot: F.snapshot(sessions))
        workspace.showView("dashboard")
        workspace.split(with: .embedded(F.runtimeID(1)))
        workspace.focusOtherPane()
        workspace.terminalEnded(try XCTUnwrap(workspace.shownTerminalView))
        let terminal = try XCTUnwrap(workspace.terminalView(for: F.runtimeID(1)))
        workspace.window.makeFirstResponder(terminal)

        XCTAssertFalse(workspace.placeholder.button.performKeyEquivalent(with: returnKey()))
        workspace.window.sendEvent(returnKey())
        XCTAssertEqual(app.madeViews, ["dashboard"])
        XCTAssertEqual(workspace.content, .viewEnded(id: "dashboard"))
    }
}
