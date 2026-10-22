package sched

import (
	"context"
	"time"
)

// Brain is everything core needs from the brain lane (parse, match, release,
// nyaa, anilist, mal, animeschedule). brain_real.go implements it with the real
// packages (RealBrain) and without network (OfflineBrain, for tests). Types below
// mirror the brain packages so most conversions are plain struct conversions.
type Brain interface {
	// parse / match / release (SPEC 5)
	Parse(name string) ParseResult
	IsRecap(r ParseResult) bool
	Match(r ParseResult, shows []ShowTitles) (showID int, episode int, ok bool, score float64)
	Pick(cands []Candidate, rules Rules, now time.Time, airedAt time.Time) (chosen *Candidate, reason string)

	// nyaa (SPEC 4)
	NyaaSearch(ctx context.Context, base, query, category string) ([]NyaaItem, error)
	Magnet(infoHash, title string) string
	// FetchTorrent downloads a release's .torrent file (torrent_file downloader mode),
	// through the same 2 s throttle as NyaaSearch; 20 s timeout, max 8 MB.
	FetchTorrent(ctx context.Context, url string) ([]byte, error)

	// AniList (SPEC 4)
	Search(ctx context.Context, q string) ([]MediaSummary, error)
	Media(ctx context.Context, id int) (Media, error)
	MediaByMAL(ctx context.Context, malID int) (Media, error)
	Season(ctx context.Context, season string, year int) ([]MediaSummary, error)
	AniListUserList(ctx context.Context, userName string) ([]ListEntry, error)
	AniListPush(ctx context.Context, token string, mediaID, progress int) error
	AniListAuthURL(clientID, redirectURI string) string
	AniListExchange(ctx context.Context, clientID, clientSecret, redirectURI, code string) (Token, error)
	AniListViewer(ctx context.Context, token string) (userID, userName string, err error)

	// MyAnimeList (SPEC 4)
	MALAuthURL(clientID, codeVerifier, state, redirectURI string) string
	MALExchange(ctx context.Context, clientID, codeVerifier, code, redirectURI string) (Token, error)
	MALViewer(ctx context.Context, token string) (userID, userName string, err error)
	MALUserList(ctx context.Context, token string) ([]ListEntry, error)
	MALPush(ctx context.Context, token string, malID, watched int) error

	// animeschedule.net (SPEC 4), only called when Dubs.Enabled and a token is set.
	DubTimes(ctx context.Context, token string, year, week int) ([]DubEntry, error)
}

// ParseResult mirrors parse.Result field for field (convertible with ParseResult(r)).
type ParseResult struct {
	Title      string
	Episode    int
	EpisodeEnd int
	Season     int
	Version    int
	Group      string
	Resolution string
	Source     string
	Codec      string
	Audio      string
	Ext        string
	IsBatch    bool
	Raw        string
	Confidence float64
}

// ShowTitles mirrors match.ShowTitles field for field.
type ShowTitles struct {
	ID            int
	Romaji        string
	English       string
	Native        string
	Synonyms      []string
	SearchQuery   string
	EpisodeOffset int    // file episode number minus AniList episode number
	EpisodesTotal int    // 0 = unknown
	ListStatus    string // watching, planning, completed, paused, dropped
	SeasonYear    int
	Season        int // season number this show is; 0 = unknown
}

// Candidate is one release offered to Pick. ID is releases.id.
type Candidate struct {
	ID          int64
	Title       string
	Group       string
	Resolution  string
	Version     int
	Episode     int
	EpisodeEnd  int
	IsBatch     bool
	IsRecap     bool
	Seeders     int
	PublishedAt time.Time
	SizeBytes   int64 // 0 = unknown
}

// Rules are the effective release rules for one show (per-show overrides already
// applied) plus what is already on disk for the episode.
type Rules struct {
	GroupOrder             []string
	Resolution             string
	FallbackHours          float64
	SkipBatchesWhileAiring bool
	Airing                 bool // show status RELEASING
	AutoReplaceV2          bool
	MinSeeders             int
	HaveFile               bool
	FileVersion            int
	FileGroup              string
	FileResolution         string
	MaxSizeBytes           int64 // 0 = no limit (conveyor.max_release_gib)
	PreferSmall            bool  // conveyor.size_preference == "small"
}

// NyaaItem is one RSS item.
type NyaaItem struct {
	ID          int64 // from guid https://nyaa.si/view/<id>
	Title       string
	TorrentURL  string
	InfoHash    string
	PublishedAt time.Time
	Seeders     int
	Leechers    int
	SizeBytes   int64
	Category    string
}

// MediaSummary is an AniList media card (search results, season grid).
type MediaSummary struct {
	ID           int    `json:"anilist_id"`
	IDMal        int    `json:"mal_id"`
	TitleRomaji  string `json:"title_romaji"`
	TitleEnglish string `json:"title_english"`
	TitleNative  string `json:"title_native"`
	CoverURL     string `json:"cover_url"`
	BannerURL    string `json:"banner_url"`
	Color        string `json:"color"`
	Format       string `json:"format"`
	Status       string `json:"status"`
	Season       string `json:"season"`
	SeasonYear   int    `json:"season_year"`
	Episodes     int    `json:"episodes"`
	Duration     int    `json:"duration"`
	NextAiringEp int    `json:"next_airing_episode"`
	NextAiringAt int64  `json:"next_airing_at"`
	AverageScore int    `json:"average_score"` // 0-100, 0 = unknown
}

// Media is the full AniList media used to create and refresh a show.
type Media struct {
	MediaSummary
	Synonyms []string
	Airing   []AiringNode // whole airingSchedule, all pages
}

// AiringNode is one airingSchedule entry.
type AiringNode struct {
	Episode  int
	AiringAt int64
}

// ListEntry is one entry of a user's AniList or MAL list. Status is the
// service's own value (CURRENT, PLANNING... or watching, plan_to_watch...);
// sched maps it with ListStatusFrom.
type ListEntry struct {
	MediaID  int // AniList id (0 for MAL entries)
	MALID    int
	Status   string
	Progress int
	Score    float64
}

// Token is an OAuth token response.
type Token struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64 // seconds
}

// DubEntry is one dub air time from animeschedule.net.
type DubEntry struct {
	AniListID int
	Title     string
	Episode   int
	AiringAt  int64
}
