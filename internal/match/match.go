// Package match decides which tracked show a parsed release belongs to and
// which AniList episode number it is (SPEC 5, internal/match).
package match

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/parse"
)

// ShowTitles is everything the matcher needs to know about one tracked show.
// Zero values mean unknown: EpisodesTotal 0 = total not known (NULL in the
// shows table), Season 0 = season number not known.
type ShowTitles struct {
	ID            int
	Romaji        string
	English       string
	Native        string
	Synonyms      []string
	SearchQuery   string
	EpisodeOffset int    // manual offset: file episode number minus AniList episode number; 0 = none
	EpisodesTotal int    // 0 = unknown
	ListStatus    string // watching, planning, completed, paused, dropped
	SeasonYear    int
	Season        int // season number this show is (2 for a "2nd Season" entry); 0 = unknown
	AbsOffset     int // episodes before this show in its series (AniList prequel chain); 0 = none / unknown
	EpisodesKnown int // highest episode number in the show's episodes table; 0 = none
}

// offset is O of SPEC v1.3 section 1.3: the manual EpisodeOffset when set,
// else AbsOffset when above 0.
func (s *ShowTitles) offset() int {
	if s.EpisodeOffset != 0 {
		return s.EpisodeOffset
	}
	if s.AbsOffset > 0 {
		return s.AbsOffset
	}
	return 0
}

// MinJaccard is the token-set Jaccard similarity needed for a fuzzy match.
const MinJaccard = 0.85

// Match finds the show r belongs to.
//
// episode is the AniList-numbered episode: r.Episode - shift, where shift is
// 0 when the number was read as the show's own (season) numbering and O (the
// show's offset, see episodeFor) when it was read as absolute numbering.
// For a batch, episode is the first episode of the range (0 when the batch
// has no range); the caller computes the range end as r.EpisodeEnd - shift.
// For a movie file (no episode number, not a batch) matched to a show with
// EpisodesTotal 1, episode is 1.
// ok is false when no show scores at least MinJaccard or every matching
// show rejects the episode number.
func Match(r parse.Result, shows []ShowTitles) (showID int, episode int, ok bool, score float64, shift int) {
	title := Normalize(r.Title)
	if title == "" {
		return 0, 0, false, 0, 0
	}
	tokens := tokenSet(title)

	bestIdx := -1
	var bestScore float64
	var bestEp, bestShift int
	for i := range shows {
		s := &shows[i]
		sc := showScore(title, tokens, r.Season, s)
		if sc == 0 {
			continue
		}
		ep, sh, valid := episodeFor(r, s)
		if !valid {
			continue
		}
		if bestIdx < 0 || better(sc, s, bestScore, &shows[bestIdx]) {
			bestIdx, bestScore, bestEp, bestShift = i, sc, ep, sh
		}
	}
	if bestIdx < 0 {
		return 0, 0, false, 0, 0
	}
	return shows[bestIdx].ID, bestEp, true, bestScore, bestShift
}

const eps = 1e-9

// better reports whether show a with score sa beats show b with score sb.
// Tie: prefer list_status watching, then the most recent season_year.
func better(sa float64, a *ShowTitles, sb float64, b *ShowTitles) bool {
	if sa > sb+eps {
		return true
	}
	if sa < sb-eps {
		return false
	}
	aw, bw := a.ListStatus == "watching", b.ListStatus == "watching"
	if aw != bw {
		return aw
	}
	return a.SeasonYear > b.SeasonYear
}

// episodeFor maps the parsed episode to the show's AniList numbering
// (SPEC v1.3 section 1.3). shift is what was subtracted: 0 for the season
// reading, O for the absolute reading.
//
// With an offset O > 0 a number n has two readings: season (S = n) and
// absolute (A = n - O). The season reading is preferred when the release
// title carries a season marker; the absolute one when the show's own titles
// carry a marker and the release's do not; otherwise the season one. The
// preferred reading is used when valid, else the other, else n is rejected.
func episodeFor(r parse.Result, s *ShowTitles) (ep, shift int, ok bool) {
	if r.Episode == 0 {
		if r.IsBatch {
			// TODO(spec): SPEC says reject ep <= 0; a batch without a range
			// has no number, so it is accepted as episode 0 (releases.episode NULL).
			return 0, 0, true
		}
		if s.EpisodesTotal == 1 {
			// TODO(spec): movie file (no episode) on a one-episode show -> episode 1.
			return 1, 0, true
		}
		return 0, 0, false
	}
	o := s.offset()
	if o <= 0 {
		// O == 0: unchanged. A negative manual offset (not reachable from the
		// v1.3 UI, still accepted by the API) keeps the v1 arithmetic.
		ep := r.Episode - o
		if ep <= 0 {
			return 0, 0, false
		}
		if s.EpisodesTotal > 0 && ep > s.EpisodesTotal+1 {
			return 0, 0, false
		}
		return ep, o, true
	}
	n := r.Episode
	bound := s.EpisodesTotal
	if bound <= 0 {
		bound = s.EpisodesKnown
	}
	// No +1 slack for the season reading: with a positive offset a number one
	// past the total is read as absolute. When nothing is known about the
	// length, a number above the offset cannot be a season number.
	seasonOK := n >= 1 && ((bound == 0 && n <= o) || (bound > 0 && n <= bound))
	a := n - o
	absOK := a >= 1 && (bound == 0 || a <= bound+1)
	preferAbs := !releaseHasMarker(r) && showHasMarker(s)
	switch {
	case preferAbs && absOK:
		return a, o, true
	case !preferAbs && seasonOK:
		return n, 0, true
	case absOK:
		return a, o, true
	case seasonOK:
		return n, 0, true
	}
	return 0, 0, false
}

// releaseHasMarker reports whether the release names a season: the parser
// found one (r.Season) or its title ends in a season suffix.
func releaseHasMarker(r parse.Result) bool {
	if r.Season > 0 {
		return true
	}
	_, num := seasonSuffix(Normalize(r.Title))
	return num > 0
}

// showHasMarker reports whether one of the show's own titles (romaji,
// english, synonyms) ends in a season suffix ("2nd Season", "Part 2", ...).
func showHasMarker(s *ShowTitles) bool {
	titles := append([]string{s.Romaji, s.English}, s.Synonyms...)
	for _, t := range titles {
		if n := Normalize(t); n != "" {
			if _, num := seasonSuffix(n); num > 0 {
				return true
			}
		}
	}
	return false
}

type candidate struct {
	norm   string
	season int // required season number, 0 = none
}

// showScore is the best score of any of the show's candidate titles, or 0.
func showScore(title string, tokens map[string]bool, season int, s *ShowTitles) float64 {
	var best float64
	for _, c := range candidates(s) {
		if season > 0 && c.season > 0 && season != c.season {
			continue
		}
		var sc float64
		if c.norm == title {
			sc = 1
		} else if j := jaccard(tokens, tokenSet(c.norm)); j >= MinJaccard {
			sc = j
		}
		if sc > best {
			best = sc
		}
	}
	return best
}

var (
	// "2nd season", "2nd stage", "2nd part", "2nd cour", and an &-joined pair
	// ("2nd & 3rd stage", normalized to "2nd 3rd stage") whose first number is
	// the marker. The base is lazy so the pair's first number is the one taken.
	reOrdSeason = regexp.MustCompile(`^(.+?) (\d{1,2})(?:st|nd|rd|th)(?: \d{1,2}(?:st|nd|rd|th))? (?:season|stage|part|cour)$`)
	reSeasonN   = regexp.MustCompile(`^(.+) season (\d{1,2})$`)
	rePartN     = regexp.MustCompile(`^(.+) part (\d{1,2})$`)
	reSN        = regexp.MustCompile(`^(.+) s(\d{1,2})$`)
	reRoman     = regexp.MustCompile(`^(.+) (ii|iii|iv)$`)
	romanNum    = map[string]int{"ii": 2, "iii": 3, "iv": 4}
)

// candidates returns the normalized titles a show can be matched by:
// romaji, english, native, synonyms and search_query, plus for a title that
// ends in a season marker (2nd Season / Stage / Part / Cour, "2nd & 3rd
// Stage", Season 2, Part 2, S2, II, III, IV) the base title, both carrying
// that season number as a requirement.
// Titles without a marker carry s.Season (usually 0 = no requirement).
func candidates(s *ShowTitles) []candidate {
	raw := make([]string, 0, 5+len(s.Synonyms))
	raw = append(raw, s.Romaji, s.English, s.Native)
	raw = append(raw, s.Synonyms...)
	raw = append(raw, s.SearchQuery)
	var out []candidate
	seen := map[candidate]bool{}
	add := func(c candidate) {
		if c.norm != "" && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	for _, t := range raw {
		n := Normalize(t)
		if n == "" {
			continue
		}
		base, num := seasonSuffix(n)
		if num > 0 {
			add(candidate{n, num})
			add(candidate{base, num})
			continue
		}
		add(candidate{n, s.Season})
	}
	return out
}

func seasonSuffix(n string) (string, int) {
	if m := reOrdSeason.FindStringSubmatch(n); m != nil {
		return m[1], atoi(m[2])
	}
	if m := reSeasonN.FindStringSubmatch(n); m != nil {
		return m[1], atoi(m[2])
	}
	if m := rePartN.FindStringSubmatch(n); m != nil {
		return m[1], atoi(m[2])
	}
	if m := reSN.FindStringSubmatch(n); m != nil {
		return m[1], atoi(m[2])
	}
	if m := reRoman.FindStringSubmatch(n); m != nil {
		return m[1], romanNum[m[2]]
	}
	return n, 0
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// Normalize lowercases, folds full-width forms, removes apostrophes, turns
// every other non-letter non-digit into a space, collapses spaces and drops
// a leading "the".
//
// TODO(spec): SPEC asks for NFKC. The standard library has no NFKC and
// golang.org/x/text is not an allowed dependency, so only the forms that
// matter for titles are folded: full-width ASCII (U+FF01-FF5E) and the
// ideographic space (U+3000).
func Normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 0xFF01 && r <= 0xFF5E:
			r -= 0xFEE0
		case r == 0x3000:
			r = ' '
		}
		switch {
		case r == '\'' || r == '’' || r == '‘' || r == '`':
			// Journey's == Journeys
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteByte(' ')
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	return strings.TrimPrefix(out, "the ")
}

func tokenSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, t := range strings.Fields(s) {
		m[t] = true
	}
	return m
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for t := range a {
		if b[t] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	return float64(inter) / float64(union)
}
