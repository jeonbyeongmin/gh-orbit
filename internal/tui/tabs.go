package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// tabKind names the bottom tab. The plan stops at Commit + Changes; File Tree
// lives in a separate spillover backlog and is intentionally not enumerated.
type tabKind int

const (
	tabCommit tabKind = iota
	tabChanges
	tabCount
)

func (t tabKind) label() string {
	switch t {
	case tabCommit:
		return "Commit"
	case tabChanges:
		return "Changes"
	}
	return ""
}

// tabsModel holds the active-tab cursor for the bottom area. Header rendering
// lives here so the model can be reused across tab content panes without each
// pane re-implementing the chip strip.
type tabsModel struct {
	active tabKind
}

func newTabsModel() tabsModel {
	return tabsModel{active: tabCommit}
}

func (t tabsModel) Active() tabKind { return t.active }

func (t *tabsModel) Next() {
	t.active = (t.active + 1) % tabCount
}

func (t *tabsModel) Prev() {
	t.active = (t.active + tabCount - 1) % tabCount
}

var (
	tabActiveS   = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true)
	tabInactiveS = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	tabSepS      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

// HeaderView renders the tab strip as a single line. Active tab is wrapped in
// brackets and styled bold/colored; inactive tabs render dimmed. The strip is
// width-naive — the host bordered box clips overflow.
func (t tabsModel) HeaderView() string {
	var b strings.Builder
	for i := tabKind(0); i < tabCount; i++ {
		if i > 0 {
			b.WriteString(tabSepS.Render(" · "))
		}
		label := i.label()
		if i == t.active {
			b.WriteString(tabActiveS.Render("[" + label + "]"))
		} else {
			b.WriteString(tabInactiveS.Render(" " + label + " "))
		}
	}
	return b.String()
}
