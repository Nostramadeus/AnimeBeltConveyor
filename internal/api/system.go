package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/player"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/sched"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// stateCounts is /api/state.counts: the store's numbers plus missing (aired
// episodes of watching shows with no file, not watched, no active release).
type stateCounts struct {
	store.Counts
	Missing int `json:"missing"`
}

func (s *Server) getState(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c := s.set.Get()
	// download_dir is null when no download folder is set; root_ok (v1 UI) means
	// it is set and reachable.
	var downloadDir any
	rootOK := false
	if dl := c.Library.DownloadDir(); dl != "" {
		rootOK = dirExists(dl)
		// free_bytes (SPEC v1.1 section 10): the volume of the nearest existing
		// ancestor; null when nothing on the path exists (unplugged drive).
		downloadDir = map[string]any{"path": dl, "exists": rootOK, "free_bytes": freeOf(dl)}
	}
	enabled, connected := s.sch.QbitState(ctx)
	mode, dlOK, dlErr := s.sch.DownloaderState(ctx)
	base, err := s.st.StateCounts(ctx, s.now().Unix())
	if err != nil {
		writeStoreErr(w, err, "state")
		return
	}
	// missing (SPEC v1.1 section 11) = the sum of GET /api/missing counts.
	miss, err := s.sch.Missing(ctx)
	if err != nil {
		writeStoreErr(w, err, "state")
		return
	}
	counts := stateCounts{Counts: base}
	for _, m := range miss {
		counts.Missing += len(m.Episodes)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"settings_ok":      len(c.Validate()) == 0,
		"qbit":             map[string]bool{"enabled": enabled, "connected": connected},
		"root_ok":          rootOK,
		"counts":           counts,
		"now":              s.now().Unix(),
		"player_detection": player.Available,
		"downloader":       map[string]any{"mode": mode, "ok": dlOK, "error": dlErr},
		"download_dir":     downloadDir,
		"folders_active":   len(c.Library.ActiveDirs()),
		"exe_dir":          s.exeDir,
		"data_dir":         s.dataDir,
		"setup_done":       c.UI.SetupDone,
	})
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.set.Get())
}

// putSettings replaces the settings. Keys missing from the body keep their current
// value. Validation failures return 400 {"error": "...", "fields": {"group.field": "message"}}.
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	next := s.set.Get()
	if err := decode(r, &next); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.set.Update(next)
	var ve *settings.ValidationError
	if errors.As(err, &ve) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": ve.Error(), "fields": ve.Fields})
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) testQbit(w http.ResponseWriter, r *http.Request) {
	if s.set.Get().Qbit.URL == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "qBittorrent URL is empty"})
		return
	}
	v, err := s.sch.QbitTest(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": v})
}

func (s *Server) testAniList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if _, err := s.brain.Search(ctx, "Frieren"); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- events ----

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListEvents(r.Context(), queryInt(r, "after", 0), int(queryInt(r, "limit", 100)))
	if err != nil {
		writeStoreErr(w, err, "events")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// stream is the SSE feed: "id: N\nevent: <kind>\ndata: <event json>\n\n" for each
// new event and "event: ping" every 25 s.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	sub := s.ev.Subscribe(64)
	defer sub.Close()
	fmt.Fprint(w, "retry: 3000\n\n")
	fl.Flush()
	t := time.NewTicker(s.ping)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-sub.C:
			b, _ := json.Marshal(e)
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.ID, e.Kind, b); err != nil {
				return
			}
			fl.Flush()
		case <-t.C:
			if _, err := fmt.Fprintf(w, "event: ping\ndata: {\"ts\":%d}\n\n", s.now().Unix()); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

// ---- releases, runs, files ----

func (s *Server) listReleases(w http.ResponseWriter, r *http.Request) {
	var ep *int
	if v := r.URL.Query().Get("episode"); v != "" {
		n := int(queryInt(r, "episode", -1))
		if n < 0 {
			writeErr(w, http.StatusBadRequest, "bad episode")
			return
		}
		ep = &n
	}
	list, err := s.st.ListReleases(r.Context(), queryInt(r, "show_id", 0), ep)
	if err != nil {
		writeStoreErr(w, err, "releases")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type releaseOut struct {
	store.Release
	ShowTitle string `json:"show_title"`
}

func (s *Server) releaseReply(w http.ResponseWriter, r *http.Request, rel store.Release) {
	sh, _ := s.st.GetShow(r.Context(), rel.ShowID)
	writeJSON(w, http.StatusOK, releaseOut{Release: rel, ShowTitle: sched.DisplayTitle(s.set.Get(), sh)})
}

func (s *Server) addRelease(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad release id")
		return
	}
	rel, err := s.sch.ForceAdd(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "release not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.releaseReply(w, r, rel)
}

func (s *Server) skipRelease(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad release id")
		return
	}
	rel, err := s.sch.Skip(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err, "release")
		return
	}
	s.releaseReply(w, r, rel)
}

func (s *Server) runConveyor(w http.ResponseWriter, r *http.Request) {
	started := s.sch.RunConveyorNow()
	writeJSON(w, http.StatusOK, map[string]any{"started": started, "already_running": !started})
}

func (s *Server) runScan(w http.ResponseWriter, r *http.Request) {
	started := s.sch.RunScanNow()
	writeJSON(w, http.StatusOK, map[string]any{"started": started, "already_running": !started})
}

func (s *Server) fileAction(w http.ResponseWriter, r *http.Request, fn func(context.Context, int64) error) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad file id")
		return
	}
	err := fn(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "file not found")
	case errors.Is(err, sched.ErrFileMissing):
		writeErr(w, http.StatusConflict, "the file is no longer on disk")
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

func (s *Server) openFile(w http.ResponseWriter, r *http.Request) { s.fileAction(w, r, s.sch.OpenFile) }
func (s *Server) revealFile(w http.ResponseWriter, r *http.Request) {
	s.fileAction(w, r, s.sch.RevealFile)
}
