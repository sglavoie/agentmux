import Foundation

/// `protocol.SnoozeRequest`: sets or clears the global snooze. `until` is a
/// Unix second, read only for `until`; an empty mode only reads the snooze.
struct SnoozeRequest: Codable, Equatable {
    static let until = "until"
    static let indefinite = "indefinite"
    static let clear = "clear"

    var mode: String
    var until: Int64?

    static let read = SnoozeRequest(mode: "")
    static let resume = SnoozeRequest(mode: clear)
    static let untilResumed = SnoozeRequest(mode: indefinite)

    static func ending(at date: Date) -> SnoozeRequest {
        SnoozeRequest(mode: until, until: Int64(date.timeIntervalSince1970.rounded(.up)))
    }
}

/// `protocol.SnoozeState`, and the snooze half of `CompanionNotification`:
/// `snoozedUntil` is a Unix second (zero for none), and `indefinite` outranks
/// it.
struct SnoozeState: Decodable, Equatable {
    var snoozedUntil: Int64 = 0
    var indefinite = false

    init(snoozedUntil: Int64 = 0, indefinite: Bool = false) {
        self.snoozedUntil = snoozedUntil
        self.indefinite = indefinite
    }

    init(_ notification: CompanionNotification) {
        self.init(snoozedUntil: notification.snoozedUntil, indefinite: notification.indefinite)
    }

    enum CodingKeys: String, CodingKey {
        case indefinite
        case snoozedUntil = "snoozed_until"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        snoozedUntil = c.lenient(.snoozedUntil, 0)
        indefinite = c.lenient(.indefinite, false)
    }

    /// The stored form of `amxSnoozeUntil`: the end of a timed snooze,
    /// `distantFuture` for one held until resumed, nil for none or one that
    /// has already run out.
    func mirrored(now: Date) -> Date? {
        if indefinite { return .distantFuture }
        guard snoozedUntil > 0 else { return nil }
        let end = Date(timeIntervalSince1970: TimeInterval(snoozedUntil))
        return end > now ? end : nil
    }

    func isSnoozed(now: Date) -> Bool { mirrored(now: now) != nil }

    /// `Snoozed until 14:05`, `Snoozed until resumed`, `Not snoozed`.
    func description(now: Date, time: (Date) -> String = SnoozeState.shortTime) -> String {
        if indefinite { return "Snoozed until resumed" }
        guard let end = mirrored(now: now) else { return "Not snoozed" }
        return "Snoozed until \(time(end))"
    }

    static func shortTime(_ date: Date) -> String {
        let calendar = Calendar.current
        let style: DateFormatter.Style = calendar.isDateInToday(date) ? .none : .short
        return DateFormatter.localizedString(from: date, dateStyle: style, timeStyle: .short)
    }
}

/// What the Notifications submenu offers.
enum SnoozeChoice: CaseIterable, Equatable {
    case oneHour
    case untilTomorrowMorning
    case untilResumed
    case resume

    /// Tomorrow's snooze ends at this hour, local time.
    static let morningHour = 8

    var title: String {
        switch self {
        case .oneHour: return "Snooze for 1 Hour"
        case .untilTomorrowMorning: return "Snooze Until Tomorrow 08:00"
        case .untilResumed: return "Snooze Until Resumed"
        case .resume: return "Resume Now"
        }
    }

    func request(now: Date, calendar: Calendar = .current) -> SnoozeRequest {
        switch self {
        case .oneHour:
            return .ending(at: now.addingTimeInterval(3600))
        case .untilTomorrowMorning:
            return .ending(at: Self.tomorrowMorning(after: now, calendar: calendar))
        case .untilResumed:
            return .untilResumed
        case .resume:
            return .resume
        }
    }

    /// 08:00 on the calendar day after `now`'s.
    static func tomorrowMorning(after now: Date, calendar: Calendar = .current) -> Date {
        let today = calendar.startOfDay(for: now)
        let tomorrow = calendar.date(byAdding: .day, value: 1, to: today) ?? today.addingTimeInterval(86400)
        return calendar.date(bySettingHour: morningHour, minute: 0, second: 0, of: tomorrow)
            ?? tomorrow.addingTimeInterval(TimeInterval(morningHour * 3600))
    }
}

/// Keeps the daemon's in-memory snooze and the app's `amxSnoozeUntil` in step.
///
/// The daemon owns the snooze and forgets it when it restarts; the app
/// remembers the last snooze it saw, and which daemon held it, so it can tell
/// a restart that dropped the snooze from an operator who cleared it with
/// `amx notifications snooze off`. Only the first is undone.
enum SnoozeMirror {
    /// The defaults key naming the daemon (`pid:started_at`) the stored
    /// snooze was last reconciled with.
    static let daemonKey = "amxSnoozeDaemon"

    enum Decision: Equatable {
        /// The stored snooze already matches the daemon's.
        case keep
        /// Store this (nil for none): the daemon's state is the truth.
        case adopt(Date?)
        /// A new daemon came up without the snooze the app still holds; ask
        /// it to snooze again.
        case reapply(SnoozeRequest)
    }

    static func identity(of daemon: CompanionDaemon) -> String {
        "\(daemon.pid):\(daemon.startedAt)"
    }

    static func reconcile(daemon: String, live: SnoozeState, stored: Date?, storedDaemon: String?, now: Date) -> Decision {
        let current = live.mirrored(now: now)
        let remembered = stored.flatMap { $0 > now ? $0 : nil }
        if storedDaemon != daemon, current == nil, let remembered {
            return .reapply(remembered == .distantFuture ? .untilResumed : .ending(at: remembered))
        }
        return same(current, stored) ? .keep : .adopt(current)
    }

    /// Equal to the second, the precision the daemon keeps.
    static func same(_ lhs: Date?, _ rhs: Date?) -> Bool {
        switch (lhs, rhs) {
        case (nil, nil): return true
        case let (l?, r?): return Int64(l.timeIntervalSince1970.rounded(.up)) == Int64(r.timeIntervalSince1970.rounded(.up))
        default: return false
        }
    }
}
