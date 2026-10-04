import AppKit

// No storyboard or nib: the delegate is installed by hand, and NSApplication
// holds it weakly, so this reference keeps it alive for the process lifetime.
let delegate = AppDelegate()
NSApplication.shared.delegate = delegate
NSApplication.shared.run()
