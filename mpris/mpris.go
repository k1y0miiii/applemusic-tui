//go:build linux

// Package mpris exposes the player on D-Bus so the desktop can drive it:
// media keys, panel widgets, playerctl and the lock screen all speak MPRIS and
// none of them can see a TUI otherwise.
package mpris

import (
	"fmt"
	"os"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

const (
	objectPath = "/org/mpris/MediaPlayer2"
	rootIface  = "org.mpris.MediaPlayer2"
	playIface  = "org.mpris.MediaPlayer2.Player"
)

type Server struct {
	conn  *dbus.Conn
	props *prop.Properties
	ctl   Controls

	last State
}

// Publish claims the bus name and exports the player. A desktop without a
// session bus is normal — a TTY, a container, a remote shell — so the caller
// treats an error as "no MPRIS" rather than as a failure to start.
func Publish(ctl Controls) (*Server, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, err
	}

	s := &Server{conn: conn, ctl: ctl}

	if err := conn.Export(rootHandler{s}, objectPath, rootIface); err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.ExportWithMap(playerHandler{s}, playerNameMap, objectPath, playIface); err != nil {
		conn.Close()
		return nil, err
	}

	props, err := prop.Export(conn, objectPath, s.propSpec())
	if err != nil {
		conn.Close()
		return nil, err
	}
	s.props = props

	node := &introspect.Node{
		Name: objectPath,
		Interfaces: []introspect.Interface{
			introspect.IntrospectData,
			prop.IntrospectData,
			{Name: rootIface, Methods: introspect.Methods(rootHandler{s}),
				Properties: props.Introspection(rootIface)},
			{Name: playIface, Methods: playerMethods(s),
				Properties: props.Introspection(playIface)},
		},
	}
	if err := conn.Export(introspect.NewIntrospectable(node), objectPath,
		"org.freedesktop.DBus.Introspectable"); err != nil {
		conn.Close()
		return nil, err
	}

	// The spec allows several instances of one player; take the plain name when
	// it is free and fall back to a pid-suffixed one rather than stealing it.
	name := "org.mpris.MediaPlayer2.amtui"
	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		name = fmt.Sprintf("%s.instance%d", name, os.Getpid())
		if reply, err = conn.RequestName(name, dbus.NameFlagDoNotQueue); err != nil {
			conn.Close()
			return nil, err
		}
		if reply != dbus.RequestNameReplyPrimaryOwner {
			conn.Close()
			return nil, fmt.Errorf("mpris: could not claim %s", name)
		}
	}
	return s, nil
}

func (s *Server) Close() {
	if s != nil && s.conn != nil {
		s.conn.Close()
	}
}

func (s *Server) propSpec() map[string]map[string]*prop.Prop {
	ro := func(v any) *prop.Prop {
		return &prop.Prop{Value: v, Writable: false, Emit: prop.EmitTrue}
	}
	return map[string]map[string]*prop.Prop{
		rootIface: {
			"CanQuit":             ro(true),
			"CanRaise":            ro(false), // there is no window to raise
			"HasTrackList":        ro(false),
			"Identity":            ro("amtui"),
			"DesktopEntry":        ro("amtui"),
			"SupportedUriSchemes": ro([]string{}),
			"SupportedMimeTypes":  ro([]string{}),
		},
		playIface: {
			"PlaybackStatus": ro("Stopped"),
			"LoopStatus":     ro("None"),
			"Rate":           ro(1.0),
			"Shuffle":        ro(false),
			"Metadata":       ro(map[string]dbus.Variant{}),
			"Volume": {
				Value: 1.0, Writable: true, Emit: prop.EmitTrue,
				Callback: func(c *prop.Change) *dbus.Error {
					v, ok := c.Value.(float64)
					if !ok {
						return dbus.MakeFailedError(fmt.Errorf("volume must be a double"))
					}
					return toDBusError(s.ctl.SetVolume(int(clamp(v, 0, 1) * 100)))
				},
			},
			"Position":      ro(int64(0)),
			"MinimumRate":   ro(1.0),
			"MaximumRate":   ro(1.0),
			"CanGoNext":     ro(true),
			"CanGoPrevious": ro(true),
			"CanPlay":       ro(true),
			"CanPause":      ro(true),
			"CanSeek":       ro(true),
			"CanControl":    ro(true),
		},
	}
}

// Update publishes a new snapshot. Only what actually changed is written, since
// every write emits PropertiesChanged and Position ticks 30 times a second.
func (s *Server) Update(st State) {
	if s == nil || s.props == nil {
		return
	}
	if st.Playing != s.last.Playing {
		s.props.SetMust(playIface, "PlaybackStatus", playbackStatus(st.Playing))
	}
	if st.Volume != s.last.Volume {
		s.props.SetMust(playIface, "Volume", float64(st.Volume)/100)
	}
	if st.Repeat != s.last.Repeat {
		s.props.SetMust(playIface, "LoopStatus", loopStatus(st.Repeat))
	}
	if st.Shuffle != s.last.Shuffle {
		s.props.SetMust(playIface, "Shuffle", st.Shuffle)
	}
	if st.TrackID != s.last.TrackID || st.Title != s.last.Title ||
		st.Duration != s.last.Duration {
		s.props.SetMust(playIface, "Metadata", metadata(st))
	}
	// Position is read on demand by clients, not signalled, so it is set
	// without emitting. A jump gets an explicit Seeked signal instead.
	s.props.SetMust(playIface, "Position", st.Position.Microseconds())
	if jumped(s.last.Position, st.Position) && st.TrackID == s.last.TrackID {
		s.conn.Emit(objectPath, playIface+".Seeked", st.Position.Microseconds())
	}
	s.last = st
}

type rootHandler struct{ s *Server }

func (h rootHandler) Raise() *dbus.Error { return nil } // CanRaise is false
func (h rootHandler) Quit() *dbus.Error {
	h.s.ctl.Quit()
	return nil
}

// playerNameMap renames Go methods to their D-Bus names.
var playerNameMap = map[string]string{"SeekBy": "Seek"}

// playerMethods reports the interface under the names actually exported, so
// introspection matches what a client can call.
func playerMethods(s *Server) []introspect.Method {
	methods := introspect.Methods(playerHandler{s})
	for i, m := range methods {
		if name, ok := playerNameMap[m.Name]; ok {
			methods[i].Name = name
		}
	}
	return methods
}

type playerHandler struct{ s *Server }

func (h playerHandler) PlayPause() *dbus.Error { return toDBusError(h.s.ctl.PlayPause()) }
func (h playerHandler) Next() *dbus.Error      { return toDBusError(h.s.ctl.Next()) }
func (h playerHandler) Previous() *dbus.Error  { return toDBusError(h.s.ctl.Prev()) }

// Play and Pause are separate methods in the spec, but the engine only offers a
// toggle, so they act on the state we last published rather than blindly
// toggling — a Pause on an already-paused player must not start it.
func (h playerHandler) Play() *dbus.Error {
	if h.s.last.Playing {
		return nil
	}
	return toDBusError(h.s.ctl.PlayPause())
}

func (h playerHandler) Pause() *dbus.Error {
	if !h.s.last.Playing {
		return nil
	}
	return toDBusError(h.s.ctl.PlayPause())
}

// Stop has no equivalent: MusicKit has no stop, and pausing is the closest
// honest thing.
func (h playerHandler) Stop() *dbus.Error { return h.Pause() }

// SeekBy is exported as Seek. The Go name differs because a method called
// Seek taking an int64 trips vet's io.Seeker check, and vet runs in CI.
func (h playerHandler) SeekBy(offset int64) *dbus.Error {
	target := h.s.last.Position + time.Duration(offset)*time.Microsecond
	return toDBusError(h.s.ctl.SeekTo(max(0, min(target, h.s.last.Duration))))
}

func (h playerHandler) SetPosition(_ dbus.ObjectPath, position int64) *dbus.Error {
	target := time.Duration(position) * time.Microsecond
	return toDBusError(h.s.ctl.SeekTo(max(0, min(target, h.s.last.Duration))))
}

func (h playerHandler) OpenUri(string) *dbus.Error {
	return dbus.MakeFailedError(fmt.Errorf("amtui cannot open URIs"))
}
