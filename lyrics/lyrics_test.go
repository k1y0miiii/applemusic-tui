package lyrics

import (
	"testing"
	"time"
)

func TestParseLRCAndCurrent(t *testing.T) {
	src := "[ar:Artist]\n[00:12.50] first line\n[00:05] early line\n\nnot a tag\n[01:02.1] later"
	ly := ParseLRC(src)
	if !ly.Synced || len(ly.Lines) != 3 {
		t.Fatalf("want 3 synced lines, got %+v", ly)
	}
	if ly.Lines[0].Text != "early line" || ly.Lines[0].At != 5*time.Second {
		t.Errorf("bad sort/parse: %+v", ly.Lines[0])
	}
	if ly.Lines[1].At != 12500*time.Millisecond {
		t.Errorf("bad fraction: %v", ly.Lines[1].At)
	}
	if got := ly.Current(13 * time.Second); got != 1 {
		t.Errorf("Current(13s) = %d, want 1", got)
	}
	if got := ly.Current(time.Second); got != -1 {
		t.Errorf("Current(1s) = %d, want -1", got)
	}
}

func TestPickPrefersSyncedWithCloseDuration(t *testing.T) {
	rs := []result{
		{Duration: 500, SyncedLyrics: "[00:01] far duration"},
		{Duration: 201, PlainLyrics: "plain close"},
		{Duration: 200, SyncedLyrics: "[00:01] synced close"},
	}
	best := pick(rs, 200)
	if best == nil || best.SyncedLyrics != "[00:01] synced close" {
		t.Errorf("pick chose %+v", best)
	}
}
