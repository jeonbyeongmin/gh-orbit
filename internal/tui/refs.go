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

	localChangesSummary         git.LocalChangesSummary
	localChangesSummaryLoadedAt time.Time

	lastFetchAt time.Time
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

func (r refModel) Worktrees() []git.Worktree { return r.worktrees }
func (r refModel) WorktreeDirty(path string) bool {
	return r.worktreeDirty[path]
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

// renderWorktreeSidebarRow formats one worktree entry inside the
// dashboard row body. `▶` + bold for the current entry; 2-col indent
// for the rest. The `Sidebar` in the name is a historical artifact —
// the dashboard reuses the same row shape.
func renderWorktreeSidebarRow(wt git.Worktree, isCurrent, selected bool, dirtyMark string, width int) string {
	const prefixWidth = 2
	prefix := "  "
	if isCurrent {
		prefix = cursorStyle.Render("▶") + " "
	}
	name := wt.Path
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	parts := []string{name}
	switch {
	case wt.Detached:
		parts = append(parts, "(detached)")
	case wt.Branch != "":
		parts = append(parts, wt.Branch)
	}
	if dirtyMark != "" {
		parts = append(parts, dirtyMark)
	}
	body := strings.Join(parts, " · ")
	avail := width - prefixWidth
	if avail < 1 {
		return prefix
	}
	body = runewidth.Truncate(body, avail, "…")
	switch {
	case selected:
		body = selectedStyle.Render(body)
	case isCurrent:
		body = cursorStyle.Render(body)
	}
	return prefix + body
}
