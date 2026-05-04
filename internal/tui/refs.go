// Left-pane ref list (Local / Remote / Tags). Like graphModel, git access
// is async via tea.Cmd → tea.Msg so the TUI never blocks on for-each-ref.
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
	// byKind caches refs partitioned in render order so View / Selected /
	// keypress bounds checks don't re-walk r.refs on every frame. Index
	// matches refSections.
	byKind [3][]git.Ref
	width  int
	height int
	cursor int
	// yOffset is the first flat-row index visible inside the pane. Lazy
	// scroll moves it ±1 only when cursor reaches the visible window edge.
	yOffset int
	// folded[i] toggles section i (Local/Remote/Tags) between expanded and
	// collapsed. Zero value = all expanded, matching the previous behaviour.
	folded [3]bool
	loaded bool
	err    error
}

func newRefsModel() refModel { return refModel{} }

type refsLoadedMsg struct{ refs []git.Ref }
type refsLoadFailedMsg struct{ err error }

// refSelectedMsg is emitted when the user picks a ref (enter). The root model
// uses it to reload the graph pane against the chosen ref.
type refSelectedMsg struct{ ref git.Ref }

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
// while a fresh loadRefsCmd is in flight. cursor is intentionally left alone
// here — refsLoadedMsg already resets it to 0 on arrival, which doubles as a
// safe fallback when the previously selected ref was deleted by another tool.
func (r *refModel) ResetForReload() {
	r.loaded = false
	r.err = nil
}

func (r refModel) Update(msg tea.Msg) (refModel, tea.Cmd) {
	switch m := msg.(type) {
	case refsLoadedMsg:
		r.byKind = partitionByKind(m.refs)
		r.cursor = 0
		r.yOffset = 0
		r.loaded = true
		r.err = nil
		return r, nil
	case refsLoadFailedMsg:
		r.loaded = true
		r.err = m.err
		return r, nil
	case tea.KeyMsg:
		if m.String() == "enter" {
			if ref, ok := r.Selected(); ok {
				return r, func() tea.Msg { return refSelectedMsg{ref: ref} }
			}
			return r, nil
		}
		return r.handleKey(m), nil
	}
	return r, nil
}

func (r refModel) handleKey(msg tea.KeyMsg) refModel {
	total := r.selectableCount()
	switch msg.String() {
	case "j", "down":
		if r.cursor < total-1 {
			r.cursor++
		}
	case "k", "up":
		if r.cursor > 0 {
			r.cursor--
		}
	case "g":
		r.cursor = 0
	case "G":
		if total > 0 {
			r.cursor = total - 1
		}
	}
	return r
}

// Selected returns the ref under the cursor, if any. Headers and empty-section
// placeholders are not counted by the cursor — only refs are selectable.
func (r refModel) Selected() (git.Ref, bool) {
	idx := r.cursor
	if idx < 0 {
		return git.Ref{}, false
	}
	for _, items := range r.byKind {
		if idx < len(items) {
			return items[idx], true
		}
		idx -= len(items)
	}
	return git.Ref{}, false
}

func (r refModel) selectableCount() int {
	return len(r.byKind[0]) + len(r.byKind[1]) + len(r.byKind[2])
}

func partitionByKind(refs []git.Ref) [3][]git.Ref {
	var out [3][]git.Ref
	for i, sec := range refSections {
		for _, ref := range refs {
			if ref.Kind == sec.kind {
				out[i] = append(out[i], ref)
			}
		}
	}
	return out
}

var refHeaderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTime)).Bold(true)

type refSection struct {
	title string
	kind  git.RefKind
}

var refSections = [3]refSection{
	{"Local branches", git.RefKindLocal},
	{"Remote branches", git.RefKindRemote},
	{"Tags", git.RefKindTag},
}

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
	cursorIdx := 0
	for i, sec := range refSections {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(refHeaderStyle.Render(runewidth.Truncate(sec.title, width, "…")))

		items := r.byKind[i]
		if len(items) == 0 {
			b.WriteByte('\n')
			b.WriteString(timeStyle.Render(runewidth.Truncate("  (empty)", width, "…")))
			continue
		}
		for _, ref := range items {
			b.WriteByte('\n')
			b.WriteString(renderRefLine(ref, width, cursorIdx == r.cursor))
			cursorIdx++
		}
	}
	return b.String()
}

func renderRefLine(ref git.Ref, width int, selected bool) string {
	const prefixWidth = 2

	// HEAD '*' always wins the prefix slot so HEAD stays identifiable even
	// when the cursor sits on it. Selection is conveyed by bold + color on the
	// name itself.
	prefix := "  "
	if ref.IsHead {
		prefix = cursorStyle.Render("*") + " "
	}

	avail := width - prefixWidth
	if avail < 1 {
		return prefix
	}
	name := runewidth.Truncate(ref.ShortName, avail, "…")
	switch {
	case selected:
		name = selectedStyle.Render(name)
	case ref.IsHead:
		name = cursorStyle.Render(name)
	}
	return prefix + name
}

func (r *refModel) SetSize(w, h int) {
	r.width = w
	r.height = h
}
