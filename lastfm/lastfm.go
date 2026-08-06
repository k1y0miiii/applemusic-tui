// Package lastfm scrobbles played tracks to Last.fm.
//
// Nothing here touches the player: scrobbling reads the state amtui already
// polls and posts it over plain HTTP, so a Last.fm outage can slow down a
// scrobble but never playback.
package lastfm

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const endpoint = "https://ws.audioscrobbler.com/2.0/"

// MinDuration and MaxPlayTime come from Last.fm's scrobbling rules: a track
// under 30 seconds is never scrobbled, and one counts as played after half its
// length or four minutes, whichever comes first.
const (
	MinDuration = 30 * time.Second
	MaxPlayTime = 4 * time.Minute
)

type Client struct {
	APIKey     string
	APISecret  string
	SessionKey string

	HTTP *http.Client // nil uses a client with a short timeout
}

type Track struct {
	Title    string
	Artist   string
	Album    string
	Duration time.Duration
}

// ScrobblePoint is how long a track must actually be listened to before it
// counts. Returns 0 when the track is too short to ever scrobble.
func ScrobblePoint(duration time.Duration) time.Duration {
	if duration < MinDuration {
		return 0
	}
	return min(duration/2, MaxPlayTime)
}

// sign is Last.fm's api_sig: every parameter except format, sorted by name,
// concatenated as name then value with no separators, the shared secret
// appended, and the whole thing MD5'd.
func sign(params map[string]string, secret string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "format" || k == "api_sig" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(params[k])
	}
	b.WriteString(secret)
	sum := md5.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// baseURL lets the tests point the client at a stub server.
var baseURL = endpoint

func (c *Client) call(ctx context.Context, method string, params map[string]string, post bool) (map[string]any, error) {
	params["method"] = method
	params["api_key"] = c.APIKey
	params["api_sig"] = sign(params, c.APISecret)
	params["format"] = "json"

	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}

	var req *http.Request
	var err error
	if post {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, baseURL,
			strings.NewReader(form.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"?"+form.Encode(), nil)
	}
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("last.fm: %s returned unreadable JSON: %w", method, err)
	}
	// Last.fm reports its own errors inside a 200, so the status code alone is
	// not enough to tell success from failure.
	if msg, ok := body["message"].(string); ok {
		code := ""
		if n, ok := body["error"].(float64); ok {
			code = fmt.Sprintf(" (%d)", int(n))
		}
		return nil, fmt.Errorf("last.fm: %s%s: %s", method, code, msg)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("last.fm: %s returned %s", method, resp.Status)
	}
	return body, nil
}

func trackParams(t Track) map[string]string {
	params := map[string]string{
		"artist": t.Artist,
		"track":  t.Title,
	}
	if t.Album != "" {
		params["album"] = t.Album
	}
	if t.Duration > 0 {
		params["duration"] = strconv.Itoa(int(t.Duration.Seconds()))
	}
	return params
}

// NowPlaying tells Last.fm what is on right now. It is decoration — the
// listening history comes from Scrobble — so its failure is not worth
// surfacing.
func (c *Client) NowPlaying(ctx context.Context, t Track) error {
	params := trackParams(t)
	params["sk"] = c.SessionKey
	_, err := c.call(ctx, "track.updateNowPlaying", params, true)
	return err
}

// Scrobble records a finished play. startedAt is when the track began, which
// Last.fm wants as a UTC unix timestamp.
func (c *Client) Scrobble(ctx context.Context, t Track, startedAt time.Time) error {
	params := trackParams(t)
	params["sk"] = c.SessionKey
	params["timestamp"] = strconv.FormatInt(startedAt.UTC().Unix(), 10)
	_, err := c.call(ctx, "track.scrobble", params, true)
	return err
}

// AuthURL is where the user approves the token, in their own browser.
func (c *Client) AuthURL(token string) string {
	return "https://www.last.fm/api/auth/?api_key=" + url.QueryEscape(c.APIKey) +
		"&token=" + url.QueryEscape(token)
}

// Token starts the desktop auth flow.
func (c *Client) Token(ctx context.Context) (string, error) {
	body, err := c.call(ctx, "auth.getToken", map[string]string{}, false)
	if err != nil {
		return "", err
	}
	token, _ := body["token"].(string)
	if token == "" {
		return "", fmt.Errorf("last.fm: auth.getToken returned no token")
	}
	return token, nil
}

// Session exchanges an approved token for the session key, which does not
// expire and is what gets saved to disk.
func (c *Client) Session(ctx context.Context, token string) (key, user string, err error) {
	body, err := c.call(ctx, "auth.getSession", map[string]string{"token": token}, false)
	if err != nil {
		return "", "", err
	}
	session, _ := body["session"].(map[string]any)
	key, _ = session["key"].(string)
	user, _ = session["name"].(string)
	if key == "" {
		return "", "", fmt.Errorf("last.fm: auth.getSession returned no session key")
	}
	return key, user, nil
}
