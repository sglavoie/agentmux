import Foundation

/// The one way the app says how long ago something happened, so a menu, a
/// sidebar, a review window, and the TUI never disagree about the same
/// moment. The units are `tui/layout`'s (age.go): whole minutes, hours, and
/// days, one unit at a time ("4m", "2h", "3d"); a clock skew that puts the
/// moment in the future reads as no time at all.
enum AgeFormatter {
    enum Style {
        /// `now`, `4m`, `2h`, `3d`: for rows and fields with little room,
        /// as the TUI's `layout.CompactAge`.
        case compact
        /// `just now`, `4m ago`, `2h ago`, `3d ago`: for text read as a
        /// phrase, in the units of the TUI's `layout.When`.
        case sentence
    }

    /// The age of `date` at `now`. With `seconds`, an age under a minute is
    /// counted (`12s`, `12s ago`, as the TUI's `layout.Ago`) instead of
    /// rounded away, for a label that ticks while it is watched.
    static func string(since date: Date, now: Date, style: Style, seconds: Bool = false) -> String {
        let elapsed = max(0, Int(now.timeIntervalSince(date)))
        let amount: String?
        switch elapsed {
        case ..<60: amount = seconds ? "\(elapsed)s" : nil
        case ..<3600: amount = "\(elapsed / 60)m"
        case ..<86400: amount = "\(elapsed / 3600)h"
        default: amount = "\(elapsed / 86400)d"
        }
        switch style {
        case .compact: return amount ?? "now"
        case .sentence: return amount.map { "\($0) ago" } ?? "just now"
        }
    }
}
