package update

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v0.6.5", "v0.6.6", true},
		{"v0.6.5", "v0.7.0", true},
		{"v0.6.5", "v1.0.0", true},
		{"0.6.5", "0.6.6", true},        // no "v" prefix
		{"v0.6.5", "v0.6.5", false},     // equal
		{"v0.6.6", "v0.6.5", false},     // older
		{"v0.6.5", "v0.6.5-rc1", false}, // suffix compares by core only → equal
		{"v0.6.5-rc1", "v0.6.5", false},
		{"dev", "v0.6.5", false}, // local build never nags
		{"v0.6.5", "garbage", false},
		{"", "v0.6.5", false},
	}
	for _, c := range cases {
		if got := Newer(c.current, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestLatestTag(t *testing.T) {
	orig := releaseExec
	t.Cleanup(func() { releaseExec = orig })
	releaseExec = func(context.Context) ([]byte, error) {
		return []byte("v0.6.5\n"), nil
	}
	got, err := LatestTag(context.Background())
	if err != nil {
		t.Fatalf("LatestTag: %v", err)
	}
	if got != "v0.6.5" {
		t.Errorf("LatestTag = %q, want v0.6.5", got)
	}
}

func TestCheck_UsesFreshCache(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := saveCache(checkCache{CheckedAt: time.Now(), Latest: "v0.6.5"}); err != nil {
		t.Fatalf("saveCache: %v", err)
	}
	orig := releaseExec
	t.Cleanup(func() { releaseExec = orig })
	calls := 0
	releaseExec = func(context.Context) ([]byte, error) {
		calls++
		return []byte("v9.9.9"), nil
	}
	got, err := Check(context.Background(), "v0.6.5")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got != "v0.6.5" {
		t.Errorf("Check = %q, want cached v0.6.5", got)
	}
	if calls != 0 {
		t.Errorf("fresh cache should not hit the network, got %d calls", calls)
	}
}

func TestCheck_RefetchesStaleCache(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	stale := time.Now().Add(-2 * cacheTTL)
	if err := saveCache(checkCache{CheckedAt: stale, Latest: "v0.6.5"}); err != nil {
		t.Fatalf("saveCache: %v", err)
	}
	orig := releaseExec
	t.Cleanup(func() { releaseExec = orig })
	calls := 0
	releaseExec = func(context.Context) ([]byte, error) {
		calls++
		return []byte("v0.7.0"), nil
	}
	got, err := Check(context.Background(), "v0.6.5")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got != "v0.7.0" {
		t.Errorf("Check = %q, want refetched v0.7.0", got)
	}
	if calls != 1 {
		t.Errorf("stale cache should refetch once, got %d calls", calls)
	}
	// The refetched tag should now be cached for the next launch.
	if c, ok := loadCache(); !ok || c.Latest != "v0.7.0" {
		t.Errorf("cache not refreshed: %+v ok=%v", c, ok)
	}
}

func TestCheck_NetworkError(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	orig := releaseExec
	t.Cleanup(func() { releaseExec = orig })
	releaseExec = func(context.Context) ([]byte, error) {
		return nil, errors.New("boom")
	}
	if _, err := Check(context.Background(), "v0.6.5"); err == nil {
		t.Error("Check should surface the network error")
	}
}
