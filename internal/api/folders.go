package api

// Library folders, the folder browser and "open folder" (SPEC v1.1 section 3).

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/diskfree"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/scan"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/sched"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// folderState is one row of GET /api/folders.
type folderState struct {
	settings.Folder
	Exists    bool   `json:"exists"`
	FreeBytes *int64 `json:"free_bytes"`
	FileCount int    `json:"file_count"` // present files under the folder
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func (s *Server) folderStates(ctx context.Context, c settings.Settings) ([]folderState, error) {
	files, err := s.st.AllFiles(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]folderState, 0, len(c.Library.Folders))
	for _, f := range c.Library.Folders {
		row := folderState{Folder: f, Exists: dirExists(f.Path)}
		if row.Exists {
			row.FreeBytes = freeOf(f.Path)
		}
		for _, file := range files {
			if file.Present && scan.Under(file.Path, f.Path) {
				row.FileCount++
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *Server) replyFolders(w http.ResponseWriter, r *http.Request) {
	list, err := s.folderStates(r.Context(), s.set.Get())
	if err != nil {
		writeStoreErr(w, err, "folders")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) getFolders(w http.ResponseWriter, r *http.Request) { s.replyFolders(w, r) }

// fieldErr writes 400 {"error": msg, "fields": {field: msg}}.
func fieldErr(w http.ResponseWriter, field, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"error": msg, "fields": map[string]string{field: msg}})
}

// updateSettings applies mutate to a copy of the current settings and saves it
// through the Manager (hooks fire). mutate returns false after writing its own
// error reply. cfgMu serialises read-modify-write cycles.
func (s *Server) updateSettings(w http.ResponseWriter, mutate func(c *settings.Settings) bool) bool {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	c := s.set.Get()
	if !mutate(&c) {
		return false
	}
	_, err := s.set.Update(c)
	var ve *settings.ValidationError
	if errors.As(err, &ve) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": ve.Error(), "fields": ve.Fields})
		return false
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return false
	}
	return true
}

// absPath turns a user-typed path into an absolute, cleaned one.
func absPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("required")
	}
	return filepath.Abs(p)
}

// setDownload makes folder i the only download folder.
func setDownload(c *settings.Settings, i int) {
	for j := range c.Library.Folders {
		c.Library.Folders[j].Download = j == i
	}
}

type folderBody struct {
	Path     string  `json:"path"`
	Label    *string `json:"label"`
	Download *bool   `json:"download"`
	Active   *bool   `json:"active"`
	Scan     *string `json:"scan"` // "flat" | "shows" | "deep" (SPEC v1.1 section 8)
	Create   bool    `json:"create"`
}

// scanErr writes the 400 for a scan value that is not one of the three depths
// and reports whether it did.
func scanErr(w http.ResponseWriter, b folderBody) bool {
	if b.Scan != nil && !settings.ValidScan(*b.Scan) {
		fieldErr(w, "scan", `must be "flat", "shows" or "deep"`)
		return true
	}
	return false
}

// postFolder adds a library folder, or reactivates a known one.
// TODO(spec): "first active folder gets Download" applies only when the body
// leaves download out; an explicit download:false (the setup guide's "folders
// you already have, ABC never writes there") is respected.
func (s *Server) postFolder(w http.ResponseWriter, r *http.Request) {
	var b folderBody
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	p, err := absPath(b.Path)
	if err != nil {
		fieldErr(w, "path", "path is required")
		return
	}
	if scanErr(w, b) {
		return
	}
	if !dirExists(p) {
		if !b.Create {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "folder does not exist", "fields": map[string]string{"path": "folder does not exist"}})
			return
		}
		if err := os.MkdirAll(p, 0o755); err != nil {
			fieldErr(w, "path", "cannot create the folder: "+err.Error())
			return
		}
	}
	ok := s.updateSettings(w, func(c *settings.Settings) bool {
		lib := &c.Library
		i, known := lib.Find(p)
		otherActive := false
		for j, f := range lib.Folders {
			if f.Active && (!known || j != i) {
				otherActive = true
			}
		}
		if !known {
			lib.Folders = append(lib.Folders, settings.Folder{Path: p, AddedAt: s.now().Unix()})
			i = len(lib.Folders) - 1
		}
		f := &lib.Folders[i]
		f.Active = true
		if b.Label != nil {
			f.Label = strings.TrimSpace(*b.Label)
		}
		if b.Scan != nil {
			f.Scan = *b.Scan // omitted: a known folder keeps its depth, a new one gets "shows" (Normalize)
		}
		switch {
		case b.Download != nil && *b.Download:
			setDownload(c, i)
		case b.Download != nil:
			f.Download = false
		case !otherActive:
			setDownload(c, i) // first active folder: downloads go here
		}
		return true
	})
	if ok {
		s.replyFolders(w, r)
	}
}

// patchFolder changes label / download / active / scan of a known folder.
func (s *Server) patchFolder(w http.ResponseWriter, r *http.Request) {
	var b folderBody
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if scanErr(w, b) {
		return
	}
	p := settings.CleanPath(b.Path)
	ok := s.updateSettings(w, func(c *settings.Settings) bool {
		i, known := c.Library.Find(p)
		if p == "" || !known {
			writeErr(w, http.StatusNotFound, "folder not found in the library")
			return false
		}
		f := &c.Library.Folders[i]
		if b.Label != nil {
			f.Label = strings.TrimSpace(*b.Label)
		}
		if b.Scan != nil {
			f.Scan = *b.Scan
		}
		if b.Active != nil {
			f.Active = *b.Active
			if !f.Active {
				f.Download = false
			}
		}
		if b.Download != nil {
			if *b.Download {
				if !f.Active {
					fieldErr(w, "download", "activate the folder first")
					return false
				}
				setDownload(c, i)
			} else {
				f.Download = false
			}
		}
		return true
	})
	if ok {
		s.replyFolders(w, r)
	}
}

// deleteFolder forgets a folder completely (its files are hidden like on deactivation).
func (s *Server) deleteFolder(w http.ResponseWriter, r *http.Request) {
	p := settings.CleanPath(r.URL.Query().Get("path"))
	ok := s.updateSettings(w, func(c *settings.Settings) bool {
		i, known := c.Library.Find(p)
		if p == "" || !known {
			writeErr(w, http.StatusNotFound, "folder not found in the library")
			return false
		}
		c.Library.Folders = append(c.Library.Folders[:i:i], c.Library.Folders[i+1:]...)
		return true
	})
	if ok {
		s.replyFolders(w, r)
	}
}

// ---- folder browser ----

type fsDir struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	FreeBytes *int64 `json:"free_bytes,omitempty"`
}

type fsList struct {
	Path   string  `json:"path"`
	Parent *string `json:"parent"` // "" = the roots view; null = this is the roots view
	Dirs   []fsDir `json:"dirs"`
	Error  string  `json:"error,omitempty"`
}

// skipName reports folders the browser never shows.
func skipName(name string, atDriveRoot bool) bool {
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "$") || strings.EqualFold(name, "System Volume Information") {
		return true
	}
	if atDriveRoot {
		for _, n := range driveRootSkips {
			if strings.EqualFold(name, n) {
				return true
			}
		}
	}
	return false
}

// listDirs returns the visible sub-folders of dir, sorted case-insensitively.
func listDirs(dir string) ([]fsDir, string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		fi, serr := os.Stat(dir)
		switch {
		case serr == nil && !fi.IsDir():
			return []fsDir{}, "not a folder"
		case errors.Is(serr, fs.ErrNotExist):
			return []fsDir{}, "folder does not exist"
		}
		return []fsDir{}, "access denied"
	}
	atRoot := filepath.Dir(dir) == dir
	out := []fsDir{}
	for _, e := range entries {
		name := e.Name()
		if skipName(name, atRoot) {
			continue
		}
		full := filepath.Join(dir, name)
		isDir := e.IsDir()
		if !isDir && e.Type()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			isDir = dirExists(full) // symlinks and junctions that lead to a folder
		}
		if !isDir {
			continue
		}
		if info, err := e.Info(); err == nil && hiddenSystem(info) {
			continue
		}
		out = append(out, fsDir{Name: name, Path: full})
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, ""
}

func (s *Server) fsList(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	if p == "" {
		writeJSON(w, http.StatusOK, fsList{Path: "", Parent: nil, Dirs: append([]fsDir{}, fsRoots()...)})
		return
	}
	if !filepath.IsAbs(p) {
		writeErr(w, http.StatusBadRequest, "path must be absolute")
		return
	}
	p = filepath.Clean(p)
	parent := filepath.Dir(p)
	if parent == p {
		parent = "" // a drive root's parent is the roots view
	}
	dirs, msg := listDirs(p)
	writeJSON(w, http.StatusOK, fsList{Path: p, Parent: &parent, Dirs: dirs, Error: msg})
}

// suggestion is one row of GET /api/fs/suggest.
type suggestion struct {
	Path      string `json:"path"`
	Label     string `json:"label"`
	Exists    bool   `json:"exists"`
	FreeBytes *int64 `json:"free_bytes"`
}

// freeOf returns the free space of p's volume (read at the closest existing
// ancestor, internal/diskfree), or nil when it cannot be read.
func freeOf(p string) *int64 {
	n, err := diskfree.Free(p)
	if err != nil {
		return nil
	}
	return &n
}

func (s *Server) suggestions() []suggestion {
	var cands []suggestion
	if s.exeDir != "" {
		cands = append(cands, suggestion{Path: filepath.Join(s.exeDir, "Anime"), Label: "Next to abc.exe"})
	}
	home, _ := s.homeDir()
	if home != "" {
		cands = append(cands, suggestion{Path: filepath.Join(home, "Videos", "Anime"), Label: "Your Videos folder"})
	}
	cands = append(cands, driveSuggestions(home)...)
	out := []suggestion{}
	for _, c := range cands {
		c.Path = filepath.Clean(c.Path)
		dup := false
		for _, o := range out {
			if settings.SamePath(o.Path, c.Path) {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		c.Exists = dirExists(c.Path)
		c.FreeBytes = freeOf(c.Path)
		out = append(out, c)
	}
	return out
}

func (s *Server) fsSuggest(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.suggestions())
}

// ---- open folder ----

// openAllowed reports whether p is a folder ABC may open: a library folder
// (active or past), the watch folder, a show's save folder, the data dir or
// the folder abc.exe is in.
func (s *Server) openAllowed(ctx context.Context, p string) (bool, error) {
	c := s.set.Get()
	allowed := []string{c.Downloader.TorrentDir, s.dataDir, s.exeDir}
	for _, f := range c.Library.Folders {
		allowed = append(allowed, f.Path)
	}
	shows, err := s.st.ListShows(ctx, "")
	if err != nil {
		return false, err
	}
	for _, sh := range shows {
		allowed = append(allowed, sched.SavePath(c, sh))
	}
	for _, a := range allowed {
		if a != "" && settings.SamePath(a, p) {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) openDir(w http.ResponseWriter, p string) {
	err := s.sch.OpenFolder(p)
	switch {
	case errors.Is(err, sched.ErrFolderMissing):
		writeErr(w, http.StatusConflict, "folder does not exist")
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

func (s *Server) openFolder(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Path string `json:"path"`
	}
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	p := settings.CleanPath(b.Path)
	if p == "" || !filepath.IsAbs(p) {
		fieldErr(w, "path", "path must be absolute")
		return
	}
	ok, err := s.openAllowed(r.Context(), p)
	if err != nil {
		writeStoreErr(w, err, "shows")
		return
	}
	if !ok {
		writeErr(w, http.StatusForbidden, "ABC only opens its own folders (library, watch, show and data folders)")
		return
	}
	s.openDir(w, p)
}

// openShowFolder opens a show's save folder. TODO(spec): when the save folder
// does not exist but the show has a file on disk (another library folder, or a
// torrent app that picked its own sub-folder), that file's folder is opened.
func (s *Server) openShowFolder(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad show id")
		return
	}
	ctx := r.Context()
	sh, err := s.st.GetShow(ctx, id)
	if err != nil {
		writeStoreErr(w, err, "show")
		return
	}
	save := sched.SavePath(s.set.Get(), sh)
	if save != "" && dirExists(save) {
		s.openDir(w, save)
		return
	}
	files, err := s.st.FilesForShow(ctx, id)
	if err != nil {
		writeStoreErr(w, err, "files")
		return
	}
	if dir := newestFileDir(files); dir != "" {
		s.openDir(w, dir)
		return
	}
	if save == "" {
		writeErr(w, http.StatusConflict, "no download folder set")
		return
	}
	writeErr(w, http.StatusConflict, "no episode downloaded yet, the folder does not exist")
}

// newestFileDir is the folder of the most recently seen present file that still exists.
func newestFileDir(files []store.File) string {
	best, bestSeen := "", int64(-1)
	for _, f := range files {
		if !f.Present || f.LastSeen <= bestSeen {
			continue
		}
		if d := filepath.Dir(f.Path); dirExists(d) {
			best, bestSeen = d, f.LastSeen
		}
	}
	return best
}

func (s *Server) testMagnet(w http.ResponseWriter, r *http.Request) {
	h, err := s.sch.MagnetHandler()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "handler": h})
}
