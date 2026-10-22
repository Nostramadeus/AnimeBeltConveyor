package match

import (
	"testing"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/parse"
)

var frieren = ShowTitles{
	ID: 154587, Romaji: "Sousou no Frieren", English: "Frieren: Beyond Journey's End",
	Native: "葬送のフリーレン", Synonyms: []string{"Frieren at the Funeral"},
	EpisodesTotal: 28, ListStatus: "watching", SeasonYear: 2023,
}

var onePiece = ShowTitles{ID: 21, Romaji: "ONE PIECE", English: "ONE PIECE", ListStatus: "watching", SeasonYear: 1999}

var others = []ShowTitles{
	{ID: 1, Romaji: "Dandadan", English: "DAN DA DAN", EpisodesTotal: 12, ListStatus: "watching", SeasonYear: 2024},
	{ID: 2, Romaji: "Kimetsu no Yaiba", English: "Demon Slayer: Kimetsu no Yaiba", EpisodesTotal: 26, ListStatus: "completed", SeasonYear: 2019},
}

func shows(extra ...ShowTitles) []ShowTitles {
	return append(append([]ShowTitles{}, others...), extra...)
}

type exp struct {
	id, ep int
	ok     bool
}

func run(t *testing.T, name string, list []ShowTitles, w exp) float64 {
	t.Helper()
	id, ep, ok, score, _ := Match(parse.Parse(name), list)
	if id != w.id || ep != w.ep || ok != w.ok {
		t.Errorf("Match(%q) = id %d ep %d ok %v score %.3f; want id %d ep %d ok %v", name, id, ep, ok, score, w.id, w.ep, w.ok)
	}
	if ok && (score < MinJaccard || score > 1) {
		t.Errorf("Match(%q) score %v out of range", name, score)
	}
	if !ok && score != 0 {
		t.Errorf("Match(%q) no match but score %v", name, score)
	}
	return score
}

func TestFrierenByRomaji(t *testing.T) {
	s := run(t, "[SubsPlease] Sousou no Frieren - 05 (1080p) [ABCD1234].mkv", shows(frieren), exp{154587, 5, true})
	if s != 1 {
		t.Errorf("exact romaji score %v, want 1", s)
	}
	run(t, "Sousou.no.Frieren.S01E05.1080p.WEB.H264-VARYG.mkv", shows(frieren), exp{154587, 5, true})
}

func TestFrierenByEnglish(t *testing.T) {
	// "Frieren - Beyond Journey's End" (S01) vs english "Frieren: Beyond Journey's End" (no season)
	run(t, "[Judas] Frieren - Beyond Journey's End - S01E05v2 [1080p][HEVC x265 10bit][Multi-Subs].mkv", shows(frieren), exp{154587, 5, true})
	// apostrophe dropped in the release name still matches
	run(t, "[Group] Frieren Beyond Journeys End - 12 [1080p].mkv", shows(frieren), exp{154587, 12, true})
	// synonym
	run(t, "[Group] Frieren at the Funeral - 03 [1080p].mkv", shows(frieren), exp{154587, 3, true})
}

func TestOnePieceAbsolute(t *testing.T) {
	run(t, "[SubsPlease] One Piece - 1122 (1080p) [ABCD1234].mkv", shows(onePiece), exp{21, 1122, true})

	// A split entry with absolute file numbering: AniList ep 1 = file ep 1089.
	egghead := ShowTitles{ID: 500, Romaji: "One Piece Egghead", SearchQuery: "One Piece", EpisodeOffset: 1088,
		EpisodesTotal: 40, ListStatus: "watching", SeasonYear: 2026}
	run(t, "[SubsPlease] One Piece - 1100 (1080p) [ABCD1234].mkv", []ShowTitles{egghead}, exp{500, 12, true})
	// file 1088 -> ep 0 -> rejected
	run(t, "[SubsPlease] One Piece - 1088 (1080p) [ABCD1234].mkv", []ShowTitles{egghead}, exp{0, 0, false})
	// total 40: ep 41 (total+1) allowed, 42 rejected
	run(t, "[SubsPlease] One Piece - 1129 (1080p) [ABCD1234].mkv", []ShowTitles{egghead}, exp{500, 41, true})
	run(t, "[SubsPlease] One Piece - 1130 (1080p) [ABCD1234].mkv", []ShowTitles{egghead}, exp{0, 0, false})
}

func TestOffsetPicksTheRightSeason(t *testing.T) {
	// Season 1 (12 eps) and Season 2 numbered 13-24 on the files.
	s1 := ShowTitles{ID: 10, Romaji: "Title", EpisodesTotal: 12, ListStatus: "watching", SeasonYear: 2023}
	s2 := ShowTitles{ID: 11, Romaji: "Title 2nd Season", EpisodeOffset: 12, EpisodesTotal: 12, ListStatus: "watching", SeasonYear: 2024}
	list := []ShowTitles{s1, s2}
	// file ep 15: s1 rejects (15 > 13), s2 gives 3
	run(t, "[Group] Title - 15 [1080p].mkv", list, exp{11, 3, true})
	// file ep 13 is total+1 for s1 and ep 1 for s2: tie on score 1.0 -> both watching -> newer year (s2)
	run(t, "[Group] Title - 13 [1080p].mkv", list, exp{11, 1, true})
}

func TestSeason2Variants(t *testing.T) {
	s1 := ShowTitles{ID: 1, Romaji: "Kusuriya no Hitorigoto", English: "The Apothecary Diaries", EpisodesTotal: 24, ListStatus: "completed", SeasonYear: 2023}
	s2 := ShowTitles{ID: 2, Romaji: "Kusuriya no Hitorigoto 2nd Season", English: "The Apothecary Diaries Season 2", EpisodesTotal: 24, ListStatus: "watching", SeasonYear: 2025}
	list := []ShowTitles{s1, s2}
	for _, name := range []string{
		"[Group] Kusuriya no Hitorigoto 2nd Season - 05 [720p].mkv",
		"Kusuriya no Hitorigoto S2 - 05",
		"Kusuriya no Hitorigoto Season 2 - 05",
		"Kusuriya no Hitorigoto II - 05",
		"[Group] Apothecary Diaries S02E05 [1080p].mkv",
	} {
		run(t, name, list, exp{2, 5, true})
	}
	// explicit season 1 never matches the season-2 entry
	run(t, "Kusuriya.no.Hitorigoto.S01E05.1080p.WEB.H264-GRP.mkv", []ShowTitles{s2}, exp{0, 0, false})
	run(t, "Kusuriya.no.Hitorigoto.S01E05.1080p.WEB.H264-GRP.mkv", list, exp{1, 5, true})

	// season 3 via roman numeral on the show side
	mob := ShowTitles{ID: 3, Romaji: "Mob Psycho 100 III", EpisodesTotal: 12, ListStatus: "watching", SeasonYear: 2022}
	run(t, "[SubsPlease] Mob Psycho 100 S3 - 05 (1080p) [ABCD1234].mkv", []ShowTitles{mob}, exp{3, 5, true})
	run(t, "[SubsPlease] Mob Psycho 100 S2 - 05 (1080p) [ABCD1234].mkv", []ShowTitles{mob}, exp{0, 0, false})
}

func TestTieBrokenByWatching(t *testing.T) {
	old := ShowTitles{ID: 7, Romaji: "Hunter x Hunter", ListStatus: "watching", SeasonYear: 1999}
	remake := ShowTitles{ID: 8, Romaji: "Hunter x Hunter", ListStatus: "completed", SeasonYear: 2011}
	// watching beats the more recent season_year
	run(t, "[Group] Hunter x Hunter - 05 [1080p].mkv", []ShowTitles{remake, old}, exp{7, 5, true})
	run(t, "[Group] Hunter x Hunter - 05 [1080p].mkv", []ShowTitles{old, remake}, exp{7, 5, true})
	// neither watching: most recent season_year
	old.ListStatus = "planning"
	run(t, "[Group] Hunter x Hunter - 05 [1080p].mkv", []ShowTitles{old, remake}, exp{8, 5, true})
}

func TestJaccard(t *testing.T) {
	slime := ShowTitles{ID: 9, Romaji: "Tensei shitara Slime Datta Ken", English: "That Time I Got Reincarnated as a Slime", ListStatus: "watching"}
	// 7 of 8 tokens: Jaccard 0.875 >= 0.85
	s := run(t, "[Group] That Time I Got Reincarnated as Slime - 05 [1080p].mkv", []ShowTitles{slime}, exp{9, 5, true})
	if s < 0.874 || s > 0.876 {
		t.Errorf("jaccard score %v, want 0.875", s)
	}
	// 3 of 5 tokens: below threshold
	run(t, "[Group] Tensei Slime Ken - 05 [1080p].mkv", []ShowTitles{slime}, exp{0, 0, false})
}

func TestNoMatch(t *testing.T) {
	run(t, "[SubsPlease] Some Other Show - 05 (1080p) [ABCD1234].mkv", shows(frieren), exp{0, 0, false})
	run(t, "", shows(frieren), exp{0, 0, false})
	run(t, "[SubsPlease] Sousou no Frieren - 05 (1080p).mkv", nil, exp{0, 0, false})
}

func TestBatchAndMovie(t *testing.T) {
	vinland := ShowTitles{ID: 4, Romaji: "Vinland Saga Season 2", EpisodesTotal: 24, ListStatus: "watching", SeasonYear: 2023}
	run(t, "[Judas] Vinland Saga (Season 2) [1080p][HEVC x265 10bit][Multi-Subs] (Batch)", []ShowTitles{vinland}, exp{4, 0, true})
	run(t, "[Group] Vinland Saga S2 (01-24) [1080p]", []ShowTitles{vinland}, exp{4, 1, true})
	movie := ShowTitles{ID: 5, Romaji: "Kimi no Na wa.", English: "Your Name.", EpisodesTotal: 1, ListStatus: "planning", SeasonYear: 2016}
	run(t, "[Group] Kimi no Na wa (2016) [1080p].mkv", []ShowTitles{movie}, exp{5, 1, true})
	// no episode number on a multi-episode show: rejected
	run(t, "[Group] Dandadan [1080p].mkv", shows(), exp{0, 0, false})
}

func TestSearchQueryCandidate(t *testing.T) {
	s := ShowTitles{ID: 6, Romaji: "Ore dake Level Up na Ken", SearchQuery: "Solo Leveling", ListStatus: "watching"}
	run(t, "[Group] Solo Leveling - 05 [1080p].mkv", []ShowTitles{s}, exp{6, 5, true})
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"Frieren: Beyond Journey's End": "frieren beyond journeys end",
		"Frieren: Beyond Journey’s End": "frieren beyond journeys end",
		"The Apothecary Diaries":        "apothecary diaries",
		"the":                           "the",
		"ＳＰＹ×ＦＡＭＩＬＹ":                    "spy family",
		"Re:ZERO -Starting Life-":       "re zero starting life",
		"  Kaiju   No. 8 ":              "kaiju no 8",
		"葬送のフリーレン":                      "葬送のフリーレン",
		"Ｄｒ．ＳＴＯＮＥ":                      "dr stone",
		"Dr.　Stone":                     "dr stone",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}
