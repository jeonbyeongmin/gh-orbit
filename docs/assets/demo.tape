# VHS tape for the gh-orbit hero demo.
# Regenerate:  vhs docs/assets/demo.tape   (from the repo root)
# Requires:    vhs + ttyd + ffmpeg, and a build at /tmp/orbit-demo
#              (go build -o /tmp/orbit-demo ./cmd/orbit)
# Needs the demo worktree setup: several feat/* worktrees off develop,
# at least two with open PRs and one or two dirty — see the README PR.

Output docs/assets/demo.gif

Set Shell "bash"
Set FontSize 15
Set Width 1320
Set Height 760
Set Padding 18
Set Framerate 24

# Boot straight into the cockpit (hide the launch line).
Hide
Type "cd /Users/jeonbyeongmin/project/gh-orbit && /tmp/orbit-demo"
Enter
Sleep 2.5s
Show

# Commit graph settles — branch chips carry #N PR badges + CI glyphs.
Sleep 1.8s

# Tab to the worktree dashboard: every tree's PR / CI / dirty at once.
Tab
Sleep 2.4s

# Drop onto a worktree and switch the whole UI into it.
Down
Sleep 0.9s
Space
Sleep 1.8s

# Read that tree's change in the full-screen patch overlay, then back out.
Right
Sleep 2.2s
Left
Sleep 1.1s

# Back to the dashboard, hop to another worktree.
Tab
Sleep 1.5s
Down
Sleep 0.7s
Down
Sleep 0.9s
Space
Sleep 1.8s

# Its working-tree changes, live, via the Local Changes page (tab cycle).
Tab
Sleep 0.9s
Tab
Sleep 1.6s
Right
Sleep 2.2s

# Quit (twice).
Ctrl+C
Ctrl+C
Sleep 1.2s
