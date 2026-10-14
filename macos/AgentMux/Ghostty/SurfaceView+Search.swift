import AppKit
import GhosttyKit

// What a terminal that holds its own history reports about it: a search
// through it, where the viewport sits in it, and a program's progress
// (OSC 9;4). A session's terminal only views the tmux server, whose history
// and search are the server's, so it reports none of the first two.
extension Ghostty {
    /// One step of a search through a terminal's buffer, as Ghostty reports
    /// it.
    enum SearchUpdate: Equatable {
        /// A search began, or was asked for again (`start_search`); the
        /// needle is empty unless it began from a selection.
        case started(needle: String)
        /// The search ended (`end_search`).
        case ended
        /// How many matches there are; nil while still counting.
        case total(Int?)
        /// Which match is selected, from zero; nil when none is.
        case selected(Int?)
    }

    /// A search in progress through a terminal's buffer.
    struct Search: Equatable {
        var needle = ""
        var total: Int?
        var selected: Int?

        /// The search after `update`: nil once it ended. Counts arriving
        /// with no search open start none.
        static func applying(_ update: SearchUpdate, to search: Search?) -> Search? {
            switch update {
            case let .started(needle):
                var search = search ?? Search()
                if !needle.isEmpty { search.needle = needle }
                return search
            case .ended:
                return nil
            case let .total(total):
                guard var search else { return nil }
                search.total = total
                return search
            case let .selected(selected):
                guard var search else { return nil }
                search.selected = selected
                return search
            }
        }
    }

    /// Where the viewport sits in a terminal's buffer, in rows (`scrollbar`
    /// action): `offset` is its first row counted from the top of history,
    /// `len` how many it shows, `total` every row there is.
    struct Scrollbar: Equatable {
        var total: UInt64
        var offset: UInt64
        var len: UInt64

        /// Whether there is anything above or below the viewport.
        var scrolls: Bool { total > len }

        /// The share of the track the knob covers, 0 to 1.
        var knobProportion: Double {
            guard total > 0 else { return 1 }
            return min(1, Double(len) / Double(total))
        }

        /// Where the knob sits: 0 at the top of history, 1 at the bottom.
        var position: Double {
            guard scrolls else { return 1 }
            return min(1, Double(offset) / Double(total - len))
        }

        /// The first row to show for a knob at `position` (0 to 1), what
        /// `scroll_to_row` takes.
        func row(at position: Double) -> UInt64 {
            guard scrolls else { return 0 }
            let clamped = min(1, max(0, position))
            return UInt64((clamped * Double(total - len)).rounded())
        }
    }

    /// What a program reports of its progress (OSC 9;4).
    struct Progress: Equatable {
        enum State: Equatable {
            case normal, error, indeterminate, paused
        }

        var state: State
        /// Percent done, 0 to 100; nil when none was given.
        var percent: Int?

        /// Upstream drops a report not renewed within this long, so a
        /// program that died mid-task leaves no stale bar behind.
        static let expiry: TimeInterval = 15

        /// The report Ghostty passed, or nil when it removes the last one.
        init?(_ report: ghostty_action_progress_report_s) {
            switch report.state {
            case GHOSTTY_PROGRESS_STATE_SET: state = .normal
            case GHOSTTY_PROGRESS_STATE_ERROR: state = .error
            case GHOSTTY_PROGRESS_STATE_INDETERMINATE: state = .indeterminate
            case GHOSTTY_PROGRESS_STATE_PAUSE: state = .paused
            default: return nil
            }
            percent = report.progress < 0 ? nil : min(100, Int(report.progress))
        }

        init(state: State, percent: Int?) {
            self.state = state
            self.percent = percent
        }
    }
}

extension Ghostty.SurfaceView {
    /// Sends Ghostty the needle to search for (`search:`), which searches
    /// for that text literally; an empty one clears the matches.
    @discardableResult
    func search(for needle: String) -> Bool {
        performBindingAction("search:\(needle)")
    }
}
