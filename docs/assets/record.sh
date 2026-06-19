#!/usr/bin/env bash
# Record the gh-orbit README GIFs by driving the REAL binary in a real tmux PTY,
# capturing with asciinema (-c so no shell prompt leaks), rendering with agg.
# This replaces the old VHS .tape pipeline — the cast is a true terminal session,
# so colors, glyphs, and the spinner match what you actually see.
#
#   usage: docs/assets/record.sh <scene>
#   scene ∈ demo | commit-graph | diff-review | pull-requests | worktrees | branches
#
# Requires: tmux, asciinema, agg  (brew install asciinema agg)
# Build first:  go build -o /tmp/orbit-demo ./cmd/orbit
#
# Demo environment (the scenes assume it — set it up before recording):
#   a few feat/* worktrees off develop, ≥2 with open PRs (CI green → ✓ badges),
#   and one worktree left dirty (uncommitted edits → the ●N marker + a populated
#   Local Changes pane). The navigation counts below are tuned to that graph; if
#   your history differs, adjust the Down/Tab counts per scene and re-verify by
#   eye (ffmpeg -i out.gif -vf fps=1,tile=4x6 montage.png).
set -euo pipefail

SCENE="${1:?scene name required (demo|commit-graph|diff-review|pull-requests|worktrees|branches)}"
BIN="${ORBIT_BIN:-/tmp/orbit-demo}"
REPO="$(git -C "$(dirname "$0")" rev-parse --show-toplevel)"
OUT="${OUT_DIR:-$REPO/docs/assets}"
CAST="$(mktemp -t "orbit-$SCENE-XXXX").cast"
S="rec_$SCENE"

# terminal geometry per scene (cols x rows). demo is the wide hero shot.
COLS=128; ROWS=32
[ "$SCENE" = demo ] && { COLS=140; ROWS=36; }

# cwd per scene: diff-review records from the dirty worktree so its Local Changes
# pane is populated; the rest run from the main repo.
CWD="$REPO"
[ "$SCENE" = diff-review ] && CWD="$(dirname "$REPO")/gh-orbit-pr-bulk-merge"

tmux kill-session -t "$S" 2>/dev/null || true
tmux new-session -d -s "$S" -x "$COLS" -y "$ROWS" -c "$CWD"
# Force truecolor so lipgloss emits full color into the cast.
tmux send-keys -t "$S" "export TERM=xterm-256color COLORTERM=truecolor; clear; asciinema rec --overwrite -c '$BIN' '$CAST'" Enter
sleep 3.0   # asciinema startup + binary boot (git log + gh pr list)

sk(){ tmux send-keys -t "$S" "$@"; }
nap(){ sleep "$1"; }
rep(){ local k="$1" n="$2" d="$3"; for ((i=0;i<n;i++)); do sk "$k"; nap "$d"; done; }

case "$SCENE" in
  commit-graph)
    nap 2.0
    rep Down 4 0.5
    nap 1.0
    sk -l G; nap 1.3
    sk -l g; nap 1.3
    ;;
  diff-review)
    # Jump to top (deterministic: worktree HEAD ≠ row 0), then down to a Go-only
    # multi-file commit so the patch opens on Go code — syntax + word-level highlight.
    nap 1.2
    sk -l g; nap 0.8
    rep Down 5 0.32
    nap 0.8
    sk Right; nap 2.6          # open patch on a .go file
    sk -l "]"; nap 1.3         # next hunk
    sk -l "}"; nap 1.6         # next file (more Go)
    sk -l "]"; nap 1.3         # hunk in the new file
    sk -l "}"; nap 1.5         # next file
    sk Left;  nap 1.0          # close patch
    sk Tab;   nap 0.6          # → Worktree
    sk Tab;   nap 1.6          # → Local Changes (dirty worktree)
    sk Right; nap 2.4          # into the diff pane
    ;;
  pull-requests)
    nap 1.5
    sk Tab; nap 0.5
    sk Tab; nap 0.5
    sk Tab; nap 2.0
    sk Down; nap 1.0
    sk -l m; nap 2.4
    sk Escape; nap 1.0
    ;;
  worktrees)
    nap 1.5
    sk Tab; nap 2.4
    rep Down 3 0.65
    nap 1.3
    sk -l s; nap 1.6
    ;;
  branches)
    nap 1.5
    sk -l b; nap 2.4
    rep Down 2 0.5
    nap 1.3
    sk Escape; nap 1.2
    ;;
  demo)
    # Hero loop: graph → worktree dashboard → switch into the dirty worktree → its
    # Local Changes diff → Pull Requests tab → merge confirm. Space switches to the
    # graph view, so after it the tab cycle restarts at graph(0): Local Changes is
    # Tab×2, Pull Requests Tab×3.
    nap 1.8
    sk Tab;    nap 2.6        # → Worktree dashboard (cards, #N✓, ●N dirty)
    sk Down;   nap 0.7
    sk Down;   nap 0.7        # cursor → the dirty worktree
    sk Space;  nap 2.0        # switch the whole UI into it (→ graph)
    sk Tab;    nap 0.8        # → Worktree
    sk Tab;    nap 1.8        # → Local Changes (dirty)
    sk Right;  nap 2.4        # into the diff pane — working-tree diff
    sk Left;   nap 0.9        # back to the tree
    sk Tab;    nap 2.4        # → Pull Requests tab
    sk Down;   nap 0.9        # cursor onto a PR
    sk -l m;   nap 2.2        # merge confirm dialog
    sk Escape; nap 1.0        # cancel
    ;;
  *) echo "unknown scene: $SCENE" >&2; exit 1 ;;
esac

# quit (twice) — bubbletea reads ^C as a byte in raw mode, requires double.
sk C-c; sk C-c
sleep 2.0
tmux kill-session -t "$S" 2>/dev/null || true

agg --theme github-dark --font-size 16 --idle-time-limit 2.5 "$CAST" "$OUT/$SCENE.gif"
rm -f "$CAST"
echo "DONE $SCENE -> $OUT/$SCENE.gif ($(du -h "$OUT/$SCENE.gif" | cut -f1))"
