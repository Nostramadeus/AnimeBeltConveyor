package sched

// Adapter from sched.Brain to the brain lane's packages. ParseResult, ShowTitles
// and Candidate are field-for-field copies of parse.Result, match.ShowTitles and
// release.Candidate, so those are plain conversions.

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/anilist"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/animeschedule"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/mal"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/match"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/nyaa"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/parse"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/release"
)

// DefaultBrain returns the brain the app runs with.
func DefaultBrain() Brain { return NewRealBrain() }

// ErrOffline is returned by OfflineBrain's network methods.
var ErrOffline = errors.New("network access is disabled in this brain")

// OfflineBrain has the real parser, matcher and release picker but no network
// access. Tests embed it and override the network calls they need.
type OfflineBrain struct{}

func (OfflineBrain) Parse(name string) ParseResult { return ParseResult(parse.Parse(name)) }

func (OfflineBrain) IsRecap(r ParseResult) bool { return parse.Result(r).IsRecap() }

func (OfflineBrain) Match(r ParseResult, shows []ShowTitles) (int, int, bool, float64, int) {
	in := make([]match.ShowTitles, len(shows))
	for i, s := range shows {
		in[i] = match.ShowTitles(s)
	}
	return match.Match(parse.Result(r), in)
}

// Pick applies release.Pick. When a file is already on disk, only candidates
// release.ShouldReplace accepts (AutoReplaceV2, higher version, same group) are offered.
func (OfflineBrain) Pick(cands []Candidate, rules Rules, now, airedAt time.Time) (*Candidate, string) {
	rr := release.Rules{
		GroupOrder: rules.GroupOrder, Resolution: rules.Resolution, FallbackHours: rules.FallbackHours,
		SkipBatchesWhileAiring: rules.SkipBatchesWhileAiring, ShowAiring: rules.Airing, MinSeeders: rules.MinSeeders,
		AutoReplaceV2: rules.AutoReplaceV2, MaxSizeBytes: rules.MaxSizeBytes, PreferSmall: rules.PreferSmall,
		// TODO(spec): SPEC 5 rule 1 mentions a rule allowing lower resolutions after the
		// fallback, but section 3 has no setting for it; off.
		AllowLowerResAfterFallback: false,
	}
	in := make([]release.Candidate, 0, len(cands))
	for _, c := range cands {
		rc := release.Candidate(c)
		if rules.HaveFile && !release.ShouldReplace(release.File{Group: rules.FileGroup, Resolution: rules.FileResolution, Version: rules.FileVersion}, rc, rr) {
			continue
		}
		in = append(in, rc)
	}
	if len(in) == 0 {
		if rules.HaveFile {
			return nil, "already on disk"
		}
		return nil, "no candidates"
	}
	got, reason := release.Pick(in, rr, now, airedAt)
	if got == nil {
		return nil, reason
	}
	out := Candidate(*got)
	return &out, reason
}

func (OfflineBrain) Magnet(infoHash, title string) string { return nyaa.Magnet(infoHash, title) }

func (OfflineBrain) AniListAuthURL(clientID, redirectURI string) string {
	return anilist.AuthURL(clientID, redirectURI)
}

func (OfflineBrain) MALAuthURL(clientID, codeVerifier, state, redirectURI string) string {
	return mal.AuthURL(clientID, redirectURI, codeVerifier, state)
}

func (OfflineBrain) NyaaSearch(context.Context, string, string, string) ([]NyaaItem, error) {
	return nil, ErrOffline
}
func (OfflineBrain) Search(context.Context, string) ([]MediaSummary, error) { return nil, ErrOffline }
func (OfflineBrain) Media(context.Context, int) (Media, error)              { return Media{}, ErrOffline }
func (OfflineBrain) Prequel(context.Context, int) (int, *RelNode, error)    { return 0, nil, ErrOffline }
func (OfflineBrain) MediaByMAL(context.Context, int) (Media, error)         { return Media{}, ErrOffline }
func (OfflineBrain) Season(context.Context, string, int) ([]MediaSummary, error) {
	return nil, ErrOffline
}
func (OfflineBrain) AniListUserList(context.Context, string) ([]ListEntry, error) {
	return nil, ErrOffline
}
func (OfflineBrain) AniListPush(context.Context, string, int, int) error { return ErrOffline }
func (OfflineBrain) AniListExchange(context.Context, string, string, string, string) (Token, error) {
	return Token{}, ErrOffline
}
func (OfflineBrain) AniListViewer(context.Context, string) (string, string, error) {
	return "", "", ErrOffline
}
func (OfflineBrain) MALExchange(context.Context, string, string, string, string) (Token, error) {
	return Token{}, ErrOffline
}
func (OfflineBrain) MALViewer(context.Context, string) (string, string, error) {
	return "", "", ErrOffline
}
func (OfflineBrain) MALUserList(context.Context, string) ([]ListEntry, error) { return nil, ErrOffline }
func (OfflineBrain) MALPush(context.Context, string, int, int) error          { return ErrOffline }
func (OfflineBrain) DubTimes(context.Context, string, int, int) ([]DubEntry, error) {
	return nil, ErrOffline
}

// RealBrain is OfflineBrain plus the network clients (AniList, MAL, nyaa, animeschedule).
type RealBrain struct {
	OfflineBrain
	al *anilist.Client
	ml *mal.Client
	th *nyaa.Throttle
}

// NewRealBrain builds the production brain.
func NewRealBrain() *RealBrain {
	return &RealBrain{al: anilist.New(), ml: mal.New(), th: nyaa.NewThrottle(nyaa.MinInterval)}
}

func (b *RealBrain) NyaaSearch(ctx context.Context, base, query, category string) ([]NyaaItem, error) {
	if err := b.th.Wait(ctx); err != nil {
		return nil, err
	}
	items, err := nyaa.Fetch(ctx, base, query, category)
	if err != nil {
		return nil, err
	}
	out := make([]NyaaItem, 0, len(items))
	for _, it := range items {
		cat := it.CategoryID
		if cat == "" {
			cat = it.Category
		}
		out = append(out, NyaaItem{ID: it.ID, Title: it.Title, TorrentURL: it.Link, InfoHash: it.InfoHash, PublishedAt: it.PubDate,
			Seeders: it.Seeders, Leechers: it.Leechers, SizeBytes: it.SizeBytes, Category: cat})
	}
	return out, nil
}

func summaryOf(m anilist.MediaSummary) MediaSummary {
	return MediaSummary{
		ID: m.AniListID, IDMal: m.MalID, TitleRomaji: m.TitleRomaji, TitleEnglish: m.TitleEnglish, TitleNative: m.TitleNative,
		CoverURL: m.CoverURL, BannerURL: m.BannerURL, Color: m.Color, Format: m.Format, Status: m.Status, Season: m.Season,
		SeasonYear: m.SeasonYear, Episodes: m.EpisodesTotal, NextAiringEp: m.NextAiringEp, NextAiringAt: m.NextAiringAt,
		AverageScore: m.AverageScore,
	}
}

func summaries(in []anilist.MediaSummary) []MediaSummary {
	out := make([]MediaSummary, len(in))
	for i, m := range in {
		out[i] = summaryOf(m)
	}
	return out
}

func mediaOf(m anilist.Media) Media {
	out := Media{MediaSummary: summaryOf(m.MediaSummary), Synonyms: m.Synonyms, Prequel: relNodeOf(m.Prequel)}
	out.Duration = m.DurationMin
	for _, a := range m.Airing {
		out.Airing = append(out.Airing, AiringNode{Episode: a.Episode, AiringAt: a.AiringAt})
	}
	return out
}

func (b *RealBrain) Search(ctx context.Context, q string) ([]MediaSummary, error) {
	r, err := b.al.Search(ctx, q)
	return summaries(r), err
}

func (b *RealBrain) Media(ctx context.Context, id int) (Media, error) {
	m, err := b.al.Media(ctx, id)
	if err != nil {
		return Media{}, err
	}
	return mediaOf(m), nil
}

func relNodeOf(p *anilist.RelNode) *RelNode {
	if p == nil {
		return nil
	}
	r := RelNode(*p)
	return &r
}

func (b *RealBrain) Prequel(ctx context.Context, id int) (int, *RelNode, error) {
	eps, p, err := b.al.Prequel(ctx, id)
	if err != nil {
		return 0, nil, err
	}
	return eps, relNodeOf(p), nil
}

func (b *RealBrain) MediaByMAL(ctx context.Context, malID int) (Media, error) {
	m, err := b.al.MediaByMalID(ctx, malID)
	if err != nil {
		return Media{}, err
	}
	return mediaOf(m), nil
}

func (b *RealBrain) Season(ctx context.Context, season string, year int) ([]MediaSummary, error) {
	r, err := b.al.Season(ctx, season, year)
	return summaries(r), err
}

func (b *RealBrain) AniListUserList(ctx context.Context, userName string) ([]ListEntry, error) {
	r, err := b.al.UserList(ctx, userName)
	if err != nil {
		return nil, err
	}
	out := make([]ListEntry, len(r))
	for i, e := range r {
		out[i] = ListEntry{MediaID: e.MediaID, MALID: e.Media.MalID, Status: e.Status, Progress: e.Progress, Score: e.Score}
	}
	return out, nil
}

func (b *RealBrain) AniListPush(ctx context.Context, token string, mediaID, progress int) error {
	return b.al.PushProgress(ctx, token, mediaID, progress)
}

func (b *RealBrain) AniListExchange(ctx context.Context, clientID, clientSecret, redirectURI, code string) (Token, error) {
	t, err := b.al.Exchange(ctx, clientID, clientSecret, redirectURI, code)
	return Token{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, ExpiresIn: t.ExpiresIn}, err
}

func (b *RealBrain) AniListViewer(ctx context.Context, token string) (string, string, error) {
	v, err := b.al.Viewer(ctx, token)
	if err != nil {
		return "", "", err
	}
	return strconv.Itoa(v.ID), v.Name, nil
}

func (b *RealBrain) MALExchange(ctx context.Context, clientID, codeVerifier, code, redirectURI string) (Token, error) {
	t, err := b.ml.Exchange(ctx, clientID, "", redirectURI, code, codeVerifier)
	return Token{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, ExpiresIn: t.ExpiresIn}, err
}

func (b *RealBrain) MALViewer(ctx context.Context, token string) (string, string, error) {
	u, err := b.ml.Me(ctx, token)
	if err != nil {
		return "", "", err
	}
	return strconv.Itoa(u.ID), u.Name, nil
}

func (b *RealBrain) MALUserList(ctx context.Context, token string) ([]ListEntry, error) {
	r, err := b.ml.UserList(ctx, token)
	if err != nil {
		return nil, err
	}
	out := make([]ListEntry, len(r))
	for i, e := range r {
		out[i] = ListEntry{MALID: e.MalID, Status: e.Status, Progress: e.Progress, Score: float64(e.Score)}
	}
	return out, nil
}

func (b *RealBrain) MALPush(ctx context.Context, token string, malID, watched int) error {
	return b.ml.PushProgress(ctx, token, malID, watched)
}

func (b *RealBrain) DubTimes(ctx context.Context, token string, year, week int) ([]DubEntry, error) {
	r, err := animeschedule.Timetable(ctx, token, "dub", year, week)
	if err != nil {
		return nil, err
	}
	out := make([]DubEntry, 0, len(r))
	for _, e := range r {
		out = append(out, DubEntry{AniListID: e.AniListID, Title: e.Title, Episode: e.Episode, AiringAt: e.AirsAt})
	}
	return out, nil
}

// FetchTorrent is unavailable offline.
func (OfflineBrain) FetchTorrent(context.Context, string) ([]byte, error) { return nil, ErrOffline }

// FetchTorrent downloads a .torrent file through the nyaa throttle.
func (b *RealBrain) FetchTorrent(ctx context.Context, url string) ([]byte, error) {
	if err := b.th.Wait(ctx); err != nil {
		return nil, err
	}
	return nyaa.FetchTorrent(ctx, url)
}
