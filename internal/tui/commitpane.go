package tui

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

const commitDetailTimeout = 30 * time.Second

// commitDetailModel renders the Commit tab body — author/committer identity,
// dates, parents, sign-status, and the full commit body. The reqID guard
// rejects responses for commits the user has already navigated away from.
// Content is hosted in a bubbles/viewport so long bodies (or many metadata
// rows) scroll instead of being clipped by the tab area.
type commitDetailModel struct {
	hash    string
	detail  git.Detail
	loaded  bool
	loading bool
	err     error
	reqID   uint64

	width, height int
	viewport      viewport.Model
}

func newCommitDetailModel() commitDetailModel {
	return commitDetailModel{viewport: viewport.New(0, 0)}
}

func (c *commitDetailModel) SetSize(w, h int) {
	c.width = w
	c.height = h
	c.viewport.Width = w
	c.viewport.Height = h
	if c.loaded {
		c.viewport.SetContent(c.renderContent())
	}
}

// MarkLoading clears prior content and stamps the request id so the Commit
// tab tracks the same hash lifecycle as the Changes tab. The viewport is
// reset to the top so a long previous body doesn't leave the new commit
// scrolled into nothing.
func (c *commitDetailModel) MarkLoading(hash string, reqID uint64) {
	c.hash = hash
	c.detail = git.Detail{}
	c.loaded = false
	c.loading = true
	c.err = nil
	c.reqID = reqID
	c.viewport.SetContent("")
	c.viewport.GotoTop()
}

func (c *commitDetailModel) accepts(reqID uint64, hash string) bool {
	return reqID == c.reqID && hash == c.hash
}

func (c *commitDetailModel) ApplyDetailLoaded(reqID uint64, hash string, d git.Detail) {
	if !c.accepts(reqID, hash) {
		return
	}
	c.loading = false
	c.loaded = true
	c.detail = d
	c.err = nil
	c.viewport.SetContent(c.renderContent())
	c.viewport.GotoTop()
}

func (c *commitDetailModel) ApplyDetailFailed(reqID uint64, hash string, err error) {
	if !c.accepts(reqID, hash) {
		return
	}
	c.loading = false
	c.err = err
}

// CurrentHash returns the focused commit's hash even while the rest of the
// detail is still loading — the y-key clipboard write needs it before the
// `git show` round-trip completes.
func (c commitDetailModel) CurrentHash() string { return c.hash }

// ScrollContent forwards a pre-filtered scroll key to the viewport. The caller
// (model.go paneTab dispatch) is responsible for whitelisting which keys reach
// here so viewport's default keymap (which would otherwise claim ctrl+d/u for
// itself) doesn't shadow the Changes-tab patch scroll bindings. g/G are
// special-cased because viewport's DefaultKeyMap doesn't bind them.
func (c *commitDetailModel) ScrollContent(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "g":
		c.viewport.GotoTop()
		return nil
	case "G":
		c.viewport.GotoBottom()
		return nil
	}
	var cmd tea.Cmd
	c.viewport, cmd = c.viewport.Update(msg)
	return cmd
}

var (
	commitLabelS = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Bold(true)
	commitHashS  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	commitSignS  = map[string]lipgloss.Style{
		"G": lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		"B": lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		"U": lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		"X": lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		"Y": lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		"R": lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		"E": lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
		"N": lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
	}
	commitEmptyS = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
)

func signStatusLabel(code string) string {
	switch code {
	case "G":
		return "Signed (good)"
	case "B":
		return "Signed (bad)"
	case "U":
		return "Signed (unknown validity)"
	case "X":
		return "Signed (expired signature)"
	case "Y":
		return "Signed (expired key)"
	case "R":
		return "Signed (revoked key)"
	case "E":
		return "Signature cannot be checked"
	case "N", "":
		return "Unsigned"
	}
	return "Sign: " + code
}

func (c commitDetailModel) View() string {
	if c.hash == "" {
		return commitEmptyS.Render("(no commit selected)")
	}
	if c.err != nil {
		return commitEmptyS.Render("error: " + firstLine(c.err.Error()))
	}
	if c.loading && !c.loaded {
		return commitEmptyS.Render("loading…")
	}
	return c.viewport.View()
}

// renderContent builds the full Commit-tab body string. Metadata rows render
// at natural width (they're short enough to never need wrapping), while the
// body is soft-wrapped to the viewport width so long lines don't overflow
// horizontally — the viewport handles vertical overflow via scroll.
func (c commitDetailModel) renderContent() string {
	d := c.detail
	var b strings.Builder
	row := func(label, value string) {
		b.WriteString(commitLabelS.Render(label))
		b.WriteString("  ")
		b.WriteString(value)
		b.WriteByte('\n')
	}
	row("commit", commitHashS.Render(d.Hash)+"  ("+shortHash(d.Hash)+")")
	if len(d.Parents) > 0 {
		parentsShort := make([]string, 0, len(d.Parents))
		for _, p := range d.Parents {
			parentsShort = append(parentsShort, shortHash(p))
		}
		row("parents", strings.Join(parentsShort, " "))
	}
	row("author", d.AuthorName+" <"+d.AuthorEmail+">")
	if !d.AuthorDate.IsZero() {
		row("authored", d.AuthorDate.Local().Format("2006-01-02 15:04:05 -0700"))
	}
	if d.CommitterEmail != "" && d.CommitterEmail != d.AuthorEmail {
		row("committer", d.CommitterName+" <"+d.CommitterEmail+">")
	}
	if !d.CommitterDate.IsZero() && !d.CommitterDate.Equal(d.AuthorDate) {
		row("committed", d.CommitterDate.Local().Format("2006-01-02 15:04:05 -0700"))
	}
	signStyle, ok := commitSignS[d.SignStatus]
	if !ok {
		signStyle = lipgloss.NewStyle()
	}
	row("sign", signStyle.Render(signStatusLabel(d.SignStatus)))
	b.WriteByte('\n')
	body := strings.TrimRight(d.Body, "\n")
	if c.width > 0 {
		body = lipgloss.NewStyle().Width(c.width).Render(body)
	}
	b.WriteString(body)
	return b.String()
}

type commitDetailLoadedMsg struct {
	reqID  uint64
	hash   string
	detail git.Detail
}

type commitDetailFailedMsg struct {
	reqID uint64
	hash  string
	err   error
}

func loadCommitDetailCmd(dir, hash string, reqID uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commitDetailTimeout)
		defer cancel()
		d, err := git.CommitDetail(ctx, dir, hash)
		if err != nil {
			return commitDetailFailedMsg{reqID: reqID, hash: hash, err: err}
		}
		return commitDetailLoadedMsg{reqID: reqID, hash: hash, detail: d}
	}
}
