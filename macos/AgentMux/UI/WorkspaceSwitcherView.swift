import AppKit

/// The Go to… list: a field to type in over the places that match, shown
/// above the workspace's pane while it is asked for.
///
/// It is the one thing in the workspace besides a terminal and the session
/// list that takes the keyboard, and only until a place is chosen or it is
/// dismissed. It lists and reports; the window controller goes there.
@MainActor
final class WorkspaceSwitcherView: NSBox, NSTextFieldDelegate, NSTableViewDataSource, NSTableViewDelegate {
    static let rowHeight: CGFloat = 36
    /// The most places shown without scrolling.
    static let visibleRows = 8

    nonisolated static let noMatches = "No matches"

    let field = NSTextField()
    let table = NSTableView()
    /// Said in place of the list when nothing matches what is typed.
    let noMatchLabel = NSTextField(labelWithString: WorkspaceSwitcherView.noMatches)
    /// The places that match what is typed, in the order they are listed.
    private(set) var shown: [WorkspaceSwitcher.Entry] = []
    private var entries: [WorkspaceSwitcher.Entry] = []
    private var listHeight: NSLayoutConstraint?

    var onChoose: ((WorkspaceSwitcher.Entry) -> Void)?
    var onCancel: (() -> Void)?
    /// The field gave up the keyboard while the list was up: a click went
    /// somewhere else.
    var onLeave: (() -> Void)?

    init() {
        super.init(frame: .zero)
        boxType = .custom
        titlePosition = .noTitle
        cornerRadius = 8
        borderWidth = 1
        borderColor = .separatorColor
        fillColor = .windowBackgroundColor
        contentViewMargins = .zero
        isHidden = true
        translatesAutoresizingMaskIntoConstraints = false

        field.placeholderString = "Go to a session or view, or > for actions"
        field.font = .systemFont(ofSize: 15)
        field.isBordered = false
        field.drawsBackground = false
        field.focusRingType = .none
        field.delegate = self
        field.setAccessibilityLabel("Go to")

        let column = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("place"))
        column.resizingMask = .autoresizingMask
        table.addTableColumn(column)
        table.headerView = nil
        table.style = .plain
        table.backgroundColor = .clear
        table.rowHeight = Self.rowHeight
        table.allowsEmptySelection = true
        table.allowsMultipleSelection = false
        // The field keeps the keyboard; a click here only chooses.
        table.refusesFirstResponder = true
        table.dataSource = self
        table.delegate = self
        table.target = self
        table.action = #selector(rowClicked(_:))
        table.setAccessibilityLabel("Places")

        let scroll = NSScrollView()
        scroll.documentView = table
        scroll.hasVerticalScroller = true
        scroll.drawsBackground = false
        // The list gives up height before the box leaves the pane.
        scroll.setContentCompressionResistancePriority(.defaultLow, for: .vertical)

        noMatchLabel.textColor = .secondaryLabelColor
        noMatchLabel.isHidden = true

        let stack = NSStackView(views: [field, noMatchLabel, scroll])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 6
        stack.edgeInsets = NSEdgeInsets(top: 10, left: 12, bottom: 8, right: 12)
        stack.translatesAutoresizingMaskIntoConstraints = false
        let content = NSView()
        content.addSubview(stack)
        contentView = content
        let height = scroll.heightAnchor.constraint(equalToConstant: 0)
        // Below the pane's hold on the box, so a short window shrinks the
        // list rather than pushing the box out of the pane.
        height.priority = .defaultHigh
        listHeight = height
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: content.topAnchor),
            stack.bottomAnchor.constraint(equalTo: content.bottomAnchor),
            stack.leadingAnchor.constraint(equalTo: content.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: content.trailingAnchor),
            field.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -24),
            scroll.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -24),
            height,
        ])
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) is not supported for this view")
    }

    /// Starts over on these places with nothing typed.
    func show(_ entries: [WorkspaceSwitcher.Entry]) {
        field.stringValue = ""
        self.entries = entries
        filter(keeping: nil)
    }

    /// Brings the list in line with the places there are now, keeping what
    /// is typed and, when it is still listed, the place that is selected.
    func update(_ entries: [WorkspaceSwitcher.Entry]) {
        guard entries != self.entries else { return }
        self.entries = entries
        filter(keeping: selected?.place)
    }

    /// Types a query, as the keyboard would.
    func enter(_ query: String) {
        field.stringValue = query
        typed()
    }

    var selected: WorkspaceSwitcher.Entry? {
        shown.indices.contains(table.selectedRow) ? shown[table.selectedRow] : nil
    }

    /// Moves the selection, stopping at either end. The field keeps the
    /// keyboard, so VoiceOver follows the field and not the list: the place
    /// moved to is said aloud.
    func move(_ offset: Int) {
        guard !shown.isEmpty else { return }
        select(min(max(table.selectedRow + offset, 0), shown.count - 1))
        if let selected { announce(selected) }
    }

    /// What VoiceOver says of a place: its title, then the line under it.
    nonisolated static func spoken(_ entry: WorkspaceSwitcher.Entry) -> String {
        [entry.title, entry.detail, entry.unavailableReason ?? ""].filter { !$0.isEmpty }.joined(separator: ", ")
    }

    /// What was last said, for tests.
    private(set) var lastAnnouncement: String?

    private func announce(_ entry: WorkspaceSwitcher.Entry) {
        announce(Self.spoken(entry), priority: .high)
    }

    private func announce(_ spoken: String, priority: NSAccessibilityPriorityLevel) {
        lastAnnouncement = spoken
        NSAccessibility.post(element: field, notification: .announcementRequested,
                             userInfo: [.announcement: spoken, .priority: priority.rawValue])
    }

    /// What VoiceOver says after a keystroke filters the list: how many
    /// places match, and the one Return goes to.
    nonisolated static func spokenMatches(_ count: Int, top: WorkspaceSwitcher.Entry?) -> String {
        guard count > 0, let top else { return noMatches }
        return "\(count == 1 ? "1 match" : "\(count) matches"), \(spoken(top))"
    }

    /// The query changed: the list is filtered and, since the field keeps
    /// the keyboard, what it now holds is said aloud at a priority that
    /// lets the typing echo first.
    private func typed() {
        filter(keeping: nil)
        announce(Self.spokenMatches(shown.count, top: selected), priority: .medium)
    }

    /// Goes to the selected place. With nothing matching there is none, and
    /// the list already says so.
    func choose() {
        guard let selected else { return }
        guard selected.unavailableReason == nil else {
            announce(selected.unavailableReason!, priority: .high)
            return
        }
        onChoose?(selected)
    }

    func cancel() { onCancel?() }

    private func filter(keeping place: WorkspaceSwitcher.Entry.Place?) {
        shown = WorkspaceSwitcher.matches(field.stringValue, in: entries)
        table.reloadData()
        listHeight?.constant = CGFloat(min(shown.count, Self.visibleRows)) * (Self.rowHeight + table.intercellSpacing.height)
        noMatchLabel.isHidden = !shown.isEmpty
        select(shown.firstIndex { $0.place == place } ?? 0)
    }

    private func select(_ index: Int) {
        guard shown.indices.contains(index) else { return table.deselectAll(nil) }
        table.selectRowIndexes([index], byExtendingSelection: false)
        table.scrollRowToVisible(index)
    }

    @objc private func rowClicked(_ sender: Any?) { click(row: table.clickedRow) }

    /// Goes to the place on the table's row `index`, as a click on it does.
    func click(row index: Int) {
        guard shown.indices.contains(index) else { return }
        guard shown[index].unavailableReason == nil else {
            announce(shown[index].unavailableReason!, priority: .high)
            return
        }
        onChoose?(shown[index])
    }

    // MARK: Field

    func controlTextDidChange(_ notification: Notification) {
        typed()
    }

    func controlTextDidEndEditing(_ notification: Notification) {
        if !isHidden { onLeave?() }
    }

    func control(_ control: NSControl, textView: NSTextView, doCommandBy selector: Selector) -> Bool {
        switch selector {
        case #selector(NSResponder.moveUp(_:)): move(-1)
        case #selector(NSResponder.moveDown(_:)): move(1)
        // Tab and Shift-Tab move in the list too, rather than taking the
        // keyboard out of the field and so putting the list away.
        case #selector(NSResponder.insertTab(_:)): move(1)
        case #selector(NSResponder.insertBacktab(_:)): move(-1)
        case #selector(NSResponder.insertNewline(_:)): choose()
        case #selector(NSResponder.cancelOperation(_:)): cancel()
        default: return false
        }
        return true
    }

    // MARK: Table

    func numberOfRows(in tableView: NSTableView) -> Int { shown.count }

    func tableView(_ tableView: NSTableView, rowViewForRow row: Int) -> NSTableRowView? {
        WorkspaceSwitcherRowView()
    }

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        let cell = tableView.makeView(withIdentifier: WorkspaceSwitcherCellView.identifier, owner: nil) as? WorkspaceSwitcherCellView
            ?? WorkspaceSwitcherCellView()
        cell.show(shown[row], query: field.stringValue)
        return cell
    }
}

/// A line of the Go to… list. The list never has the keyboard — the field
/// does — so its selection is drawn as a focused list's would be: it is what
/// Return goes to.
final class WorkspaceSwitcherRowView: NSTableRowView {
    override var isEmphasized: Bool {
        get { true }
        set {}
    }
}

/// One place in the Go to… list: its glyph, its title, and the line under it.
final class WorkspaceSwitcherCellView: NSTableCellView {
    static let identifier = NSUserInterfaceItemIdentifier("workspace.switcher")
    let glyphLabel = NSTextField(labelWithString: "")
    let titleLabel = NSTextField(labelWithString: "")
    let detailLabel = NSTextField(labelWithString: "")

    init() {
        super.init(frame: .zero)
        identifier = Self.identifier
        glyphLabel.alignment = .center
        titleLabel.lineBreakMode = .byTruncatingTail
        detailLabel.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        detailLabel.textColor = .secondaryLabelColor
        detailLabel.lineBreakMode = .byTruncatingTail
        textField = titleLabel
        let text = NSStackView(views: [titleLabel, detailLabel])
        text.orientation = .vertical
        text.alignment = .leading
        text.spacing = 1
        let stack = NSStackView(views: [glyphLabel, text])
        stack.orientation = .horizontal
        stack.spacing = 6
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        for label in [titleLabel, detailLabel] {
            label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        }
        NSLayoutConstraint.activate([
            glyphLabel.widthAnchor.constraint(equalToConstant: 20),
            stack.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 2),
            stack.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor, constant: -4),
            stack.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) is not supported for this view")
    }

    func show(_ entry: WorkspaceSwitcher.Entry, query: String = "") {
        glyphLabel.stringValue = entry.glyph
        let title = NSMutableAttributedString(string: entry.title)
        for range in WorkspaceSwitcher.highlightRanges(query, in: entry.title) {
            title.addAttributes([.font: NSFont.boldSystemFont(ofSize: NSFont.systemFontSize),
                                 .underlineStyle: NSUnderlineStyle.single.rawValue], range: range)
        }
        titleLabel.attributedStringValue = title
        detailLabel.stringValue = entry.unavailableReason ?? entry.detail
        detailLabel.isHidden = detailLabel.stringValue.isEmpty
        titleLabel.textColor = entry.unavailableReason == nil ? .labelColor : .secondaryLabelColor
        toolTip = entry.unavailableReason ?? entry.detail
        // The line is said as one, as the session list's are.
        setAccessibilityLabel(WorkspaceSwitcherView.spoken(entry))
    }
}
