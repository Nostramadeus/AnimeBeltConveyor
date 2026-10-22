package animeschedule

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func serve(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	old := BaseURL
	BaseURL = srv.URL + "/api/v3"
	t.Cleanup(func() { BaseURL = old; srv.Close() })
}

func TestTimetable(t *testing.T) {
	body, err := os.ReadFile("testdata/dub_2026_w43.json")
	if err != nil {
		t.Fatal(err)
	}
	var path, query, auth string
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		path, query, auth = r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	})
	got, err := Timetable(context.Background(), "tok", "dub", 2026, 43)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/api/v3/timetables/dub" || query != "week=43&year=2026" || auth != "Bearer tok" {
		t.Errorf("request path %q query %q auth %q", path, query, auth)
	}
	want := []Entry{
		{AniListID: 0, Title: "Sousou no Frieren 2nd Season", English: "Frieren: Beyond Journey's End Season 2", Route: "sousou-no-frieren-2nd-season",
			Episode: 4, AirsAt: time.Date(2026, 10, 24, 16, 30, 0, 0, time.UTC).Unix(), Kind: "dub"},
		{AniListID: 185660, Title: "Dandadan 2nd Season", English: "DAN DA DAN Season 2", Route: "dandadan-season-2",
			Episode: 16, AirsAt: time.Date(2026, 10, 23, 15, 0, 0, 0, time.UTC).Unix(), Kind: "dub"},
		// empty romaji falls back to title; null date -> 0; numeric string episode
		{AniListID: 176301, Title: "Kusuriya no Hitorigoto", English: "The Apothecary Diaries Season 2", Route: "kusuriya-no-hitorigoto-2nd-season",
			Episode: 18, AirsAt: 0, Kind: "dub"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries (broken one must be skipped): %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d\n got  %+v\n want %+v", i, got[i], want[i])
		}
	}
}

func TestCurrentWeekOmitsParams(t *testing.T) {
	var query string
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Write([]byte(`[]`))
	})
	got, err := Timetable(context.Background(), "tok", "sub", 0, 0)
	if err != nil || len(got) != 0 || query != "" {
		t.Errorf("got %v err %v query %q", got, err, query)
	}
}

func TestLenientShapes(t *testing.T) {
	wrapped := `{"data":[{"romaji":"A","episodeNumber":3,"episodeDate":"2026-10-20T10:00:00Z"}],"page":1}`
	got, err := Decode([]byte(wrapped), "sub")
	if err != nil || len(got) != 1 || got[0].Title != "A" || got[0].Episode != 3 || got[0].Kind != "sub" {
		t.Errorf("wrapped: %+v %v", got, err)
	}
	odd := `{"weekly":[{"title":"B","anilist_id":"42","episodeNumber":1.0,"airType":"Raw"}]}`
	got, err = Decode([]byte(odd), "all")
	if err != nil || len(got) != 1 || got[0].AniListID != 42 || got[0].Kind != "raw" || got[0].Episode != 1 {
		t.Errorf("odd: %+v %v", got, err)
	}
	got, err = Decode([]byte(`{"message":"nothing"}`), "dub")
	if err != nil || len(got) != 0 {
		t.Errorf("no array: %+v %v", got, err)
	}
	if _, err := Decode([]byte(`<html>`), "dub"); err == nil {
		t.Error("want error for non-JSON")
	}
}

func TestErrors(t *testing.T) {
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"invalid token"}`))
	})
	ctx := context.Background()
	if _, err := Timetable(ctx, "bad", "dub", 2026, 43); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("want 401, got %v", err)
	}
	if _, err := Timetable(ctx, "tok", "dubbed", 2026, 43); err == nil {
		t.Error("want error for unknown kind")
	}
	if _, err := Timetable(ctx, "", "dub", 2026, 43); err == nil {
		t.Error("want error for missing token")
	}
}
