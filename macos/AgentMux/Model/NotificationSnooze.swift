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
    case untilMorning
    case untilResumed
    case resume

    /// The morning snooze ends at this hour, local time.
    static let morningHour = 8
    /// Why Resume Now is greyed out, in the menu and in Preferences.
    static let notSnoozed = "Notifications are not snoozed"

    /// The menu title as of `now`: the morning snooze names its end in the
    /// operator's time format, and says Tomorrow only when it is tomorrow.
    func title(now: Date, calendar: Calendar = .current, time: (Date) -> String = SnoozeChoice.localTime) -> String {
        switch self {
        case .oneHour: return "Snooze for 1 Hour"
        case .untilMorning:
            let end = Self.nextMorning(after: now, calendar: calendar)
            let tomorrow = !calendar.isDate(end, inSameDayAs: now)
            return "Snooze Until \(tomorrow ? "Tomorrow " : "")\(time(end))"
        case .untilResumed: return "Snooze Until Resumed"
        case .resume: return "Resume Now"
        }
    }

    func request(now: Date, calendar: Calendar = .current) -> SnoozeRequest {
        switch self {
        case .oneHour:
            return .ending(at: now.addingTimeInterval(3600))
        case .untilMorning:
            return .ending(at: Self.nextMorning(after: now, calendar: calendar))
        case .untilResumed:
            return .untilResumed
        case .resume:
            return .resume
        }
    }

    /// The next 08:00 after `now`: today's when it is still to come,
    /// otherwise tomorrow's.
    static func nextMorning(after now: Date, calendar: Calendar = .current) -> Date {
        let today = calendar.startOfDay(for: now)
        let morning = calendar.date(bySettingHour: morningHour, minute: 0, second: 0, of: today)
            ?? today.addingTimeInterval(TimeInterval(morningHour * 3600))
        if morning > now { return morning }
        return calendar.date(byAdding: .day, value: 1, to: morning) ?? morning.addingTimeInterval(86400)
    }

    /// A time of day alone, as the operator's locale writes it (`08:00`,
    /// `8:00 AM`).
    static func localTime(_ date: Date) -> String {
        DateFormatter.localizedString(from: date, dateStyle: .none, timeStyle: .short)
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
    /// The defaults key set while a Resume Now pressed with no live feed is
    /// still owed to the daemon: clearing the stored snooze alone would let
    /// the next snapshot from the same daemon adopt it again.
    static let resumeQueuedKey = "amxSnoozeResumeQueued"

    enum Decision: Equatable {
        /// The stored snooze already matches the daemon's.
        case keep
        /// Store this (nil for none): the daemon's state is the truth.
        case adopt(Date?)
        /// A new daemon came up without the snooze the app still holds; ask
        /// it to snooze again.
        case reapply(SnoozeRequest)
        /// The operator asked to resume while the feed was down and the
        /// daemon still snoozes; ask it to resume.
        case resume
    }

    static func identity(of daemon: CompanionDaemon) -> String {
        "\(daemon.pid):\(daemon.startedAt)"
    }

    static func reconcile(daemon: String, live: SnoozeState, stored: Date?, storedDaemon: String?,
                          resumeQueued: Bool = false, now: Date) -> Decision {
        let current = live.mirrored(now: now)
        // Whichever daemon this is, a queued resume wins over its snooze.
        if resumeQueued, current != nil { return .resume }
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
