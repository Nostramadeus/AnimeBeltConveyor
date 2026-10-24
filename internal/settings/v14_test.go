package settings

import (
	"encoding/json"
	"math"
	"slices"
	"testing"
)

func TestV14NeutralUpgradeAndPlayerTitles(t *testing.T) {
	legacy := []string{"mpv", "VLC media player", "MPC-HC", "MPC-BE", "PotPlayer"}
	for _, titles := range [][]string{legacy, {"custom"}, {}} {
		blob, _ := json.Marshal(map[string]any{"open": map[string]any{"intro_skip_sec": 10}, "watched": map[string]any{"player_titles": titles}})
		loaded, err := Load(memKV{Key: string(blob)})
		if err != nil || loaded.Open.SubtitleDelaySec != 0 || loaded.Open.SubtitleScale != 1 {
			t.Fatal(loaded.Open, err)
		}
		expected := titles
		if slices.Equal(titles, legacy) {
			expected = append(append([]string{}, legacy...), "SMPlayer")
		}
		if !slices.Equal(loaded.Watched.PlayerTitles, expected) {
			t.Fatal(loaded.Watched.PlayerTitles, expected)
		}
	}
	for _, invalid := range []float64{math.NaN(), math.Inf(1), -61, 61} {
		config := Defaults()
		config.Open.SubtitleDelaySec = invalid
		if config.Validate()["open.subtitle_delay_sec"] == "" {
			t.Fatal("invalid delay accepted", invalid)
		}
	}
}
