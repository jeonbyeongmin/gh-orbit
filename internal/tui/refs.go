// Left sidebar. Post-refs-LIST-subtract, the sidebar hosts only three
// surfaces: the worktrees inventory (the cockpit's first-class anchor for
// "which working tree am I reviewing?"), the sticky `● Local Changes`
// row, and a `fetched Xm ago` freshness footer. The Local/Remote/Tags ref
// sections are gone — graph Enter's decision tree is the single checkout
// surface; the branches modal (`b`) is the single delete-branch surface.
//
// byKind storage stays because graph Enter's chip evaluator still consumes
// LocalRefs/RemoteRefs (for the cross-branch FF + branch-picker paths) and
// the branches modal pulls its candidate list from LocalRefs. Nothing in
// the sidebar renders a ref row anymore.
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
	// byKind is the [local, remote, tag] storage produced by partitionByKind.
	// LocalRefs / RemoteRefs expose the slices to graph Enter + branches
	// modal; the sidebar itself no longer renders these.
	byKind [3][]git.Ref
	width  int
	height int
	loaded bool
	err    error

	// Worktrees inventory + cursor.
	worktrees           []git.Worktree
	currentWorktreePath string
	worktreeDirty       map[string]bool
	worktreeTimedOut    map[string]bool

	// onWorktree is 0..len-1 when the cursor sits on a worktree row,
	// otherwise -1. onLocalChanges flags the sticky row as the focus.
	// Exactly one of (onWorktree >= 0) or onLocalChanges is true while
	// there is anything to focus; default = onLocalChanges true so an
	// empty-worktree load still has a sensible cursor.
	onWorktree     int
	onLocalChanges bool

	// localChangesSummary feeds the inline meta on the sticky row.
	localChangesSummary         git.LocalChangesSummary
	localChangesSummaryLoadedAt time.Time

	// lastFetchAt feeds the sidebar footer's `fetched Xm ago`.
	lastFetchAt time.Time
}

func newRefsModel() refModel {
	return refModel{onWorktree: -1, onLocalChanges: true}
}

type refsLoadedMsg struct{ refs []git.Ref }
type refsLoadFailedMsg struct{ err error }

// refWorktreeSwitchRequestedMsg fires when enter is pressed on a worktree
// row. The Model turns it into a switchWorktreeMsg via the existing seam.
type refWorktreeSwitchRequestedMsg struct{ path string }

// refWorktreeAddRequestedMsg / refWorktreeRemoveRequestedMsg fire on `a`
// / `d` from a worktree row — the Model opens the add-input / remove-
// confirm sub-modal.
type refWorktreeAddRequestedMsg struct{}
type refWorktreeRemoveRequestedMsg struct{ target git.Worktree }

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

// ResetForReload clears loaded/err so View renders the "loading…" placeholder
// while a fresh loadRefsCmd is in flight. cursor state (onWorktree /
// onLocalChanges) is intentionally left alone so a reload preserves the
// user's focus.
func (r *refModel) ResetForReload() {
	r.loaded = false
	r.err = nil
}

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
	case tea.KeyMsg:
		switch m.String() {
		case "enter":
			if wt, ok := r.SelectedWorktree(); ok {
				return r, func() tea.Msg { return refWorktreeSwitchRequestedMsg{path: wt.Path} }
			}
			if r.onLocalChanges {
				return r, func() tea.Msg { return localChangesEnterRequestedMsg{} }
			}
			return r, nil
		case "a":
			if r.onWorktree != -1 {
				return r, func() tea.Msg { return refWorktreeAddRequestedMsg{} }
			}
			return r, nil
		}
		return r.handleKey(m), nil
	}
	return r, nil
}

// handleKey owns j/k/g/G cursor movement across worktree rows + the
// sticky Local Changes row. Worktree section is "above" the sticky row in
// flat order, so j from the last worktree lands on Local Changes; k from
// Local Changes lands on the bottom worktree (when any). g/G jump to the
// top / bottom of the visible inventory.
func (r refModel) handleKey(msg tea.KeyMsg) refModel {
	wtCount := len(r.worktrees)
	switch msg.String() {
	case "j", "down":
		if r.onWorktree != -1 {
			if r.onWorktree < wtCount-1 {
				r.onWorktree++
				return r
			}
			// Last worktree → fall through to Local Changes.
			r.onWorktree = -1
			r.onLocalChanges = true
			return r
		}
		// onLocalChanges: nowhere further down.
		return r
	case "k", "up":
		if r.onWorktree != -1 {
			if r.onWorktree > 0 {
				r.onWorktree--
			}
			return r
		}
		if r.onLocalChanges && wtCount > 0 {
			r.onLocalChanges = false
			r.onWorktree = wtCount - 1
		}
		return r
	case "g":
		if wtCount > 0 {
			r.onLocalChanges = false
			r.onWorktree = 0
		} else {
			r.onLocalChanges = true
			r.onWorktree = -1
		}
		return r
	case "G":
		r.onLocalChanges = true
		r.onWorktree = -1
		return r
	}
	return r
}

// LocalRefs returns the cached local-branch slice. branches modal + graph
// Enter's chip evaluator both reach into byKind through this API.
func (r refModel) LocalRefs() []git.Ref { return r.byKind[0] }

// RemoteRefs returns the cached remote-tracking slice (origin/<branch>
// entries). graph Enter's cross-branch FF path consumes this.
func (r refModel) RemoteRefs() []git.Ref { return r.byKind[1] }

// IsLocalChangesSelected reports whether the sticky row is the focus.
func (r refModel) IsLocalChangesSelected() bool { return r.onLocalChanges }

// SelectedWorktree returns the worktree under the cursor, if any.
func (r refModel) SelectedWorktree() (git.Worktree, bool) {
	if r.onWorktree < 0 || r.onWorktree >= len(r.worktrees) {
		return git.Worktree{}, false
	}
	return r.worktrees[r.onWorktree], true
}

// SetWorktrees rewrites the inventory. The cursor is clamped so an entry
// that just disappeared doesn't strand onWorktree on an invalid index;
// when the previous focus is gone the cursor falls back to Local Changes.
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
	if r.onWorktree >= len(entries) {
		r.onWorktree = -1
		r.onLocalChanges = true
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

// SetLocalChangesSummary publishes the latest numstat + reload time into
// the sidebar so the sticky row's inline meta can render.
func (r *refModel) SetLocalChangesSummary(summary git.LocalChangesSummary, loadedAt time.Time) {
	r.localChangesSummary = summary
	r.localChangesSummaryLoadedAt = loadedAt
}

// ResetLocalChangesSummary clears the inline meta. Used when the freshness
// signal is no longer trustworthy (e.g. directory change).
func (r *refModel) ResetLocalChangesSummary() {
	r.localChangesSummary = git.LocalChangesSummary{}
	r.localChangesSummaryLoadedAt = time.Time{}
}

// SetLastFetchAt records the wall-clock of the most recent fetch attempt.
func (r *refModel) SetLastFetchAt(t time.Time) { r.lastFetchAt = t }

func (r *refModel) SetSize(w, h int) {
	r.width = w
	r.height = h
}

// partitionByKind sorts refs into [local, remote, tag] slots. Same Q5
// remote-mirror filter as before — a remote-tracking ref whose stripped
// name matches a local branch is hidden so the chip evaluator + branches
// modal see the cleaned list.
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

func (r refModel) View() string {
	if !r.loaded {
		return "loading…"
	}
	if r.err != nil {
		return fmt.Sprintf("(load error: %s)", r.err)
	}
	width := r.width
	if width < 1 {
		width = 1
	}

	var b strings.Builder
	if len(r.worktrees) > 0 {
		b.WriteString(refHeaderStyle.Render(runewidth.Truncate("Worktrees", width, "…")))
		b.WriteByte('\n')
		for i, wt := range r.worktrees {
			isCurrent := wt.Path == r.currentWorktreePath
			selected := r.onWorktree == i
			dirtyMark := ""
			if r.worktreeTimedOut[wt.Path] {
				dirtyMark = "?"
			} else if r.worktreeDirty[wt.Path] {
				dirtyMark = "●"
			}
			b.WriteString(renderWorktreeSidebarRow(wt, isCurrent, selected, dirtyMark, width))
			b.WriteByte('\n')
		}
		b.WriteByte('\n') // gap before sticky row
	}
	b.WriteString(r.renderLocalChangesRow(width, r.onLocalChanges, time.Now()))

	if footer := r.formatFetchFooter(time.Now(), width); footer != "" {
		b.WriteByte('\n')
		b.WriteString(footer)
	}

	return b.String()
}

func (r refModel) renderLocalChangesRow(width int, selected bool, now time.Time) string {
	label := "● Local Changes"
	meta := r.formatLocalChangesMeta(now)
	return composeLocalChangesRow(label, meta, width, selected)
}

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

// formatFetchFooter renders the sidebar's bottom freshness footer. "" when
// no fetch has been attempted yet (so the body keeps full height). Reuses
// relativeShortAt for "just now" / "Xm" / "Xh" parity with the Local
// Changes inline meta.
func (r refModel) formatFetchFooter(now time.Time, width int) string {
	if r.lastFetchAt.IsZero() || width < 1 {
		return ""
	}
	age := relativeShortAt(r.lastFetchAt, now)
	var text string
	if age == "just now" {
		text = "fetched " + age
	} else {
		text = "fetched " + age + " ago"
	}
	text = runewidth.Truncate(text, width, "…")
	return timeStyle.Render(text)
}
