import AppKit
import SwiftUI

extension WorkspaceWindowController {
    // MARK: Window

    static let minimumSize = NSSize(width: 640, height: 360)

    func makeWindow() -> NSWindow {
        let sidebarController = WorkspaceWindowChrome.sidebar(table: sidebar, groupFilter: groupFilter, feedLabel: feedLabel, feedButton: feedButton)
        groupFilter.onSelect = { [weak self] group in self?.selectGroup(group) }
        groupFilter.onWaitingOnly = { [weak self] waitingOnly in self?.setSidebarWaitingOnly(waitingOnly) }
        let sidebarZoomView = WorkspaceSidebarZoomView(content: sidebarController.view)
        sidebarZoomView.zoom = sidebarZoom
        welcomeView.zoom = sidebarZoom
        sidebarZoomView.opaqueInLight = true
        sidebarController.view = sidebarZoomView
        self.sidebarZoomView = sidebarZoomView
        sidebar.dataSource = self
        sidebar.delegate = self
        sidebar.target = self
        sidebar.action = #selector(rowClicked(_:))
        sidebar.onPointerChanged = { [weak self] in self?.reloadSidebar() }
        sidebar.onReturn = { [weak self] in self?.focusSelectedItem() }
        sidebar.onWidthChanged = { [weak self] in self?.refreshWorkspaceNavigationLayout() }
        sidebar.onCollapse = { [weak self] collapsed in
            guard let self else { return }
            if self.sidebar.selectedRow >= 0, self.items[self.sidebar.selectedRow] == .navigation {
                self.selectWorkspaceNavigation(!collapsed)
            } else { self.setSelectedProjectCollapsed(collapsed) }
        }
        sidebar.onStep = { [weak self] offset in self?.stepFromUnlistedPlace(offset) ?? false }
        sidebar.hasMenu = { [weak self] row in !(self?.contextMenuItems(forRow: row).isEmpty ?? true) }
        sidebar.registerForDraggedTypes([Self.dragType])
        sidebar.setDraggingSourceOperationMask(.move, forLocal: true)
        sidebar.setDraggingSourceOperationMask([], forLocal: false)
        let context = NSMenu()
        // Each item says itself whether it can be chosen: End Session… is
        // greyed out on a session already closing, which validation would
        // otherwise turn back on.
        context.autoenablesItems = false
        context.delegate = self
        sidebar.menu = context
        feedButton.target = self
        feedButton.action = #selector(daemonButtonPressed(_:))

        scrollbarView.onScroll = { [weak self] row in self?.scrollbarMoved(toRow: row) }
        findBar.onNeedle = { [weak self] needle in self?.findBarNeedleChanged(needle) }
        findBar.onNext = { [weak self] in self?.findBarNavigate(next: true) }
        findBar.onPrevious = { [weak self] in self?.findBarNavigate(next: false) }
        findBar.onClose = { [weak self] in self?.closeFindBar() }
        switcher.onChoose = { [weak self] entry in self?.go(to: entry) }
        switcher.onCancel = { [weak self] in self?.closeSwitcher(restoring: true) }
        switcher.onLeave = { [weak self] in self?.closeSwitcher(restoring: false) }
        WorkspaceWindowChrome.installPaneOverlays(
            in: pane, placeholder: placeholder, linkLabel: linkLabel,
            scrollbar: scrollbarView, findBar: findBar, switcher: switcher
        )
        conversationSearchButton.target = self
        conversationSearchButton.action = #selector(searchFullConversation(_:))
        conversationSearchButton.bezelStyle = .rounded
        conversationSearchButton.controlSize = .small
        conversationSearchButton.refusesFirstResponder = true
        conversationSearchButton.toolTip = "Search saved conversation prose, including messages outside terminal Find’s scope. Carries the active terminal query into History."
        conversationSearchButton.isHidden = true
        conversationSearchButton.translatesAutoresizingMaskIntoConstraints = false
        pane.addSubview(conversationSearchButton)
        NSLayoutConstraint.activate([
            conversationSearchButton.topAnchor.constraint(equalTo: pane.topAnchor, constant: 8),
            conversationSearchButton.trailingAnchor.constraint(equalTo: pane.trailingAnchor, constant: -12),
            conversationSearchButton.leadingAnchor.constraint(greaterThanOrEqualTo: pane.leadingAnchor, constant: 8),
        ])
        let paneController = NSViewController()
        otherPlaceholder.frame = otherPane.bounds
        otherPlaceholder.autoresizingMask = [.width, .height]
        otherPane.addSubview(otherPlaceholder)
        paneLayout.other.onFocus = { [weak self] in _ = self?.focusOtherPane() }
        paneLayout.active.onFocus = { [weak self] in self?.updateContent(focus: true) }
        paneController.view = paneLayout

        let split = NSSplitViewController()
        let sidebarItem = NSSplitViewItem(sidebarWithViewController: sidebarController)
        sidebarItem.minimumThickness = Self.sidebarWidths.lowerBound
        sidebarItem.maximumThickness = Self.sidebarWidths.upperBound
        sidebarItem.canCollapse = true
        sidebarItem.canCollapseFromWindowResize = false
        self.sidebarItem = sidebarItem
        sidebarObservation = sidebarItem.observe(\.isCollapsed) { [weak self] _, _ in
            MainActor.assumeIsolated { self?.sidebarCollapseChanged() }
        }
        let paneItem = NSSplitViewItem(viewController: paneController)
        paneItem.minimumThickness = 320
        contentItem = paneItem

        inspector.onTab = { [weak self] tab in self?.setInspectorTab(tab) }
        inspector.onAllWaitingGroups = { [weak self] showAll in
            self?.showsAllWaitingGroups = showAll
            self?.reloadInspector()
        }
        inspector.onWaiting = { [weak self] key in
            guard let self, let row = self.state.row(key) else { return }
            self.activate(row)
            self.revealInSidebar()
        }
        inspector.onAction = { [weak self] action in self?.delegate?.workspaceInspectorAction(action) }
        inspector.onAnswer = { [weak self] answer in self?.delegate?.workspaceAnswer(answer) }
        inspector.onLeaveReader = { [weak self] in
            guard let self else { return }
            let back = self.waitingReturn
            self.waitingReturn = nil
            if let back, (back as? NSView)?.isHiddenOrHasHiddenAncestor != true, self.window.makeFirstResponder(back) { return }
            self.focusListOrPane()
        }
        inspector.onContext = { [weak self] row, completion in
            self?.delegate?.workspaceContext(row, completion: completion)
        }
        let inspectorController = NSViewController()
        let inspectorZoomView = WorkspaceSidebarZoomView(content: inspector)
        inspectorZoomView.zoom = sidebarZoom
        inspectorController.view = inspectorZoomView
        self.inspectorZoomView = inspectorZoomView
        let inspectorItem = NSSplitViewItem(viewController: inspectorController)
        inspectorItem.minimumThickness = Self.inspectorWidths.lowerBound
        inspectorItem.maximumThickness = Self.inspectorWidths.upperBound
        inspectorItem.canCollapse = true
        // Like the sidebar it keeps its width while the window is resized:
        // the pane takes the difference.
        inspectorItem.holdingPriority = sidebarItem.holdingPriority
        self.inspectorItem = inspectorItem
        split.addSplitViewItem(sidebarItem)
        split.addSplitViewItem(paneItem)
        split.addSplitViewItem(inspectorItem)

        let window = WorkspaceZoomWindow(contentViewController: split)
        window.onSidebarZoom = { [weak self] change in self?.zoomSidebars(change) }
        window.title = "Agent Mux"
        window.styleMask = [.titled, .closable, .miniaturizable, .resizable]
        window.isReleasedWhenClosed = false
        window.minSize = Self.minimumSize
        window.setContentSize(NSSize(width: 1100, height: 700))
        window.delegate = self
        window.center()
        if let frameAutosaveName { window.setFrameAutosaveName(frameAutosaveName) }
        let splitView = split.splitView
        splitView.layoutSubtreeIfNeeded()
        // The inspector gets its width while shown, so that showing it
        // again brings that width back. It goes first: moving its divider
        // takes from the sidebar too.
        let inspectorShown = Self.width(inspectorWidth, default: Self.defaultInspectorWidth, in: Self.inspectorWidths)
        splitView.setPosition(splitView.bounds.width - inspectorShown - splitView.dividerThickness, ofDividerAt: 1)
        // Hidden until it is asked for. An item is collapsed for good only
        // once its split view is laid out in a window.
        splitView.layoutSubtreeIfNeeded()
        inspectorItem.isCollapsed = true
        // The sidebar goes last, once the hidden inspector has given its
        // room back: beside a shown one in a narrow window it would be cut
        // to what is left, and that width saved over its own.
        splitView.layoutSubtreeIfNeeded()
        splitView.setPosition(Self.width(sidebarWidth, default: Self.defaultSidebarWidth, in: Self.sidebarWidths), ofDividerAt: 0)
        splitView.layoutSubtreeIfNeeded()
        reportedSidebarWidth = sidebarWidth
        reportedInspectorWidth = inspectorWidth
        splitObservation = NotificationCenter.default.addObserver(
            forName: NSSplitView.didResizeSubviewsNotification, object: splitView, queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated { self?.splitResized() }
        }
        inspectorObservation = inspectorItem.observe(\.isCollapsed) { [weak self] _, _ in
            MainActor.assumeIsolated { self?.inspectorCollapseChanged() }
        }

        sidebarToggle.target = self
        sidebarToggle.action = #selector(toggleSidebar(_:))
        window.addTitlebarAccessoryViewController(WorkspaceWindowChrome.titlebarAccessory(sidebarToggle, at: .leading))
        inspectorToggle.target = self
        inspectorToggle.action = #selector(toggleInspector(_:))
        window.addTitlebarAccessoryViewController(WorkspaceWindowChrome.titlebarAccessory(inspectorToggle, at: .trailing))
        return window
    }
}

/// The hovered link's overlay. It only shows the link: clicks under it go
/// to the terminal beneath.
@MainActor
final class WorkspaceLinkLabel: NSTextField {
    override func hitTest(_ point: NSPoint) -> NSView? { nil }
}
