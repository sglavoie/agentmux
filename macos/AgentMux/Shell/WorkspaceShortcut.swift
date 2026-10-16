import AppKit

/// Stable preference IDs, independent of menu wording. These are application
/// menu equivalents, never Carbon registrations that capture keys in other apps.
enum WorkspaceShortcut: String, CaseIterable, Identifiable {
    case splitShell, splitChanges, focusOtherPane, swapPanes, hideOtherPane
    case runs, runPlan, activity, history, changes, commandPalette
    var id: String { rawValue }
    var title: String {
        switch self {
        case .splitShell: return "Split with Shell"
        case .splitChanges: return "Split with Checkout Changes"
        case .focusOtherPane: return "Focus Other Pane"
        case .swapPanes: return "Swap Panes"
        case .hideOtherPane: return "Hide Other Pane"
        case .runs: return "ASL Runs…"
        case .runPlan: return "Run a Plan…"
        case .activity: return "Since Last Looked…"
        case .history: return "Conversation History…"
        case .changes: return "Review Checkout Changes…"
        case .commandPalette: return "Command Palette…"
        }
    }
    @MainActor var action: Selector {
        switch self {
        case .splitShell: return #selector(AppDelegate.splitWorkspaceShell(_:))
        case .splitChanges: return #selector(AppDelegate.splitWorkspaceChanges(_:))
        case .focusOtherPane: return #selector(AppDelegate.focusOtherWorkspacePane(_:))
        case .swapPanes: return #selector(AppDelegate.swapWorkspacePanes(_:))
        case .hideOtherPane: return #selector(AppDelegate.collapseWorkspacePane(_:))
        case .runs: return #selector(AppDelegate.showRuns(_:))
        case .runPlan: return #selector(AppDelegate.runPlan(_:))
        case .activity: return #selector(AppDelegate.showActivity(_:))
        case .history: return #selector(AppDelegate.showHistory(_:))
        case .changes: return #selector(AppDelegate.showChanges(_:))
        case .commandPalette: return #selector(AppDelegate.showCommandPalette(_:))
        }
    }
    var defaultCombo: HotKeyCombo? {
        self == .commandPalette ? HotKeyCombo(string: "shift+cmd+p", scope: .application) : nil
    }
    func combo(in preferences: AppPreferences) -> HotKeyCombo? {
        guard let stored = preferences.workspaceShortcuts[rawValue] else { return defaultCombo }
        if stored.isEmpty { return nil }
        return HotKeyCombo(string: stored, scope: .application) ?? defaultCombo
    }

    @MainActor
    func problem(for combo: HotKeyCombo?, preferences: AppPreferences, menu: NSMenu?) -> String? {
        guard let combo else { return nil }
        let others = Self.allCases.filter { $0 != self }.map { (label: $0.title, combo: $0.combo(in: preferences)) }
            + [(label: HotKeyPreference.dashboard.label, combo: preferences.hotKeyDashboard),
               (label: HotKeyPreference.nextAttention.label, combo: preferences.hotKeyNextAttention)]
        if let problem = HotKeyConflicts.duplicate(combo, earlier: others) { return problem }
        guard let equivalent = combo.menuKeyEquivalent else { return "This key cannot be used in a menu shortcut." }
        let actions = Set(Self.allCases.map(\.action))
        func fixedKeys(_ menu: NSMenu?) -> [HotKeyConflicts.MenuKey] {
            (menu?.items ?? []).flatMap { item -> [HotKeyConflicts.MenuKey] in
                var result = fixedKeys(item.submenu)
                if !item.keyEquivalent.isEmpty && (item.action.map({ !actions.contains($0) }) ?? true) {
                    result.append(.init(title: item.title, key: item.keyEquivalent, modifiers: item.keyEquivalentModifierMask))
                }
                return result
            }
        }
        // Menu uppercase and shifted punctuation imply Shift, even when the
        // menu item's modifier mask only says Command.
        let shifted = Dictionary(uniqueKeysWithValues: zip(Array("~!@#$%^&*()_+{}|:\"<>?"), Array("`1234567890-=[]\\;',./")))
        func normalized(_ key: String, _ flags: NSEvent.ModifierFlags) -> (String, NSEvent.ModifierFlags) {
            var flags = flags.intersection([.command, .control, .option, .shift])
            if key != key.lowercased() { flags.insert(.shift) }
            if key.count == 1, let char = key.first, let base = shifted[char] { flags.insert(.shift); return (String(base), flags) }
            return (key.lowercased(), flags)
        }
        let wanted = normalized(equivalent.key, equivalent.modifiers)
        if let owner = fixedKeys(menu).first(where: { normalized($0.key, $0.modifiers) == wanted }) {
            return "\(combo.displayString) is already used for “\(owner.title)”. Choose another shortcut."
        }
        return nil
    }

    /// Physical-key matching also covers shifted punctuation and control keys
    /// that the terminal normally encodes before AppKit can claim them.
    @MainActor
    static func perform(_ event: NSEvent, in menu: NSMenu?) -> Bool {
        guard let menu else { return false }
        for item in menu.items {
            if let submenu = item.submenu, perform(event, in: submenu) { return true }
            guard let combo = item.representedObject as? HotKeyCombo, !item.keyEquivalent.isEmpty,
                  combo.keyCode == UInt32(event.keyCode), combo.modifiers == HotKeyCombo.carbonModifiers(event.modifierFlags) else { continue }
            menu.update()
            guard item.isEnabled else { return false }
            menu.performActionForItem(at: menu.index(of: item))
            return true
        }
        return false
    }

    @MainActor
    static func apply(_ preferences: AppPreferences, to menu: NSMenu) {
        let equivalents = Dictionary(uniqueKeysWithValues: allCases.map { shortcut in
            let combo = shortcut.combo(in: preferences)
            let equivalent = shortcut.problem(for: combo, preferences: preferences, menu: menu) == nil ? combo?.menuKeyEquivalent : nil
            return (shortcut.action, equivalent)
        })
        func update(_ menu: NSMenu) {
            for item in menu.items {
                if let action = item.action, let value = equivalents[action] {
                    item.keyEquivalent = value?.key ?? ""
                    item.keyEquivalentModifierMask = value?.modifiers ?? []
                    item.representedObject = value == nil ? nil : allCases.first { $0.action == action }?.combo(in: preferences)
                }
                if let submenu = item.submenu { update(submenu) }
            }
        }
        update(menu)
    }
}
