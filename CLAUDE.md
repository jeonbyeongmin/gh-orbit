# CLAUDE.md

Behavioral contract for AI coding agents working in this repo.

`gh-orbit` is itself a review cockpit for AI-coding-agent work
(see [README.md](./README.md)), and the repo is developed with
Claude Code as the primary author. That means two things for any
agent reading this file:

1. The end user of this product is *another developer* reviewing an
   agent's diff. Bias every UX decision toward "can a reviewer figure
   out what just happened in three seconds?" — terse status lines,
   commit-row truncation order, modal hint lines, etc.
2. Your own diff in this repo will be reviewed in `gh-orbit` itself.
   Keep diffs surgical, commit messages legible, and don't leave
   half-finished scaffolding — those are the things the cockpit
   surfaces most loudly.

The four guidelines below bias toward caution over speed. For trivial
tasks, use judgment.

## 1. Think Before Coding

**Don't assume. Don't hide confusion. Surface tradeoffs.**

Before implementing:

- State your assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them - don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

## 2. Simplicity First

**Minimum code that solves the problem. Nothing speculative.**

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- If you write 200 lines and it could be 50, rewrite it.

Ask yourself: "Would a senior engineer say this is overcomplicated?"
If yes, simplify.

## 3. Surgical Changes

**Touch only what you must. Clean up only your own mess.**

When editing existing code:

- Don't "improve" adjacent code, comments, or formatting.
- Don't refactor things that aren't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it - don't delete it.

When your changes create orphans:

- Remove imports/variables/functions that YOUR changes made unused.
- Don't remove pre-existing dead code unless asked.

The test: Every changed line should trace directly to the user's
request.

## 4. Goal-Driven Execution

**Define success criteria. Loop until verified.**

Transform tasks into verifiable goals:

- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make it pass"
- "Refactor X" → "Ensure tests pass before and after"

For multi-step tasks, state a brief plan:

```
1. [Step] → verify: [check]
2. [Step] → verify: [check]
3. [Step] → verify: [check]
```

Strong success criteria let you loop independently. Weak criteria
("make it work") require constant clarification.

---

**These guidelines are working if:** fewer unnecessary changes in
diffs, fewer rewrites due to overcomplication, and clarifying
questions come before implementation rather than after mistakes.

## Workflow conventions

- `/pr` to open a PR (runs `/simplify` → format/lint → typecheck →
  build → test before push; failures are bugs to fix, not bypass
  targets).
- `/release` for tagged releases (`v<x.y.z>` push → GoReleaser).

Default base branch is `develop`. Don't `--force` or `--no-verify`
unless the human explicitly asks.

## Docs

Feature-level reference lives under [`docs/`](docs/). Start at
[`docs/index.md`](docs/index.md) for the map. When a task touches a
documented surface (refs pane, checkout flow, stash, branches modal,
git wrappers, config), read the relevant doc before editing — the
invariants there are deliberate and shouldn't be silently
overridden.

## gstack

Use the `/browse` skill from gstack for all web browsing. Never use
`mcp__claude-in-chrome__*` tools.

Available gstack skills: `/office-hours`, `/plan-ceo-review`,
`/plan-eng-review`, `/plan-design-review`, `/design-consultation`,
`/design-shotgun`, `/design-html`, `/review`, `/ship`,
`/land-and-deploy`, `/canary`, `/benchmark`, `/browse`,
`/connect-chrome`, `/qa`, `/qa-only`, `/design-review`,
`/setup-browser-cookies`, `/setup-deploy`, `/setup-gbrain`, `/retro`,
`/investigate`, `/document-release`, `/document-generate`, `/codex`,
`/cso`, `/autoplan`, `/plan-devex-review`, `/devex-review`,
`/careful`, `/freeze`, `/guard`, `/unfreeze`, `/gstack-upgrade`,
`/learn`.
