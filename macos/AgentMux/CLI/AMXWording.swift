import Foundation

/// What every surface says when there is no amx to run, in two cases kept
/// apart: amx is still being found (at launch, or while a resolve runs), or
/// it was looked for and not found, which Settings › Advanced fixes.
enum AMXWording {
    /// A resolve is under way: waiting is the fix.
    static let resolving = "amx is still being found; try again in a moment."
    /// No amx was found: Settings › Advanced names one.
    static let notFound = "amx was not found. Choose it in Settings › Advanced."
    /// The override Settings names cannot be run.
    static let invalidOverride = "The amx binary set in Settings › Advanced is not an executable."
    /// The title of the button that opens Settings on the amx binary.
    static let openSettings = "Open Settings"

    /// Why amx cannot run now: the resolver's own words once it gave up
    /// (`notFound`), else that it is still being found.
    static func unavailable(notFound: String?) -> String {
        notFound ?? resolving
    }

    /// The app's current answer, for the review windows, which hold only a
    /// command closure. Set by the app whenever it publishes a resolve.
    @MainActor static var notFoundProblem: String?

    @MainActor static var current: String {
        unavailable(notFound: notFoundProblem)
    }

    /// Whether `message` says that amx was not found, so an alert showing it
    /// offers `openSettings`. Still being found is not: waiting fixes that.
    static func isNotFound(_ message: String) -> Bool {
        message.contains(notFound) || message.contains(invalidOverride)
    }
}
