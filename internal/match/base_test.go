package match

import "testing"

// SPEC v1.3 follow-up D: title bases for the prequel chain walk.
func TestBaseTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		// the examples from the brief
		{"JoJo no Kimyou na Bouken: Steel Ball Run - 2nd & 3rd STAGE", "jojo no kimyou na bouken steel ball run"},
		{"JoJo no Kimyou na Bouken: Stardust Crusaders - Egypt-hen", "jojo no kimyou na bouken stardust crusaders"},
		{"Black Clover 2nd Season", "black clover"},
		{"Shingeki no Kyojin Season 3 Part 2", "shingeki no kyojin"},
		{"Kusuriya no Hitorigoto 3rd Season", "kusuriya no hitorigoto"},
		{"Jujutsu Kaisen", "jujutsu kaisen"},
		{"Jojo no Kimyou na Bouken (2012)", "jojo no kimyou na bouken"},
		// the other trailing groups
		{"JoJo no Kimyou na Bouken: Steel Ball Run", "jojo no kimyou na bouken steel ball run"},
		{"JoJo no Kimyou na Bouken: Stardust Crusaders", "jojo no kimyou na bouken stardust crusaders"},
		{"JoJo no Kimyou na Bouken: Stone Ocean", "jojo no kimyou na bouken stone ocean"},
		{"Dr. Stone: Stone Wars Cour 2", "dr stone stone wars"},
		{"Dr. Stone 2nd Cour", "dr stone"},
		{"Black Clover (2026)", "black clover"},
		{"Black Clover 2026", "black clover"},
		{"Shingeki no Kyojin: The Final Season", "shingeki no kyojin"},
		{"Shingeki no Kyojin: The Final Season Part 2", "shingeki no kyojin"},
		{"Shingeki no Kyojin: The Final Season - Kanketsu-hen", "shingeki no kyojin"},
		{"Some Show: The Final Chapter", "some show"},
		{"Some Show Final", "some show"},
		{"Mob Psycho 100 III", "mob psycho 100"},
		{"Kusuriya no Hitorigoto S2", "kusuriya no hitorigoto"},
		{"[Oshi no Ko] 2nd Season", "oshi no ko"},
		// arc markers: one or two words, counted from the last separator
		{"Kimetsu no Yaiba: Yuukaku-hen", "kimetsu no yaiba"},
		{"Kimetsu no Yaiba: Mugen Ressha-hen", "kimetsu no yaiba"},
		{"Bleach: Sennen Kessen-hen", "bleach"},
		{"Some Show: Dark Tournament Arc", "some show"},
		{"Kimetsu no Yaiba: Katanakaji no Sato-hen", "kimetsu no yaiba katanakaji no sato hen"}, // 3 words: not an arc marker
		{"Some Show Egypt-hen", "some show"},                                                    // no separator: one word
		{"JoJo no Kimyou na Bouken: Stardust Crusaders - Battle in Egypt", "jojo no kimyou na bouken stardust crusaders battle in egypt"},
		// never stripped to nothing
		{"Final", "final"},
		{"Season 2", "season 2"},
		{"2026", "2026"},
		{"", ""},
	}
	for _, c := range cases {
		if got := BaseTitle(c.in); got != c.want {
			t.Errorf("BaseTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// equality, not prefix: the 2012 series is not Stardust Crusaders
	if BaseTitle("Jojo no Kimyou na Bouken (2012)") == BaseTitle("JoJo no Kimyou na Bouken: Stardust Crusaders") {
		t.Error("2012 series and Stardust Crusaders must differ")
	}
}
