// Package parse turns anime release names (nyaa titles, file names) into
// structured data: title, episode, season, group, resolution and so on.
//
// It is an anitomy-style heuristic parser written from scratch (anitogo is
// MPL-2.0, which the project does not accept). It has no dependencies.
package parse

import (
	"regexp"
	"strconv"
	"strings"
)

// Result is the parse of one release or file name.
//
// Zero values mean "not found": Episode 0 = no episode number (movie or
// batch), Season 0 = no season marker, Resolution "" = unknown.
// Version is 1 unless the name carries v2, v3, ...
type Result struct {
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

// RecapMarker is appended to Result.Raw when the episode number has a
// fractional part (05.5). SPEC 5: "flag recap in Raw (treat as skip)".
const RecapMarker = " #recap"

// IsRecap reports whether the name had a fractional episode number (05.5).
// Such releases are recaps or specials and the release picker skips them.
func (r Result) IsRecap() bool { return strings.HasSuffix(r.Raw, RecapMarker) }

var videoExts = map[string]bool{
	"mkv": true, "mp4": true, "avi": true, "webm": true, "ts": true, "m4v": true,
	"wmv": true, "flv": true, "mov": true, "mpg": true, "mpeg": true, "ogm": true,
	"rmvb": true, "3gp": true,
}

var (
	reBracket    = regexp.MustCompile(`[\[\(\{【]([^\[\]\(\)\{\}【】]*)[\]\)\}】]`)
	reCRC        = regexp.MustCompile(`^[0-9A-Fa-f]{8}$`)
	reRange      = regexp.MustCompile(`^(\d{1,4})\s*[-~]\s*(\d{1,4})$`)
	reEp         = regexp.MustCompile(`^(\d{1,4})(?:\.(\d))?(?:\.?[vV](\d{1,2}))?$`)
	reYear       = regexp.MustCompile(`^(?:19[3-9]\d|20[0-4]\d)$`)
	reVersion    = regexp.MustCompile(`^\.?[vV](\d{1,2})$`)
	reSxE        = regexp.MustCompile(`(?i)^S(\d{1,2})E(\d{1,4})(?:[-~]E?(\d{1,4}))?(?:\.?v(\d{1,2}))?$`)
	reEpKey      = regexp.MustCompile(`(?i)^(?:E|EP|EP\.|#)(\d{1,4})(?:\.?v(\d{1,2}))?$`)
	reEpWord     = regexp.MustCompile(`(?i)^(?:Episode|Ep|Ep\.)$`)
	reJPEp       = regexp.MustCompile(`第(\d{1,4})話`)
	reVol        = regexp.MustCompile(`(?i)^Vol\.?(\d{1,3})?(?:[-~](\d{1,3}))?$`)
	reRes        = regexp.MustCompile(`(?i)^(\d{3,4})p$`)
	reResWH      = regexp.MustCompile(`(?i)^\d{3,4}[x×](\d{3,4})$`)
	reSTok       = regexp.MustCompile(`(?i)^S(\d{1,2})$`)
	reSeasonGlue = regexp.MustCompile(`(?i)^Season(\d{1,2})$`)
	reOrdinal    = regexp.MustCompile(`(?i)^(\d{1,2})(?:st|nd|rd|th)$`)
	reSmallNum   = regexp.MustCompile(`^\d{1,2}$`)
	reAudio      = regexp.MustCompile(`(?i)^(?:aac|flac|ac3|eac3|e-ac-3|ddp?|dts(?:-hd)?|truehd|opus|mp3|lpcm|pcm)[\d.]*$`)
	reBitDepth   = regexp.MustCompile(`(?i)^(?:8|10|12)-?bits?$`)
	reH26x       = regexp.MustCompile(`(?i)\bH\.(26[45])`)
	reAlnum      = regexp.MustCompile(`^[A-Za-z0-9]+$`)
	reStars      = regexp.MustCompile(`[★☆][^★☆]*[★☆]`) // fansub banners: ★10月新番★

	// bracket-only season markers: "Season 1", "1st Season", "S1", "Part 2"
	reBrSeason  = regexp.MustCompile(`(?i)^(?:Season\s*(\d{1,2})|(\d{1,2})(?:st|nd|rd|th)\s+Season|S(\d{1,2}))$`)
	reBrPart    = regexp.MustCompile(`(?i)^Part\s*(\d{1,2})$`)
	romanSeason = map[string]int{"II": 2, "III": 3, "IV": 4}
)

var codecs = map[string]bool{
	"h264": true, "x264": true, "avc": true, "h265": true, "x265": true, "hevc": true,
	"av1": true, "vp9": true, "xvid": true, "divx": true, "hi10p": true, "hi10": true,
	"hi444pp": true,
}

var sources = map[string]bool{
	"web": true, "web-dl": true, "webdl": true, "webrip": true, "web-rip": true,
	"bd": true, "bdrip": true, "bd-rip": true, "bluray": true, "blu-ray": true,
	"bdremux": true, "bdmv": true, "dvd": true, "dvdrip": true, "dvd-rip": true,
	"hdtv": true, "tv": true, "tvrip": true, "hdrip": true,
}

var otherMeta = map[string]bool{
	"multi": true, "multi-sub": true, "multi-subs": true, "multisub": true, "multisubs": true,
	"multiple": true, "subtitle": true, "subtitles": true, "subs": true, "sub": true,
	"subbed": true, "dub": true, "dubbed": true, "dual": true, "dual-audio": true,
	"audio": true, "eng": true, "jpn": true, "jap": true, "raw": true, "raws": true,
	"uncensored": true, "uncen": true, "end": true, "fin": true, "remux": true,
	"hdr": true, "hdr10": true, "sdr": true, "cr": true, "amzn": true, "nf": true,
	"dsnp": true, "hidive": true, "adn": true, "vostfr": true, "softsub": true,
	"hardsub": true, "bits": true, "proper": true, "repack": true,
}

type metaKind int

const (
	kNone metaKind = iota
	kRes
	kCodec
	kBits
	kSource
	kAudio
	kVersion
	kBatch
	kOther
)

// classify reports what kind of metadata token tok is, without side effects.
// val is the normalised value (resolution "1080p", version "2").
func classify(tok string) (metaKind, string) {
	lt := strings.ToLower(tok)
	if m := reRes.FindStringSubmatch(tok); m != nil {
		return kRes, m[1] + "p"
	}
	if m := reResWH.FindStringSubmatch(tok); m != nil {
		return kRes, m[1] + "p"
	}
	if lt == "4k" || lt == "uhd" {
		return kRes, "2160p"
	}
	if codecs[strings.ReplaceAll(lt, ".", "")] {
		return kCodec, tok
	}
	if reBitDepth.MatchString(tok) {
		return kBits, tok
	}
	if sources[lt] {
		return kSource, tok
	}
	if reAudio.MatchString(tok) {
		return kAudio, tok
	}
	if m := reVersion.FindStringSubmatch(tok); m != nil {
		return kVersion, m[1]
	}
	if lt == "batch" {
		return kBatch, ""
	}
	if otherMeta[lt] {
		return kOther, ""
	}
	return kNone, ""
}

type parser struct {
	r            Result
	season       int // from title tail ("Season 2", "2nd Season", "S2", "II")
	part         int // from "Part 2"
	brSeason     int // from a bracket "(Season 1)"
	brPart       int
	brEp         int // from a bracket "[05]"
	brEpVer      int
	brRangeStart int // from a bracket "(01-12)"
	brRangeEnd   int
	recap        bool
	epFound      bool // an explicit episode marker was found
	weakEp       bool // episode came from a bare trailing number
	altTitles    []string
}

func (p *parser) apply(k metaKind, v string) {
	switch k {
	case kRes:
		if p.r.Resolution == "" {
			p.r.Resolution = strings.ToLower(v)
		}
	case kCodec:
		if p.r.Codec == "" {
			p.r.Codec = v
		}
	case kSource:
		if p.r.Source == "" {
			p.r.Source = v
		}
	case kAudio:
		if p.r.Audio == "" {
			p.r.Audio = v
		}
	case kVersion:
		if n, _ := strconv.Atoi(v); n > p.r.Version {
			p.r.Version = n
		}
	case kBatch:
		p.r.IsBatch = true
	}
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func isDash(t string) bool { return t == "-" || t == "–" || t == "—" }

// Parse parses a release title or file name. It never fails; when nothing
// useful is found, Title is "" and Confidence is 0.
func Parse(name string) Result {
	p := &parser{}
	p.r.Raw = name
	p.r.Version = 1
	s := strings.TrimSpace(name)

	// 1. extension
	if i := strings.LastIndexByte(s, '.'); i > 0 {
		if e := strings.ToLower(s[i+1:]); videoExts[e] {
			p.r.Ext = e
			s = strings.TrimSpace(s[:i])
		}
	}
	// 2. underscores used as spaces
	if strings.Count(s, "_") > strings.Count(s, " ") {
		s = strings.ReplaceAll(s, "_", " ")
	}
	// 3. leading [Group]
	if strings.HasPrefix(s, "[") || strings.HasPrefix(s, "【") {
		if loc := reBracket.FindStringSubmatchIndex(s); loc != nil && loc[0] == 0 {
			p.r.Group = strings.TrimSpace(s[loc[2]:loc[3]])
			s = strings.TrimSpace(s[loc[1]:])
		}
	}
	// 4. every other bracket: metadata, episode numbers, or alternate titles
	s = reBracket.ReplaceAllStringFunc(s, func(m string) string {
		inner := reBracket.FindStringSubmatch(m)[1]
		p.bracket(inner)
		return " "
	})
	s = reStars.ReplaceAllString(s, " ")
	// 5. dotted scene names: Sousou.no.Frieren.S01E05.1080p.WEB.H264-VARYG
	s = strings.TrimSpace(s)
	if !strings.Contains(s, " ") && strings.Count(s, ".") >= 2 {
		s = undot(reH26x.ReplaceAllString(s, "H$1"))
	}
	toks := strings.Fields(s)

	// 6. scene group glued to the last token: H264-VARYG
	if p.r.Group == "" && len(toks) > 1 {
		last := toks[len(toks)-1]
		if k, _ := classify(last); k == kNone {
			if i := strings.LastIndexByte(last, '-'); i > 0 && i < len(last)-1 {
				left, right := last[:i], last[i+1:]
				if k2, _ := classify(left); k2 != kNone && reAlnum.MatchString(right) && !isAllDigits(right) {
					p.r.Group = right
					toks[len(toks)-1] = left
				}
			}
		}
	}

	titleEnd, after := p.findEpisode(toks)
	for _, t := range toks[after:] {
		if k, v := classify(t); k != kNone {
			p.apply(k, v)
		}
	}
	p.title(toks[:titleEnd])

	// season precedence: SxxExx > title tail > bracket > Part N
	if p.r.Season == 0 {
		switch {
		case p.season > 0:
			p.r.Season = p.season
		case p.brSeason > 0:
			p.r.Season = p.brSeason
		case p.part > 0:
			p.r.Season = p.part
		case p.brPart > 0:
			p.r.Season = p.brPart
		}
	}
	if p.r.Title == "" && len(p.altTitles) > 0 {
		p.r.Title = p.altTitles[0]
	}
	if p.r.EpisodeEnd > p.r.Episode {
		p.r.IsBatch = true
	}
	// a season pack: season marker and no episode number
	if p.r.Episode == 0 && p.r.Season > 0 {
		p.r.IsBatch = true
	}
	if p.recap {
		p.r.Raw = name + RecapMarker
	}
	p.r.Confidence = p.confidence()
	return p.r
}

func isAllDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// undot turns dots into spaces, keeping dots that sit between a digit and a
// single following digit (05.5, 5.1, 2.0).
func undot(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] != '.' {
			continue
		}
		keep := i > 0 && i+1 < len(b) && isDigit(b[i-1]) && isDigit(b[i+1]) &&
			(i+2 >= len(b) || !isDigit(b[i+2]))
		if !keep {
			b[i] = ' '
		}
	}
	return string(b)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func (p *parser) bracket(inner string) {
	c := strings.TrimSpace(inner)
	switch {
	case c == "":
		return
	case reCRC.MatchString(c) && !isAllDigits(c):
		return
	case reRange.MatchString(c):
		m := reRange.FindStringSubmatch(c)
		if a, b := atoi(m[1]), atoi(m[2]); b > a && p.brRangeEnd == 0 {
			p.brRangeStart, p.brRangeEnd = a, b
		}
		return
	case reYear.MatchString(c):
		return
	case reEp.MatchString(c):
		m := reEp.FindStringSubmatch(c)
		if p.brEp == 0 {
			p.brEp = atoi(m[1])
			p.brEpVer = atoi(m[3])
			if m[2] != "" {
				p.recap = true
			}
		}
		return
	case reBrSeason.MatchString(c):
		m := reBrSeason.FindStringSubmatch(c)
		p.brSeason = atoi(m[1] + m[2] + m[3])
		return
	case reBrPart.MatchString(c):
		p.brPart = atoi(reBrPart.FindStringSubmatch(c)[1])
		return
	}
	known := 0
	for _, t := range strings.FieldsFunc(c, func(r rune) bool {
		return r == ' ' || r == ',' || r == '|' || r == '+' || r == '/'
	}) {
		k, v := classify(t)
		if k == kNone && strings.EqualFold(t, "complete") {
			k = kBatch
		}
		if k != kNone {
			known++
			p.apply(k, v)
		}
	}
	if known == 0 {
		p.altTitles = append(p.altTitles, c)
	}
}

// findEpisode locates the episode marker. It returns the index where the
// title ends and the index from which metadata tokens are read.
func (p *parser) findEpisode(toks []string) (titleEnd, after int) {
	n := len(toks)
	// a. S01E05, S01E01-E12, S01E05v2
	for i := n - 1; i >= 0; i-- {
		if m := reSxE.FindStringSubmatch(toks[i]); m != nil {
			p.r.Season = atoi(m[1])
			p.r.Episode = atoi(m[2])
			if m[3] != "" {
				p.r.EpisodeEnd = atoi(m[3])
			}
			if m[4] != "" {
				p.r.Version = atoi(m[4])
			}
			p.epFound = true
			return i, i + 1
		}
	}
	// b. "- 05", "- 05v2", "- 05.5", "- 01-12", "- 01 ~ 12", "- 01 - 12"
	for i := n - 2; i >= 0; i-- {
		if !isDash(toks[i]) {
			continue
		}
		next := toks[i+1]
		if m := reRange.FindStringSubmatch(next); m != nil {
			if a, b := atoi(m[1]), atoi(m[2]); b > a {
				p.r.Episode, p.r.EpisodeEnd = a, b
				p.epFound = true
				return i, i + 2
			}
		}
		if m := reEp.FindStringSubmatch(next); m != nil {
			p.setEp(m)
			p.epFound = true
			// "- 01 ~ 12" / "- 01 - 12" written with spaces
			if i+3 < n && (toks[i+2] == "~" || isDash(toks[i+2])) && isAllDigits(toks[i+3]) && atoi(toks[i+3]) > p.r.Episode {
				p.r.EpisodeEnd = atoi(toks[i+3])
				return i, i + 4
			}
			// "Title - 01 - 12": this dash is the second one
			if i >= 2 && isAllDigits(toks[i-1]) && isDash(toks[i-2]) && atoi(toks[i-1]) < p.r.Episode && p.r.Version == 1 && !p.recap {
				p.r.EpisodeEnd = p.r.Episode
				p.r.Episode = atoi(toks[i-1])
				return i - 2, i + 2
			}
			return i, i + 2
		}
	}
	// c. E05 / EP05 / #05 / "Episode 5" / 第5話
	for i := 0; i < n; i++ {
		if i > 0 {
			if m := reEpKey.FindStringSubmatch(toks[i]); m != nil {
				p.r.Episode = atoi(m[1])
				if m[2] != "" {
					p.r.Version = atoi(m[2])
				}
				p.epFound = true
				return i, i + 1
			}
			if reEpWord.MatchString(toks[i]) && i+1 < n {
				if m := reEp.FindStringSubmatch(toks[i+1]); m != nil {
					p.setEp(m)
					p.epFound = true
					return i, i + 2
				}
			}
		}
		if m := reJPEp.FindStringSubmatch(toks[i]); m != nil {
			p.r.Episode = atoi(m[1])
			p.epFound = true
			return i, i + 1
		}
	}
	// d. Vol.1 / Vol 1 / Vol.1-3 -> batch
	for i := 1; i < n; i++ {
		if reVol.MatchString(toks[i]) {
			p.r.IsBatch = true
			end := i + 1
			if i+1 < n && reVol.FindStringSubmatch(toks[i])[1] == "" && isAllDigits(toks[i+1]) {
				end = i + 2
			}
			return i, end
		}
	}
	// first metadata token after the title (movies, season packs, bare numbers)
	metaAt := n
	for i := 1; i < n; i++ {
		if k, _ := classify(toks[i]); k != kNone {
			metaAt = i
			break
		}
	}
	// e. bracketed episode or range: [05], (01-12)
	if p.brRangeEnd > 0 {
		p.r.Episode, p.r.EpisodeEnd = p.brRangeStart, p.brRangeEnd
		p.epFound = true
		return metaAt, metaAt
	}
	if p.brEp > 0 {
		p.r.Episode = p.brEp
		if p.brEpVer > 0 {
			p.r.Version = p.brEpVer
		}
		p.epFound = true
		return metaAt, metaAt
	}
	// f. bare trailing number before the metadata: "Title 05 1080p"
	if j := metaAt - 1; j >= 1 {
		if m := reEp.FindStringSubmatch(toks[j]); m != nil && !reYear.MatchString(toks[j]) {
			prev := strings.ToLower(toks[j-1])
			if prev != "season" && prev != "part" && prev != "vol" && prev != "vol." && prev != "movie" {
				p.setEp(m)
				p.weakEp = true
				return j, j + 1
			}
		}
	}
	return metaAt, metaAt
}

func (p *parser) setEp(m []string) {
	p.r.Episode = atoi(m[1])
	if m[2] != "" {
		p.recap = true
	}
	if m[3] != "" {
		p.r.Version = atoi(m[3])
	}
}

// title cleans the title tokens: strips dashes, a trailing year and the
// season markers at the end ("2nd Season", "Season 2", "S2", "II", "Part 2").
func (p *parser) title(toks []string) {
	t := append([]string(nil), toks...)
	trim := func() {
		for len(t) > 0 && isDash(t[len(t)-1]) {
			t = t[:len(t)-1]
		}
		for len(t) > 0 && isDash(t[0]) {
			t = t[1:]
		}
	}
	trim()
	for changed := true; changed && len(t) > 1; {
		changed = false
		n := len(t)
		last, prev := t[n-1], t[n-2]
		switch {
		case reYear.MatchString(last):
			t = t[:n-1]
			changed = true
		case strings.EqualFold(prev, "season") && reSmallNum.MatchString(last):
			p.setSeason(atoi(last))
			t = t[:n-2]
			changed = true
		case reOrdinal.MatchString(prev) && strings.EqualFold(last, "season"):
			p.setSeason(atoi(reOrdinal.FindStringSubmatch(prev)[1]))
			t = t[:n-2]
			changed = true
		case strings.EqualFold(prev, "part") && reSmallNum.MatchString(last):
			if p.part == 0 {
				p.part = atoi(last)
			}
			t = t[:n-2]
			changed = true
		case reSTok.MatchString(last):
			p.setSeason(atoi(reSTok.FindStringSubmatch(last)[1]))
			t = t[:n-1]
			changed = true
		case reSeasonGlue.MatchString(last):
			p.setSeason(atoi(reSeasonGlue.FindStringSubmatch(last)[1]))
			t = t[:n-1]
			changed = true
		case romanSeason[last] > 0:
			p.setSeason(romanSeason[last])
			t = t[:n-1]
			changed = true
		}
		trim()
	}
	p.r.Title = strings.Trim(strings.Join(t, " "), " -–—:,")
}

func (p *parser) setSeason(n int) {
	if p.season == 0 {
		p.season = n
	}
}

func (p *parser) confidence() float64 {
	if p.r.Title == "" {
		return 0
	}
	c := 0.3
	switch {
	case p.epFound:
		c += 0.3
	case p.weakEp:
		c += 0.15
	case p.r.IsBatch:
		c += 0.2
	default: // movie: no episode is a valid outcome
		c += 0.1
	}
	if p.r.Group != "" {
		c += 0.2
	}
	if p.r.Resolution != "" {
		c += 0.1
	}
	if p.r.Ext != "" || p.r.Codec != "" || p.r.Source != "" {
		c += 0.1
	}
	if c > 1 {
		c = 1
	}
	return c
}
