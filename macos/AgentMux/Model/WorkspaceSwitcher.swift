import Foundation

/// The workspace's Go to… list as data: every place the sidebar lists, and
/// which of them a typed query keeps.
///
/// Ordinary queries name open places. A leading > explicitly switches to
/// actions, whose availability is supplied by the application.
enum WorkspaceSwitcher {
    /// One place to go to.
    struct Entry: Equatable {
        enum Place: Equatable {
            case session(AttentionCycler.Key)
            /// A utility view, by `WorkspaceUtilityView.id`.
            case view(String)
            case command(WorkspaceCommand)
        }

        let place: Place
        let glyph: String
        let title: String
        let detail: String
        /// Everything a query is matched against, folded (`fold`).
        let text: String
        var unavailableReason: String?
        var relevance = 0

        var isAction: Bool {
            if case .command = place { return true }
            return false
        }

        init(place: Place, glyph: String, title: String, detail: String, also: [String] = [], unavailableReason: String? = nil) {
            self.place = place
            self.unavailableReason = unavailableReason
            self.glyph = glyph
            self.title = title
            self.detail = detail
            let aliases: [String]
            if case let .command(command) = place { aliases = command.searchAliases } else { aliases = [] }
            text = WorkspaceSwitcher.fold(([title, detail] + also + aliases).joined(separator: " "))
        }
    }

    /// Case and accents do not matter: "e" finds "é", "release" "Release".
    static func fold(_ text: String) -> String {
        text.folding(options: [.caseInsensitive, .diacriticInsensitive, .widthInsensitive], locale: nil)
    }

    /// The sessions in sidebar order, then the views, except that the place
    /// the pane was on before this one comes first: ⌘P then ↩ goes back, as
    /// ⌘L does. An overlay view — the key reference, a picker — is passed
    /// through, not gone to.
    /// A view about a session says which, by the name in `names` (keyed by
    /// runtime session) or of its listed row.
    static func entries(rows: [WorkspaceRow], views: [WorkspaceUtilityView], names: [String: String] = [:],
                        previous: Entry.Place? = nil) -> [Entry] {
        var names = names
        for row in rows {
            if let id = row.embeddedRuntimeSessionID { names[id] = row.displayName }
        }
        let sessions = rows.map { row -> Entry in
            let place = "\(row.project) — \(row.label)"
            return Entry(
                place: .session(row.key),
                glyph: row.glyph,
                title: row.displayName,
                detail: row.name.isEmpty ? row.detail : [place, row.detail].filter { !$0.isEmpty }.joined(separator: " · "),
                also: [row.project, row.label, row.harness]
            )
        }
        let others = views.filter { !$0.isOverlay }.map { view in
            Entry(place: .view(view.id), glyph: view.glyph, title: view.displayName,
                  detail: view.detail(session: view.aboutRuntimeSessionID.flatMap { names[$0] }))
        }
        var all = sessions + others
        if let previous, let index = all.firstIndex(where: { $0.place == previous }) {
            all.insert(all.remove(at: index), at: 0)
        }
        return all
    }

    /// Exact names win. Available contextual actions follow, with literal
    /// matches ahead of abbreviations and stable input order breaking ties.
    static func matches(_ query: String, in entries: [Entry]) -> [Entry] {
        let query = fold(query).trimmingCharacters(in: .whitespacesAndNewlines)
        let actions = query.hasPrefix(">")
        let wanted = (actions ? String(query.dropFirst()) : query).trimmingCharacters(in: .whitespacesAndNewlines)
        let words = wanted.split(whereSeparator: { $0.isWhitespace }).map(String.init)
        return entries.enumerated().compactMap { index, entry -> (Entry, [Int])? in
            guard entry.isAction == actions else { return nil }
            let title = fold(entry.title)
            guard words.allSatisfy({ entry.text.contains($0) || !highlightRanges($0, in: entry.title).isEmpty }) else { return nil }
            let quality = title == wanted ? 0 : title.hasPrefix(wanted) ? 1 : title.contains(wanted) ? 2 : words.allSatisfy({ entry.text.contains($0) }) ? 3 : 4
            return (entry, [wanted.isEmpty ? 1 : quality == 0 ? 0 : 1,
                           entry.unavailableReason == nil ? 0 : 1,
                           -entry.relevance, quality, index])
        }.sorted { $0.1.lexicographicallyPrecedes($1.1) }.map { $0.0 }
    }

    /// Ranges refer to the original string, so accent folding and emoji never
    /// shift the highlight. Abbreviations may skip characters in a title.
    static func highlightRanges(_ query: String, in text: String) -> [NSRange] {
        let query = query.trimmingCharacters(in: .whitespacesAndNewlines)
        let wanted = query.hasPrefix(">") ? String(query.dropFirst()) : query
        var ranges: [NSRange] = []
        for word in wanted.split(whereSeparator: { $0.isWhitespace }) {
            if let range = text.range(of: String(word), options: [.caseInsensitive, .diacriticInsensitive, .widthInsensitive]) {
                ranges.append(NSRange(range, in: text))
                continue
            }
            let needle = Array(fold(String(word)))
            var next = 0
            var matched: [NSRange] = []
            for index in text.indices where next < needle.count {
                let end = text.index(after: index)
                if fold(String(text[index..<end])).contains(needle[next]) {
                    matched.append(NSRange(index..<end, in: text)); next += 1
                }
            }
            if next == needle.count { ranges += matched }
        }
        return ranges
    }

}

/// Typed actions keep user-provided project and conversation identities out of
/// shell command strings. Execution rechecks availability in the app delegate.
enum WorkspaceCommand: Equatable {
    case splitSession(AttentionCycler.Key)
    case splitShell, splitChanges, focusReview, focusOtherPane, swapPanes, collapsePane
    case launch(ProjectEntry)
    case resume(RecentConversation)
    case runs, runPlan, activity
    case newTerminal, newShell, dashboard, overview, pastSessions, nextWaiting, settings, history, changes
}


extension WorkspaceCommand {
    var searchAliases: [String] {
        switch self {
        case .changes, .splitChanges, .focusReview: return ["diff", "review", "git"]
        case .settings: return ["preferences", "configuration"]
        case .newShell, .newTerminal, .splitShell: return ["terminal", "shell", "console"]
        case .history: return ["transcript", "conversation", "search"]
        case .pastSessions: return ["history", "resume", "continue", "fork"]
        case .activity: return ["unread", "activity", "recent"]
        case .nextWaiting: return ["attention", "inbox", "question", "permission"]
        default: return []
        }
    }
}
