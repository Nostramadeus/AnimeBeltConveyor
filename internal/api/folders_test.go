package api

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/sched"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

type folderRow struct {
	Path      string `json:"path"`
	Label     string `json:"label"`
	Download  bool   `json:"download"`
	Active    bool   `json:"active"`
	AddedAt   int64  `json:"added_at"`
	Scan      string `json:"scan"`
	Exists    bool   `json:"exists"`
	FreeBytes *int64 `json:"free_bytes"`
	FileCount int    `json:"file_count"`
}

type errReply struct {
	Error  string            `json:"error"`
	Fields map[string]string `json:"fields"`
}

func (e *testEnv) folders(t *testing.T, method, path string, body any) (int, []folderRow, errReply) {
	t.Helper()
	code, b := e.do(t, method, path, body)
	if code != 200 {
		return code, nil, decodeT[errReply](t, b)
	}
	return code, decodeT[[]folderRow](t, b), errReply{}
}

func TestFoldersCRUD(t *testing.T) {
	e := newTestEnv(t)
	code, rows, _ := e.folders(t, "GET", "/api/folders", nil)
	if code != 200 || len(rows) != 1 || rows[0].Path != e.root || !rows[0].Download || !rows[0].Active || !rows[0].Exists || rows[0].FreeBytes == nil {
		t.Fatalf("initial folders: %d %+v", code, rows)
	}
	second := filepath.Join(e.dir, "Old Anime")
	code, _, er := e.folders(t, "POST", "/api/folders", map[string]any{"path": second})
	if code != 400 || er.Error != "folder does not exist" || er.Fields["path"] != "folder does not exist" {
		t.Fatalf("missing folder without create: %d %+v", code, er)
	}
	code, rows, _ = e.folders(t, "POST", "/api/folders", map[string]any{"path": second, "label": " Old ", "create": true})
	if code != 200 || len(rows) != 2 || rows[1].Path != second || rows[1].Label != "Old" || rows[1].Download || !rows[1].Active || !dirExists(second) || rows[1].AddedAt == 0 {
		t.Fatalf("add with create: %d %+v", code, rows)
	}
	// same folder again (trailing separator): reactivates, never a duplicate row
	code, rows, _ = e.folders(t, "POST", "/api/folders", map[string]any{"path": second + string(filepath.Separator)})
	if code != 200 || len(rows) != 2 {
		t.Fatalf("re-add made a duplicate: %+v", rows)
	}
	// remove -> past folder
	code, rows, _ = e.folders(t, "PATCH", "/api/folders", map[string]any{"path": second, "active": false})
	if code != 200 || rows[1].Active {
		t.Fatalf("deactivate: %d %+v", code, rows)
	}
	code, _, er = e.folders(t, "PATCH", "/api/folders", map[string]any{"path": second, "download": true})
	if code != 400 || er.Error != "activate the folder first" {
		t.Fatalf("download on a past folder: %d %+v", code, er)
	}
	// re-add the past folder
	code, rows, _ = e.folders(t, "POST", "/api/folders", map[string]any{"path": second})
	if code != 200 || len(rows) != 2 || !rows[1].Active || rows[1].Download {
		t.Fatalf("re-add past folder: %+v", rows)
	}
	// make it the download folder: the flag moves
	code, rows, _ = e.folders(t, "PATCH", "/api/folders", map[string]any{"path": second, "download": true})
	if code != 200 || rows[0].Download || !rows[1].Download {
		t.Fatalf("move download flag: %+v", rows)
	}
	if got := e.set.Get().Library.DownloadDir(); got != second {
		t.Fatalf("DownloadDir = %q", got)
	}
	// download=false clears it: no download folder at all
	code, rows, _ = e.folders(t, "PATCH", "/api/folders", map[string]any{"path": second, "download": false})
	if code != 200 || rows[0].Download || rows[1].Download {
		t.Fatalf("clear download: %+v", rows)
	}
	_, b := e.do(t, "GET", "/api/state", nil)
	st := decodeT[map[string]any](t, b)
	if st["download_dir"] != nil || st["root_ok"] != false || st["folders_active"] != float64(2) {
		t.Fatalf("state without download folder: %s", b)
	}
	// POST with download:true moves it back to the first folder
	code, rows, _ = e.folders(t, "POST", "/api/folders", map[string]any{"path": e.root, "download": true})
	if code != 200 || !rows[0].Download || rows[1].Download {
		t.Fatalf("POST download:true: %+v", rows)
	}
	if code, _, _ := e.folders(t, "PATCH", "/api/folders", map[string]any{"path": filepath.Join(e.dir, "nope"), "active": true}); code != 404 {
		t.Fatalf("PATCH unknown -> %d", code)
	}
	if code, _, _ := e.folders(t, "DELETE", "/api/folders?path="+url.QueryEscape(filepath.Join(e.dir, "nope")), nil); code != 404 {
		t.Fatalf("DELETE unknown -> %d", code)
	}
	code, rows, _ = e.folders(t, "DELETE", "/api/folders?path="+url.QueryEscape(second), nil)
	if code != 200 || len(rows) != 1 || rows[0].Path != e.root {
		t.Fatalf("forget: %d %+v", code, rows)
	}
	// settings validation still guards the list
	code, b = e.do(t, "PUT", "/api/settings", map[string]any{"library": map[string]any{"folders": []any{map[string]any{"path": "relative", "active": true}}}})
	if er := decodeT[errReply](t, b); code != 400 || er.Fields["library.folders"] != "paths must be absolute" {
		t.Fatalf("relative folder via PUT: %d %s", code, b)
	}
}

func TestFirstFolderGetsDownload(t *testing.T) {
	e := newTestEnv(t)
	if code, b := e.do(t, "PUT", "/api/settings", map[string]any{"library": map[string]any{"folders": []any{}}}); code != 200 {
		t.Fatalf("clear folders: %d %s", code, b)
	}
	existing := filepath.Join(e.dir, "existing")
	os.MkdirAll(existing, 0o755)
	// explicit download:false is respected even for the first folder
	_, rows, _ := e.folders(t, "POST", "/api/folders", map[string]any{"path": existing, "download": false})
	if len(rows) != 1 || rows[0].Download {
		t.Fatalf("explicit download:false: %+v", rows)
	}
	e.folders(t, "DELETE", "/api/folders?path="+url.QueryEscape(existing), nil)
	_, rows, _ = e.folders(t, "POST", "/api/folders", map[string]any{"path": e.root})
	if len(rows) != 1 || !rows[0].Download {
		t.Fatalf("first active folder should get Download: %+v", rows)
	}
	_, rows, _ = e.folders(t, "POST", "/api/folders", map[string]any{"path": existing})
	if len(rows) != 2 || rows[1].Download || !rows[0].Download {
		t.Fatalf("second folder must not take Download: %+v", rows)
	}
}

func TestRemovingAFolderHidesItsFiles(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	other := filepath.Join(e.dir, "other")
	os.MkdirAll(other, 0o755)
	e.folders(t, "POST", "/api/folders", map[string]any{"path": other})
	f := filepath.Join(other, "x.mkv")
	if err := e.db.UpsertFiles(ctx, []store.File{{Path: f, Size: 1}, {Path: filepath.Join(e.root, "y.mkv"), Size: 1}}); err != nil {
		t.Fatal(err)
	}
	_, rows, _ := e.folders(t, "GET", "/api/folders", nil)
	if rows[0].FileCount != 1 || rows[1].FileCount != 1 {
		t.Fatalf("file counts: %+v", rows)
	}
	e.folders(t, "PATCH", "/api/folders", map[string]any{"path": other, "active": false})
	if got, _ := e.db.GetFileByPath(ctx, f); got.Present {
		t.Fatal("file under a past folder still present")
	}
	if got, _ := e.db.GetFileByPath(ctx, filepath.Join(e.root, "y.mkv")); !got.Present {
		t.Fatal("file under the download folder hidden")
	}
}

func TestFSList(t *testing.T) {
	e := newTestEnv(t)
	tree := filepath.Join(e.dir, "tree")
	for _, d := range []string{"beta", "Alpha", "gamma", ".hidden", "$RECYCLE.BIN", "System Volume Information"} {
		os.MkdirAll(filepath.Join(tree, d), 0o755)
	}
	os.WriteFile(filepath.Join(tree, "file.txt"), []byte("x"), 0o644)
	code, b := e.do(t, "GET", "/api/fs/list?path="+url.QueryEscape(tree), nil)
	got := decodeT[fsList](t, b)
	var names []string
	for _, d := range got.Dirs {
		names = append(names, d.Name)
		if d.Path != filepath.Join(tree, d.Name) {
			t.Errorf("dir path %q", d.Path)
		}
	}
	if code != 200 || strings.Join(names, ",") != "Alpha,beta,gamma" || got.Path != tree || got.Parent == nil || *got.Parent != e.dir || got.Error != "" {
		t.Fatalf("list: %d %s", code, b)
	}
	// a file, a missing path and an unreadable folder: 200 with an error, never 500
	code, b = e.do(t, "GET", "/api/fs/list?path="+url.QueryEscape(filepath.Join(tree, "file.txt")), nil)
	if got := decodeT[fsList](t, b); code != 200 || len(got.Dirs) != 0 || got.Error != "not a folder" || got.Dirs == nil {
		t.Fatalf("file: %d %s", code, b)
	}
	if runtime.GOOS == "windows" {
		// System Volume Information is readable only by SYSTEM.
		svi := filepath.Join(os.Getenv("SystemDrive")+`\`, "System Volume Information")
		if _, err := os.Stat(svi); err == nil {
			code, b = e.do(t, "GET", "/api/fs/list?path="+url.QueryEscape(svi), nil)
			if got := decodeT[fsList](t, b); code != 200 || len(got.Dirs) != 0 || got.Error != "access denied" {
				t.Fatalf("unreadable: %d %s", code, b)
			}
		}
	} else {
		locked := filepath.Join(tree, "locked")
		os.Mkdir(locked, 0o000)
		defer os.Chmod(locked, 0o755)
		if _, err := os.ReadDir(locked); err != nil { // root can read anything
			code, b = e.do(t, "GET", "/api/fs/list?path="+url.QueryEscape(locked), nil)
			if got := decodeT[fsList](t, b); code != 200 || len(got.Dirs) != 0 || got.Error != "access denied" {
				t.Fatalf("unreadable: %d %s", code, b)
			}
		}
	}
	code, b = e.do(t, "GET", "/api/fs/list?path="+url.QueryEscape(filepath.Join(tree, "nope")), nil)
	if got := decodeT[fsList](t, b); code != 200 || len(got.Dirs) != 0 || got.Error == "" {
		t.Fatalf("missing: %d %s", code, b)
	}
	if code, _ := e.do(t, "GET", "/api/fs/list?path=relative", nil); code != 400 {
		t.Fatalf("relative path -> %d", code)
	}
	code, b = e.do(t, "GET", "/api/fs/list", nil)
	roots := decodeT[fsList](t, b)
	if code != 200 || roots.Path != "" || roots.Parent != nil || len(roots.Dirs) == 0 {
		t.Fatalf("roots: %d %s", code, b)
	}
	// a root's parent is the roots view ("")
	code, b = e.do(t, "GET", "/api/fs/list?path="+url.QueryEscape(roots.Dirs[0].Path), nil)
	if got := decodeT[fsList](t, b); code != 200 || got.Parent == nil || *got.Parent != "" {
		t.Fatalf("root listing: %d %s", code, b)
	}
}

func TestSkipNameAtDriveRoot(t *testing.T) {
	for _, n := range []string{".git", "$Recycle.Bin", "System Volume Information", "system volume information"} {
		if !skipName(n, false) {
			t.Errorf("%q should be skipped everywhere", n)
		}
	}
	if skipName("Windows", false) || skipName("Anime", true) {
		t.Error("only drive roots skip Windows; Anime is never skipped")
	}
	if len(driveRootSkips) > 0 && !skipName("program files (x86)", true) {
		t.Error("Program Files (x86) should be skipped at a drive root")
	}
}

func TestFSSuggestDedup(t *testing.T) {
	e := newTestEnv(t)
	home := filepath.Join(e.dir, "home")
	os.MkdirAll(filepath.Join(home, "Videos", "Anime"), 0o755)
	e.srv.homeDir = func() (string, error) { return home, nil }
	e.srv.exeDir = filepath.Join(home, "Videos") // "Next to abc.exe" == "Your Videos folder"
	code, b := e.do(t, "GET", "/api/fs/suggest", nil)
	rows := decodeT[[]suggestion](t, b)
	if code != 200 || len(rows) < 2 {
		t.Fatalf("suggest: %d %s", code, b)
	}
	want := filepath.Join(home, "Videos", "Anime")
	if rows[0].Path != want || rows[0].Label != "Next to abc.exe" || !rows[0].Exists || rows[0].FreeBytes == nil {
		t.Fatalf("first row: %+v", rows[0])
	}
	seen := map[string]bool{}
	for _, r := range rows {
		k := strings.ToLower(r.Path)
		if seen[k] {
			t.Fatalf("duplicate suggestion %s in %s", r.Path, b)
		}
		seen[k] = true
	}
}

func TestOpenFolderAllowList(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	outside := filepath.Join(e.dir, "outside")
	os.MkdirAll(outside, 0o755)
	if code, _ := e.do(t, "POST", "/api/folders/open", map[string]any{"path": outside}); code != 403 {
		t.Fatalf("outside the allow-list -> %d", code)
	}
	if code, _ := e.do(t, "POST", "/api/folders/open", map[string]any{"path": "relative"}); code != 400 {
		t.Fatalf("relative -> %d", code)
	}
	if len(e.l.dirs) != 0 {
		t.Fatalf("opened something: %v", e.l.dirs)
	}
	for _, p := range []string{e.root, e.dir} { // library folder, data dir
		if code, b := e.do(t, "POST", "/api/folders/open", map[string]any{"path": p}); code != 200 {
			t.Fatalf("open %s -> %d %s", p, code, b)
		}
	}
	if code, b := e.do(t, "POST", "/api/folders/open", map[string]any{"path": filepath.Join(e.dir, "app")}); code != 409 || !strings.Contains(string(b), "folder does not exist") {
		t.Fatalf("missing exe dir -> %d %s", code, b)
	}
	// the watch folder is allowed
	watch := filepath.Join(e.dir, "watch")
	os.MkdirAll(watch, 0o755)
	e.do(t, "PUT", "/api/settings", map[string]any{"downloader": map[string]any{"mode": settings.ModeTorrentFile, "torrent_dir": watch}})
	if code, b := e.do(t, "POST", "/api/folders/open", map[string]any{"path": watch}); code != 200 {
		t.Fatalf("watch folder -> %d %s", code, b)
	}

	// show folder: 409 until it exists, then 200; allowed in /api/folders/open too
	sh := store.Show{TitleRomaji: "Sousou no Frieren"}
	if err := e.db.CreateShow(ctx, &sh); err != nil {
		t.Fatal(err)
	}
	showPath := "/api/shows/" + strconv.FormatInt(sh.ID, 10) + "/open-folder"
	if code, b := e.do(t, "POST", showPath, nil); code != 409 || !strings.Contains(string(b), "no episode downloaded yet") {
		t.Fatalf("show folder missing -> %d %s", code, b)
	}
	save := filepath.Join(e.root, "Sousou no Frieren")
	os.MkdirAll(save, 0o755)
	if code, b := e.do(t, "POST", showPath, nil); code != 200 {
		t.Fatalf("show folder -> %d %s", code, b)
	}
	if code, _ := e.do(t, "POST", "/api/folders/open", map[string]any{"path": save}); code != 200 {
		t.Fatalf("show save folder via folders/open -> %d", code)
	}
	if n := len(e.l.dirs); n != 5 || e.l.dirs[0] != e.root || e.l.dirs[3] != save {
		t.Fatalf("opened %v", e.l.dirs)
	}
	e.folders(t, "PATCH", "/api/folders", map[string]any{"path": e.root, "download": false})
	if code, b := e.do(t, "POST", showPath, nil); code != 409 || !strings.Contains(string(b), "no download folder set") {
		t.Fatalf("no download folder -> %d %s", code, b)
	}
	if code, _ := e.do(t, "POST", "/api/shows/999/open-folder", nil); code != 404 {
		t.Fatalf("unknown show -> %d", code)
	}
}

func TestMagnetTestAndDownloaderState(t *testing.T) {
	e := newTestEnv(t)
	code, b := e.do(t, "GET", "/api/settings/test/magnet", nil)
	if r := decodeT[map[string]any](t, b); code != 200 || r["ok"] != true || !strings.Contains(r["handler"].(string), "qbittorrent.exe") {
		t.Fatalf("test magnet: %d %s", code, b)
	}
	e.do(t, "PUT", "/api/settings", map[string]any{"downloader": map[string]any{"mode": "magnet"}})
	_, b = e.do(t, "GET", "/api/state", nil)
	st := decodeT[struct {
		Downloader struct {
			Mode  string `json:"mode"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"downloader"`
		DownloadDir *struct {
			Path      string `json:"path"`
			Exists    bool   `json:"exists"`
			FreeBytes *int64 `json:"free_bytes"` // SPEC v1.1 section 10
		} `json:"download_dir"`
		DataDir   string `json:"data_dir"`
		ExeDir    string `json:"exe_dir"`
		SetupDone bool   `json:"setup_done"`
	}](t, b)
	if st.Downloader.Mode != "magnet" || !st.Downloader.OK || st.DownloadDir == nil || st.DownloadDir.Path != e.root || !st.DownloadDir.Exists ||
		st.DownloadDir.FreeBytes == nil || *st.DownloadDir.FreeBytes <= 0 ||
		st.DataDir != e.dir || st.ExeDir != filepath.Join(e.dir, "app") || st.SetupDone {
		t.Fatalf("state: %s", b)
	}
	e.sch.SetMagnetHandler(func() (string, error) { return "", sched.ErrNoMagnetHandler })
	code, b = e.do(t, "GET", "/api/settings/test/magnet", nil)
	if r := decodeT[map[string]any](t, b); code != 200 || r["ok"] != false || r["error"] != sched.ErrNoMagnetHandler.Error() {
		t.Fatalf("test magnet without handler: %d %s", code, b)
	}
	e.do(t, "PUT", "/api/settings", map[string]any{"ui": map[string]any{"setup_done": true}})
	_, b = e.do(t, "GET", "/api/state", nil)
	if !strings.Contains(string(b), `"setup_done":true`) || !strings.Contains(string(b), `"ok":false`) {
		t.Fatalf("state after setup / without handler: %s", b)
	}
}

// SPEC v1.1 section 8: POST and PATCH accept scan, GET returns it.
func TestFoldersScanRoundTrip(t *testing.T) {
	e := newTestEnv(t)
	code, rows, _ := e.folders(t, "GET", "/api/folders", nil)
	if code != 200 || rows[0].Scan != "shows" {
		t.Fatalf("default scan: %d %+v", code, rows)
	}
	second := filepath.Join(e.dir, "Drive")
	code, _, er := e.folders(t, "POST", "/api/folders", map[string]any{"path": second, "create": true, "scan": "recursive"})
	if code != 400 || er.Fields["scan"] != `must be "flat", "shows" or "deep"` || dirExists(second) {
		t.Fatalf("bad scan on POST: %d %+v (folder created: %v)", code, er, dirExists(second))
	}
	code, rows, _ = e.folders(t, "POST", "/api/folders", map[string]any{"path": second, "create": true, "scan": "deep"})
	if code != 200 || len(rows) != 2 || rows[1].Scan != "deep" {
		t.Fatalf("POST scan deep: %d %+v", code, rows)
	}
	code, rows, _ = e.folders(t, "PATCH", "/api/folders", map[string]any{"path": second, "scan": "flat"})
	if code != 200 || rows[1].Scan != "flat" {
		t.Fatalf("PATCH scan flat: %d %+v", code, rows)
	}
	// re-adding without scan keeps the folder's depth
	code, rows, _ = e.folders(t, "POST", "/api/folders", map[string]any{"path": second})
	if code != 200 || rows[1].Scan != "flat" {
		t.Fatalf("re-add changed scan: %+v", rows)
	}
	if c := e.set.Get(); c.Library.Folders[1].Scan != "flat" {
		t.Fatalf("settings scan = %q", c.Library.Folders[1].Scan)
	}
	code, _, er = e.folders(t, "PATCH", "/api/folders", map[string]any{"path": second, "scan": ""})
	if code != 400 || er.Fields["scan"] == "" {
		t.Fatalf("empty scan on PATCH: %d %+v", code, er)
	}
	// the download folder cannot be flat while qBittorrent (mode qbittorrent) saves into show folders
	qc := e.set.Get()
	qc.Downloader.Mode = "qbittorrent"
	if _, err := e.set.Update(qc); err != nil {
		t.Fatal(err)
	}
	code, _, er = e.folders(t, "PATCH", "/api/folders", map[string]any{"path": e.root, "scan": "flat"})
	if code != 400 || !strings.Contains(er.Fields["library.folders"], "show folders") {
		t.Fatalf("flat download folder: %d %+v", code, er)
	}
	code, rows, _ = e.folders(t, "GET", "/api/folders", nil)
	if code != 200 || rows[0].Scan != "shows" {
		t.Fatalf("rejected PATCH changed the folder: %+v", rows)
	}
}
