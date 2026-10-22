// Package api serves the JSON API (SPEC section 8), the SSE event stream and the
// embedded web UI.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/events"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/sched"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// Server holds the API dependencies.
type Server struct {
	st    *store.DB
	set   *settings.Manager
	ev    *events.Logger
	sch   *sched.Sched
	brain sched.Brain
	web   fs.FS

	now  func() time.Time
	ping time.Duration

	authMu    sync.Mutex
	malStates map[string]malState

	cfgMu   sync.Mutex // serialises settings read-modify-write (PUT /api/settings, folder routes)
	dataDir string     // ABC's data folder (reported in /api/state, may be opened)
	exeDir  string     // folder abc.exe runs from
	homeDir func() (string, error)
}

type malState struct {
	verifier string
	expires  time.Time
}

// New builds the API server. web is the static UI (may be nil in tests).
func New(st *store.DB, set *settings.Manager, ev *events.Logger, sch *sched.Sched, web fs.FS) *Server {
	s := &Server{st: st, set: set, ev: ev, sch: sch, brain: sch.Brain(), web: web,
		now: time.Now, ping: 25 * time.Second, malStates: map[string]malState{}, homeDir: os.UserHomeDir}
	if exe, err := os.Executable(); err == nil {
		s.exeDir = filepath.Dir(exe)
	}
	return s
}

// SetDirs tells the server the data folder and abc.exe's folder (main does this;
// exeDir "" keeps the one from os.Executable).
func (s *Server) SetDirs(dataDir, exeDir string) {
	s.dataDir = dataDir
	if exeDir != "" {
		s.exeDir = exeDir
	}
}

// Handler returns the full HTTP handler (API + static UI) behind the local-only guard.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	h := func(pattern string, fn func(http.ResponseWriter, *http.Request)) { mux.HandleFunc(pattern, fn) }

	h("GET /api/state", s.getState)
	h("GET /api/settings", s.getSettings)
	h("PUT /api/settings", s.putSettings)
	h("GET /api/settings/test/qbit", s.testQbit)
	h("GET /api/settings/test/magnet", s.testMagnet)

	h("GET /api/folders", s.getFolders)
	h("POST /api/folders", s.postFolder)
	h("PATCH /api/folders", s.patchFolder)
	h("DELETE /api/folders", s.deleteFolder)
	h("POST /api/folders/open", s.openFolder)
	h("GET /api/fs/list", s.fsList)
	h("GET /api/fs/suggest", s.fsSuggest)
	h("POST /api/shows/{id}/open-folder", s.openShowFolder)
	h("GET /api/settings/test/anilist", s.testAniList)

	h("GET /api/shows", s.listShows)
	h("POST /api/shows", s.addShow)
	h("GET /api/shows/{id}", s.getShow)
	h("PATCH /api/shows/{id}", s.patchShow)
	h("DELETE /api/shows/{id}", s.deleteShow)
	h("POST /api/shows/{id}/episodes/{n}/watched", s.setWatched)
	h("POST /api/shows/{id}/episodes/{n}/auto", s.setEpisodeAuto)
	h("POST /api/shows/{id}/watched-through", s.watchedThrough)
	h("GET /api/shows/{id}/download-missing", s.downloadMissingPreview)
	h("POST /api/shows/{id}/download-missing", s.downloadMissing)
	h("POST /api/shows/{id}/refresh", s.refreshShow)

	h("GET /api/search", s.search)
	h("GET /api/season", s.season)

	h("GET /api/upnext", s.upNext)
	h("GET /api/missing", s.missing)
	h("GET /api/calendar", s.calendar)
	h("GET /api/events", s.listEvents)
	h("GET /api/events/stream", s.stream)

	h("GET /api/releases", s.listReleases)
	h("POST /api/releases/{id}/add", s.addRelease)
	h("POST /api/releases/{id}/skip", s.skipRelease)
	h("POST /api/conveyor/run", s.runConveyor)
	h("POST /api/scan/run", s.runScan)

	h("POST /api/files/{id}/open", s.openFile)
	h("POST /api/files/{id}/reveal", s.revealFile)

	h("GET /api/auth/anilist/start", s.anilistStart)
	h("GET /api/auth/anilist/callback", s.anilistCallback)
	h("GET /api/auth/mal/start", s.malStart)
	h("GET /api/auth/mal/callback", s.malCallback)
	h("POST /api/import/anilist", s.importAniList)
	h("POST /api/import/mal", s.importMAL)
	h("GET /api/accounts", s.listAccounts)
	h("DELETE /api/accounts/{service}", s.deleteAccount)

	h("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "no such API route: "+r.Method+" "+r.URL.Path)
	})
	if s.web != nil {
		files := http.FileServerFS(s.web)
		mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if r.URL.Path == "/embed.go" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Cache-Control", "no-cache")
			files.ServeHTTP(w, r)
		}))
	}
	return localOnly(mux)
}

// localOnly rejects requests that do not come from this machine's own pages:
// the Host must be a loopback name (blocks DNS rebinding), and state-changing
// requests carrying an Origin or Sec-Fetch-Site from another site are refused
// (blocks cross-site form posts, which could otherwise e.g. set a player path
// and open it).
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			writeErr(w, http.StatusForbidden, "only local requests are allowed")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" || site == "same-site" {
				writeErr(w, http.StatusForbidden, "cross-site request refused")
				return
			}
			if o := r.Header.Get("Origin"); o != "" {
				u, err := url.Parse(o)
				if err != nil || u.Host != r.Host {
					writeErr(w, http.StatusForbidden, "cross-origin request refused")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeStoreErr maps store.ErrNotFound to 404 and everything else to 500.
func writeStoreErr(w http.ResponseWriter, err error, what string) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, what+" not found")
		return
	}
	writeErr(w, http.StatusInternalServerError, err.Error())
}

func decode(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, v); err != nil {
		return errors.New("invalid JSON body: " + err.Error())
	}
	return nil
}

func pathInt(r *http.Request, name string) (int64, bool) {
	n, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return n, err == nil && n > 0
}

func queryInt(r *http.Request, name string, def int64) int64 {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}
