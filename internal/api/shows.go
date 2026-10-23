package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/sched"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

func (s *Server) listShows(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" && !slices.Contains(store.ListStatuses, status) {
		writeErr(w, http.StatusBadRequest, "status must be one of "+strings.Join(store.ListStatuses, ", "))
		return
	}
	shows, err := s.st.ListShows(r.Context(), status)
	if err != nil {
		writeStoreErr(w, err, "shows")
		return
	}
	cc, err := s.newCardCtx(r.Context())
	if err != nil {
		writeStoreErr(w, err, "shows")
		return
	}
	out := make([]ShowCard, 0, len(shows))
	for _, sh := range shows {
		out = append(out, cc.card(sh))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) cardFor(ctx context.Context, sh store.Show) (ShowFull, error) {
	cc, err := s.newCardCtx(ctx)
	if err != nil {
		return ShowFull{}, err
	}
	return cc.full(sh), nil
}

func (s *Server) addShow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AniListID  int    `json:"anilist_id"`
		ListStatus string `json:"list_status"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.AniListID <= 0 {
		writeErr(w, http.StatusBadRequest, "anilist_id is required")
		return
	}
	if body.ListStatus != "" && !slices.Contains(store.ListStatuses, body.ListStatus) {
		writeErr(w, http.StatusBadRequest, "list_status must be one of "+strings.Join(store.ListStatuses, ", "))
		return
	}
	sh, created, err := s.sch.AddShow(r.Context(), body.AniListID, body.ListStatus)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if !created {
		writeErr(w, http.StatusConflict, "Already tracked")
		return
	}
	full, err := s.cardFor(r.Context(), sh)
	if err != nil {
		writeStoreErr(w, err, "show")
		return
	}
	writeJSON(w, http.StatusOK, full)
}

func (s *Server) getShow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad show id")
		return
	}
	s.showDetailReply(w, r, id)
}

// showDetailReply writes the ShowDetail of a show ({show, episodes, releases, files}), or 404.
func (s *Server) showDetailReply(w http.ResponseWriter, r *http.Request, id int64) {
	sh, err := s.st.GetShow(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err, "show")
		return
	}
	full, err := s.cardFor(r.Context(), sh)
	if err != nil {
		writeStoreErr(w, err, "show")
		return
	}
	rows, files, rels, err := s.showRows(r.Context(), s.set.Get(), sh)
	if err != nil {
		writeStoreErr(w, err, "show")
		return
	}
	fo := make([]FileOut, 0, len(files))
	for _, f := range files {
		fo = append(fo, FileOut{File: f, Name: baseName(f.Path)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"show": full, "episodes": rows, "releases": rels, "files": fo})
}

// watchedThrough is POST /api/shows/{id}/watched-through {number} -> ShowDetail
// (SPEC v1.1 section 11): marks 1..number watched, never unmarks, one push.
func (s *Server) watchedThrough(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad show id")
		return
	}
	var body struct {
		Number *int `json:"number"`
	}
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "fields": map[string]string{"number": "must be a whole number"}})
		return
	}
	if body.Number == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "number is required", "fields": map[string]string{"number": "required"}})
		return
	}
	_, err := s.sch.WatchedThrough(r.Context(), id, *body.Number)
	var te *sched.ThroughError
	if errors.As(err, &te) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": te.Error(), "fields": map[string]string{"number": te.Msg}})
		return
	}
	if err != nil {
		writeStoreErr(w, err, "show")
		return
	}
	s.showDetailReply(w, r, id)
}

// MissingItem is one row of GET /api/missing (SPEC v1.1 section 11).
type MissingItem struct {
	ShowID       int64  `json:"show_id"`
	Title        string `json:"title"`
	CoverURL     string `json:"cover_url"`
	Color        string `json:"color"`
	Count        int    `json:"count"`
	FirstEpisode int    `json:"first_episode"`
	LastEpisode  int    `json:"last_episode"`
}

// missingItems builds GET /api/missing: watching shows with aired episodes
// that are not watched, have no present file and no active release, most
// recently active show first.
func (s *Server) missingItems(ctx context.Context) ([]MissingItem, error) {
	list, err := s.sch.Missing(ctx)
	if err != nil {
		return nil, err
	}
	cc, err := s.newCardCtx(ctx)
	if err != nil {
		return nil, err
	}
	type withAct struct {
		MissingItem
		act int64
	}
	rows := make([]withAct, 0, len(list))
	for _, m := range list {
		card := cc.card(m.Show)
		rows = append(rows, withAct{MissingItem{ShowID: m.Show.ID, Title: card.Title, CoverURL: m.Show.CoverURL, Color: m.Show.Color,
			Count: len(m.Episodes), FirstEpisode: m.Episodes[0], LastEpisode: m.Episodes[len(m.Episodes)-1]}, card.LastActivity})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].act > rows[j].act })
	out := make([]MissingItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.MissingItem)
	}
	return out, nil
}

func (s *Server) missing(w http.ResponseWriter, r *http.Request) {
	out, err := s.missingItems(r.Context())
	if err != nil {
		writeStoreErr(w, err, "shows")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// patchShow applies the fields present in the body; JSON null clears an override.
func (s *Server) patchShow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad show id")
		return
	}
	var body map[string]json.RawMessage
	if err := decode(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	sh, err := s.st.GetShow(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err, "show")
		return
	}
	before := sh
	isNull := func(raw json.RawMessage) bool { return strings.TrimSpace(string(raw)) == "null" }
	for k, raw := range body {
		var err error
		switch k {
		case "list_status":
			var v string
			if err = json.Unmarshal(raw, &v); err == nil && !slices.Contains(store.ListStatuses, v) {
				err = errors.New("must be one of " + strings.Join(store.ListStatuses, ", "))
			}
			sh.ListStatus = v
		case "search_query":
			sh.SearchQuery = ""
			if !isNull(raw) {
				err = json.Unmarshal(raw, &sh.SearchQuery)
				sh.SearchQuery = strings.TrimSpace(sh.SearchQuery)
			}
		case "episode_offset":
			sh.EpisodeOffset = 0
			if !isNull(raw) {
				err = json.Unmarshal(raw, &sh.EpisodeOffset)
			}
		case "group_order":
			sh.GroupOrder = nil
			if !isNull(raw) {
				var g []string
				if err = json.Unmarshal(raw, &g); err == nil {
					sh.GroupOrder = []string{}
					for _, x := range g {
						if x = strings.TrimSpace(x); x != "" {
							sh.GroupOrder = append(sh.GroupOrder, x)
						}
					}
				}
			}
		case "resolution":
			sh.Resolution = ""
			if !isNull(raw) {
				err = json.Unmarshal(raw, &sh.Resolution)
				sh.Resolution = strings.ToLower(strings.TrimSpace(sh.Resolution))
			}
		case "auto_download":
			sh.AutoDownload = nil
			if !isNull(raw) {
				var b bool
				err = json.Unmarshal(raw, &b)
				sh.AutoDownload = &b
			}
		case "folder":
			sh.Folder = ""
			if !isNull(raw) {
				err = json.Unmarshal(raw, &sh.Folder)
				sh.Folder = strings.TrimSpace(sh.Folder)
			}
		case "auto_from":
			sh.AutoFrom = nil
			if !isNull(raw) {
				var n int
				if err = json.Unmarshal(raw, &n); err == nil && n < 1 {
					err = errors.New("must be 1 or more (null = all episodes)")
				}
				sh.AutoFrom = &n
			}
		case "intro_skip_sec":
			sh.IntroSkipSec = nil
			if !isNull(raw) {
				var n int
				if err = json.Unmarshal(raw, &n); err == nil && (n < 0 || n > 600) {
					err = errors.New("must be between 0 and 600 seconds (null = automatic)")
				}
				sh.IntroSkipSec = &n
			}
		case "score":
			sh.Score = nil
			if !isNull(raw) {
				var f float64
				err = json.Unmarshal(raw, &f)
				sh.Score = &f
			}
		default:
			err = errors.New("field cannot be changed")
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": k + ": " + err.Error(), "fields": map[string]string{k: err.Error()}})
			return
		}
	}
	if err := s.st.UpdateShowPrefs(r.Context(), &sh); err != nil {
		writeStoreErr(w, err, "show")
		return
	}
	if before.SearchQuery != sh.SearchQuery || before.EpisodeOffset != sh.EpisodeOffset || !slices.Equal(before.GroupOrder, sh.GroupOrder) ||
		before.Resolution != sh.Resolution || before.ListStatus != sh.ListStatus || !eqBool(before.AutoDownload, sh.AutoDownload) {
		s.sch.ShowPrefsChanged(id)
	} else if !eqInt(before.AutoFrom, sh.AutoFrom) {
		s.sch.RunConveyorForShow(id)
	}
	full, err := s.cardFor(r.Context(), sh)
	if err != nil {
		writeStoreErr(w, err, "show")
		return
	}
	writeJSON(w, http.StatusOK, full)
}

func eqBool(a, b *bool) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
func eqInt(a, b *int) bool   { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

func (s *Server) deleteShow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad show id")
		return
	}
	del := r.URL.Query().Get("delete_files")
	n, err := s.sch.DeleteShow(r.Context(), id, del == "1" || del == "true")
	if err != nil {
		writeStoreErr(w, err, "show")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files_deleted": n})
}

func (s *Server) setWatched(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	n, ok2 := pathInt(r, "n")
	if !ok || !ok2 {
		writeErr(w, http.StatusBadRequest, "bad show id or episode number")
		return
	}
	var body struct {
		Watched *bool `json:"watched"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Watched == nil {
		writeErr(w, http.StatusBadRequest, "watched (true/false) is required")
		return
	}
	if _, err := s.sch.SetWatched(r.Context(), id, int(n), *body.Watched, "manual"); err != nil {
		writeStoreErr(w, err, "show")
		return
	}
	s.episodeReply(w, r, id, int(n))
}

// episodeReply writes the EpisodeRow of (showID, n), or 404.
func (s *Server) episodeReply(w http.ResponseWriter, r *http.Request, showID int64, n int) {
	sh, err := s.st.GetShow(r.Context(), showID)
	if err != nil {
		writeStoreErr(w, err, "show")
		return
	}
	rows, _, _, err := s.showRows(r.Context(), s.set.Get(), sh)
	if err != nil {
		writeStoreErr(w, err, "show")
		return
	}
	for _, row := range rows {
		if row.Number == n {
			writeJSON(w, http.StatusOK, row)
			return
		}
	}
	writeErr(w, http.StatusNotFound, "episode not found")
}

// setEpisodeAuto is POST /api/shows/{id}/episodes/{n}/auto {auto: null|0|1} -> EpisodeRow.
func (s *Server) setEpisodeAuto(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	n, ok2 := pathInt(r, "n")
	if !ok || !ok2 {
		writeErr(w, http.StatusBadRequest, "bad show id or episode number")
		return
	}
	var body map[string]json.RawMessage
	if err := decode(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	raw, present := body["auto"]
	var auto *int
	bad := !present
	if present && strings.TrimSpace(string(raw)) != "null" {
		var v int
		if err := json.Unmarshal(raw, &v); err != nil || (v != 0 && v != 1) {
			bad = true
		}
		auto = &v
	}
	if bad {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "auto must be null, 0 or 1", "fields": map[string]string{"auto": "must be null, 0 or 1"}})
		return
	}
	if err := s.sch.SetEpisodeAuto(r.Context(), id, int(n), auto); err != nil {
		writeStoreErr(w, err, "show")
		return
	}
	s.episodeReply(w, r, id, int(n))
}

// downloadMissingPreview is GET /api/shows/{id}/download-missing -> {count, bytes}.
func (s *Server) downloadMissingPreview(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad show id")
		return
	}
	count, bytes, err := s.sch.DownloadMissingPreview(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err, "show")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": count, "bytes": bytes})
}

// downloadMissing is POST /api/shows/{id}/download-missing -> {added, skipped}.
func (s *Server) downloadMissing(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad show id")
		return
	}
	added, skipped, err := s.sch.DownloadMissing(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err, "show")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"added": added, "skipped": skipped})
}

// clearFailed is POST /api/shows/{id}/releases/clear-failed, body optional
// {"episode": N}: every failed release of the show (or only those covering
// episode N) becomes skipped, note "cleared by user" -> {cleared: n}. No event
// is logged and auto flags are not touched (SPEC v1.3 section 2.3).
func (s *Server) clearFailed(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad show id")
		return
	}
	var body map[string]json.RawMessage
	if err := decode(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var episode *int
	if raw, present := body["episode"]; present && strings.TrimSpace(string(raw)) != "null" {
		var n int
		if err := json.Unmarshal(raw, &n); err != nil || n < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "episode must be a whole number 1 or more",
				"fields": map[string]string{"episode": "must be a whole number 1 or more"}})
			return
		}
		episode = &n
	}
	if _, err := s.st.GetShow(r.Context(), id); err != nil {
		writeStoreErr(w, err, "show")
		return
	}
	n, err := s.st.ClearFailedReleases(r.Context(), id, episode, "cleared by user")
	if err != nil {
		writeStoreErr(w, err, "show")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cleared": n})
}

func (s *Server) refreshShow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad show id")
		return
	}
	if _, err := s.sch.RefreshShow(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "show not found")
			return
		}
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"started": true})
}

// MediaOut is an AniList result plus the display title and whether it is tracked.
type MediaOut struct {
	sched.MediaSummary
	Title   string `json:"title"`
	Tracked bool   `json:"tracked"`
}

func (s *Server) mediaOut(ctx context.Context, list []sched.MediaSummary) ([]MediaOut, error) {
	shows, err := s.st.ListShows(ctx, "")
	if err != nil {
		return nil, err
	}
	tracked := map[int64]bool{}
	for _, sh := range shows {
		tracked[sh.AniListID] = true
	}
	c := s.set.Get()
	out := make([]MediaOut, 0, len(list))
	for _, m := range list {
		t := m.TitleRomaji
		if c.Titles.Language == "english" && m.TitleEnglish != "" {
			t = m.TitleEnglish
		}
		out = append(out, MediaOut{MediaSummary: m, Title: t, Tracked: tracked[int64(m.ID)]})
	}
	return out, nil
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, []MediaOut{})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	list, err := s.brain.Search(ctx, q)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "AniList: "+err.Error())
		return
	}
	out, err := s.mediaOut(r.Context(), list)
	if err != nil {
		writeStoreErr(w, err, "shows")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// currentSeason returns the AniList season and year for t.
func currentSeason(t time.Time) (string, int) {
	switch t.Month() {
	case time.January, time.February, time.March:
		return "WINTER", t.Year()
	case time.April, time.May, time.June:
		return "SPRING", t.Year()
	case time.July, time.August, time.September:
		return "SUMMER", t.Year()
	}
	return "FALL", t.Year()
}

func (s *Server) season(w http.ResponseWriter, r *http.Request) {
	season, year := currentSeason(s.now())
	if v := strings.ToUpper(r.URL.Query().Get("season")); v != "" {
		if !slices.Contains([]string{"WINTER", "SPRING", "SUMMER", "FALL"}, v) {
			writeErr(w, http.StatusBadRequest, "season must be WINTER, SPRING, SUMMER or FALL")
			return
		}
		season = v
	}
	year = int(queryInt(r, "year", int64(year)))
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	list, err := s.brain.Season(ctx, season, year)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "AniList: "+err.Error())
		return
	}
	out, err := s.mediaOut(r.Context(), list)
	if err != nil {
		writeStoreErr(w, err, "shows")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// upNext lists on-disk unwatched episodes of watching shows, most recently active show first.
func (s *Server) upNext(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	shows, err := s.st.ListShows(ctx, "watching")
	if err != nil {
		writeStoreErr(w, err, "shows")
		return
	}
	cc, err := s.newCardCtx(ctx)
	if err != nil {
		writeStoreErr(w, err, "shows")
		return
	}
	sort.SliceStable(shows, func(i, j int) bool {
		return cc.card(shows[i]).LastActivity > cc.card(shows[j]).LastActivity
	})
	out := []EpisodeRow{}
	for _, sh := range shows {
		if cc.stats[sh.ID].OnDisk == 0 {
			continue
		}
		rows, _, _, err := s.showRows(ctx, cc.c, sh)
		if err != nil {
			writeStoreErr(w, err, "show")
			return
		}
		for _, row := range rows {
			if row.File != nil && row.WatchedAt == nil {
				out = append(out, row)
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// calendar lists episodes airing (sub or dub) in [from, to). Defaults: now-7d .. now+7d.
// Dropped shows are left out.
func (s *Server) calendar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := s.now().Unix()
	from := queryInt(r, "from", now-7*86400)
	to := queryInt(r, "to", now+7*86400)
	if to <= from {
		writeErr(w, http.StatusBadRequest, "to must be after from")
		return
	}
	eps, err := s.st.EpisodesAiringBetween(ctx, from, to)
	if err != nil {
		writeStoreErr(w, err, "episodes")
		return
	}
	c := s.set.Get()
	type showData struct {
		sh    store.Show
		files map[int]store.File
		rels  []store.Release
	}
	cache := map[int64]*showData{}
	out := []CalendarItem{}
	for _, e := range eps {
		d, ok := cache[e.ShowID]
		if !ok {
			sh, err := s.st.GetShow(ctx, e.ShowID)
			if err != nil {
				continue
			}
			files, _ := s.st.FilesForShow(ctx, sh.ID)
			rels, _ := s.st.ListReleases(ctx, sh.ID, nil)
			d = &showData{sh: sh, files: bestFiles(files), rels: rels}
			cache[e.ShowID] = d
		}
		if d.sh.ListStatus == "dropped" {
			continue
		}
		item := CalendarItem{ShowID: e.ShowID, Title: sched.DisplayTitle(c, d.sh), Episode: e.Number, AbsNumber: absNumber(d.sh, e.Number),
			AiredAt: e.AiredAt, DubAiredAt: e.DubAiredAt, Watched: e.WatchedAt != nil}
		_, item.OnDisk = d.files[e.Number]
		if rel := currentRelease(d.rels, e.Number, now); rel != nil {
			item.ReleaseStatus = rel.Status
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, out)
}

func baseName(p string) string {
	i := strings.LastIndexAny(p, `/\`)
	return p[i+1:]
}
