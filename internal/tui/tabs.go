package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// tabKind names the bottom tab.
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

// tabsModel holds the active-tab cursor for the bottom area.
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

// HeaderView renders the tab strip as a single line. The host bordered box
// clips overflow if the terminal is too narrow.
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
