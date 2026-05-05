package tui

import (
	"strings"
	"testing"
)

func TestTabsModelInitialActiveIsCommit(t *testing.T) {
	tm := newTabsModel()
	if tm.Active() != tabCommit {
		t.Errorf("default active tab = %v, want tabCommit", tm.Active())
	}
}

func TestTabsModelNextWrapsAfterChanges(t *testing.T) {
	tm := newTabsModel()
	tm.Next()
	if tm.Active() != tabChanges {
		t.Errorf("after Next from Commit, active = %v, want tabChanges", tm.Active())
	}
	// Two-tab cycle: Next from Changes wraps to Commit.
	tm.Next()
	if tm.Active() != tabCommit {
		t.Errorf("Next from Changes should wrap to Commit, got %v", tm.Active())
	}
}

func TestTabsModelPrevWrapsBeforeCommit(t *testing.T) {
	tm := newTabsModel()
	tm.Prev()
	if tm.Active() != tabChanges {
		t.Errorf("Prev from Commit should wrap to Changes, got %v", tm.Active())
	}
}

func TestTabsModelHeaderHighlightsActive(t *testing.T) {
	tm := newTabsModel()
	header := tm.HeaderView()
	if !strings.Contains(header, "Commit") || !strings.Contains(header, "Changes") {
		t.Errorf("header should list both tabs, got %q", header)
	}
	// Active label is wrapped in brackets in the rendered output. Inactive
	// labels are surrounded by spaces, not brackets.
	if !strings.Contains(header, "[Commit]") {
		t.Errorf("active tab should be bracketed, got %q", header)
	}
	if strings.Contains(header, "[Changes]") {
		t.Errorf("inactive tab should not be bracketed, got %q", header)
	}
	tm.Next()
	header = tm.HeaderView()
	if !strings.Contains(header, "[Changes]") {
		t.Errorf("after Next the Changes tab should be bracketed, got %q", header)
	}
	if strings.Contains(header, "[Commit]") {
		t.Errorf("after Next the Commit tab should no longer be bracketed, got %q", header)
	}
}
