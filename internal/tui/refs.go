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
	refs   []git.Ref
	width  int
	height int
	cursor int
	loaded bool
	err    error
}

func newRefsModel() refModel { return refModel{} }

type refsLoadedMsg struct{ refs []git.Ref }
type refsLoadFailedMsg struct{ err error }

// loadRefsCmd runs git.ForEachRef in a tea.Cmd. dir == "" uses the process cwd.
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

func (r refModel) Update(msg tea.Msg) (refModel, tea.Cmd) {
	switch m := msg.(type) {
	case refsLoadedMsg:
		r.refs = m.refs
		r.cursor = 0
		r.loaded = true
		r.err = nil
		return r, nil
	case refsLoadFailedMsg:
		r.loaded = true
		r.err = m.err
		return r, nil
	case tea.KeyMsg:
		return r.handleKey(m), nil
	}
	return r, nil
}

func (r refModel) handleKey(msg tea.KeyMsg) refModel {
	sel := r.selectable()
	switch msg.String() {
	case "j", "down":
		if r.cursor < len(sel)-1 {
			r.cursor++
		}
	case "k", "up":
		if r.cursor > 0 {
			r.cursor--
		}
	case "g":
		r.cursor = 0
	case "G":
		if len(sel) > 0 {
			r.cursor = len(sel) - 1
		}
	}
	return r
}

// Selected returns the ref under the cursor, if any. Headers and empty-section
// placeholders are not counted by the cursor — only refs are selectable.
func (r refModel) Selected() (git.Ref, bool) {
	sel := r.selectable()
	if r.cursor < 0 || r.cursor >= len(sel) {
		return git.Ref{}, false
	}
	return sel[r.cursor], true
}

// selectable returns refs in render order (Local → Remote → Tags). The cursor
// indexes into this slice.
func (r refModel) selectable() []git.Ref {
	out := make([]git.Ref, 0, len(r.refs))
	for _, k := range []git.RefKind{git.RefKindLocal, git.RefKindRemote, git.RefKindTag} {
		for _, ref := range r.refs {
			if ref.Kind == k {
				out = append(out, ref)
			}
		}
	}
	return out
}

var (
	refHeaderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTime)).Bold(true)
	refEmptyStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color(colorTime))
	refHeadStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(colorSelected))
)

type refSection struct {
	title string
	kind  git.RefKind
}

var refSections = []refSection{
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
	cursorIdx := -1
	first := true
	for _, sec := range refSections {
		if !first {
			b.WriteByte('\n')
		}
		first = false
		b.WriteString(refHeaderStyle.Render(runewidth.Truncate(sec.title, width, "…")))

		items := refsOfKind(r.refs, sec.kind)
		if len(items) == 0 {
			b.WriteByte('\n')
			b.WriteString(refEmptyStyle.Render(runewidth.Truncate("  (empty)", width, "…")))
			continue
		}
		for _, ref := range items {
			cursorIdx++
			b.WriteByte('\n')
			b.WriteString(renderRefLine(ref, width, cursorIdx == r.cursor))
		}
	}
	return b.String()
}

func refsOfKind(refs []git.Ref, kind git.RefKind) []git.Ref {
	var out []git.Ref
	for _, r := range refs {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

func renderRefLine(ref git.Ref, width int, selected bool) string {
	const prefixWidth = 2

	// HEAD '*' always wins the prefix slot so HEAD stays identifiable even
	// when the cursor sits on it. Selection is conveyed by bold + color on the
	// name itself.
	prefix := "  "
	if ref.IsHead {
		prefix = refHeadStyle.Render("*") + " "
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
		name = refHeadStyle.Render(name)
	}
	return prefix + name
}

// SetSize must be called when the parent pane's inner content area changes.
func (r *refModel) SetSize(w, h int) {
	r.width = w
	r.height = h
}
