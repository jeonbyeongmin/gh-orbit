// Background `gh pr list` dispatch — fuels the PR badge on branch chips.
// Same tea.Cmd → tea.Msg pattern as fetchCmd. The cockpit is a gh
// extension, so the gh CLI is guaranteed present; repos without a GitHub
// remote (or an unauthenticated gh) fail quietly to the runtime log
// instead of the status line — the badge is passive enrichment, not an
// action the user just triggered.
package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// prListTimeout matches the 30s budget of the other read-only loaders.
const prListTimeout = 30 * time.Second

// prCheckState is the one-glyph CI rollup rendered inside a chip's PR badge.
type prCheckState int

const (
	prChecksNone prCheckState = iota
	prChecksPending
	prChecksPassing
	prChecksFailing
)

// prInfo is one open PR. Model.prs keys these by head branch (the chip badge
// lookup); Model.prList keeps them in gh's newest-first order (the `l` modal).
type prInfo struct {
	Number  int
	HeadRef string
	Title   string
	Author  string
	Checks  prCheckState
}

type prsLoadedMsg struct {
	prs  map[string]prInfo
	list []prInfo
}
type prsLoadFailedMsg struct{ err error }

// prListExec is the package-level seam over the `gh pr list` subprocess.
var prListExec = func(ctx context.Context, dir string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "list",
		"--state", "open", "--limit", "100",
		"--json", "number,headRefName,title,author,statusCheckRollup")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, fmt.Errorf("gh pr list: %w", err)
		}
		return nil, fmt.Errorf("gh pr list: %s", firstLine(msg))
	}
	return stdout.Bytes(), nil
}

func prListCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), prListTimeout)
		defer cancel()
		out, err := prListExec(ctx, dir)
		if err != nil {
			return prsLoadFailedMsg{err: err}
		}
		list, err := parsePRList(out)
		if err != nil {
			return prsLoadFailedMsg{err: err}
		}
		prs := make(map[string]prInfo, len(list))
		for _, pr := range list {
			prs[pr.HeadRef] = pr
		}
		return prsLoadedMsg{prs: prs, list: list}
	}
}

// prListItem mirrors the subset of `gh pr list --json` fields the badge
// needs. statusCheckRollup entries are a union of two GraphQL types:
// CheckRun rows carry status/conclusion, StatusContext rows carry state.
type prListItem struct {
	Number      int    `json:"number"`
	HeadRefName string `json:"headRefName"`
	Title       string `json:"title"`
	Author      struct {
		Login string `json:"login"`
	} `json:"author"`
	StatusCheckRollup []struct {
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		State      string `json:"state"`
	} `json:"statusCheckRollup"`
}

// parsePRList turns `gh pr list --json` output into an ordered prInfo slice
// (gh's newest-first order, preserved for the `l` modal). prListCmd folds it
// into the head-branch → prInfo map the chip renderer reads; when two open
// PRs share a head branch the later row wins that map slot — gh orders by
// recency, and the badge only needs "the PR you'd land on".
func parsePRList(data []byte) ([]prInfo, error) {
	var items []prListItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("gh pr list: parse: %w", err)
	}
	list := make([]prInfo, 0, len(items))
	for _, it := range items {
		if it.HeadRefName == "" {
			continue
		}
		state := prChecksNone
		for _, c := range it.StatusCheckRollup {
			state = worseCheckState(state, classifyCheck(c.Status, c.Conclusion, c.State))
		}
		list = append(list, prInfo{
			Number:  it.Number,
			HeadRef: it.HeadRefName,
			Title:   it.Title,
			Author:  it.Author.Login,
			Checks:  state,
		})
	}
	return list, nil
}

// classifyCheck maps one rollup context onto the 3-way verdict. state is
// the StatusContext field (SUCCESS / PENDING / FAILURE / ERROR / EXPECTED);
// when it's empty the row is a CheckRun and status ("COMPLETED" or a
// not-finished-yet value) + conclusion decide.
func classifyCheck(status, conclusion, state string) prCheckState {
	if state != "" {
		switch state {
		case "SUCCESS":
			return prChecksPassing
		case "PENDING", "EXPECTED":
			return prChecksPending
		default: // FAILURE / ERROR
			return prChecksFailing
		}
	}
	if status != "COMPLETED" {
		return prChecksPending
	}
	switch conclusion {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return prChecksPassing
	default: // FAILURE / TIMED_OUT / CANCELLED / ACTION_REQUIRED / …
		return prChecksFailing
	}
}

// worseCheckState folds two verdicts: failing dominates pending dominates
// passing — the same priority GitHub's own rollup icon uses.
func worseCheckState(a, b prCheckState) prCheckState {
	if a == prChecksFailing || b == prChecksFailing {
		return prChecksFailing
	}
	if a == prChecksPending || b == prChecksPending {
		return prChecksPending
	}
	if a == prChecksPassing || b == prChecksPassing {
		return prChecksPassing
	}
	return prChecksNone
}
