import Foundation

/// Finds Flight Log milestones unlocked since the app last looked.
struct WorkspaceMilestoneWatch {
    struct Milestone: Equatable { let id: String; let title: String }
    var defaults: UserDefaults = .standard

    /// Records every unlock, including unknown or ineligible ones, so changing
    /// settings later cannot defer a celebration into another check.
    mutating func newlyUnlocked(in report: FlightReport) -> [Milestone] {
        let unlocked = report.achievements.filter { $0.unlockedAt != nil }
        let current = Set(unlocked.map(\.id))
        let previous = defaults.stringArray(forKey: "amxSeenMilestones")
        let samePeriod = defaults.string(forKey: "amxSeenMilestonesSince") == report.trackingSince
        let seen = samePeriod ? Set(previous ?? []) : []
        defaults.set(seen.union(current).sorted(), forKey: "amxSeenMilestones")
        defaults.set(report.trackingSince, forKey: "amxSeenMilestonesSince")
        guard previous != nil, samePeriod, report.settings.enabled, report.settings.celebrations else { return [] }
        return unlocked.compactMap {
            guard !seen.contains($0.id), !$0.id.hasPrefix("mission:"), $0.celebrationEligible == true else { return nil }
            return Milestone(id: $0.id, title: $0.title)
        }
    }
}
