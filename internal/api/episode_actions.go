package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/sched"
)

func (s *Server) episodeActionIDs(w http.ResponseWriter, r *http.Request) (int64, int, bool) {
	id, ok := pathInt(r, "id")
	number, valid := pathInt(r, "n")
	if !ok || !valid || number < 1 || number > 1000000 {
		writeErr(w, http.StatusBadRequest, "bad show or episode number")
		return 0, 0, false
	}
	return id, int(number), true
}

func (s *Server) checkEpisode(w http.ResponseWriter, r *http.Request) {
	id, number, ok := s.episodeActionIDs(w, r)
	if !ok {
		return
	}
	var body struct {
		Download *bool `json:"download"`
	}
	if err := decode(r, &body); err != nil || body.Download == nil {
		writeErr(w, http.StatusBadRequest, "download must be true or false")
		return
	}
	result, err := s.sch.CheckEpisode(r.Context(), id, number, *body.Download)
	if err != nil {
		writeStoreErr(w, err, "episode search")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) previewEpisodeDelete(w http.ResponseWriter, r *http.Request) {
	id, number, ok := s.episodeActionIDs(w, r)
	if !ok {
		return
	}
	result, err := s.sch.PreviewEpisodeDelete(r.Context(), id, number)
	if err != nil {
		writeStoreErr(w, err, "episode delete preview")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) deleteEpisode(w http.ResponseWriter, r *http.Request) {
	id, number, ok := s.episodeActionIDs(w, r)
	if !ok {
		return
	}
	var body map[string]json.RawMessage
	var token string
	if err := decode(r, &body); err != nil || len(body) != 1 || json.Unmarshal(body["token"], &token) != nil || token == "" {
		writeErr(w, http.StatusConflict, sched.ErrDeleteStale.Error())
		return
	}
	result, err := s.sch.DeleteEpisode(r.Context(), id, number, token)
	if errors.Is(err, sched.ErrDeleteStale) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeStoreErr(w, err, "episode deletion")
		return
	}
	writeJSON(w, http.StatusOK, result)
}
