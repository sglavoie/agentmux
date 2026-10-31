on pressNamed(axRef, wanted)
    tell application "System Events"
        try
            if role of axRef is "AXButton" or role of axRef is "AXMenuItem" then
                set matchesWanted to false
                try
                    set matchesWanted to title of axRef is wanted
                end try
                if not matchesWanted then
                    try
                        set matchesWanted to name of axRef is wanted
                    end try
                end if
                if not matchesWanted then
                    try
                        set matchesWanted to description of axRef is wanted
                    end try
                end if
                if matchesWanted then
                    click axRef
                    return true
                end if
            end if
        end try
        try
            set axChildren to UI elements of axRef
        on error
            return false
        end try
    end tell
    repeat with axChild in axChildren
        if my pressNamed(contents of axChild, wanted) then return true
    end repeat
    return false
end pressNamed

on run argv
    if (system attribute "AMX_UI_REMOTE_EXECUTION") is not "1" then error "Run this driver over SSH on the approved test Mac"
    set targetPID to (read POSIX file "/tmp/amx-firstsession-1030/app.pid") as integer
    tell application "System Events"
        set appRef to first application process whose unix id is targetPID
        if bundle identifier of appRef is not "io.github.sglavoie.agentmux.firstsession1030" then error "Not the isolated test app"
        set frontmost of appRef to true
        if not frontmost of appRef then error "Test app is not frontmost"
        set appWindow to window 1 of appRef
    end tell
    set verb to item 1 of argv
    if verb is "press" then
        if not my pressNamed(appWindow, item 2 of argv) then error "Button not found"
    else if verb is "return" then
        tell application "System Events" to key code 36
    else if verb is "type" then
        tell application "System Events" to keystroke (item 2 of argv)
    else if verb is "history" then
        tell application "System Events" to keystroke "y" using command down
    else if verb is "close" then
        tell application "System Events" to keystroke "w" using command down
    else if verb is "resume" then
        tell application "System Events"
            if title of appWindow is not "Conversation History" then error "Unexpected window"
            key code 36 using command down
        end tell
    else if verb is "inspect" then
        tell application "System Events" to return properties of every UI element of appWindow
    end if
end run
