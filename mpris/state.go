package mpris

// The conversion from player state to what MPRIS publishes is plain data
// shuffling with no D-Bus calls in it, so it lives outside the build tag and
// gets tested on any host rather than only on Linux.

import (
	"time"

	"github.com/godbus/dbus/v5"
)

// Controls is the slice of the engine MPRIS can reach. Kept to what the spec's
// mandatory methods need, so this package never grows a dependency on the UI.
type Controls interface {
	PlayPause() error
	Next() error
	Prev() error
	SeekTo(time.Duration) error
	SetVolume(int) error
	Quit()
}

// State is a snapshot of what to publish. It mirrors what the UI already polls
// every 500 ms, so nothing extra is asked of the engine.
type State struct {
	TrackID  string
	Title    string
	Artist   string
	Album    string
	ArtURL   string
	Duration time.Duration
	Position time.Duration
	Playing  bool
	Volume   int // 0..100
	Repeat   int // 0 off, 1 one, 2 all
	Shuffle  bool
}

// jumped tells a seek from ordinary playback. Between two polls the position
// should creep forward by well under a second; anything else is a jump.
func jumped(before, now time.Duration) bool {
	delta := now - before
	return delta < 0 || delta > 2*time.Second
}

func playbackStatus(playing bool) string {
	if playing {
		return "Playing"
	}
	return "Paused"
}

func loopStatus(repeat int) string {
	switch repeat {
	case 1:
		return "Track"
	case 2:
		return "Playlist"
	}
	return "None"
}

func metadata(st State) map[string]dbus.Variant {
	// A track id is a D-Bus object path, so it can only hold [A-Za-z0-9_] —
	// Apple's ids do not qualify and an invalid path breaks strict clients.
	path := dbus.ObjectPath("/org/mpris/MediaPlayer2/amtui/track/" + sanitize(st.TrackID))
	if st.TrackID == "" {
		path = "/org/mpris/MediaPlayer2/TrackList/NoTrack"
	}
	md := map[string]dbus.Variant{
		"mpris:trackid": dbus.MakeVariant(path),
		"mpris:length":  dbus.MakeVariant(st.Duration.Microseconds()),
		"xesam:title":   dbus.MakeVariant(st.Title),
		"xesam:artist":  dbus.MakeVariant([]string{st.Artist}),
		"xesam:album":   dbus.MakeVariant(st.Album),
	}
	if st.ArtURL != "" {
		md["mpris:artUrl"] = dbus.MakeVariant(st.ArtURL)
	}
	return md
}

func sanitize(id string) string {
	if id == "" {
		return "none"
	}
	out := make([]rune, 0, len(id))
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

func clamp(v, lo, hi float64) float64 { return max(lo, min(hi, v)) }

func toDBusError(err error) *dbus.Error {
	if err == nil {
		return nil
	}
	return dbus.MakeFailedError(err)
}
