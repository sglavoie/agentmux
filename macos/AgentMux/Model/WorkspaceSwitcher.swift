import Foundation

/// The workspace's Go to… list as data: every place the sidebar lists, and
/// which of them a typed query keeps.
///
/// It only names places that are already there. Choosing one does what a
/// click on its sidebar line does, so nothing is launched from here and a
/// view is no more a session here than it is in the sidebar.
enum WorkspaceSwitcher {
    /// One place to go to.
    struct Entry: Equatable {
        enum Place: Equatable {
            case session(AttentionCycler.Key)
            /// A utility view, by `WorkspaceUtilityView.id`.
            case view(String)
        }

        let place: Place
        let glyph: String
        let title: String
        let detail: String
        /// Everything a query is matched against, folded (`fold`).
        let text: String

        init(place: Place, glyph: String, title: String, detail: String, also: [String] = []) {
            self.place = place
            self.glyph = glyph
            self.title = title
            self.detail = detail
            text = WorkspaceSwitcher.fold(([title, detail] + also).joined(separator: " "))
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
            Entry(place: .view(view.id), glyph: view.glyph, title: view.title,
                  detail: view.detail(session: view.aboutRuntimeSessionID.flatMap { names[$0] }))
        }
        var all = sessions + others
        if let previous, let index = all.firstIndex(where: { $0.place == previous }) {
            all.insert(all.remove(at: index), at: 0)
        }
        return all
    }

    /// The entries that hold every word of the query, in the order given,
    /// with those whose title starts with the query first. An empty query
    /// keeps them all.
    static func matches(_ query: String, in entries: [Entry]) -> [Entry] {
        let wanted = fold(query).trimmingCharacters(in: .whitespaces)
        let words = wanted.split(separator: " ")
        guard !words.isEmpty else { return entries }
        let kept = entries.filter { entry in words.allSatisfy { entry.text.contains($0) } }
        let leading = kept.filter { fold($0.title).hasPrefix(wanted) }
        return leading + kept.filter { !leading.contains($0) }
    }
}
