# VHS tape for the gh-orbit hero demo.
# Regenerate:  vhs docs/assets/demo.tape   (from the repo root)
# Requires:    vhs + ttyd + ffmpeg, and a build at /tmp/orbit-demo
#              (go build -o /tmp/orbit-demo ./cmd/orbit)
# The "review a worktree's PR" beat needs a multi-worktree repo with at
# least one open PR — see the demo-worktree setup that backs this tape.

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

# Jump to a real code commit — a merge commit's `git show -p` renders an empty diff.
Type "g"
Sleep 0.6s
Down@360ms 4
Sleep 1.0s

# Open the focused commit's full-screen patch overlay (10 files), walk a couple.
Type "d"
Sleep 1.6s
Type "]"
Sleep 1.2s
Type "]"
Sleep 1.2s
Escape
Sleep 1.0s

# The wedge: a worktree dashboard — every branch's PR + CI + dirty at once.
Type "w"
Sleep 2.6s

# Move the cursor to the worktree that carries an open PR.
Down@500ms 1
Sleep 1.6s

# Review that worktree's PR without leaving the dashboard.
Type "O"
Sleep 2.4s
Type "]"
Sleep 1.4s
Type "]"
Sleep 1.4s

# Land it — pick a merge strategy (squash / merge / rebase), then back out.
Type "m"
Sleep 2.4s
Escape
Sleep 1.0s

# Back to the dashboard, then the graph.
Escape
Sleep 1.3s
Escape
Sleep 1.0s

# Quit (twice).
Ctrl+C
Ctrl+C
Sleep 1.2s
