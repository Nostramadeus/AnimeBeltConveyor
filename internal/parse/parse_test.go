package parse

import "testing"

type want struct {
	Title      string
	Episode    int
	EpisodeEnd int
	Group      string
	Resolution string
	Version    int
	IsBatch    bool
	Season     int
	Recap      bool
}

func check(t *testing.T, name string, got Result, w want) {
	t.Helper()
	if w.Version == 0 {
		w.Version = 1
	}
	g := want{got.Title, got.Episode, got.EpisodeEnd, got.Group, got.Resolution, got.Version, got.IsBatch, got.Season, got.IsRecap()}
	if g != w {
		t.Errorf("Parse(%q)\n got  %+v\n want %+v", name, g, w)
	}
	if got.Confidence < 0 || got.Confidence > 1 {
		t.Errorf("Parse(%q) confidence %v out of 0..1", name, got.Confidence)
	}
}

// TestSpecLines covers every filename line in SPEC section 5, verbatim.
func TestSpecLines(t *testing.T) {
	cases := []struct {
		name string
		w    want
	}{
		{"[SubsPlease] Sousou no Frieren - 05 (1080p) [ABCD1234].mkv",
			want{Title: "Sousou no Frieren", Episode: 5, Group: "SubsPlease", Resolution: "1080p"}},
		{"[Erai-raws] Sousou no Frieren - 05 [1080p][HEVC][Multiple Subtitle][ENG].mkv",
			want{Title: "Sousou no Frieren", Episode: 5, Group: "Erai-raws", Resolution: "1080p"}},
		{"[Judas] Frieren - Beyond Journey's End - S01E05v2 [1080p][HEVC x265 10bit][Multi-Subs].mkv",
			want{Title: "Frieren - Beyond Journey's End", Episode: 5, Group: "Judas", Resolution: "1080p", Version: 2, Season: 1}},
		{"[EMBER] Sousou no Frieren (2023) (Season 1) [BDRip] [1080p HEVC 10 bits] (Frieren Beyond Journey's End)",
			want{Title: "Sousou no Frieren", Group: "EMBER", Resolution: "1080p", IsBatch: true, Season: 1}},
		{"Sousou.no.Frieren.S01E05.1080p.WEB.H264-VARYG.mkv",
			want{Title: "Sousou no Frieren", Episode: 5, Group: "VARYG", Resolution: "1080p", Season: 1}},
		{"[Group] Title 2nd Season - 03 [720p].mkv",
			want{Title: "Title", Episode: 3, Group: "Group", Resolution: "720p", Season: 2}},
		{"Title S2 - 03", want{Title: "Title", Episode: 3, Season: 2}},
		{"Title Season 2 - 03", want{Title: "Title", Episode: 3, Season: 2}},
		{"Title II - 03", want{Title: "Title", Episode: 3, Season: 2}},
		{"[Group] Title - 1041 (1080p)",
			want{Title: "Title", Episode: 1041, Group: "Group", Resolution: "1080p"}},
		{"[Group] Title - 05.5 ...", want{Title: "Title", Episode: 5, Group: "Group", Recap: true}},
		{"[Group] Title - 01-12 (Batch)",
			want{Title: "Title", Episode: 1, EpisodeEnd: 12, Group: "Group", IsBatch: true}},
		{"[Group] Title (01-12) [1080p]",
			want{Title: "Title", Episode: 1, EpisodeEnd: 12, Group: "Group", Resolution: "1080p", IsBatch: true}},
		{"Title - Vol.1", want{Title: "Title", IsBatch: true}},
		{"[Group] Title - 05 END", want{Title: "Title", Episode: 5, Group: "Group"}},
		{"[Group] Title - 05 [v2]", want{Title: "Title", Episode: 5, Group: "Group", Version: 2}},
		{"Title - 05v2", want{Title: "Title", Episode: 5, Version: 2}},
		// "Movies with no episode -> Episode 0, IsBatch false"
		{"[Group] Kimi no Na wa (2016) [1080p].mkv",
			want{Title: "Kimi no Na wa", Group: "Group", Resolution: "1080p"}},
	}
	for _, c := range cases {
		check(t, c.name, Parse(c.name), c.w)
	}
}

func TestEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		w    want
	}{
		// recap with a real file name
		{"[Group] Title - 05.5 (1080p) [ABCD1234].mkv",
			want{Title: "Title", Episode: 5, Group: "Group", Resolution: "1080p", Recap: true}},
		// resolutions
		{"[Group] Title - 05 (2160p).mkv", want{Title: "Title", Episode: 5, Group: "Group", Resolution: "2160p"}},
		{"[Group] Title - 05 [4K].mkv", want{Title: "Title", Episode: 5, Group: "Group", Resolution: "2160p"}},
		{"[Group] Title - 05 [480p].mkv", want{Title: "Title", Episode: 5, Group: "Group", Resolution: "480p"}},
		{"[Group] Title - 05 [1920x1080].mkv", want{Title: "Title", Episode: 5, Group: "Group", Resolution: "1080p"}},
		{"[Group] Title - 05 [720p].mkv", want{Title: "Title", Episode: 5, Group: "Group", Resolution: "720p"}},
		// versions
		{"Title.S01E05.v3.1080p.WEB.H264-GRP.mkv",
			want{Title: "Title", Episode: 5, Group: "GRP", Resolution: "1080p", Version: 3, Season: 1}},
		{"Title - 05.v3", want{Title: "Title", Episode: 5, Version: 3}},
		{"[Group] Title - 05v2 (1080p).mkv", want{Title: "Title", Episode: 5, Group: "Group", Resolution: "1080p", Version: 2}},
		// ranges
		{"[Group] Title - 01~12 [1080p]",
			want{Title: "Title", Episode: 1, EpisodeEnd: 12, Group: "Group", Resolution: "1080p", IsBatch: true}},
		{"[Group] Title (01~12)", want{Title: "Title", Episode: 1, EpisodeEnd: 12, Group: "Group", IsBatch: true}},
		{"[Group] Title - 01 ~ 12 [Batch]", want{Title: "Title", Episode: 1, EpisodeEnd: 12, Group: "Group", IsBatch: true}},
		{"[Group] Title - 01 - 12 [1080p]",
			want{Title: "Title", Episode: 1, EpisodeEnd: 12, Group: "Group", Resolution: "1080p", IsBatch: true}},
		{"Title.S01E01-E12.1080p.BluRay.x265-GRP",
			want{Title: "Title", Episode: 1, EpisodeEnd: 12, Group: "GRP", Resolution: "1080p", Season: 1, IsBatch: true}},
		{"Title - Vol. 2 [1080p]", want{Title: "Title", Resolution: "1080p", IsBatch: true}},
		// seasons
		{"[Group] Title Part 2 - 05 [1080p].mkv",
			want{Title: "Title", Episode: 5, Group: "Group", Resolution: "1080p", Season: 2}},
		{"[Group] Title III - 07 [1080p].mkv",
			want{Title: "Title", Episode: 7, Group: "Group", Resolution: "1080p", Season: 3}},
		{"[Group] Title Season 3 Part 2 - 07 [1080p].mkv",
			want{Title: "Title", Episode: 7, Group: "Group", Resolution: "1080p", Season: 3}},
		{"[Group] Title S2 [1080p]", want{Title: "Title", Group: "Group", Resolution: "1080p", Season: 2, IsBatch: true}},
		{"[Group] Title Season 2 [1080p]", want{Title: "Title", Group: "Group", Resolution: "1080p", Season: 2, IsBatch: true}},
		// other shapes
		{"[Group]_Title_-_05_[720p].mkv", want{Title: "Title", Episode: 5, Group: "Group", Resolution: "720p"}},
		{"[Group] Title [05][720p].mkv", want{Title: "Title", Episode: 5, Group: "Group", Resolution: "720p"}},
		{"[Group] Title - 05 - The Journey Begins [1080p].mkv",
			want{Title: "Title", Episode: 5, Group: "Group", Resolution: "1080p"}},
		{"[Group] 86 - Eighty Six - 05 [1080p].mkv",
			want{Title: "86 - Eighty Six", Episode: 5, Group: "Group", Resolution: "1080p"}},
		{"[Group] Title EP05 [1080p].mkv", want{Title: "Title", Episode: 5, Group: "Group", Resolution: "1080p"}},
		{"[Group] Title Episode 5 [1080p].mkv", want{Title: "Title", Episode: 5, Group: "Group", Resolution: "1080p"}},
		{"[Group] Title 05 [1080p].mkv", want{Title: "Title", Episode: 5, Group: "Group", Resolution: "1080p"}},
		{"[SubsPlease] Dr. Stone - 05 (1080p) [ABCD1234].mkv",
			want{Title: "Dr. Stone", Episode: 5, Group: "SubsPlease", Resolution: "1080p"}},
		{"[SubsPlease] Shingeki no Kyojin - The Final Season Part 2 - 05 (1080p) [ABCD1234].mkv",
			want{Title: "Shingeki no Kyojin - The Final Season", Episode: 5, Group: "SubsPlease", Resolution: "1080p", Season: 2}},
		// movies
		{"[Beatrice-Raws] Suzume no Tojimari [BDRip 1920x1080 HEVC TrueHD].mkv",
			want{Title: "Suzume no Tojimari", Group: "Beatrice-Raws", Resolution: "1080p"}},
		{"Kimi.no.Na.wa.2016.1080p.BluRay.x264-GRP.mkv",
			want{Title: "Kimi no Na wa", Group: "GRP", Resolution: "1080p"}},
	}
	for _, c := range cases {
		check(t, c.name, Parse(c.name), c.w)
	}
}

func TestMetadataFields(t *testing.T) {
	r := Parse("[Beatrice-Raws] Suzume no Tojimari [BDRip 1920x1080 HEVC TrueHD].mkv")
	if r.Source != "BDRip" || r.Codec != "HEVC" || r.Audio != "TrueHD" || r.Ext != "mkv" {
		t.Errorf("metadata: %+v", r)
	}
	r = Parse("Sousou.no.Frieren.S01E05.1080p.WEB.H264-VARYG.mkv")
	if r.Source != "WEB" || r.Codec != "H264" || r.Ext != "mkv" {
		t.Errorf("scene metadata: %+v", r)
	}
	r = Parse("[EMBER] Sousou no Frieren (2023) (Season 1) [BDRip] [1080p HEVC 10 bits] (Frieren Beyond Journey's End)")
	if r.Source != "BDRip" || r.Codec != "HEVC" || r.Ext != "" {
		t.Errorf("EMBER metadata: %+v", r)
	}
	if r.Raw != "[EMBER] Sousou no Frieren (2023) (Season 1) [BDRip] [1080p HEVC 10 bits] (Frieren Beyond Journey's End)" {
		t.Errorf("Raw changed for a non-recap: %q", r.Raw)
	}
}

func TestConfidence(t *testing.T) {
	good := Parse("[SubsPlease] Sousou no Frieren - 05 (1080p) [ABCD1234].mkv")
	weak := Parse("Title 05")
	none := Parse("")
	if good.Confidence < 0.8 {
		t.Errorf("good confidence %v", good.Confidence)
	}
	if weak.Confidence >= good.Confidence {
		t.Errorf("weak %v >= good %v", weak.Confidence, good.Confidence)
	}
	if none.Confidence != 0 || none.Title != "" {
		t.Errorf("empty input: %+v", none)
	}
}

func TestFansubBanner(t *testing.T) {
	r := Parse("【喵萌奶茶屋】★10月新番★[葬送的芙莉莲 / Sousou no Frieren][05][1080p][简日双语]")
	if r.Group != "喵萌奶茶屋" || r.Episode != 5 || r.Title != "葬送的芙莉莲 / Sousou no Frieren" || r.Resolution != "1080p" {
		t.Errorf("fansub banner: %+v", r)
	}
}

// FuzzParse checks that Parse never panics and keeps its invariants.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"[SubsPlease] Sousou no Frieren - 05 (1080p) [ABCD1234].mkv",
		"Sousou.no.Frieren.S01E05.1080p.WEB.H264-VARYG.mkv",
		"[Group] Title - 01 - 12 [1080p]", "Title - Vol.1", "[][(]】【", "- - -", "S01E", "第話",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		r := Parse(s)
		if r.Confidence < 0 || r.Confidence > 1 {
			t.Fatalf("confidence %v", r.Confidence)
		}
		if r.Version < 1 {
			t.Fatalf("version %d", r.Version)
		}
		if r.EpisodeEnd != 0 && r.EpisodeEnd <= r.Episode {
			t.Fatalf("range %d-%d", r.Episode, r.EpisodeEnd)
		}
	})
}
