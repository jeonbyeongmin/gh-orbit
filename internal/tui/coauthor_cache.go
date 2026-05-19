// Lazy AI-vendor chip cache for the graph row. CEO Q24 D7: row render
// reads the cache; background `git show -s --format=%B <hash>` fetches
// missing entries on demand; results land in a 5000-entry LRU keyed by
// commit hash. CEO D8: cache miss reads as a dim-dot placeholder until
// the fetch lands, then the row repaints with the real chip (or, for
// commits without an AI vendor, no chip at all).
package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

const coAuthorCacheSize = 5000

const coAuthorFetchTimeout = 15 * time.Second

// coAuthorPrefetchCount caps how many of the freshly-streamed top-of-log
// commits get their AI chip prefetched after a graph reload. Picked to
// cover a typical terminal viewport (≤30 visible rows on a tall screen)
// without flooding the OS with parallel `git show` processes — each
// dispatched cmd becomes its own goroutine on the Bubble Tea side.
const coAuthorPrefetchCount = 30

// coAuthorCacheEntry is the per-commit cached result. vendors is nil for
// commits without an AI trailer; the cache distinguishes "fetched + no
// AI" (entry present, vendors nil) from "not yet fetched" (cache miss).
type coAuthorCacheEntry struct {
	vendors []git.AIVendor
}

// coAuthorCache is a thin façade over golang-lru/v2 so the rest of the
// package only sees Get/Put with the cockpit's value shape. Constructor
// returns nil-but-usable when the LRU init fails (defensive — Lookup
// returns "miss" so the row falls back to the dim-dot placeholder
// instead of crashing).
type coAuthorCache struct {
	store *lru.Cache[string, coAuthorCacheEntry]
}

func newCoAuthorCache() *coAuthorCache {
	c, err := lru.New[string, coAuthorCacheEntry](coAuthorCacheSize)
	if err != nil {
		return &coAuthorCache{}
	}
	return &coAuthorCache{store: c}
}

// Lookup reports the cached vendors and whether the hash was present. A
// present entry with nil vendors means "fetched, no AI vendor"; a missing
// entry means "not yet fetched" — the row layer renders a dim dot
// placeholder for the latter so the user can see fetch is in progress.
func (c *coAuthorCache) Lookup(hash string) (vendors []git.AIVendor, ok bool) {
	if c == nil || c.store == nil || hash == "" {
		return nil, false
	}
	entry, found := c.store.Get(hash)
	if !found {
		return nil, false
	}
	return entry.vendors, true
}

// Put records the fetch result. Empty body / missing AI trailer still
// stores an entry (with nil vendors) so the cache acts as a "we asked,
// nothing AI here" memo and prevents re-fetching the same commit every
// time the row scrolls into view.
func (c *coAuthorCache) Put(hash string, vendors []git.AIVendor) {
	if c == nil || c.store == nil || hash == "" {
		return
	}
	c.store.Add(hash, coAuthorCacheEntry{vendors: vendors})
}

// coAuthorChipLoadedMsg lands when a lazy fetch completes. vendors is
// nil for non-AI commits — the Model writes the entry into the cache
// either way so the next render skips the dim dot.
type coAuthorChipLoadedMsg struct {
	hash    string
	vendors []git.AIVendor
}

// coAuthorChipFailedMsg surfaces fetch errors. The Model still writes a
// negative cache entry (nil vendors) so a flaky git fetch doesn't loop
// forever re-dispatching the same hash.
type coAuthorChipFailedMsg struct {
	hash string
	err  error
}

// Package-level seam over git.CommitDetail so the lazy-fetch cmd suite
// stays hermetic in unit tests.
var coAuthorDetailExec = git.CommitDetail

// loadCoAuthorChipCmd dispatches a single commit-body fetch and routes
// its result through CommitAIVendors. Callers run a tea.Batch over many
// hashes for prefetch; individual cache misses fire one at a time.
func loadCoAuthorChipCmd(dir, hash string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), coAuthorFetchTimeout)
		defer cancel()
		d, err := coAuthorDetailExec(ctx, dir, hash)
		if err != nil {
			return coAuthorChipFailedMsg{hash: hash, err: err}
		}
		return coAuthorChipLoadedMsg{hash: hash, vendors: git.CommitAIVendors(d.Body)}
	}
}

// coAuthorPrefetchCmds composes a tea.Batch covering the first `count`
// graph rows whose hash is neither cached nor already in flight. nil
// return means "nothing to prefetch" (cold cache after a reload would
// still produce a batch; this guard fires after warm reloads where the
// cache already covers the top of the log). Mutates m.coAuthorInFlight
// to mark each dispatched hash so a second prefetch round can't double-
// dispatch the same commit.
func (m *Model) coAuthorPrefetchCmds(count int) tea.Cmd {
	hashes := m.graph.FirstHashes(count)
	cmds := make([]tea.Cmd, 0, len(hashes))
	for _, h := range hashes {
		if _, cached := m.coAuthorCache.Lookup(h); cached {
			continue
		}
		if _, inFlight := m.coAuthorInFlight[h]; inFlight {
			continue
		}
		m.coAuthorInFlight[h] = struct{}{}
		cmds = append(cmds, loadCoAuthorChipCmd(m.workdir, h))
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}
