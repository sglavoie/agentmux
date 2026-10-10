import AppKit

// No storyboard or nib: the delegate is installed by hand, and NSApplication
// holds it weakly, so this reference keeps it alive for the process lifetime.
// Top-level code runs on the main thread, which is what AppDelegate's main
// actor isolation asks for.
//
// libghostty is initialized here, once, before anything can ask for a terminal.
// A failure is logged and leaves the companion running without embedded
// terminals.
_ = Ghostty.initialized
let delegate = MainActor.assumeIsolated { AppDelegate() }
MainActor.assumeIsolated { NSApplication.shared.delegate = delegate }
NSApplication.shared.run()
