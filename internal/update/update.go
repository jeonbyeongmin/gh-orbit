// Package update powers gh-orbit's self-update reminder. gh-orbit ships as a
// `gh` extension, so `gh` — not this binary — owns the install lifecycle: the
// right shape for "auto-update" here is to *check* the latest published
// release and delegate the actual swap to `gh extension upgrade`, never to
// overwrite our own binary behind gh's back. The check is throttled to once a
// day (matching gh's own update-notifier manners) via a tiny cache in the
// XDG state dir.
package update

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jeonbyeongmin/gh-orbit/internal/config"
)

const (
	// repoSlug is the GitHub repository the release check queries.
	repoSlug = "jeonbyeongmin/gh-orbit"
	// extName is the installed extension's command name (`gh orbit`), which is
	// also the argument `gh extension upgrade` expects — gh strips the `gh-`
	// prefix from the repo name.
	extName = "orbit"
	// cacheTTL bounds how often a launch hits the network for the release tag.
	cacheTTL = 24 * time.Hour
	// cacheFile lives alongside the debug log under StateDir.
	cacheFile = "update-check.json"
)

// releaseExec is the package-level seam over the `gh release view` subprocess,
// mirroring internal/tui.prListExec so tests can stub the network call.
var releaseExec = func(ctx context.Context) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", "release", "view",
		"--repo", repoSlug, "--json", "tagName", "-q", ".tagName")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("gh release view: %s", firstLine(msg))
		}
		return nil, fmt.Errorf("gh release view: %w", err)
	}
	return stdout.Bytes(), nil
}

// upgradeExec is the seam over `gh extension upgrade orbit`.
var upgradeExec = func(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "gh", "extension", "upgrade", extName)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("gh extension upgrade: %s", firstLine(msg))
		}
		return fmt.Errorf("gh extension upgrade: %w", err)
	}
	return nil
}

// LatestTag returns the newest published release tag (e.g. "v0.6.5").
func LatestTag(ctx context.Context) (string, error) {
	out, err := releaseExec(ctx)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Upgrade runs `gh extension upgrade orbit`. Note: the *running* process keeps
// the old binary — gh replaces the on-disk file, so callers must tell the user
// to restart `gh orbit` rather than expecting a hot swap.
func Upgrade(ctx context.Context) error {
	return upgradeExec(ctx)
}

// Check returns the latest release tag, served from the once-a-day cache when
// it's fresh and re-fetched (and re-cached) when it's stale. A failed network
// fetch surfaces the error; the caller treats that as "no update" and stays
// quiet — the reminder is passive enrichment, never an error on screen.
func Check(ctx context.Context, current string) (string, error) {
	if c, ok := loadCache(); ok && time.Since(c.CheckedAt) < cacheTTL {
		return c.Latest, nil
	}
	latest, err := LatestTag(ctx)
	if err != nil {
		return "", err
	}
	_ = saveCache(checkCache{CheckedAt: time.Now(), Latest: latest})
	return latest, nil
}

// Newer reports whether latest is a strictly higher version than current.
// Both accept an optional "v" prefix and a pre-release/build suffix (compared
// by major.minor.patch only). Any unparseable input — notably the "dev"
// sentinel of a local build — returns false so a non-release build never nags.
func Newer(current, latest string) bool {
	cur, ok1 := parseVersion(current)
	lat, ok2 := parseVersion(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := range cur {
		if lat[i] != cur[i] {
			return lat[i] > cur[i]
		}
	}
	return false
}

// parseVersion splits "v1.2.3" / "1.2.3-rc1" into [major, minor, patch].
func parseVersion(v string) ([3]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// checkCache is the on-disk throttle state: when the last check ran and what
// tag it saw.
type checkCache struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

func cachePath() (string, error) {
	dir, err := config.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, cacheFile), nil
}

// loadCache reads the cache file; a missing or malformed file is "no cache"
// (ok=false), not an error — the caller just re-fetches.
func loadCache() (checkCache, bool) {
	path, err := cachePath()
	if err != nil {
		return checkCache{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return checkCache{}, false
	}
	var c checkCache
	if err := json.Unmarshal(data, &c); err != nil {
		return checkCache{}, false
	}
	return c, true
}

func saveCache(c checkCache) error {
	path, err := cachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// firstLine trims a multi-line subprocess error down to its first line for the
// status row.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
