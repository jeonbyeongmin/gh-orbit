package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

// changesModel hosts the Changes-tab body. Step 3 covers the file-list cursor
// only; step 4 adds a path-scoped patch viewport on the right and routes
// ctrl+d/u scroll keys to it.
type changesModel struct {
	files         []git.FileStat
	cursor        int
	width, height int
}

func newChangesModel() changesModel { return changesModel{} }

func (c *changesModel) SetSize(w, h int) {
	c.width = w
	c.height = h
}

// SetFiles replaces the file list. The cursor clamps to the new length so a
// shorter result after the next git show doesn't leave the cursor dangling.
func (c *changesModel) SetFiles(files []git.FileStat) {
	c.files = files
	if c.cursor >= len(files) {
		c.cursor = 0
	}
	if c.cursor < 0 {
		c.cursor = 0
	}
}

func (c changesModel) Update(msg tea.Msg) (changesModel, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "j", "down":
			if c.cursor < len(c.files)-1 {
				c.cursor++
			}
		case "k", "up":
			if c.cursor > 0 {
				c.cursor--
			}
		case "g":
			c.cursor = 0
		case "G":
			if len(c.files) > 0 {
				c.cursor = len(c.files) - 1
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
)

func (c changesModel) View() string {
	if len(c.files) == 0 {
		return changesEmptyS.Render("(no changes)")
	}
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
	pathW := c.width - statColW - 1
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
