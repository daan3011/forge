# The README GIF: forge on fictional data (`forge demo app`).
# Record with `make gif` (needs charmbracelet/vhs); it writes docs/forge.gif.

Output docs/forge.gif

Require forge

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 680
Set Padding 16
Set Framerate 20
Set PlaybackSpeed 1.0
Set TypingSpeed 60ms
Set Theme { "name": "forge", "background": "#1c1b1a", "foreground": "#e8e2d8", "cursor": "#ff7a1a", "selection": "#3a3633", "black": "#1c1b1a", "brightBlack": "#6b7078", "red": "#e5484d", "brightRed": "#ff6b6b", "green": "#7fb069", "brightGreen": "#9fd089", "yellow": "#ffd23f", "brightYellow": "#ffe27a", "blue": "#5fa8d3", "brightBlue": "#8cc4e6", "magenta": "#c678dd", "brightMagenta": "#d79be8", "cyan": "#56b6c2", "brightCyan": "#7fd3dd", "white": "#e8e2d8", "brightWhite": "#fff6e0" }

Hide
Type "clear && forge demo app"
Enter
Show

# The splash, then Home: what needs you.
Sleep 4.5s

# PRs: the list, then the diff of the failing PR.
Type "2"
Sleep 2s
Type "v"
Sleep 2.5s
Escape
Sleep 500ms

# Pipelines: the failed run's log.
Type "3"
Sleep 1s
Down
Sleep 1s
Type "l"
Sleep 2.5s
Escape
Sleep 500ms

# Plane: the cycle board.
Type "4"
Sleep 1.2s
Type "b"
Sleep 2.5s
Type "b"

# Sessions: the agents and a transcript.
Type "5"
Sleep 2s
Type "t"
Sleep 2.5s
Escape
Sleep 500ms

# Docs.
Type "6"
Sleep 800ms
Type "jjj"
Sleep 2s

# The palette.
Ctrl+K
Sleep 600ms
Type "doctor"
Sleep 1s
Enter
Sleep 2.5s
Escape
Sleep 500ms

Type "1"
Sleep 1s
Type "q"
Sleep 300ms
