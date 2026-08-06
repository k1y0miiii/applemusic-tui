package lastfm

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSignFollowsLastfmsRecipe(t *testing.T) {
	// Sorted by name, name and value run together with no separator, secret on
	// the end, MD5 of the lot. Worked by hand so the test is not just the
	// implementation restated.
	params := map[string]string{
		"track":  "Song",
		"artist": "Band",
		"method": "track.scrobble",
		"format": "json", // excluded from the signature
	}
	want := md5.Sum([]byte("artistBandmethodtrack.scrobbletrackSong" + "s3cret"))
	if got := sign(params, "s3cret"); got != hex.EncodeToString(want[:]) {
		t.Errorf("sign() = %s, want %s", got, hex.EncodeToString(want[:]))
	}
}

func TestSignIgnoresFormatAndAnyPriorSignature(t *testing.T) {
	base := map[string]string{"a": "1", "b": "2"}
	withNoise := map[string]string{"a": "1", "b": "2", "format": "json", "api_sig": "stale"}
	if sign(base, "k") != sign(withNoise, "k") {
		t.Error("format or api_sig leaked into the signature")
	}
}

func TestScrobblePointFollowsTheRules(t *testing.T) {
	cases := []struct {
		duration time.Duration
		want     time.Duration
	}{
		{10 * time.Second, 0},                // too short to ever scrobble
		{29 * time.Second, 0},                // still under the floor
		{30 * time.Second, 15 * time.Second}, // exactly at the floor: half of it
		{3 * time.Minute, 90 * time.Second},  // half
		{10 * time.Minute, 4 * time.Minute},  // capped at four minutes
	}
	for _, c := range cases {
		if got := ScrobblePoint(c.duration); got != c.want {
			t.Errorf("ScrobblePoint(%v) = %v, want %v", c.duration, got, c.want)
		}
	}
}

func stubServer(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	old := baseURL
	baseURL = srv.URL
	t.Cleanup(func() { baseURL = old })

	return &Client{APIKey: "key", APISecret: "secret", SessionKey: "session", HTTP: srv.Client()}
}

func TestScrobblePostsTheTrackAndTimestamp(t *testing.T) {
	var got map[string]string
	client := stubServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("scrobble used %s, want POST", r.Method)
		}
		r.ParseForm()
		got = map[string]string{}
		for k := range r.PostForm {
			got[k] = r.PostForm.Get(k)
		}
		w.Write([]byte(`{"scrobbles":{"@attr":{"accepted":1}}}`))
	})

	started := time.Unix(1_700_000_000, 0)
	err := client.Scrobble(context.Background(), Track{
		Title: "Song", Artist: "Band", Album: "Record", Duration: 3 * time.Minute,
	}, started)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"method":    "track.scrobble",
		"artist":    "Band",
		"track":     "Song",
		"album":     "Record",
		"duration":  "180",
		"sk":        "session",
		"api_key":   "key",
		"timestamp": "1700000000",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if got["api_sig"] == "" {
		t.Error("request went out unsigned")
	}
}

func TestErrorInsideA200IsStillAnError(t *testing.T) {
	// Last.fm answers 200 with an error body, so trusting the status code alone
	// would report a silent failure as a successful scrobble.
	client := stubServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"error":9,"message":"Invalid session key"}`))
	})
	err := client.Scrobble(context.Background(), Track{Title: "S", Artist: "A"}, time.Now())
	if err == nil {
		t.Fatal("a 200 carrying an error body was treated as success")
	}
	if !strings.Contains(err.Error(), "Invalid session key") {
		t.Errorf("error lost the message from Last.fm: %v", err)
	}
}

func TestSessionReadsTheKey(t *testing.T) {
	client := stubServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"session":{"name":"listener","key":"abc123"}}`))
	})
	key, user, err := client.Session(context.Background(), "token")
	if err != nil {
		t.Fatal(err)
	}
	if key != "abc123" || user != "listener" {
		t.Errorf("got key=%q user=%q", key, user)
	}
}
