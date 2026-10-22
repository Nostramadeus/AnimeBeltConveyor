// Package animeschedule reads weekly sub/dub timetables from the
// animeschedule.net API v3 (SPEC 4). Off by default; the caller decides
// whether to fetch at all.
//
// Response shape (from https://animeschedule.net/api/v3/documentation/anime,
// read 2026-10-22): GET /timetables/{airType}?week=&year= returns timetable
// anime with fields title, route, romaji, english, native, episodeDate
// (ISO 8601, UTC), episodeNumber, airType, status, ... The documented
// timetable entry has NO AniList id; that only appears on the anime
// endpoint as websites.aniList. The decoder is lenient: it accepts a bare
// array or an object wrapping one, and reads an AniList id from
// anilist_id / anilistId / websites.aniList when present. Otherwise
// AniListID is 0 and the caller matches by normalized romaji title.
package animeschedule

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// BaseURL is the API root. Tests replace it.
var BaseURL = "https://animeschedule.net/api/v3"

// HTTPClient is used for requests. Tests or callers may replace it.
var HTTPClient = &http.Client{Timeout: 30 * time.Second}

// Entry is one episode slot in a timetable.
type Entry struct {
	AniListID int    `json:"anilist_id"` // 0 when the response has none
	Title     string `json:"title"`      // romaji when given, else the display title
	English   string `json:"english"`
	Route     string `json:"route"` // animeschedule's slug
	Episode   int    `json:"episode"`
	AirsAt    int64  `json:"airs_at"` // unix seconds UTC, 0 when unknown
	Kind      string `json:"kind"`    // raw, sub or dub
}

type rawEntry struct {
	Title         string          `json:"title"`
	Route         string          `json:"route"`
	Romaji        string          `json:"romaji"`
	English       string          `json:"english"`
	EpisodeDate   string          `json:"episodeDate"`
	EpisodeNumber json.RawMessage `json:"episodeNumber"`
	AirType       string          `json:"airType"`
	AniListSnake  json.RawMessage `json:"anilist_id"`
	AniListCamel  json.RawMessage `json:"anilistId"` // also matches aniListId (case-insensitive)
	Websites      *struct {
		AniList string `json:"aniList"`
	} `json:"websites"`
}

// Timetable fetches one week. kind is raw, sub, dub or all. year and week
// (the week number within the year; ISO week assumed, see PROGRESS) may both
// be 0 for the current week.
func Timetable(ctx context.Context, token, kind string, year, week int) ([]Entry, error) {
	switch kind {
	case "raw", "sub", "dub", "all":
	default:
		return nil, fmt.Errorf("animeschedule: kind %q is not raw, sub, dub or all", kind)
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("animeschedule: an API token is required")
	}
	u := strings.TrimRight(BaseURL, "/") + "/timetables/" + kind
	if year > 0 && week > 0 {
		v := url.Values{}
		v.Set("week", strconv.Itoa(week))
		v.Set("year", strconv.Itoa(year))
		u += "?" + v.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return nil, fmt.Errorf("animeschedule: HTTP %d: %s", resp.StatusCode, msg)
	}
	return Decode(data, kind)
}

// Decode parses a timetable response body. defaultKind fills Entry.Kind
// when an entry has no airType (and kind is not "all").
func Decode(data []byte, defaultKind string) ([]Entry, error) {
	arr, err := findArray(data)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(arr))
	for _, raw := range arr {
		var r rawEntry
		if err := json.Unmarshal(raw, &r); err != nil {
			continue // one odd entry must not drop the week
		}
		e := Entry{
			AniListID: aniListID(&r),
			Title:     strings.TrimSpace(r.Romaji),
			English:   strings.TrimSpace(r.English),
			Route:     r.Route,
			Episode:   intFrom(r.EpisodeNumber),
			AirsAt:    unix(r.EpisodeDate),
			Kind:      strings.ToLower(strings.TrimSpace(r.AirType)),
		}
		if e.Title == "" {
			e.Title = strings.TrimSpace(r.Title)
		}
		if e.Kind == "" && defaultKind != "all" {
			e.Kind = defaultKind
		}
		if e.Title == "" && e.AniListID == 0 {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// findArray returns the entries whether the body is an array or an object
// holding one (first array-valued field, preferring common names).
func findArray(data []byte) ([]json.RawMessage, error) {
	data = bytes.TrimSpace(data)
	var arr []json.RawMessage
	if len(data) > 0 && data[0] == '[' {
		if err := json.Unmarshal(data, &arr); err != nil {
			return nil, fmt.Errorf("animeschedule: bad JSON: %w", err)
		}
		return arr, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, fmt.Errorf("animeschedule: bad JSON: %w", err)
	}
	for _, k := range []string{"data", "timetables", "timetable", "anime", "entries", "results"} {
		if v, ok := obj[k]; ok && json.Unmarshal(v, &arr) == nil {
			return arr, nil
		}
	}
	for _, v := range obj {
		if t := bytes.TrimSpace(v); len(t) > 0 && t[0] == '[' && json.Unmarshal(t, &arr) == nil {
			return arr, nil
		}
	}
	return nil, nil
}

var reAniListURL = regexp.MustCompile(`anilist\.co/anime/(\d+)`)

func aniListID(r *rawEntry) int {
	if n := intFrom(r.AniListSnake); n > 0 {
		return n
	}
	if n := intFrom(r.AniListCamel); n > 0 {
		return n
	}
	if r.Websites != nil {
		if m := reAniListURL.FindStringSubmatch(r.Websites.AniList); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n
		}
	}
	return 0
}

// intFrom reads a JSON number or numeric string; anything else is 0.
func intFrom(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return int(f)
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		n, _ := strconv.Atoi(strings.TrimSpace(s))
		return n
	}
	return 0
}

// unix parses an ISO 8601 time; the API's null date 0001-01-01T00:00:00Z
// and unparsable values give 0.
func unix(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.Year() <= 1 {
		return 0
	}
	return t.Unix()
}
