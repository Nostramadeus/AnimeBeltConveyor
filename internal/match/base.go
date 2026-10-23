package match

import (
	"regexp"
	"strings"
)

// Title base (SPEC v1.3 follow-up D): the part of a title that names the
// series, so "Black Clover 2nd Season" and "Black Clover" share a base while
// "JoJo no Kimyou na Bouken: Steel Ball Run" and "JoJo no Kimyou na Bouken:
// Stone Ocean" do not. The prequel chain walk stops where the base changes.

var (
	reBaseCour = regexp.MustCompile(`^(.+) cour \d{1,2}$`)
	reBaseYear = regexp.MustCompile(`^(.+) (?:19|20)\d\d$`)
	// separators that start a subtitle in a raw title: ":", " - ", "~", "(", "[" and dashes
	reBaseSep = regexp.MustCompile(`\s*(?::|~|\(|\)|\[|\]|\s[-‐‑–—]\s)\s*`)
	// trailing words that never name the series, longest first; "the" forms
	// because Normalize only drops a leading "the"
	baseTails = []string{" the final chapter", " the final season", " kanketsu hen", " final season", " the final", " final"}
)

// BaseTitle returns the normalized series name of a title: Normalize(t), then,
// repeatedly until nothing changes, a trailing season marker (2nd Season,
// Season 2, Part 2, S2, II, 2nd Stage, 2nd & 3rd Stage, 2nd Cour, Cour 2), a
// trailing year ("(2026)" or "2026"), "final season" / "final" / "the final
// chapter" / "kanketsu hen", or an arc marker (one or two words followed by
// "hen" or "arc") is removed. At least one word always stays.
//
// The arc marker's length comes from the raw title: the words between the
// last separator (":", " - ", "~", brackets) and hen/arc, when that is one or
// two words ("Stardust Crusaders - Egypt-hen" -> 1, "Kimetsu no Yaiba:
// Mugen Ressha-hen" -> 2); one word when the title has no separator there;
// nothing when the name is longer than two words.
func BaseTitle(t string) string {
	n := Normalize(t)
	if n == "" {
		return ""
	}
	arc := arcWords(t)
	for {
		next := stripTail(n, &arc)
		if next == n || next == "" {
			return n
		}
		n = next
	}
}

// stripTail removes one trailing group from n, or returns n unchanged.
func stripTail(n string, arc *int) string {
	if base, num := seasonSuffix(n); num > 0 {
		return base
	}
	if m := reBaseCour.FindStringSubmatch(n); m != nil {
		return m[1]
	}
	if m := reBaseYear.FindStringSubmatch(n); m != nil {
		return m[1]
	}
	for _, w := range baseTails {
		if strings.HasSuffix(n, w) && len(n) > len(w) {
			return strings.TrimSuffix(n, w)
		}
	}
	if *arc > 0 && (strings.HasSuffix(n, " hen") || strings.HasSuffix(n, " arc")) {
		words := strings.Fields(n)
		k := *arc + 1 // the arc name plus "hen"/"arc"
		if len(words) > k {
			*arc = 1 // a second arc marker (rare) falls back to one word
			return strings.Join(words[:len(words)-k], " ")
		}
	}
	return n
}

// arcWords is the number of words naming the arc of a raw title that ends in
// "<name>-hen" / "<name> Arc" (see BaseTitle): 1 or 2 from the segment after
// the last separator, 1 without a separator, 0 when the name has more than
// two words or the title has no trailing hen/arc.
func arcWords(t string) int {
	segs := reBaseSep.Split(strings.TrimSpace(t), -1)
	for i := len(segs) - 1; i >= 0; i-- {
		seg := Normalize(segs[i])
		if seg == "" {
			continue
		}
		words := strings.Fields(seg)
		last := words[len(words)-1]
		if last != "hen" && last != "arc" {
			// a year or season marker after the arc ("X-hen (2026)") is stripped
			// first; look at the segment before it
			if isMarkerOnly(seg) {
				continue
			}
			return 0
		}
		name := len(words) - 1
		switch {
		case name < 1:
			return 0
		case i == 0:
			return 1 // no separator before the arc name
		case name <= 2:
			return name
		}
		return 0
	}
	return 0
}

// isMarkerOnly reports whether seg is nothing but a season marker or a year
// ("2nd season", "part 2", "2026").
func isMarkerOnly(seg string) bool {
	if base, num := seasonSuffix("x " + seg); num > 0 && base == "x" {
		return true
	}
	if m := reBaseYear.FindStringSubmatch("x " + seg); m != nil && m[1] == "x" {
		return true
	}
	return false
}
