// Refs storage container. Post-sidebar-shell-subtract there is no
// ref-list view, no cursor, no sidebar at all — refModel survives as a
// data holder for everything the rest of the cockpit needs:
//
//   - byKind ([local, remote, tag]) backs graph Enter's chip evaluator
//     and the branches modal's source list (via LocalRefs / RemoteRefs).
//   - worktrees + dirty/timed-out maps + currentWorktreePath back the
//     the worktrees modal (via Worktrees /
//     WorktreeDirty / SelectedWorktree-style consumers in worktree.go).
//   - lastFetchAt backs the worktrees modal's fetched-Xm-ago line.
//
// The row render func (renderWorktreeSidebarRow) lives here because it
// reads refModel state with the same visual vocabulary as its caller.
// The "Sidebar" in the name is a historical artifact, not a place.
package tui

import (
	"context"
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

	lastFetchAt time.Time
}

// worktreeCommitMeta caches one worktree's last-commit subject + time for
// the modal row. The zero value (empty subject, zero time) renders as a
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
// and the worktrees modal, so refModel.Update is purely about
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
// entries that disappeared so the modal never paints a marker for a
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

// WorktreeLastCommit returns the cached last-commit subject + time for a
// worktree path. Missing / not-yet-loaded paths return the zero value, which
// the modal row renders as a blank last-commit column.
func (r refModel) WorktreeLastCommit(path string) (string, time.Time) {
	m := r.worktreeLastCommit[path]
	return m.subject, m.when
}

// SetLastFetchAt records the wall-clock of the most recent fetch
// attempt. The worktrees modal formats it as `fetched Xm ago`.
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

// worktreeSubjectFloor is the minimum leftover width (after the fixed
// columns + separator) the last-commit subject needs before it renders at
// all. Below it the subject column is dropped whole rather than chopped to a
// useless "f…" fragment. worktreeSubjectCap bounds it on the other end so a
// wide terminal can't let one verbose subject swallow the row.
const (
	worktreeSubjectFloor = 12
	worktreeSubjectCap   = 30
)

// worktreeNameCap bounds the worktree name column. Names are derived from the
// worktree directory basename, which can be long (e.g. a branch-shaped
// `feat+worktree-sort-by-last-commit`); without a cap one long name swallows
// the row and starves the higher-value branch / subject columns.
const worktreeNameCap = 24

// worktreeDisplayName is the basename of a worktree path, capped at
// worktreeNameCap. Shared by the row renderer and the modal caller (which
// pre-measures it to compute the aligned name-column width) so the cap lives
// in one place.
func worktreeDisplayName(path string) string {
	name := path
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	return runewidth.Truncate(name, worktreeNameCap, "…")
}

// renderWorktreeSidebarRow formats one worktree entry inside the worktrees-modal
// row body. `▶` + bold for the current entry; 2-col indent for the rest. The
// `Sidebar` in the name is a historical artifact — the modal reuses the
// same row shape.
//
// Display order is `▶ name · branch · ● · subject · time`. `name` is capped
// at worktreeNameCap and, when `nameColW` > the row's own name, padded to that
// width so the columns after it line up across rows (the caller passes the
// set-wide max). Padding is skipped on a terminal too narrow to spare it, so
// alignment yields to information density.
//
// Columns are allocated in keep-priority order
// `name > branch > subject > ● dirty > time`: each is added only if it
// (plus its separator) still fits, but a column that doesn't fit is skipped
// while smaller lower-priority columns still get a shot at the leftover — so a
// too-long subject never leaves the row half-empty. `name` always survives;
// subject takes up to worktreeSubjectCap and is hidden below
// worktreeSubjectFloor. A zero `when` / empty `subject` (loading, timed-out,
// or unborn-HEAD worktree) simply omits that column — the last-commit slots
// render blank, never `?`.
func renderWorktreeSidebarRow(wt git.Worktree, isCurrent, selected bool, dirtyMark, subject string, when, now time.Time, width, nameColW int) string {
	const prefixWidth = 2
	const sep = " · "
	sepW := runewidth.StringWidth(sep)
	prefix := "  "
	if isCurrent {
		prefix = cursorStyle.Render("▶") + " "
	}
	name := worktreeDisplayName(wt.Path)
	avail := width - prefixWidth
	if avail < 1 {
		return prefix
	}
	// Align the name column to the set-wide max so the following columns share
	// a start column across rows. Skipped when the row can't spare the padding
	// (name col + a separator + a floor-width subject) — density wins there.
	if nameW := runewidth.StringWidth(name); nameColW > nameW && nameColW+sepW+worktreeSubjectFloor <= avail {
		name += strings.Repeat(" ", nameColW-nameW)
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

	// Greedy allocation in keep-priority order. cur tracks the running display
	// width (incl. separators); fits() asks whether one more column still fits.
	cur := runewidth.StringWidth(name)
	fits := func(w int) bool { return cur+sepW+w <= avail }

	useBranch := branch != "" && fits(runewidth.StringWidth(branch))
	if useBranch {
		cur += sepW + runewidth.StringWidth(branch)
	}
	// subject is elastic: it takes its own width up to worktreeSubjectCap, but
	// only when at least worktreeSubjectFloor is free — otherwise it drops whole
	// and the leftover falls through to the small dirty / time columns.
	if subject != "" {
		if room := avail - cur - sepW; room >= worktreeSubjectFloor {
			budget := room
			if budget > worktreeSubjectCap {
				budget = worktreeSubjectCap
			}
			subject = runewidth.Truncate(subject, budget, "…")
			cur += sepW + runewidth.StringWidth(subject)
		} else {
			subject = ""
		}
	}
	useDirty := dirtyMark != "" && fits(runewidth.StringWidth(dirtyMark))
	if useDirty {
		cur += sepW + runewidth.StringWidth(dirtyMark)
	}
	useTime := timeStr != "" && fits(runewidth.StringWidth(timeStr))

	parts := []string{name}
	if useBranch {
		parts = append(parts, branch)
	}
	if useDirty {
		parts = append(parts, dirtyMark)
	}
	if subject != "" {
		parts = append(parts, subject)
	}
	if useTime {
		parts = append(parts, timeStr)
	}
	body := runewidth.Truncate(strings.Join(parts, sep), avail, "…")
	// isCurrent paints the "you're here" body styling (bold + accent fg)
	// independent of focus — Decision 4 keeps the ▶ row visually salient
	// whether or not the modal has the cursor.
	if isCurrent {
		body = selectedStyle.Render(body)
	}
	// selected overlays a background tint to mark the modal cursor
	// row. fg / bg are independent channels in lipgloss, so the bold +
	// accent fg above survives the bg overlay.
	if selected {
		body = cursorRowBgStyle.Render(body)
	}
	return prefix + body
}
