import AppKit

// No storyboard or nib: the delegate is installed by hand, and NSApplication
// holds it weakly, so this reference keeps it alive for the process lifetime.
// Top-level code runs on the main thread, which is what AppDelegate's main
// actor isolation asks for.
let delegate = MainActor.assumeIsolated { AppDelegate() }
MainActor.assumeIsolated { NSApplication.shared.delegate = delegate }
NSApplication.shared.run()
