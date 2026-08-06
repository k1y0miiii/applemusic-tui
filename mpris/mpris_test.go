package mpris

import (
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestTrackIDBecomesAValidObjectPath(t *testing.T) {
	// Apple's ids carry dots and dashes; a D-Bus object path allows only
	// [A-Za-z0-9_] between slashes, and strict clients drop the whole metadata
	// when the path is malformed.
	md := metadata(State{TrackID: "i.1443109064-us.2", Title: "Song"})
	path, ok := md["mpris:trackid"].Value().(dbus.ObjectPath)
	if !ok {
		t.Fatalf("mpris:trackid is %T, want dbus.ObjectPath", md["mpris:trackid"].Value())
	}
	if !path.IsValid() {
		t.Errorf("%q is not a valid object path", path)
	}
}

func TestNoTrackGetsTheSpecsPlaceholderPath(t *testing.T) {
	md := metadata(State{})
	path := md["mpris:trackid"].Value().(dbus.ObjectPath)
	if want := dbus.ObjectPath("/org/mpris/MediaPlayer2/TrackList/NoTrack"); path != want {
		t.Errorf("empty track gave %q, want %q", path, want)
	}
	if !path.IsValid() {
		t.Errorf("%q is not a valid object path", path)
	}
}

func TestMetadataUsesTheTypesClientsExpect(t *testing.T) {
	md := metadata(State{
		TrackID: "abc", Title: "Song", Artist: "Band", Album: "Record",
		Duration: 3 * time.Minute, ArtURL: "https://example.invalid/a.jpg",
	})
	if _, ok := md["mpris:length"].Value().(int64); !ok {
		t.Errorf("mpris:length is %T, want int64 microseconds", md["mpris:length"].Value())
	}
	if got := md["mpris:length"].Value().(int64); got != 180_000_000 {
		t.Errorf("mpris:length = %d, want 180000000", got)
	}
	// xesam:artist is a list even for one artist; a bare string is a common
	// mistake that makes clients show nothing.
	if _, ok := md["xesam:artist"].Value().([]string); !ok {
		t.Errorf("xesam:artist is %T, want []string", md["xesam:artist"].Value())
	}
}

func TestArtUrlIsOmittedRatherThanEmpty(t *testing.T) {
	if _, ok := metadata(State{TrackID: "a"})["mpris:artUrl"]; ok {
		t.Error("mpris:artUrl present with no artwork; clients render a broken image")
	}
}

func TestLoopAndPlaybackStatusUseSpecStrings(t *testing.T) {
	for repeat, want := range map[int]string{0: "None", 1: "Track", 2: "Playlist"} {
		if got := loopStatus(repeat); got != want {
			t.Errorf("loopStatus(%d) = %q, want %q", repeat, got, want)
		}
	}
	if got := playbackStatus(true); got != "Playing" {
		t.Errorf("playbackStatus(true) = %q", got)
	}
	if got := playbackStatus(false); got != "Paused" {
		t.Errorf("playbackStatus(false) = %q", got)
	}
}

func TestJumpedTellsSeeksFromPlayback(t *testing.T) {
	// Seeked must fire on a jump and stay quiet during ordinary playback, or
	// panel widgets fight the position they are being told.
	cases := []struct {
		name          string
		before, after time.Duration
		want          bool
	}{
		{"one poll of playback", 10 * time.Second, 10500 * time.Millisecond, false},
		{"a slow poll", 10 * time.Second, 11 * time.Second, false},
		{"seek forward", 10 * time.Second, 90 * time.Second, true},
		{"seek back", 90 * time.Second, 10 * time.Second, true},
		{"restart", 200 * time.Second, 0, true},
	}
	for _, c := range cases {
		if got := jumped(c.before, c.after); got != c.want {
			t.Errorf("%s: jumped(%v, %v) = %v, want %v", c.name, c.before, c.after, got, c.want)
		}
	}
}
