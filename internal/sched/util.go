package sched

import (
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

func itoa(n int) string     { return strconv.Itoa(n) }
func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

// title is the show name used in event messages (romaji; stable across settings).
func title(s store.Show) string { return s.TitleRomaji }

func epLabel(ep int) string {
	if ep <= 0 {
		return "(batch)"
	}
	return "ep " + itoa(ep)
}

// DisplayTitle picks the title per Titles.Language, falling back to romaji.
func DisplayTitle(c settings.Settings, s store.Show) string {
	if c.Titles.Language == "english" && s.TitleEnglish != "" {
		return s.TitleEnglish
	}
	return s.TitleRomaji
}

// SafeName turns a show title into a folder name valid on Windows, macOS and Linux.
func SafeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case strings.ContainsRune(`<>:"/\|?*`, r), unicode.IsControl(r):
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	out = strings.TrimRight(out, ". ")
	if r := []rune(out); len(r) > 120 {
		out = strings.TrimRight(string(r[:120]), ". ")
	}
	if out == "" {
		out = "Unknown show"
	}
	return out
}

// SavePath is where qBittorrent should save a show's downloads.
// "" means qBittorrent's own default (no download folder set). An absolute
// show.folder override is used as is; a relative one resolves against the
// download folder ("" when there is none).
// TODO(spec): the show folder name uses the romaji title so it never moves when
// Titles.Language changes.
func SavePath(c settings.Settings, s store.Show) string {
	dl := c.Library.DownloadDir()
	if s.Folder != "" {
		if filepath.IsAbs(s.Folder) {
			return filepath.Clean(s.Folder)
		}
		if dl == "" {
			return ""
		}
		return filepath.Join(dl, s.Folder)
	}
	if dl == "" {
		return ""
	}
	if c.Qbit.SavePathStyle == "root" {
		return dl
	}
	return filepath.Join(dl, SafeName(s.TitleRomaji))
}

// Titles converts shows to the matcher's input. known is the highest episode
// number per show id (store.MaxEpisodeNumbers; nil = none known).
func Titles(shows []store.Show, known map[int64]int) []ShowTitles {
	out := make([]ShowTitles, 0, len(shows))
	for _, s := range shows {
		t := ShowTitles{
			ID: int(s.ID), Romaji: s.TitleRomaji, English: s.TitleEnglish, Native: s.TitleNative,
			Synonyms: s.Synonyms, SearchQuery: s.SearchQuery, EpisodeOffset: s.EpisodeOffset, ListStatus: s.ListStatus,
			EpisodesKnown: known[s.ID],
		}
		if s.AbsOffset != nil && *s.AbsOffset > 0 { // -1 = chain unknown: no offset
			t.AbsOffset = *s.AbsOffset
		}
		if s.EpisodesTotal != nil {
			t.EpisodesTotal = *s.EpisodesTotal
		}
		if s.SeasonYear != nil {
			t.SeasonYear = *s.SeasonYear
		}
		out = append(out, t)
	}
	return out
}

// NyaaQuery is the search query for a show: the override, else romaji with punctuation stripped.
func NyaaQuery(s store.Show) string {
	if q := strings.TrimSpace(s.SearchQuery); q != "" {
		return q
	}
	var b strings.Builder
	for _, r := range s.TitleRomaji {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// ListStatusFrom maps AniList and MAL list statuses to shows.list_status.
func ListStatusFrom(s string) string {
	switch strings.ToUpper(s) {
	case "CURRENT", "REPEATING", "WATCHING":
		return "watching"
	case "PLANNING", "PLAN_TO_WATCH":
		return "planning"
	case "COMPLETED":
		return "completed"
	case "PAUSED", "ON_HOLD":
		return "paused"
	case "DROPPED":
		return "dropped"
	}
	return "planning" // unknown: never auto-download
}

func autoDownload(s store.Show) bool {
	return s.AutoDownload == nil || *s.AutoDownload
}

// normTitle lowercases, keeps letters/digits (any script), collapses spaces and
// drops a leading "the". Used for dub-title fallback matching.
func normTitle(s string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(s) {
		if unicode.IsLetter(c) || unicode.IsDigit(c) {
			b.WriteRune(c)
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.TrimPrefix(strings.Join(strings.Fields(b.String()), " "), "the ")
}
