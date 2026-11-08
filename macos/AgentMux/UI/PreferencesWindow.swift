import AppKit
import Combine
import SwiftUI

/// The Settings window: a toolbar tab per `PreferencesTab`, as a Mac Settings
/// window has, each hosting its SwiftUI pane. The window keeps one width and
/// takes the height of the pane it shows, up to what the screen has room for;
/// a pane taller than that scrolls.
@MainActor
final class PreferencesWindowController: NSObject, NSWindowDelegate {
    let model: PreferencesModel
    let dashboardRecorder = HotKeyRecorder()
    let attentionRecorder = HotKeyRecorder()
    let workspaceRecorder = HotKeyRecorder()
    private(set) lazy var window: NSWindow = makeWindow()
    /// The toolbar tabs, made with the window.
    private(set) var tabViewController: PreferencesTabViewController?
    private var selection: AnyCancellable?
    /// Kept clear between the window and the screen's visible edges.
    static let screenMargin: CGFloat = 60
    /// The width every pane is laid out at.
    static let contentWidth: CGFloat = 560
    /// The height a pane is given when its own cannot be measured.
    nonisolated static let fallbackContentHeight: CGFloat = 620
    /// The shortest a pane is made, so a short one still reads as a window.
    nonisolated static let minimumContentHeight: CGFloat = 160

    init(model: PreferencesModel) {
        self.model = model
        super.init()
    }

    /// Told when the window opens and closes: while it is open the app keeps
    /// a Dock icon and a place in ⌘Tab, so the window can be found again.
    var onVisibilityChanged: (Bool) -> Void = { _ in }

    func show() {
        model.refresh()
        onVisibilityChanged(true)
        // An accessory app is not active by default; bring it forward so the
        // window takes key focus.
        NSApp.activate()
        fitToSelectedPane(animate: false)
        window.makeKeyAndOrderFront(nil)
        // The toolbar is in place once the window is on screen; the pane
        // keeps its height under it.
        fitToSelectedPane(animate: false)
    }

    private func makeWindow() -> NSWindow {
        let tabs = PreferencesTabViewController()
        tabs.tabStyle = .toolbar
        tabs.transitionOptions = []
        for tab in PreferencesTab.allCases {
            let pane = NSHostingController(rootView: pane(tab))
            // The pane fills what the window gives it and asks nothing of
            // the window's size: the controller sets that, from `idealHeight`.
            pane.sizingOptions = []
            pane.title = tab.title
            let item = NSTabViewItem(viewController: pane)
            item.identifier = tab.rawValue
            item.label = tab.title
            item.image = NSImage(systemSymbolName: tab.symbolName, accessibilityDescription: tab.title)
            tabs.addTabViewItem(item)
        }
        tabs.selectedTabViewItemIndex = PreferencesTab.allCases.firstIndex(of: model.selectedTab) ?? 0
        tabs.onSelect = { [weak self] tab in self?.didSelect(tab) }
        tabViewController = tabs

        let window = NSWindow(contentViewController: tabs)
        // A fixed width, as a Mac Settings window has; the height follows
        // the pane, set by `fitToSelectedPane` rather than by dragging.
        window.styleMask = [.titled, .closable]
        window.toolbarStyle = .preference
        window.isReleasedWhenClosed = false
        window.delegate = self
        dashboardRecorder.window = window
        attentionRecorder.window = window
        workspaceRecorder.window = window
        window.setContentSize(NSSize(width: Self.contentWidth, height: Self.fallbackContentHeight))
        fit(window, to: model.selectedTab, animate: false)
        window.center()
        // A request to show a tab (Open Settings at the amx binary, the
        // launch destination) selects it here, open or not.
        selection = model.$selectedTab.removeDuplicates().sink { [weak self] tab in
            self?.select(tab)
        }
        return window
    }

    private func pane(_ tab: PreferencesTab) -> PreferencesView {
        PreferencesView(model: model, dashboardRecorder: dashboardRecorder, attentionRecorder: attentionRecorder,
                        workspaceRecorder: workspaceRecorder, tab: tab)
    }

    /// The tab the toolbar shows.
    var selectedTab: PreferencesTab? {
        guard let tabs = tabViewController, tabs.tabViewItems.indices.contains(tabs.selectedTabViewItemIndex) else { return nil }
        return (tabs.tabViewItems[tabs.selectedTabViewItemIndex].identifier as? String).flatMap(PreferencesTab.init(rawValue:))
    }

    private func select(_ tab: PreferencesTab) {
        guard let tabs = tabViewController, let index = PreferencesTab.allCases.firstIndex(of: tab),
              tabs.selectedTabViewItemIndex != index else { return }
        tabs.selectedTabViewItemIndex = index
    }

    /// The operator, or a request, chose another tab: it is remembered, what
    /// was being typed or recorded on the last one ends, and the window takes
    /// the new pane's height.
    private func didSelect(_ tab: PreferencesTab) {
        if model.selectedTab != tab { model.selectedTab = tab }
        model.commitBinaryPath()
        model.commitEnvironment()
        dashboardRecorder.stop()
        attentionRecorder.stop()
        workspaceRecorder.stop()
        fitToSelectedPane(animate: tabViewController?.view.window?.isVisible ?? false)
    }

    private func fitToSelectedPane(animate: Bool) {
        guard let tab = selectedTab, let window = tabViewController?.view.window else { return }
        fit(window, to: tab, animate: animate)
    }

    /// Gives the window the height `tab`'s pane lays out at, keeping its top
    /// edge where it is.
    private func fit(_ window: NSWindow, to tab: PreferencesTab, animate: Bool) {
        let chrome = window.frame.height - (window.contentView?.frame.height ?? window.contentLayoutRect.height)
        let visible = (window.screen ?? NSScreen.main)?.visibleFrame
        let height = Self.contentHeight(ideal: idealHeight(of: tab),
                                        available: visible.map { $0.height - Self.screenMargin - chrome })
        var frame = window.frame
        let newHeight = height + chrome
        guard abs(frame.height - newHeight) > 0.5 || abs(window.contentLayoutRect.width - Self.contentWidth) > 0.5 else { return }
        frame.origin.y = frame.maxY - newHeight
        frame.size.height = newHeight
        frame.size.width = Self.contentWidth + (window.frame.width - (window.contentView?.frame.width ?? Self.contentWidth))
        window.setFrame(frame, display: true, animate: animate)
    }

    /// The height `tab`'s pane lays out at with nothing to scroll, measured
    /// on a copy so the pane on screen keeps its scroll position.
    func idealHeight(of tab: PreferencesTab) -> CGFloat {
        NSHostingView(rootView: pane(tab)).fittingSize.height
    }

    /// The height a pane is given: its own, at least `minimumContentHeight`,
    /// at most what `available` leaves; `fallbackContentHeight` when its own
    /// could not be measured.
    nonisolated static func contentHeight(ideal: CGFloat, available: CGFloat?) -> CGFloat {
        var height = ideal.isFinite && ideal > 0 ? ideal.rounded(.up) : fallbackContentHeight
        height = max(height, minimumContentHeight)
        if let available { height = min(height, max(available.rounded(.down), minimumContentHeight)) }
        return height
    }

    /// A closed window records nothing, and ends any editing of the binary
    /// path or the environment.
    func windowWillClose(_ notification: Notification) {
        model.commitBinaryPath()
        model.commitEnvironment()
        dashboardRecorder.stop()
        attentionRecorder.stop()
        workspaceRecorder.stop()
        onVisibilityChanged(false)
    }

    /// Coming back from System Settings: what was allowed or denied there
    /// (the login item, notifications) is read again, so a note about it
    /// does not outlive the reason for it.
    func windowDidBecomeKey(_ notification: Notification) {
        model.refreshLoginItem()
        model.onRefreshAuthorization?()
    }

    /// Recording ends when Settings loses key, so the shortcuts are back
    /// while the operator works in another window.
    func windowDidResignKey(_ notification: Notification) {
        dashboardRecorder.stop()
        attentionRecorder.stop()
        workspaceRecorder.stop()
    }
}

/// Settings' toolbar tabs, telling its window controller which one the
/// operator picked.
final class PreferencesTabViewController: NSTabViewController {
    var onSelect: ((PreferencesTab) -> Void)?

    override func tabView(_ tabView: NSTabView, didSelect tabViewItem: NSTabViewItem?) {
        super.tabView(tabView, didSelect: tabViewItem)
        guard let id = tabViewItem?.identifier as? String, let tab = PreferencesTab(rawValue: id) else { return }
        onSelect?(tab)
    }
}
