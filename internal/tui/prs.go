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
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// prListTimeout matches the 30s budget of the other read-only loaders.
const prListTimeout = 30 * time.Second

// prsPollInterval is how often the Pull Requests page re-runs prListCmd while
// it's open and the window is focused, so PR state landed by CI / reviewers /
// merges surfaces without a manual `r`. 30s keeps it fresh without burning the
// GitHub API budget a few-second poll would: `gh pr list` is a remote GraphQL
// call (statusCheckRollup + mergeable are costly), not the local git read the
// Local Changes 1s poll runs — focusFetchThrottle already pegs remote refresh
// at ~60s, so 30s is the floor that stays a good citizen.
const prsPollInterval = 30 * time.Second

// prsPollMsg fires on the poll tick. Self-perpetuating but mode-gated like
// localChangesPollMsg — the handler stops re-arming the moment the page is left.
type prsPollMsg struct{}

func prsPollCmd() tea.Cmd {
	return tea.Tick(prsPollInterval, func(time.Time) tea.Msg {
		return prsPollMsg{}
	})
}

// prCheckState is the one-glyph CI rollup rendered inside a chip's PR badge.
type prCheckState int

const (
	prChecksNone prCheckState = iota
	prChecksPending
	prChecksPassing
	prChecksFailing
)

// prCheck is one CI context from a PR's statusCheckRollup, kept before
// worseCheckState collapses the lot into the single badge glyph so the PR
// checks modal can show *which* check failed. Name / URL are the union of the
// two GraphQL row types: a CheckRun carries name + detailsUrl, a StatusContext
// carries context + targetUrl.
type prCheck struct {
	Name  string
	State prCheckState
	URL   string
}

// checkRank orders checks for the modal: failing first, then pending, then
// passing, then anything else. A stable sort on this keeps gh's rollup order
// within each rank, so the failures a reviewer came for sit at the top.
func checkRank(s prCheckState) int {
	switch s {
	case prChecksFailing:
		return 0
	case prChecksPending:
		return 1
	case prChecksPassing:
		return 2
	default:
		return 3
	}
}

// prReviewState is the PR's review-decision rollup, drawn as a colored dot on
// the Pull Requests page (green approved · red changes · grey pending). None
// covers "no review required by branch protection" — nothing to report.
type prReviewState int

const (
	prReviewNone             prReviewState = iota
	prReviewPending                        // REVIEW_REQUIRED
	prReviewApproved                       // APPROVED
	prReviewChangesRequested               // CHANGES_REQUESTED
)

// prInfo is one open PR. Model.prs keys these by head branch (the chip badge
// lookup); Model.prList keeps them in gh's newest-first order (the Pull
// Requests page). The chip badge only reads Number + Checks; the rest feed the
// page's 3-line cards.
type prInfo struct {
	Number      int
	HeadRef     string
	BaseRef     string
	Title       string
	Author      string
	Checks      prCheckState
	Review      prReviewState
	Conflicting bool // mergeable == CONFLICTING
	Additions   int
	Deletions   int
	Files       int
	UpdatedAt   time.Time
	// CheckRows is every CI context, failures-first, preserved before
	// worseCheckState folded them into Checks. The PR checks modal (`C`)
	// renders these; the chip badge only reads the collapsed Checks glyph.
	CheckRows []prCheck
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
		"--json", "number,headRefName,baseRefName,title,author,statusCheckRollup,reviewDecision,mergeable,additions,deletions,changedFiles,updatedAt")
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

// isGHAuthError reports whether a `gh pr list` failure is a logged-out gh,
// as opposed to no GitHub remote or gh-not-installed (both stay quiet per the
// passive-enrichment contract). gh's auth gate prints a "run: gh auth login"
// line; neither the no-remote ("no known GitHub host") nor the not-found
// ("executable file not found") message mentions auth, so a substring match
// separates the three cleanly.
func isGHAuthError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "auth")
}

// prListItem mirrors the subset of `gh pr list --json` fields the badge
// needs. statusCheckRollup entries are a union of two GraphQL types:
// CheckRun rows carry status/conclusion, StatusContext rows carry state.
type prListItem struct {
	Number      int    `json:"number"`
	HeadRefName string `json:"headRefName"`
	BaseRefName string `json:"baseRefName"`
	Title       string `json:"title"`
	Author      struct {
		Login string `json:"login"`
	} `json:"author"`
	ReviewDecision    string    `json:"reviewDecision"`
	Mergeable         string    `json:"mergeable"`
	Additions         int       `json:"additions"`
	Deletions         int       `json:"deletions"`
	ChangedFiles      int       `json:"changedFiles"`
	UpdatedAt         time.Time `json:"updatedAt"`
	StatusCheckRollup []struct {
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		State      string `json:"state"`
		// name/detailsUrl are the CheckRun fields; context/targetUrl the
		// StatusContext ones. Decoded so the checks modal can label each row
		// and jump to its log — both were dropped before.
		Name       string `json:"name"`
		Context    string `json:"context"`
		DetailsURL string `json:"detailsUrl"`
		TargetURL  string `json:"targetUrl"`
	} `json:"statusCheckRollup"`
}

// parsePRList turns `gh pr list --json` output into an ordered prInfo slice
// (gh's newest-first order, the order the Pull Requests page renders).
// prListCmd folds it into the head-branch → prInfo map the chip renderer reads;
// when two open PRs share a head branch the later row wins that map slot — gh
// orders by recency, and the badge only needs "the PR you'd land on".
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
		var rows []prCheck
		for _, c := range it.StatusCheckRollup {
			cs := classifyCheck(c.Status, c.Conclusion, c.State)
			state = worseCheckState(state, cs)
			name := c.Name
			if name == "" {
				name = c.Context
			}
			url := c.DetailsURL
			if url == "" {
				url = c.TargetURL
			}
			rows = append(rows, prCheck{Name: name, State: cs, URL: url})
		}
		sort.SliceStable(rows, func(i, j int) bool {
			return checkRank(rows[i].State) < checkRank(rows[j].State)
		})
		list = append(list, prInfo{
			Number:      it.Number,
			HeadRef:     it.HeadRefName,
			BaseRef:     it.BaseRefName,
			Title:       it.Title,
			Author:      it.Author.Login,
			Checks:      state,
			Review:      classifyReview(it.ReviewDecision),
			Conflicting: it.Mergeable == "CONFLICTING",
			Additions:   it.Additions,
			Deletions:   it.Deletions,
			Files:       it.ChangedFiles,
			UpdatedAt:   it.UpdatedAt,
			CheckRows:   rows,
		})
	}
	return list, nil
}

// classifyReview maps gh's reviewDecision onto the 3-way dot. "" (no review
// required by branch protection) draws no dot — there's nothing to report.
func classifyReview(decision string) prReviewState {
	switch decision {
	case "APPROVED":
		return prReviewApproved
	case "CHANGES_REQUESTED":
		return prReviewChangesRequested
	case "REVIEW_REQUIRED":
		return prReviewPending
	}
	return prReviewNone
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
