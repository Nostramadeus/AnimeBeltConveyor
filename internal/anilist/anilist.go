// Package anilist is a small AniList GraphQL client: public media queries,
// seasonal lists, public user lists, progress push and the OAuth
// authorization-code flow (SPEC 4, AniList).
package anilist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Default endpoints.
const (
	DefaultEndpoint = "https://graphql.anilist.co"
	AuthorizeURL    = "https://anilist.co/api/v2/oauth/authorize"
	DefaultTokenURL = "https://anilist.co/api/v2/oauth/token"
)

// MediaSummary is one search / season result. JSON names follow the shows
// table columns (SPEC 2). Zero values mean NULL / unknown.
type MediaSummary struct {
	AniListID     int    `json:"anilist_id"`
	MalID         int    `json:"mal_id"`
	TitleRomaji   string `json:"title_romaji"`
	TitleEnglish  string `json:"title_english"`
	TitleNative   string `json:"title_native"`
	CoverURL      string `json:"cover_url"`
	BannerURL     string `json:"banner_url"`
	Color         string `json:"color"`
	Format        string `json:"format"`
	Status        string `json:"status"`
	Season        string `json:"season"`
	SeasonYear    int    `json:"season_year"`
	EpisodesTotal int    `json:"episodes_total"`
	NextAiringEp  int    `json:"next_airing_ep"`
	NextAiringAt  int64  `json:"next_airing_at"`
	AverageScore  int    `json:"average_score"` // 0-100, 0 = unknown
}

// Media is the full record for one show: every shows-table column AniList
// provides, plus the complete airing schedule.
type Media struct {
	MediaSummary
	Synonyms    []string     `json:"synonyms"`
	DurationMin int          `json:"duration_min"`
	Airing      []AiringNode `json:"airing"`
	// Prequel is the TV / TV_SHORT / ONA entry this show directly follows
	// (pickPrequel); nil = none (SPEC v1.3 section 1.2).
	Prequel *RelNode `json:"prequel"`
}

// RelNode is one related anime of the prequel chain. StartDate is yyyymmdd as
// a sortable number (20231001), 0 = unknown.
type RelNode struct {
	ID         int    `json:"id"`
	Format     string `json:"format"`
	Episodes   int    `json:"episodes"` // 0 = unknown
	SeasonYear int    `json:"season_year"`
	StartDate  int64  `json:"start_date"`
}

// AiringNode is one airingSchedule entry. AiringAt is unix seconds UTC.
type AiringNode struct {
	Episode  int   `json:"episode"`
	AiringAt int64 `json:"airingAt"`
}

// ListEntry is one entry of a user's anime list. Status is AniList's
// (CURRENT, PLANNING, COMPLETED, DROPPED, PAUSED, REPEATING); see
// LocalStatus. Score uses the 0-10 decimal scale, 0 = unscored.
type ListEntry struct {
	MediaID  int          `json:"media_id"`
	Status   string       `json:"status"`
	Progress int          `json:"progress"`
	Score    float64      `json:"score"`
	Media    MediaSummary `json:"media"`
}

// Token is an OAuth token. ExpiresIn is the lifetime in seconds as sent;
// ExpiresAt = exchange time + ExpiresIn, unix seconds (accounts.expires_at).
type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	ExpiresAt    int64  `json:"expires_at"`
}

// Viewer is the logged-in user behind a token.
type Viewer struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Error is a GraphQL or HTTP error from AniList.
type Error struct {
	Status   int
	Messages []string
}

func (e *Error) Error() string {
	if len(e.Messages) == 0 {
		return fmt.Sprintf("anilist: HTTP %d", e.Status)
	}
	return fmt.Sprintf("anilist: HTTP %d: %s", e.Status, strings.Join(e.Messages, "; "))
}

// Client talks to AniList. Create it with New; the zero value is not usable.
// It is safe for concurrent use; requests are spaced by the token bucket.
type Client struct {
	Endpoint string
	TokenURL string
	HTTP     *http.Client
	// Interval is the token-bucket refill time: one request per Interval
	// (SPEC: 1 req/s against AniList's 90 req/min).
	Interval time.Duration
	// MaxRetryWait caps how long a 429 Retry-After is honoured.
	MaxRetryWait time.Duration

	mu    sync.Mutex
	next  time.Time
	sleep func(ctx context.Context, d time.Duration) error
	now   func() time.Time
}

// New returns a client for the public AniList endpoints.
func New() *Client {
	return &Client{
		Endpoint:     DefaultEndpoint,
		TokenURL:     DefaultTokenURL,
		HTTP:         &http.Client{Timeout: 30 * time.Second},
		Interval:     time.Second,
		MaxRetryWait: 2 * time.Minute,
		sleep:        sleepCtx,
		now:          time.Now,
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// take blocks until the bucket has a token (capacity 1, refill Interval).
func (c *Client) take(ctx context.Context) error {
	c.mu.Lock()
	now := c.now()
	slot := now
	if c.next.After(now) {
		slot = c.next
	}
	c.next = slot.Add(c.Interval)
	c.mu.Unlock()
	return c.sleep(ctx, slot.Sub(now))
}

const mediaFields = `id idMal title { romaji english native } coverImage { extraLarge color } bannerImage format status season seasonYear episodes averageScore nextAiringEpisode { episode airingAt }`

// relationFields are the related-media fields the prequel chain needs.
const relationFields = `relations { edges { relationType node { id type format episodes seasonYear startDate { year month day } } } }`

// QueryMedia is used for Media and MediaByMalID; variables id or idMal, page.
// relations are sent on every page; only page 1's are read.
const QueryMedia = `query ($id: Int, $idMal: Int, $page: Int) {
  Media(id: $id, idMal: $idMal, type: ANIME) {
    ` + mediaFields + ` synonyms duration
    ` + relationFields + `
    airingSchedule(page: $page, perPage: 50) { pageInfo { hasNextPage } nodes { episode airingAt } }
  }
}`

// QueryRelations fetches one media's episode count and relations (one
// request, no airing schedule): one hop of the prequel chain.
const QueryRelations = `query ($id: Int) {
  Media(id: $id, type: ANIME) { id episodes ` + relationFields + ` }
}`

// QuerySearch: Page(perPage:25){media(search:$q,type:ANIME)}. 25 so a franchise
// with many entries (JoJo has 12+) does not hide its newest part.
const QuerySearch = `query ($q: String) {
  Page(perPage: 25) { media(search: $q, type: ANIME) { ` + mediaFields + ` } }
}`

// QuerySeason: one page of a season, TV/TV_SHORT/ONA, by popularity.
const QuerySeason = `query ($season: MediaSeason, $year: Int, $page: Int) {
  Page(page: $page, perPage: 50) {
    pageInfo { hasNextPage }
    media(season: $season, seasonYear: $year, type: ANIME, format_in: [TV, TV_SHORT, ONA], sort: POPULARITY_DESC) { ` + mediaFields + ` }
  }
}`

// QueryUserList: a user's public anime list.
const QueryUserList = `query ($user: String) {
  MediaListCollection(userName: $user, type: ANIME) {
    lists { entries { mediaId status progress score(format: POINT_10_DECIMAL) media { ` + mediaFields + ` } } }
  }
}`

// QueryViewer: the user behind a token.
const QueryViewer = `query { Viewer { id name } }`

// MutationProgress: SaveMediaListEntry(mediaId, progress).
const MutationProgress = `mutation ($id: Int, $progress: Int) {
  SaveMediaListEntry(mediaId: $id, progress: $progress) { id progress }
}`

// pageLimit caps paging loops (50 x 50 = 2500 nodes or shows).
const pageLimit = 50

type rawMedia struct {
	ID    int `json:"id"`
	IDMal int `json:"idMal"`
	Title struct {
		Romaji  string `json:"romaji"`
		English string `json:"english"`
		Native  string `json:"native"`
	} `json:"title"`
	CoverImage struct {
		ExtraLarge string `json:"extraLarge"`
		Color      string `json:"color"`
	} `json:"coverImage"`
	BannerImage       string      `json:"bannerImage"`
	Format            string      `json:"format"`
	Status            string      `json:"status"`
	Season            string      `json:"season"`
	SeasonYear        int         `json:"seasonYear"`
	Episodes          int         `json:"episodes"`
	AverageScore      int         `json:"averageScore"`
	NextAiringEpisode *AiringNode `json:"nextAiringEpisode"`
	Synonyms          []string    `json:"synonyms"`
	Duration          int         `json:"duration"`
	AiringSchedule    *struct {
		PageInfo pageInfo     `json:"pageInfo"`
		Nodes    []AiringNode `json:"nodes"`
	} `json:"airingSchedule"`
	Relations *struct {
		Edges []rawEdge `json:"edges"`
	} `json:"relations"`
}

// rawEdge is one relations edge. Null numbers decode as 0.
type rawEdge struct {
	RelationType string `json:"relationType"`
	Node         struct {
		ID         int    `json:"id"`
		Type       string `json:"type"`
		Format     string `json:"format"`
		Episodes   int    `json:"episodes"`
		SeasonYear int    `json:"seasonYear"`
		StartDate  struct {
			Year  int `json:"year"`
			Month int `json:"month"`
			Day   int `json:"day"`
		} `json:"startDate"`
	} `json:"node"`
}

func (r *rawMedia) edges() []rawEdge {
	if r.Relations == nil {
		return nil
	}
	return r.Relations.Edges
}

// pickPrequel selects the prequel a show directly follows: among PREQUEL edges
// to an ANIME of format TV, TV_SHORT or ONA (movies, OVAs, specials and
// non-anime are never part of the chain), the one with the greatest start
// date; when dates tie, or when any candidate's start date is unknown, the
// greatest id. nil when there is no such edge.
func pickPrequel(edges []rawEdge) *RelNode {
	var cands []RelNode
	unknown := false
	for _, e := range edges {
		n := e.Node
		if e.RelationType != "PREQUEL" || n.Type != "ANIME" {
			continue
		}
		if n.Format != "TV" && n.Format != "TV_SHORT" && n.Format != "ONA" {
			continue
		}
		rn := RelNode{ID: n.ID, Format: n.Format, Episodes: n.Episodes, SeasonYear: n.SeasonYear}
		if n.StartDate.Year > 0 {
			rn.StartDate = int64(n.StartDate.Year)*10000 + int64(n.StartDate.Month)*100 + int64(n.StartDate.Day)
		} else {
			unknown = true
		}
		cands = append(cands, rn)
	}
	if len(cands) == 0 {
		return nil
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if !unknown && c.StartDate != best.StartDate {
			if c.StartDate > best.StartDate {
				best = c
			}
			continue
		}
		if c.ID > best.ID {
			best = c
		}
	}
	return &best
}

type pageInfo struct {
	HasNextPage bool `json:"hasNextPage"`
}

func (r *rawMedia) summary() MediaSummary {
	s := MediaSummary{
		AniListID:     r.ID,
		MalID:         r.IDMal,
		TitleRomaji:   r.Title.Romaji,
		TitleEnglish:  r.Title.English,
		TitleNative:   r.Title.Native,
		CoverURL:      r.CoverImage.ExtraLarge,
		BannerURL:     r.BannerImage,
		Color:         r.CoverImage.Color,
		Format:        r.Format,
		Status:        r.Status,
		Season:        r.Season,
		SeasonYear:    r.SeasonYear,
		EpisodesTotal: r.Episodes,
		AverageScore:  r.AverageScore,
	}
	if r.NextAiringEpisode != nil {
		s.NextAiringEp = r.NextAiringEpisode.Episode
		s.NextAiringAt = r.NextAiringEpisode.AiringAt
	}
	return s
}

// Search returns up to 10 anime matching q.
func (c *Client) Search(ctx context.Context, q string) ([]MediaSummary, error) {
	var out struct {
		Page struct {
			Media []rawMedia `json:"media"`
		} `json:"Page"`
	}
	if err := c.query(ctx, "", QuerySearch, map[string]any{"q": q}, &out); err != nil {
		return nil, err
	}
	res := make([]MediaSummary, 0, len(out.Page.Media))
	for i := range out.Page.Media {
		res = append(res, out.Page.Media[i].summary())
	}
	return res, nil
}

// Media fetches one show by AniList id, paging through the whole airing
// schedule.
func (c *Client) Media(ctx context.Context, id int) (Media, error) {
	return c.media(ctx, map[string]any{"id": id})
}

// MediaByMalID fetches one show by MyAnimeList id (used by the MAL import).
func (c *Client) MediaByMalID(ctx context.Context, malID int) (Media, error) {
	return c.media(ctx, map[string]any{"idMal": malID})
}

func (c *Client) media(ctx context.Context, vars map[string]any) (Media, error) {
	var m Media
	for page := 1; page <= pageLimit; page++ {
		vars["page"] = page
		var out struct {
			Media *rawMedia `json:"Media"`
		}
		if err := c.query(ctx, "", QueryMedia, vars, &out); err != nil {
			return Media{}, err
		}
		if out.Media == nil {
			return Media{}, &Error{Status: http.StatusNotFound, Messages: []string{"media not found"}}
		}
		r := out.Media
		if page == 1 {
			m.MediaSummary = r.summary()
			m.Synonyms = r.Synonyms
			if m.Synonyms == nil {
				m.Synonyms = []string{}
			}
			m.DurationMin = r.Duration
			m.Prequel = pickPrequel(r.edges())
			// later pages by AniList id, even when the lookup was by idMal
			delete(vars, "idMal")
			vars["id"] = r.ID
		}
		if r.AiringSchedule == nil {
			break
		}
		m.Airing = append(m.Airing, r.AiringSchedule.Nodes...)
		if !r.AiringSchedule.PageInfo.HasNextPage {
			break
		}
	}
	return m, nil
}

// Prequel fetches one media by AniList id with QueryRelations (one request)
// and returns its own episode count (0 = unknown) and its prequel (nil = none).
// One hop of the prequel chain (SPEC v1.3 section 1.2).
func (c *Client) Prequel(ctx context.Context, id int) (episodes int, prequel *RelNode, err error) {
	var out struct {
		Media *rawMedia `json:"Media"`
	}
	if err := c.query(ctx, "", QueryRelations, map[string]any{"id": id}, &out); err != nil {
		return 0, nil, err
	}
	if out.Media == nil {
		return 0, nil, &Error{Status: http.StatusNotFound, Messages: []string{"media not found"}}
	}
	return out.Media.Episodes, pickPrequel(out.Media.edges()), nil
}

// Season returns every TV, TV_SHORT and ONA anime of a season (WINTER,
// SPRING, SUMMER, FALL; any case), most popular first.
func (c *Client) Season(ctx context.Context, season string, year int) ([]MediaSummary, error) {
	var res []MediaSummary
	for page := 1; page <= pageLimit; page++ {
		var out struct {
			Page struct {
				PageInfo pageInfo   `json:"pageInfo"`
				Media    []rawMedia `json:"media"`
			} `json:"Page"`
		}
		vars := map[string]any{"season": strings.ToUpper(strings.TrimSpace(season)), "year": year, "page": page}
		if err := c.query(ctx, "", QuerySeason, vars, &out); err != nil {
			return nil, err
		}
		for i := range out.Page.Media {
			res = append(res, out.Page.Media[i].summary())
		}
		if !out.Page.PageInfo.HasNextPage {
			break
		}
	}
	return res, nil
}

// UserList returns a user's public anime list, one entry per media (an
// entry in several custom lists appears once).
func (c *Client) UserList(ctx context.Context, userName string) ([]ListEntry, error) {
	var out struct {
		MediaListCollection *struct {
			Lists []struct {
				Entries []struct {
					MediaID  int      `json:"mediaId"`
					Status   string   `json:"status"`
					Progress int      `json:"progress"`
					Score    float64  `json:"score"`
					Media    rawMedia `json:"media"`
				} `json:"entries"`
			} `json:"lists"`
		} `json:"MediaListCollection"`
	}
	if err := c.query(ctx, "", QueryUserList, map[string]any{"user": userName}, &out); err != nil {
		return nil, err
	}
	if out.MediaListCollection == nil {
		return nil, &Error{Status: http.StatusNotFound, Messages: []string{"list not found"}}
	}
	seen := map[int]bool{}
	var res []ListEntry
	for _, l := range out.MediaListCollection.Lists {
		for _, e := range l.Entries {
			if seen[e.MediaID] {
				continue
			}
			seen[e.MediaID] = true
			res = append(res, ListEntry{MediaID: e.MediaID, Status: e.Status, Progress: e.Progress, Score: e.Score, Media: e.Media.summary()})
		}
	}
	return res, nil
}

// LocalStatus maps an AniList list status to shows.list_status.
func LocalStatus(s string) string {
	switch strings.ToUpper(s) {
	case "CURRENT", "REPEATING":
		return "watching"
	case "PLANNING":
		return "planning"
	case "COMPLETED":
		return "completed"
	case "PAUSED":
		return "paused"
	case "DROPPED":
		return "dropped"
	}
	return "planning"
}

// Viewer returns the user a token belongs to.
func (c *Client) Viewer(ctx context.Context, token string) (Viewer, error) {
	var out struct {
		Viewer *Viewer `json:"Viewer"`
	}
	if err := c.query(ctx, token, QueryViewer, nil, &out); err != nil {
		return Viewer{}, err
	}
	if out.Viewer == nil {
		return Viewer{}, &Error{Status: http.StatusUnauthorized, Messages: []string{"no viewer for token"}}
	}
	return *out.Viewer, nil
}

// PushProgress sets the watched-episode count of mediaID on the token
// owner's list.
func (c *Client) PushProgress(ctx context.Context, token string, mediaID, progress int) error {
	if token == "" {
		return errors.New("anilist: PushProgress needs a token")
	}
	var out struct {
		SaveMediaListEntry *struct {
			ID       int `json:"id"`
			Progress int `json:"progress"`
		} `json:"SaveMediaListEntry"`
	}
	if err := c.query(ctx, token, MutationProgress, map[string]any{"id": mediaID, "progress": progress}, &out); err != nil {
		return err
	}
	if out.SaveMediaListEntry == nil {
		return &Error{Status: http.StatusOK, Messages: []string{"SaveMediaListEntry returned nothing"}}
	}
	return nil
}

// AuthURL is the page the user opens to authorize the app (authorization
// code flow; redirect must match the AniList API client's redirect URL).
func AuthURL(clientID, redirect string) string {
	v := url.Values{}
	v.Set("client_id", clientID)
	v.Set("redirect_uri", redirect)
	v.Set("response_type", "code")
	return AuthorizeURL + "?" + v.Encode()
}

// Exchange trades an authorization code for a token.
func (c *Client) Exchange(ctx context.Context, clientID, secret, redirect, code string) (Token, error) {
	body, _ := json.Marshal(map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     clientID,
		"client_secret": secret,
		"redirect_uri":  redirect,
		"code":          code,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, bytes.NewReader(body))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Token{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Token{}, err
	}
	var t struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		Error        string `json:"error"`
		Message      string `json:"message"`
		Hint         string `json:"hint"`
	}
	_ = json.Unmarshal(data, &t)
	if resp.StatusCode != http.StatusOK || t.AccessToken == "" {
		msg := strings.TrimSpace(strings.Join(nonEmpty(t.Error, t.Message, t.Hint), ": "))
		if msg == "" {
			msg = "no access_token in response"
		}
		return Token{}, &Error{Status: resp.StatusCode, Messages: []string{msg}}
	}
	tok := Token{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, TokenType: t.TokenType, ExpiresIn: t.ExpiresIn}
	if t.ExpiresIn > 0 {
		tok.ExpiresAt = c.now().Unix() + t.ExpiresIn
	}
	return tok, nil
}

func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// query runs one GraphQL request, retrying once on HTTP 429.
func (c *Client) query(ctx context.Context, token, q string, vars map[string]any, out any) error {
	payload := map[string]any{"query": q}
	if vars != nil {
		payload["variables"] = vars
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		if err := c.take(ctx); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			if err := c.sleep(ctx, c.retryAfter(resp.Header)); err != nil {
				return err
			}
			continue
		}
		var env struct {
			Data   json.RawMessage `json:"data"`
			Errors []struct {
				Message string `json:"message"`
				Status  int    `json:"status"`
			} `json:"errors"`
		}
		jerr := json.Unmarshal(data, &env)
		if len(env.Errors) > 0 {
			e := &Error{Status: resp.StatusCode}
			for _, ge := range env.Errors {
				e.Messages = append(e.Messages, ge.Message)
			}
			return e
		}
		if resp.StatusCode != http.StatusOK {
			return &Error{Status: resp.StatusCode}
		}
		if jerr != nil {
			return fmt.Errorf("anilist: bad JSON: %w", jerr)
		}
		if len(env.Data) == 0 || string(env.Data) == "null" {
			return &Error{Status: resp.StatusCode, Messages: []string{"empty data"}}
		}
		return json.Unmarshal(env.Data, out)
	}
}

// retryAfter reads Retry-After (seconds or HTTP date); default 2 s, capped
// at MaxRetryWait.
func (c *Client) retryAfter(h http.Header) time.Duration {
	d := 2 * time.Second
	if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			d = time.Duration(n) * time.Second
		} else if t, err := http.ParseTime(v); err == nil {
			d = t.Sub(c.now())
		}
	}
	if d < 0 {
		d = 0
	}
	if c.MaxRetryWait > 0 && d > c.MaxRetryWait {
		d = c.MaxRetryWait
	}
	return d
}
