package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// changesFileListRatio is the percent of the changes-tab width given to the
// file list; the patch viewport takes the remainder. Tuned so paths still
// breathe on a typical 120-col terminal.
const changesFileListRatio = 35

const filePatchTimeout = 60 * time.Second

// changesModel hosts the Changes-tab body: a file-list cursor on the left
// and a path-scoped patch viewport (follower) on the right.
type changesModel struct {
	files         []git.FileStat
	cursor        int
	hash          string
	loadingFiles  bool
	statErr       error
	width, height int

	viewport     viewport.Model
	patchText    string
	loadingPatch bool
	patchErr     error
	// fileReqID counts every patch dispatch (cursor move, file refresh).
	// Stale-drop: messages whose reqID/hash/path no longer match are ignored
	// so a slow `git show` for an old file doesn't paint over the current one.
	fileReqID uint64
}

func newChangesModel() changesModel {
	return changesModel{viewport: viewport.New(0, 0)}
}

func (c *changesModel) SetSize(w, h int) {
	c.width = w
	c.height = h
	_, patchW := c.columnWidths()
	c.viewport.Width = patchW
	c.viewport.Height = h
	if c.patchText != "" {
		c.viewport.SetContent(c.patchText)
	}
}

// columnWidths splits the available width between the file-list (left) and
// the patch viewport (right). Both are inner content widths — borders are not
// drawn between the columns.
func (c changesModel) columnWidths() (fileW, patchW int) {
	fileW = c.width * changesFileListRatio / 100
	if fileW < 12 {
		fileW = 12
	}
	if fileW > c.width-12 {
		fileW = c.width - 12
	}
	if fileW < 1 {
		fileW = 1
	}
	patchW = c.width - fileW - 1 // 1 col separator
	if patchW < 1 {
		patchW = 1
	}
	return fileW, patchW
}

// MarkPending clears prior state and stamps the new hash so the loading
// indicator shows during the 200ms debounce window — without this the panel
// would flash the previous commit's files until the stat dispatch returns.
func (c *changesModel) MarkPending(hash string) {
	c.hash = hash
	c.files = nil
	c.cursor = 0
	c.loadingFiles = true
	c.statErr = nil
	c.patchText = ""
	c.patchErr = nil
	c.loadingPatch = false
	c.viewport.SetContent("")
}

// SetFiles replaces the file list and the commit hash they belong to. Returns
// a cmd that loads the patch for the current cursor position so the right
// column refreshes whenever a new commit is selected.
func (c *changesModel) SetFiles(hash string, files []git.FileStat) tea.Cmd {
	c.files = files
	c.hash = hash
	c.loadingFiles = false
	c.statErr = nil
	if c.cursor >= len(files) {
		c.cursor = 0
	}
	if c.cursor < 0 {
		c.cursor = 0
	}
	if len(files) == 0 {
		c.patchText = ""
		c.patchErr = nil
		c.loadingPatch = false
		c.viewport.SetContent("")
		return nil
	}
	return c.loadCurrentFileCmd()
}

// ApplyStatFailed surfaces a numstat failure into the file-list area; the
// patch viewport is left alone since there is nothing to show until the user
// moves to a different commit.
func (c *changesModel) ApplyStatFailed(hash string, err error) {
	if hash != c.hash {
		return
	}
	c.loadingFiles = false
	c.files = nil
	c.statErr = err
}

func (c *changesModel) loadCurrentFileCmd() tea.Cmd {
	if c.hash == "" || c.cursor < 0 || c.cursor >= len(c.files) {
		return nil
	}
	c.fileReqID++
	c.loadingPatch = true
	c.patchText = ""
	c.patchErr = nil
	c.viewport.SetContent("")
	c.viewport.GotoTop()
	return loadFilePatchCmd("", c.hash, c.files[c.cursor].Path, c.fileReqID)
}

// acceptsPatch gates Apply* against stale dispatches: the reqID + (hash,
// path) tuple must all match to avoid two-cursor-moves-in-one-show races.
func (c *changesModel) acceptsPatch(reqID uint64, hash, path string) bool {
	if reqID != c.fileReqID || hash != c.hash {
		return false
	}
	if c.cursor >= len(c.files) {
		return false
	}
	return path == c.files[c.cursor].Path
}

func (c *changesModel) ApplyFilePatchLoaded(reqID uint64, hash, path, text string) {
	if !c.acceptsPatch(reqID, hash, path) {
		return
	}
	c.loadingPatch = false
	c.patchErr = nil
	c.patchText = text
	c.viewport.SetContent(text)
	c.viewport.GotoTop()
}

func (c *changesModel) ApplyFilePatchFailed(reqID uint64, hash, path string, err error) {
	if !c.acceptsPatch(reqID, hash, path) {
		return
	}
	c.loadingPatch = false
	c.patchErr = err
}

// ScrollPatch forwards a scroll key (ctrl+d/u/PgUp/PgDn) to the viewport.
func (c *changesModel) ScrollPatch(msg tea.KeyMsg) {
	c.viewport, _ = c.viewport.Update(msg)
}

func (c changesModel) Update(msg tea.Msg) (changesModel, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "j", "down":
			if c.cursor < len(c.files)-1 {
				c.cursor++
				return c, c.loadCurrentFileCmd()
			}
		case "k", "up":
			if c.cursor > 0 {
				c.cursor--
				return c, c.loadCurrentFileCmd()
			}
		case "g":
			if c.cursor != 0 {
				c.cursor = 0
				return c, c.loadCurrentFileCmd()
			}
		case "G":
			if len(c.files) > 0 && c.cursor != len(c.files)-1 {
				c.cursor = len(c.files) - 1
				return c, c.loadCurrentFileCmd()
			}
		}
	}
	return c, nil
}

var (
	changesCursorS = lipgloss.NewStyle().Reverse(true)
	changesInsS    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	changesDelS    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	changesBinS    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	changesEmptyS  = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	changesSepS    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

func (c changesModel) View() string {
	if c.hash == "" {
		return changesEmptyS.Render("(no commit selected)")
	}
	if c.statErr != nil {
		return changesEmptyS.Render("error: " + firstLine(c.statErr.Error()))
	}
	if c.loadingFiles {
		return changesEmptyS.Render("loading…")
	}
	if len(c.files) == 0 {
		return changesEmptyS.Render("(no changes)")
	}
	fileW, _ := c.columnWidths()
	left := c.renderFileList(fileW)
	right := c.renderPatch()
	rows := max(c.height, 1)
	sep := changesSepS.Render(strings.TrimRight(strings.Repeat("│\n", rows), "\n"))
	leftBox := lipgloss.NewStyle().Width(fileW).Height(c.height).Render(left)
	return lipgloss.JoinHorizontal(lipgloss.Top, leftBox, sep, right)
}

func (c changesModel) renderFileList(width int) string {
	insW, delW := 1, 1
	for _, f := range c.files {
		if f.Binary() {
			continue
		}
		if w := len(strconv.Itoa(f.Insertions)) + 1; w > insW {
			insW = w
		}
		if w := len(strconv.Itoa(f.Deletions)) + 1; w > delW {
			delW = w
		}
	}
	statColW := insW + 2 + delW
	pathW := width - statColW - 1
	if pathW < 4 {
		pathW = 4
	}

	var b strings.Builder
	for i, f := range c.files {
		var line string
		if f.Binary() {
			binCell := runewidth.FillRight(changesBinS.Render("Bin"), statColW)
			line = fmt.Sprintf("%s %s", binCell, truncatePath(f.Path, pathW))
		} else {
			ins := fmt.Sprintf("%*s", insW, "+"+strconv.Itoa(f.Insertions))
			del := fmt.Sprintf("%*s", delW, "-"+strconv.Itoa(f.Deletions))
			line = fmt.Sprintf("%s  %s %s",
				changesInsS.Render(ins),
				changesDelS.Render(del),
				truncatePath(f.Path, pathW))
		}
		if i == c.cursor {
			line = changesCursorS.Render(line)
		}
		b.WriteString(line)
		if i < len(c.files)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func (c changesModel) renderPatch() string {
	if c.patchErr != nil {
		return changesEmptyS.Render("error: " + firstLine(c.patchErr.Error()))
	}
	if c.loadingPatch {
		return changesEmptyS.Render("loading…")
	}
	if strings.TrimSpace(c.patchText) == "" {
		return changesEmptyS.Render("(no patch)")
	}
	return c.viewport.View()
}

// truncatePath shortens a path with a leading ellipsis when too wide. Paths
// are more recognizable from the right end (the filename) than the left, so
// we drop directory prefixes first.
func truncatePath(p string, width int) string {
	if runewidth.StringWidth(p) <= width {
		return p
	}
	if width <= 1 {
		return "…"
	}
	for i := range p {
		if runewidth.StringWidth(p[i:])+1 <= width {
			return "…" + p[i:]
		}
	}
	return "…"
}

type filePatchLoadedMsg struct {
	reqID uint64
	hash  string
	path  string
	text  string
}

type filePatchFailedMsg struct {
	reqID uint64
	hash  string
	path  string
	err   error
}

func loadFilePatchCmd(dir, hash, path string, reqID uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), filePatchTimeout)
		defer cancel()
		text, err := git.PatchForFile(ctx, dir, hash, path)
		if err != nil {
			return filePatchFailedMsg{reqID: reqID, hash: hash, path: path, err: err}
		}
		return filePatchLoadedMsg{reqID: reqID, hash: hash, path: path, text: text}
	}
}
