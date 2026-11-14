import AppKit
import XCTest
@testable import AgentMux

@MainActor
final class WorkspaceStateSymbolTests: XCTestCase {
    func testAllStatesUseAvailableSymbolsAndSemanticColors() {
        let states: [(String, String, NSColor)] = [
            ("bolt", "bolt.fill", .systemBlue),
            ("stop", "checkmark.circle.fill", WorkspaceStateSymbol.completedColor),
            ("key", "key.fill", .systemOrange),
            ("question", "questionmark.bubble.fill", .systemOrange),
            ("warning", "exclamationmark.triangle.fill", .secondaryLabelColor),
            ("error", "xmark.octagon.fill", .systemRed),
            ("subagent", "person.2.fill", .systemBlue),
            ("compact", "arrow.triangle.2.circlepath", .systemBlue),
        ]
        let cell = WorkspaceRowCellView()
        for (state, name, color) in states {
            let session = WorkspaceFixture.tmux(1, project: "API", icon: state)
            cell.show(WorkspaceSidebar.row(session, now: Date(), glyphs: .fallback), muteGlyph: "")
            XCTAssertEqual(WorkspaceStateSymbol.name(for: state), name)
            XCTAssertNotNil(cell.stateImage.image, name)
            XCTAssertEqual(cell.stateImage.contentTintColor, color)
            XCTAssertEqual(cell.stateImage.accessibilityLabel(), WorkspaceInspector.word(for: state))
            XCTAssertFalse(cell.stateImage.isPulsing, "Tests disable symbol motion")
            cell.setTabText(.white, selected: true)
            cell.setStateGlyphOutsidePill(true)
            XCTAssertEqual(cell.stateImage.contentTintColor, color, "Selection preserves state meaning")
        }
    }

    func testReuseClearsStateAndUtilityViewsKeepTheirGlyph() {
        let cell = WorkspaceRowCellView()
        for state in ["bolt", "", "future-state"] {
            cell.show(WorkspaceSidebar.row(WorkspaceFixture.tmux(1, project: "API", icon: state), now: Date(), glyphs: .fallback), muteGlyph: "")
            XCTAssertEqual(cell.stateImage.image == nil, state != "bolt")
            XCTAssertFalse(cell.stateImage.isPulsing)
        }
        cell.show(WorkspaceSidebar.row(WorkspaceFixture.tmux(1, project: "API", icon: "bolt"), now: Date(), glyphs: .fallback), muteGlyph: "")
        cell.prepareForReuse()
        XCTAssertNil(cell.stateImage.image)
        XCTAssertFalse(cell.stateImage.isPulsing)
        cell.show(WorkspaceUtilityView.dashboard)
        XCTAssertTrue(cell.stateImage.isHidden)
        XCTAssertNil(cell.stateImage.image)
        XCTAssertFalse(cell.glyphLabel.isHidden)
        XCTAssertEqual(cell.glyphLabel.stringValue, WorkspaceUtilityView.dashboard.glyph)
    }
}
