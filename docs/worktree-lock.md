# worktree lock / unlock

(Draft — backing branch for dashboard lock support.)

`git worktree lock` / `unlock` keep a tree from being pruned or
removed by accident. The dashboard surfaces a lock glyph on the
card and gates `d` (remove) behind an explicit unlock step.

Policy still open: when to *recommend* a lock — long-lived trees,
detached HEAD, dirty-and-idle — versus leaving it to the user.
