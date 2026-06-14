// Refs storage container. Post-sidebar-shell-subtract there is no
// ref-list view, no cursor, no sidebar at all — refModel survives as a
// data holder for everything the rest of the cockpit needs:
//
//   - byKind ([local, remote, tag]) backs the branches modal's source
//     list and the rebase/refs targets (via LocalRefs / RemoteRefs); its
//     remote slice hides remotes a same-name local already mirrors.
//   - allRemotes keeps every remote-tracking ref unfiltered — the graph
//     Space evaluator needs origin/xx on a row even when local xx exists
//     (to offer checkout+FF / a picker), so it reads AllRemoteRefs, not
//     the mirror-filtered RemoteRefs.
//   - worktrees + dirty/timed-out maps + currentWorktreePath back the
//     worktrees dashboard (via Worktrees /
//     WorktreeDirty / SelectedWorktree-style consumers in worktree.go).
//   - lastFetchAt backs the worktrees dashboard's fetched-Xm-ago line.
//
// The dashboard's card renderer lives in worktreeview.go.
package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

const refLoadTimeout = 30 * time.Second

type refModel struct {
	byKind     [3][]git.Ref
	allRemotes []git.Ref
	loaded     bool
	err        error

	worktrees           []git.Worktree
	currentWorktreePath string
	worktreeDirtyCount  map[string]int
	worktreeTimedOut    map[string]bool
	worktreeLastCommit  map[string]worktreeCommitMeta
	worktreeSync        map[string]worktreeSyncMeta

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

// worktreeSyncMeta caches one worktree's ahead/behind counts vs its upstream.
// hasUpstream=false (no upstream configured, detached, or unborn HEAD) makes the
// card omit the `↑↓` column rather than show a misleading 0/0.
type worktreeSyncMeta struct {
	ahead, behind int
	hasUpstream   bool
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
		r.allRemotes = remotesOnly(m.refs)
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

// RemoteRefs returns the cached remote-tracking slice with same-name-local
// mirrors hidden — the rebase target list and refs display want the deduped
// view.
func (r refModel) RemoteRefs() []git.Ref { return r.byKind[1] }

// AllRemoteRefs returns every remote-tracking ref, including ones a
// same-name local mirrors. The graph Space evaluator consumes this so
// "Space on origin/xx" still sees origin/xx on the row when local xx
// exists (to drive the checkout+FF / picker decision).
func (r refModel) AllRemoteRefs() []git.Ref { return r.allRemotes }

// SetWorktrees rewrites the inventory. Dirty / timed-out maps drop
// entries that disappeared so the modal never paints a marker for a
// pruned worktree.
func (r *refModel) SetWorktrees(entries []git.Worktree, currentPath string) {
	r.worktrees = entries
	r.currentWorktreePath = currentPath
	if r.worktreeDirtyCount == nil {
		r.worktreeDirtyCount = make(map[string]int)
	}
	if r.worktreeTimedOut == nil {
		r.worktreeTimedOut = make(map[string]bool)
	}
	if r.worktreeLastCommit == nil {
		r.worktreeLastCommit = make(map[string]worktreeCommitMeta)
	}
	if r.worktreeSync == nil {
		r.worktreeSync = make(map[string]worktreeSyncMeta)
	}
	live := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		live[e.Path] = struct{}{}
	}
	for p := range r.worktreeDirtyCount {
		if _, ok := live[p]; !ok {
			delete(r.worktreeDirtyCount, p)
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
	for p := range r.worktreeSync {
		if _, ok := live[p]; !ok {
			delete(r.worktreeSync, p)
		}
	}
}

func (r *refModel) SetWorktreeDirty(path string, dirtyCount int, timedOut bool) {
	if r.worktreeDirtyCount == nil {
		r.worktreeDirtyCount = make(map[string]int)
	}
	if r.worktreeTimedOut == nil {
		r.worktreeTimedOut = make(map[string]bool)
	}
	r.worktreeDirtyCount[path] = dirtyCount
	if timedOut {
		r.worktreeTimedOut[path] = true
	} else {
		delete(r.worktreeTimedOut, path)
	}
}

// SetWorktreeSync stores one path's ahead/behind-vs-upstream counts from the
// fan-out, alongside SetWorktreeDirty / SetWorktreeLastCommit in the same
// worktreeDirtyResultMsg handler.
func (r *refModel) SetWorktreeSync(path string, ahead, behind int, hasUpstream bool) {
	if r.worktreeSync == nil {
		r.worktreeSync = make(map[string]worktreeSyncMeta)
	}
	r.worktreeSync[path] = worktreeSyncMeta{ahead: ahead, behind: behind, hasUpstream: hasUpstream}
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
	return r.worktreeDirtyCount[path] > 0
}
func (r refModel) WorktreeDirtyCount(path string) int        { return r.worktreeDirtyCount[path] }
func (r refModel) WorktreeSync(path string) worktreeSyncMeta { return r.worktreeSync[path] }

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
// remotesOnly returns every remote-tracking ref unfiltered, preserving
// for-each-ref order. Unlike partitionByKind's remote slice it keeps refs a
// same-name local mirrors — the graph Space evaluator needs the full set.
func remotesOnly(refs []git.Ref) []git.Ref {
	var out []git.Ref
	for _, ref := range refs {
		if ref.Kind == git.RefKindRemote {
			out = append(out, ref)
		}
	}
	return out
}

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
