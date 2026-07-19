// Package lyrics fetches lyrics from LRCLIB (lrclib.net, free, no keys)
// and parses synced LRC into timestamped lines.
package lyrics

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Line struct {
	At   time.Duration // -1 in plain (unsynced) lyrics
	Text string
}

type Lyrics struct {
	Synced bool
	Lines  []Line
}

var client = &http.Client{Timeout: 8 * time.Second}

type result struct {
	Duration     float64 `json:"duration"`
	Instrumental bool    `json:"instrumental"`
	PlainLyrics  string  `json:"plainLyrics"`
	SyncedLyrics string  `json:"syncedLyrics"`
}

// Fetch looks the track up on LRCLIB and returns the best match:
// synced with a close duration first, any synced second, plain third.
func Fetch(ctx context.Context, artist, title string, dur time.Duration) (Lyrics, error) {
	q := url.Values{"track_name": {title}, "artist_name": {artist}}
	req, err := http.NewRequestWithContext(ctx,
		http.MethodGet, "https://lrclib.net/api/search?"+q.Encode(), nil)
	if err != nil {
		return Lyrics{}, err
	}
	req.Header.Set("User-Agent", "amtui/0.1 (https://github.com/k1y0miiii/applemusic-tui)")
	resp, err := client.Do(req)
	if err != nil {
		return Lyrics{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Lyrics{}, fmt.Errorf("lrclib: %s", resp.Status)
	}
	var results []result
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return Lyrics{}, err
	}
	best := pick(results, dur.Seconds())
	switch {
	case best == nil:
		return Lyrics{}, nil
	case best.Instrumental:
		return Lyrics{Lines: []Line{{At: -1, Text: "· instrumental ·"}}}, nil
	case best.SyncedLyrics != "":
		return ParseLRC(best.SyncedLyrics), nil
	case best.PlainLyrics != "":
		return plain(best.PlainLyrics), nil
	}
	return Lyrics{}, nil
}

func pick(rs []result, durSec float64) *result {
	var bestScore int
	var best *result
	for i := range rs {
		r := &rs[i]
		score := 0
		if r.SyncedLyrics != "" {
			score += 4
		} else if r.PlainLyrics != "" || r.Instrumental {
			score += 1
		} else {
			continue
		}
		if durSec > 0 && math.Abs(r.Duration-durSec) <= 3 {
			score += 2
		}
		if score > bestScore {
			bestScore, best = score, r
		}
	}
	return best
}

var lrcLine = regexp.MustCompile(`^\[(\d+):(\d+)(?:\.(\d+))?\](.*)$`)

// ParseLRC parses "[mm:ss.xx] text" lines; malformed lines are skipped.
func ParseLRC(src string) Lyrics {
	var lines []Line
	for _, raw := range strings.Split(src, "\n") {
		m := lrcLine.FindStringSubmatch(strings.TrimSpace(raw))
		if m == nil {
			continue
		}
		mins, _ := strconv.Atoi(m[1])
		secs, _ := strconv.Atoi(m[2])
		at := time.Duration(mins)*time.Minute + time.Duration(secs)*time.Second
		if m[3] != "" {
			frac, _ := strconv.ParseFloat("0."+m[3], 64)
			at += time.Duration(frac * float64(time.Second))
		}
		lines = append(lines, Line{At: at, Text: strings.TrimSpace(m[4])})
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].At < lines[j].At })
	return Lyrics{Synced: len(lines) > 0, Lines: lines}
}

func plain(src string) Lyrics {
	var lines []Line
	for _, l := range strings.Split(src, "\n") {
		lines = append(lines, Line{At: -1, Text: strings.TrimSpace(l)})
	}
	return Lyrics{Lines: lines}
}

// Current returns the index of the line active at pos, or -1 before the first.
func (l Lyrics) Current(pos time.Duration) int {
	cur := -1
	for i, line := range l.Lines {
		if line.At <= pos {
			cur = i
		} else {
			break
		}
	}
	return cur
}
