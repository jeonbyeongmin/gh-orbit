// Refs storage container. Post-sidebar-shell-subtract there is no
// ref-list view, no cursor, no sidebar at all — refModel survives as a
// data holder for everything the rest of the cockpit needs:
//
//   - byKind ([local, remote, tag]) backs graph Enter's chip evaluator
//     and the branches modal's source list (via LocalRefs / RemoteRefs).
//   - worktrees + dirty/timed-out maps + currentWorktreePath back the
//     top dashboard and the worktrees modal (via Worktrees /
//     WorktreeDirty / SelectedWorktree-style consumers in worktree.go).
//   - localChangesSummary + lastFetchAt back the dashboard's inline
//     Local Changes meta and the fetched-Xm-ago footer.
//
// The helper render funcs (renderWorktreeSidebarRow, composeLocalChangesRow,
// formatLocalChangesMeta, formatFetchFooter) live here because the
// dashboard reuses them with the same visual vocabulary — moving them to
// dashboard.go would only push their package-private callers around. The
// "Sidebar" in those names is now a historical artifact, not a place.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

const refLoadTimeout = 30 * time.Second

type refModel struct {
	byKind [3][]git.Ref
	loaded bool
	err    error

	worktrees           []git.Worktree
	currentWorktreePath string
	worktreeDirty       map[string]bool
	worktreeTimedOut    map[string]bool
	worktreeLastCommit  map[string]worktreeCommitMeta
	agentActive         map[string]bool

	localChangesSummary         git.LocalChangesSummary
	localChangesSummaryLoadedAt time.Time

	lastFetchAt time.Time
}

// worktreeCommitMeta caches one worktree's last-commit subject + time for
// the dashboard row. The zero value (empty subject, zero time) renders as a
// blank last-commit column — used both while the fan-out is in flight and
// when the worktree has no commits yet (unborn HEAD / bare).
type worktreeCommitMeta struct {
	subject string
	when    time.Time
}

func newRefsModel() refModel { return refModel{} }

type refsLoadedMsg struct{ refs []git.Ref }
type refsLoadFailedMsg struct{ err error }

func loadRefsCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), refLoadTimeout)
		defer cancel()
		refs, err := git.ForEachRef(ctx, git.ForEachRefOptions{Dir: dir})
		if err != nil {
			return refsLoadFailedMsg{err: err}
		}
		return refsLoadedMsg{refs: refs}
	}
}

func (r refModel) Init() tea.Cmd { return nil }

// ResetForReload flips loaded/err back to their "in-flight" defaults
// while a fresh loadRefsCmd is running. Other panes never block on
// r.loaded — they read LocalRefs/RemoteRefs/Worktrees which return the
// last good snapshot even mid-reload.
func (r *refModel) ResetForReload() {
	r.loaded = false
	r.err = nil
}

// Update only consumes refsLoaded* messages. KeyMsg handling lived here
// when the sidebar owned a cursor; the cursor moved into branchesModal
// and the top-dashboard focus mode, so refModel.Update is purely about
// ingesting fresh for-each-ref output.
func (r refModel) Update(msg tea.Msg) (refModel, tea.Cmd) {
	switch m := msg.(type) {
	case refsLoadedMsg:
		r.byKind = partitionByKind(m.refs)
		r.loaded = true
		r.err = nil
		return r, nil
	case refsLoadFailedMsg:
		r.loaded = true
		r.err = m.err
		return r, nil
	}
	return r, nil
}

// LocalRefs returns the cached local-branch slice. graph Enter's chip
// evaluator + the branches modal both reach into byKind through this API.
func (r refModel) LocalRefs() []git.Ref { return r.byKind[0] }

// RemoteRefs returns the cached remote-tracking slice. graph Enter's
// cross-branch FF path consumes this.
func (r refModel) RemoteRefs() []git.Ref { return r.byKind[1] }

// SetWorktrees rewrites the inventory. Dirty / timed-out maps drop
// entries that disappeared so the dashboard never paints a marker for a
// pruned worktree.
func (r *refModel) SetWorktrees(entries []git.Worktree, currentPath string) {
	r.worktrees = entries
	r.currentWorktreePath = currentPath
	if r.worktreeDirty == nil {
		r.worktreeDirty = make(map[string]bool)
	}
	if r.worktreeTimedOut == nil {
		r.worktreeTimedOut = make(map[string]bool)
	}
	if r.worktreeLastCommit == nil {
		r.worktreeLastCommit = make(map[string]worktreeCommitMeta)
	}
	if r.agentActive == nil {
		r.agentActive = make(map[string]bool)
	}
	live := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		live[e.Path] = struct{}{}
	}
	for p := range r.worktreeDirty {
		if _, ok := live[p]; !ok {
			delete(r.worktreeDirty, p)
		}
	}
	for p := range r.worktreeTimedOut {
		if _, ok := live[p]; !ok {
			delete(r.worktreeTimedOut, p)
		}
	}
	for p := range r.worktreeLastCommit {
		if _, ok := live[p]; !ok {
			delete(r.worktreeLastCommit, p)
		}
	}
	for p := range r.agentActive {
		if _, ok := live[p]; !ok {
			delete(r.agentActive, p)
		}
	}
}

func (r *refModel) SetWorktreeDirty(path string, dirty, timedOut bool) {
	if r.worktreeDirty == nil {
		r.worktreeDirty = make(map[string]bool)
	}
	if r.worktreeTimedOut == nil {
		r.worktreeTimedOut = make(map[string]bool)
	}
	r.worktreeDirty[path] = dirty
	if timedOut {
		r.worktreeTimedOut[path] = true
	} else {
		delete(r.worktreeTimedOut, path)
	}
}

// SetWorktreeLastCommit stores one path's last-commit subject + time from the
// fan-out. Paired with SetWorktreeDirty in the worktreeDirtyResultMsg handler
// (one msg feeds both) so the `●` marker and the subject/time columns update
// in the same frame.
func (r *refModel) SetWorktreeLastCommit(path, subject string, when time.Time) {
	if r.worktreeLastCommit == nil {
		r.worktreeLastCommit = make(map[string]worktreeCommitMeta)
	}
	r.worktreeLastCommit[path] = worktreeCommitMeta{subject: subject, when: when}
}

func (r refModel) Worktrees() []git.Worktree { return r.worktrees }
func (r refModel) WorktreeDirty(path string) bool {
	return r.worktreeDirty[path]
}

// SetAgentActive records whether a worktree path currently hosts a live
// Claude Code agent session. Written by the ~30s agent-session poll
// (agentSessionPollMsg); read by the dashboard row to paint the 🤖 marker.
func (r *refModel) SetAgentActive(path string, active bool) {
	if r.agentActive == nil {
		r.agentActive = make(map[string]bool)
	}
	r.agentActive[path] = active
}

// AgentActive reports the last polled agent-session state for a path. A
// path the poll has never seen returns false → no marker.
func (r refModel) AgentActive(path string) bool {
	return r.agentActive[path]
}

// WorktreeLastCommit returns the cached last-commit subject + time for a
// worktree path. Missing / not-yet-loaded paths return the zero value, which
// the dashboard row renders as a blank last-commit column.
func (r refModel) WorktreeLastCommit(path string) (string, time.Time) {
	m := r.worktreeLastCommit[path]
	return m.subject, m.when
}

// SetLocalChangesSummary publishes the latest numstat + reload time so
// the dashboard's inline meta can render `N files · +X -Y · Zm ago`.
func (r *refModel) SetLocalChangesSummary(summary git.LocalChangesSummary, loadedAt time.Time) {
	r.localChangesSummary = summary
	r.localChangesSummaryLoadedAt = loadedAt
}

// ResetLocalChangesSummary clears the inline meta — used when the
// freshness signal is no longer trustworthy (e.g. directory change).
func (r *refModel) ResetLocalChangesSummary() {
	r.localChangesSummary = git.LocalChangesSummary{}
	r.localChangesSummaryLoadedAt = time.Time{}
}

// SetLastFetchAt records the wall-clock of the most recent fetch
// attempt. The dashboard footer formats it as `fetched Xm ago`.
func (r *refModel) SetLastFetchAt(t time.Time) { r.lastFetchAt = t }

// partitionByKind sorts refs into [local, remote, tag] slots. The Q5
// remote-mirror filter hides a remote-tracking ref whose stripped name
// matches a local branch — graph Enter + branches modal see the cleaned
// list, no redundant mirror noise.
func partitionByKind(refs []git.Ref) [3][]git.Ref {
	localNames := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if ref.Kind == git.RefKindLocal {
			localNames[ref.ShortName] = struct{}{}
		}
	}
	kinds := [3]git.RefKind{git.RefKindLocal, git.RefKindRemote, git.RefKindTag}
	var out [3][]git.Ref
	for i, kind := range kinds {
		for _, ref := range refs {
			if ref.Kind != kind {
				continue
			}
			if kind == git.RefKindRemote {
				if _, mirrored := localNames[git.CheckoutTarget(ref)]; mirrored {
					continue
				}
			}
			out[i] = append(out[i], ref)
		}
	}
	return out
}

var refHeaderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTime)).Bold(true)

// formatLocalChangesMeta builds the `N files · +X -Y · Zm ago` string.
// Returns "" when the summary carries no signal so the dashboard header
// falls back to a bare "Worktrees (N)" label instead of a stale meta.
func (r refModel) formatLocalChangesMeta(now time.Time) string {
	if r.localChangesSummary.Empty() {
		return ""
	}
	s := r.localChangesSummary
	filesWord := "files"
	if s.FilesChanged == 1 {
		filesWord = "file"
	}
	parts := []string{
		fmt.Sprintf("%d %s", s.FilesChanged, filesWord),
		fmt.Sprintf("+%d -%d", s.Insertions, s.Deletions),
	}
	if !r.localChangesSummaryLoadedAt.IsZero() {
		age := relativeShortAt(r.localChangesSummaryLoadedAt, now)
		if age == "just now" {
			parts = append(parts, age)
		} else {
			parts = append(parts, age+" ago")
		}
	}
	return strings.Join(parts, " · ")
}

// composeLocalChangesRow lays out label + meta against a width budget.
// Kept here because the dashboard header reuses the same truncation
// rule for the right-aligned Local Changes meta.
func composeLocalChangesRow(label, meta string, width int, selected bool) string {
	labelStyle := cursorStyle
	if selected {
		labelStyle = selectedStyle
	}
	if meta == "" {
		text := runewidth.Truncate(label, width, "…")
		return labelStyle.Render(text)
	}
	const sep = "  "
	labelW := runewidth.StringWidth(label)
	sepW := runewidth.StringWidth(sep)
	if labelW+sepW >= width {
		text := runewidth.Truncate(label, width, "…")
		return labelStyle.Render(text)
	}
	availForMeta := width - labelW - sepW
	metaOut := meta
	if runewidth.StringWidth(meta) > availForMeta {
		metaOut = runewidth.Truncate(meta, availForMeta, "…")
	}
	return labelStyle.Render(label) + sep + timeStyle.Render(metaOut)
}

// worktreeSubjectFloor is the minimum leftover width (after the fixed
// columns + separator) the last-commit subject needs before it renders at
// all. Below it the subject column is dropped whole rather than chopped to a
// useless "f…" fragment. worktreeSubjectCap bounds it on the other end so a
// wide terminal can't let one verbose subject swallow the row.
const (
	worktreeSubjectFloor = 12
	worktreeSubjectCap   = 30
)

// renderWorktreeSidebarRow formats one worktree entry inside the dashboard
// row body. `▶` + bold for the current entry; 2-col indent for the rest. The
// `Sidebar` in the name is a historical artifact — the dashboard reuses the
// same row shape.
//
// Display order is `▶ name · 🤖 · branch · ● · subject · time`. When the band
// is too narrow the columns drop whole (no leftover "…" fragment) in priority
// order subject → branch → time → ● → 🤖, with `▶ name` always preserved. The
// 🤖 (a live Claude Code agent session on this tree) drops last among the
// fixed columns — it's the highest-value signal in a review cockpit. subject
// gets whatever width is left after the fixed columns fit, capped at
// worktreeSubjectCap and hidden below worktreeSubjectFloor. A zero `when` /
// empty `subject` (loading, timed-out, or unborn-HEAD worktree) simply omits
// that column — the last-commit slots render blank, never `?`.
func renderWorktreeSidebarRow(wt git.Worktree, isCurrent, selected, agentActive bool, dirtyMark, subject string, when, now time.Time, width int) string {
	const prefixWidth = 2
	const sep = " · "
	sepW := runewidth.StringWidth(sep)
	prefix := "  "
	if isCurrent {
		prefix = cursorStyle.Render("▶") + " "
	}
	name := wt.Path
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	avail := width - prefixWidth
	if avail < 1 {
		return prefix
	}

	branch := ""
	switch {
	case wt.Detached:
		branch = "(detached)"
	case wt.Branch != "":
		branch = wt.Branch
	}
	timeStr := ""
	if !when.IsZero() {
		timeStr = relativeShortAt(when, now)
	}

	// Fixed (non-subject) columns, present-flag gated. Width is measured in
	// display order: name · 🤖 · branch · ● · time.
	const agentMark = "🤖"
	hasAgent := agentActive
	hasBranch := branch != ""
	hasDirty := dirtyMark != ""
	hasTime := timeStr != ""
	fixedWidth := func() int {
		parts := []string{name}
		if hasAgent {
			parts = append(parts, agentMark)
		}
		if hasBranch {
			parts = append(parts, branch)
		}
		if hasDirty {
			parts = append(parts, dirtyMark)
		}
		if hasTime {
			parts = append(parts, timeStr)
		}
		return runewidth.StringWidth(strings.Join(parts, sep))
	}
	// Drop fixed columns until they fit. subject is dropped before any of
	// these (it's added afterward from the leftover), so the order here is
	// branch → time → ● → 🤖 ; name is never dropped. The presence guard
	// stops the loop once only name remains — the final Truncate clips that
	// as a last resort.
	for fixedWidth() > avail && (hasBranch || hasTime || hasDirty || hasAgent) {
		switch {
		case hasBranch:
			hasBranch = false
		case hasTime:
			hasTime = false
		case hasDirty:
			hasDirty = false
		default: // hasAgent — highest-value fixed column, dropped last
			hasAgent = false
		}
	}

	// subject takes the width left after the fixed columns + one separator,
	// capped and floored. A negative leftover (name alone overflows) falls
	// below the floor, so subject drops out here too.
	if subject != "" {
		if leftover := avail - fixedWidth() - sepW; leftover >= worktreeSubjectFloor {
			budget := leftover
			if budget > worktreeSubjectCap {
				budget = worktreeSubjectCap
			}
			subject = runewidth.Truncate(subject, budget, "…")
		} else {
			subject = ""
		}
	}

	parts := []string{name}
	if hasAgent {
		parts = append(parts, agentMark)
	}
	if hasBranch {
		parts = append(parts, branch)
	}
	if hasDirty {
		parts = append(parts, dirtyMark)
	}
	if subject != "" {
		parts = append(parts, subject)
	}
	if hasTime {
		parts = append(parts, timeStr)
	}
	body := runewidth.Truncate(strings.Join(parts, sep), avail, "…")
	// isCurrent paints the "you're here" body styling (bold + accent fg)
	// independent of focus — Decision 4 keeps the ▶ row visually salient
	// whether or not the dashboard has the cursor.
	if isCurrent {
		body = selectedStyle.Render(body)
	}
	// selected overlays a background tint to mark the dashboard cursor
	// row. fg / bg are independent channels in lipgloss, so the bold +
	// accent fg above survives the bg overlay.
	if selected {
		body = cursorRowBgStyle.Render(body)
	}
	return prefix + body
}
