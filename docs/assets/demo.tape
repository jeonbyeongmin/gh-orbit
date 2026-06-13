# VHS tape for the gh-orbit hero demo.
# Regenerate:  vhs docs/assets/demo.tape   (from the repo root)
# Requires:    vhs + ttyd + ffmpeg, and a build at /tmp/orbit-demo
#              (go build -o /tmp/orbit-demo ./cmd/orbit)

Output docs/assets/demo.gif

Set Shell "bash"
Set FontSize 15
Set Width 1320
Set Height 760
Set Padding 18
Set Framerate 24
Set PlaybackSpeed 1.0

# Boot straight into the cockpit (hide the launch line).
Hide
Type "cd /Users/jeonbyeongmin/project/gh-orbit && /tmp/orbit-demo"
Enter
Sleep 2.5s
Show

# Let the commit graph settle.
Sleep 1.6s

# Walk the commit graph.
Down@420ms 2
Sleep 1.2s

# Open the focused commit's full-screen patch overlay.
Type "d"
Sleep 1.8s

# Jump file-to-file inside the patch ( [ / ] ).
Type "]"
Sleep 1.3s
Type "]"
Sleep 1.3s
Type "]"
Sleep 1.3s
Down@200ms 6
Sleep 1.2s
Type "["
Sleep 1.3s
Escape
Sleep 1.2s

# Worktrees modal — switch context without a second terminal.
Type "w"
Sleep 2s
Down@450ms 2
Sleep 1.4s
Escape
Sleep 1s

# Branches modal.
Type "b"
Sleep 2s
Down@450ms 2
Sleep 1.4s
Escape
Sleep 1s

# Inline help reference panel.
Type "?"
Sleep 2.4s
Type "?"
Sleep 1s

# Quit (twice).
Ctrl+C
Ctrl+C
Sleep 1.2s
