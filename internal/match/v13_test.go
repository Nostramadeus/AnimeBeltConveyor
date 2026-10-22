package match

import (
	"testing"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/parse"
)

// SPEC v1.3 section 1.3: worked examples for absolute vs season numbering.
func TestAbsoluteNumbering(t *testing.T) {
	jjk := ShowTitles{ID: 1, Romaji: "Jujutsu Kaisen 2nd Season", English: "JUJUTSU KAISEN Season 2", AbsOffset: 24,
		EpisodesTotal: 23, EpisodesKnown: 23, ListStatus: "watching", SeasonYear: 2023}
	bc := ShowTitles{ID: 2, Romaji: "Black Clover", AbsOffset: 170, EpisodesKnown: 4, ListStatus: "watching", SeasonYear: 2026}
	bcDone := bc
	bcDone.EpisodesTotal, bcDone.EpisodesKnown = 51, 51
	sbr := ShowTitles{ID: 3, Romaji: "Steel Ball Run 2nd & 3rd STAGE", Synonyms: []string{"Steel Ball Run Part 2"}, AbsOffset: 1,
		EpisodesTotal: 24, EpisodesKnown: 24, ListStatus: "watching", SeasonYear: 2026}
	manual := ShowTitles{ID: 4, Romaji: "Title", EpisodeOffset: 12, EpisodesTotal: 12, EpisodesKnown: 12, ListStatus: "watching"}
	none := ShowTitles{ID: 5, Romaji: "Title", EpisodesTotal: 12, EpisodesKnown: 12, ListStatus: "watching"}
	cases := []struct {
		show      ShowTitles
		name      string
		wantEp    int
		wantOK    bool
		wantShift int
	}{
		{jjk, "[SubsPlease] Jujutsu Kaisen - 25 (1080p) [ABCD1234].mkv", 1, true, 24},
		{jjk, "[Erai-raws] Jujutsu Kaisen 2nd Season - 01 [1080p].mkv", 1, true, 0},
		{jjk, "[SubsPlease] Jujutsu Kaisen - 05 (1080p) [ABCD1234].mkv", 5, true, 0}, // absolute preferred, -19 invalid -> season 5
		{bc, "[SubsPlease] Black Clover - 173 (1080p) [ABCD1234].mkv", 3, true, 170},
		{bc, "[Erai-raws] Black Clover - 03 [1080p].mkv", 3, true, 0},
		{bcDone, "[SubsPlease] Black Clover - 52 (1080p) [ABCD1234].mkv", 0, false, 0},
		{sbr, "[SubsPlease] Steel Ball Run - 05 (1080p) [ABCD1234].mkv", 4, true, 1},
		{sbr, "[Group] Steel Ball Run Part 2 - 04 [1080p].mkv", 4, true, 0},
		{manual, "[Group] Title - 13 [1080p].mkv", 1, true, 12},
		{manual, "[Group] Title - 01 [1080p].mkv", 1, true, 0},
		{none, "[Group] Title - 13 [1080p].mkv", 13, true, 0},
	}
	for _, c := range cases {
		id, ep, ok, _, shift := Match(parse.Parse(c.name), []ShowTitles{c.show})
		wantID := 0
		if c.wantOK {
			wantID = c.show.ID
		}
		if id != wantID || ep != c.wantEp || ok != c.wantOK || shift != c.wantShift {
			t.Errorf("%s / %q: id %d ep %d ok %v shift %d; want ep %d ok %v shift %d",
				c.show.Romaji, c.name, id, ep, ok, shift, c.wantEp, c.wantOK, c.wantShift)
		}
	}
}

// The manual offset wins over AbsOffset; no length known means a number above
// the offset cannot be a season number.
func TestOffsetRules(t *testing.T) {
	s := ShowTitles{ID: 1, Romaji: "Black Clover", AbsOffset: 170, EpisodeOffset: 100, ListStatus: "watching"}
	if _, ep, ok, _, shift := Match(parse.Parse("[SubsPlease] Black Clover - 103 (1080p).mkv"), []ShowTitles{s}); !ok || ep != 3 || shift != 100 {
		t.Errorf("manual offset: ep %d ok %v shift %d, want 3 true 100", ep, ok, shift)
	}
	s.EpisodeOffset = 0
	// nothing known: 3 <= O -> season; 171 > O -> absolute 1
	if _, ep, ok, _, _ := Match(parse.Parse("[Group] Black Clover - 03.mkv"), []ShowTitles{s}); !ok || ep != 3 {
		t.Errorf("unknown length, 03: ep %d ok %v", ep, ok)
	}
	if _, ep, ok, _, shift := Match(parse.Parse("[Group] Black Clover - 171.mkv"), []ShowTitles{s}); !ok || ep != 1 || shift != 170 {
		t.Errorf("unknown length, 171: ep %d ok %v shift %d", ep, ok, shift)
	}
	// batch range: start read as absolute -> shift O for the end too
	r := parse.Parse("[Group] Black Clover (171-174) [1080p]")
	if !r.IsBatch || r.Episode != 171 || r.EpisodeEnd != 174 {
		t.Fatalf("parse batch: %+v", r)
	}
	if _, ep, ok, _, shift := Match(r, []ShowTitles{s}); !ok || ep != 1 || r.EpisodeEnd-shift != 4 {
		t.Errorf("batch: ep %d ok %v end %d", ep, ok, r.EpisodeEnd-shift)
	}
}

func TestSeasonSuffix(t *testing.T) {
	cases := []struct {
		in   string
		base string
		num  int
	}{
		{"Kusuriya no Hitorigoto 2nd Season", "kusuriya no hitorigoto", 2},
		{"Attack on Titan 3rd Part", "attack on titan", 3},
		{"Dr Stone 2nd Cour", "dr stone", 2},
		{"Steel Ball Run 2nd Stage", "steel ball run", 2},
		{"Steel Ball Run 2nd & 3rd STAGE", "steel ball run", 2},
		{"Steel Ball Run Part 2", "steel ball run", 2},
		{"Mob Psycho 100 III", "mob psycho 100", 3},
		{"Frieren", "frieren", 0},
	}
	for _, c := range cases {
		base, num := seasonSuffix(Normalize(c.in))
		if base != c.base || num != c.num {
			t.Errorf("seasonSuffix(%q) = %q %d, want %q %d", c.in, base, num, c.base, c.num)
		}
	}
	// candidates of a "2nd & 3rd STAGE" show carry season 2 on the full and the base title
	got := map[candidate]bool{}
	for _, c := range candidates(&ShowTitles{Romaji: "Steel Ball Run 2nd & 3rd STAGE"}) {
		got[c] = true
	}
	if !got[candidate{"steel ball run 2nd 3rd stage", 2}] || !got[candidate{"steel ball run", 2}] {
		t.Errorf("candidates = %v", got)
	}
	if !showHasMarker(&ShowTitles{Romaji: "x", Synonyms: []string{"Steel Ball Run Part 2"}}) || showHasMarker(&ShowTitles{Romaji: "Black Clover"}) {
		t.Error("showHasMarker")
	}
}
