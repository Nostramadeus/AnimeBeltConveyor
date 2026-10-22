package player

import "testing"

// Current must not panic and must respect filters; it only reads window titles,
// it never opens anything.
func TestCurrentFilters(t *testing.T) {
	if got := Current(nil); got != nil {
		t.Fatalf("no filters should return nil, got %v", got)
	}
	for _, p := range Current([]string{"a-title-no-window-will-ever-have-7f3c"}) {
		t.Fatalf("unexpected match %q", p.Title)
	}
	if Available {
		if len(windowTitles()) == 0 {
			t.Log("no visible windows (headless session?)")
		}
	}
}
