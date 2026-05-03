package tui

import (
	"testing"
	"time"
)

func TestRelativeShortAt(t *testing.T) {
	base := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		delta time.Duration
		want  string
	}{
		{0, "just now"},
		{59 * time.Second, "just now"},
		{60 * time.Second, "1m"},
		{59*time.Minute + 59*time.Second, "59m"},
		{time.Hour, "1h"},
		{23*time.Hour + 59*time.Minute, "23h"},
		{24 * time.Hour, "1d"},
		{6*24*time.Hour + 23*time.Hour, "6d"},
		{7 * 24 * time.Hour, "1w"},
		{29 * 24 * time.Hour, "4w"},
		{30 * 24 * time.Hour, "1mo"},
		{359 * 24 * time.Hour, "11mo"},
		{364 * 24 * time.Hour, "12mo"},
		{365 * 24 * time.Hour, "1y"},
		{2 * 365 * 24 * time.Hour, "2y"},
	}
	for _, c := range cases {
		got := relativeShortAt(base.Add(-c.delta), base)
		if got != c.want {
			t.Errorf("delta %v: got %q, want %q", c.delta, got, c.want)
		}
	}
}

func TestRelativeShortAtFutureTime(t *testing.T) {
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	if got := relativeShortAt(now.Add(2*time.Minute), now); got != "2m" {
		t.Errorf("future time: got %q, want %q", got, "2m")
	}
}
