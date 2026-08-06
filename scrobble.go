package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/k1y0miiii/applemusic-tui/engine"
	"github.com/k1y0miiii/applemusic-tui/lastfm"
)

// Scrobbling counts time actually listened rather than reading the playback
// position, because a seek would otherwise hand Last.fm a play that never
// happened — jump to the end of a track and the position alone says it was
// heard in full.

// The session key does not expire, and it lives in its own one-line file next
// to the theme so writing it never rewrites config.toml and loses the comments
// around the API credentials.
func lastfmSessionFile() string {
	d := configDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "lastfm")
}

func loadLastfmSession() string {
	p := lastfmSessionFile()
	if p == "" {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func saveLastfmSession(key string) error {
	p := lastfmSessionFile()
	if p == "" {
		return fmt.Errorf("no config directory to write the session key to")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	// The session key is a credential: it authorizes writes to the account for
	// as long as it lives, so keep it off other users' eyes.
	return os.WriteFile(p, []byte(key+"\n"), 0o600)
}

// newScrobbler returns nil when Last.fm is not set up, which is the normal
// case; scrobbling is opt-in and its absence must be silent.
func newScrobbler(cfg map[string]string) *lastfm.Client {
	key, secret := configString(cfg, "lastfm.api_key"), configString(cfg, "lastfm.api_secret")
	session := loadLastfmSession()
	if key == "" || secret == "" || session == "" {
		return nil
	}
	return &lastfm.Client{APIKey: key, APISecret: secret, SessionKey: session}
}

func scrobbleTrack(t engine.Track) lastfm.Track {
	return lastfm.Track{
		Title:    t.Title,
		Artist:   t.Artist,
		Album:    t.Album,
		Duration: t.Duration,
	}
}

// trackKey identifies a play. Title and artist rather than the MusicKit id
// because the same track repeated is a second scrobble, and the id would not
// tell the two apart on its own.
func trackKey(t engine.Track) string {
	if t.Title == "" {
		return ""
	}
	return t.Artist + "\x00" + t.Title
}

// advanceScrobble is called once per tick. It returns any command to run —
// telling Last.fm what is playing, or recording a finished play.
func (m *model) advanceScrobble(elapsed time.Duration) tea.Cmd {
	if m.scrobbler == nil {
		return nil
	}
	now := m.st.Now
	key := trackKey(now)
	if key == "" {
		m.scrKey, m.scrPlayed, m.scrDone = "", 0, false
		return nil
	}

	if key != m.scrKey {
		m.scrKey, m.scrPlayed, m.scrDone = key, 0, false
		m.scrStart = time.Now()
		client, track := m.scrobbler, scrobbleTrack(now)
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			// A failed now-playing update is cosmetic; the history that matters
			// is the scrobble, so do not spend a notification on it.
			_ = client.NowPlaying(ctx, track)
			return nil
		}
	}

	if !m.st.Playing || m.scrDone {
		return nil
	}
	m.scrPlayed += elapsed

	point := lastfm.ScrobblePoint(now.Duration)
	if point == 0 || m.scrPlayed < point {
		return nil
	}
	m.scrDone = true
	client, track, started := m.scrobbler, scrobbleTrack(now), m.scrStart
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := client.Scrobble(ctx, track, started); err != nil {
			return noteMsg(err.Error())
		}
		return noteMsg("scrobbled · " + track.Title)
	}
}

// runLastfmAuth is the one-time `amtui lastfm-auth` flow. Last.fm's desktop
// auth needs a browser round trip that cannot be automated, so this prints the
// URL and waits rather than pretending otherwise.
func runLastfmAuth() int {
	cfg := loadConfig()
	key, secret := configString(cfg, "lastfm.api_key"), configString(cfg, "lastfm.api_secret")
	if key == "" || secret == "" {
		fmt.Fprintln(os.Stderr, "Set api_key and api_secret under [lastfm] in")
		fmt.Fprintf(os.Stderr, "  %s\n", filepath.Join(configDir(), "config.toml"))
		fmt.Fprintln(os.Stderr, "Create them at https://www.last.fm/api/account/create")
		return 1
	}

	client := &lastfm.Client{APIKey: key, APISecret: secret}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	token, err := client.Token(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	fmt.Println("Open this and approve amtui:")
	fmt.Println()
	fmt.Println("   " + client.AuthURL(token))
	fmt.Println()
	fmt.Print("Press Enter once you have approved it… ")
	bufio.NewReader(os.Stdin).ReadString('\n')

	// The token is only exchangeable after approval, so this call is also the
	// check that the user actually did it.
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Minute)
	defer cancel2()
	session, user, err := client.Session(ctx2, token)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "If you have not approved the link yet, run this again.")
		return 1
	}
	if err := saveLastfmSession(session); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("Scrobbling to Last.fm as %s.\n", user)
	return 0
}
