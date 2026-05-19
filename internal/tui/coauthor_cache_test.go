package tui

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jeonbyeongmin/gh-orbit/internal/git"
)

func TestCoAuthorCachePresentVsMiss(t *testing.T) {
	c := newCoAuthorCache()
	if _, ok := c.Lookup("abc"); ok {
		t.Errorf("fresh cache should miss")
	}
	c.Put("abc", []git.AIVendor{git.VendorAnthropic})
	got, ok := c.Lookup("abc")
	if !ok || !reflect.DeepEqual(got, []git.AIVendor{git.VendorAnthropic}) {
		t.Errorf("after Put: got (%+v, %v), want ([anthropic], true)", got, ok)
	}
}

func TestCoAuthorCacheNegativeEntry(t *testing.T) {
	c := newCoAuthorCache()
	c.Put("def", nil)
	got, ok := c.Lookup("def")
	if !ok {
		t.Errorf("negative entry should still report present=true")
	}
	if got != nil {
		t.Errorf("negative entry should have nil vendors, got %+v", got)
	}
}

func TestCoAuthorCacheNilSafe(t *testing.T) {
	var c *coAuthorCache
	if _, ok := c.Lookup("x"); ok {
		t.Errorf("nil cache must report miss without panicking")
	}
	c.Put("x", []git.AIVendor{git.VendorOpenAI}) // must not panic
}

func TestLoadCoAuthorChipCmdSurfacesVendors(t *testing.T) {
	prev := coAuthorDetailExec
	t.Cleanup(func() { coAuthorDetailExec = prev })
	coAuthorDetailExec = func(_ context.Context, _, hash string) (git.Detail, error) {
		return git.Detail{
			Hash: hash,
			Body: "feat: x\n\nCo-Authored-By: A <a@anthropic.com>\nCo-Authored-By: B <b@openai.com>\n",
		}, nil
	}
	msg := loadCoAuthorChipCmd("dir", "deadbeef")()
	loaded, ok := msg.(coAuthorChipLoadedMsg)
	if !ok {
		t.Fatalf("expected coAuthorChipLoadedMsg, got %T", msg)
	}
	if loaded.hash != "deadbeef" {
		t.Errorf("hash = %q, want deadbeef", loaded.hash)
	}
	if want := []git.AIVendor{git.VendorAnthropic, git.VendorOpenAI}; !reflect.DeepEqual(loaded.vendors, want) {
		t.Errorf("vendors = %+v, want %+v", loaded.vendors, want)
	}
}

func TestLoadCoAuthorChipCmdSurfacesNonAIAsNilVendors(t *testing.T) {
	prev := coAuthorDetailExec
	t.Cleanup(func() { coAuthorDetailExec = prev })
	coAuthorDetailExec = func(_ context.Context, _, _ string) (git.Detail, error) {
		return git.Detail{Body: "fix: y\n\nCo-Authored-By: Human <h@example.com>\n"}, nil
	}
	msg := loadCoAuthorChipCmd("dir", "abc")()
	loaded, ok := msg.(coAuthorChipLoadedMsg)
	if !ok {
		t.Fatalf("expected coAuthorChipLoadedMsg, got %T", msg)
	}
	if len(loaded.vendors) != 0 {
		t.Errorf("non-AI body should yield no vendors, got %+v", loaded.vendors)
	}
}

func TestLoadCoAuthorChipCmdRoutesErrorToFailedMsg(t *testing.T) {
	prev := coAuthorDetailExec
	t.Cleanup(func() { coAuthorDetailExec = prev })
	coAuthorDetailExec = func(_ context.Context, _, _ string) (git.Detail, error) {
		return git.Detail{}, errors.New("boom")
	}
	msg := loadCoAuthorChipCmd("dir", "xx")()
	failed, ok := msg.(coAuthorChipFailedMsg)
	if !ok {
		t.Fatalf("expected coAuthorChipFailedMsg, got %T", msg)
	}
	if failed.err == nil || failed.err.Error() != "boom" {
		t.Errorf("expected err 'boom', got %v", failed.err)
	}
}
