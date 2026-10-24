package settings

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type memKV map[string]string

func (m memKV) GetSetting(k string) (string, bool, error) { v, ok := m[k]; return v, ok, nil }
func (m memKV) PutSetting(k, v string) error              { m[k] = v; return nil }

func TestDefaults(t *testing.T) {
	d := Defaults()
	if d.Server.Port != 7700 || d.Library.ScanIntervalMin != 10 || len(d.Library.VideoExts) != 6 ||
		d.Qbit.URL != "http://127.0.0.1:8080" || d.Qbit.Category != "abc" || d.Qbit.SavePathStyle != "show_folder" ||
		!d.Conveyor.Enabled || d.Conveyor.PollIntervalMin != 15 || d.Conveyor.NyaaBase != "https://nyaa.si" ||
		d.Conveyor.NyaaCategory != "1_2" || d.Conveyor.GroupOrder[0] != "SubsPlease" || d.Conveyor.Resolution != "1080p" ||
		d.Conveyor.FallbackHours != 12 || !d.Conveyor.SkipBatchesWhileAiring || !d.Conveyor.AutoReplaceV2 ||
		d.Conveyor.MinSeeders != 1 || d.Conveyor.LookbackDays != 14 || d.Conveyor.MaxReleaseGiB != 4 || d.Conveyor.MinFreeGiB != 5 || d.Conveyor.SizePreference != SizeSmall ||
		d.Watched.Mode != "fraction" || d.Watched.Fraction != 0.5 || d.Watched.DefaultRuntimeMin != 24 || d.Watched.PollSec != 5 ||
		len(d.Watched.PlayerTitles) != 6 || d.Open.SubtitleDelaySec != 0 || d.Open.SubtitleScale != 1 || d.Titles.Language != "romaji" || d.Dubs.Enabled || !d.Dubs.ShowInCalendar ||
		d.Push.AniList || d.Push.MAL || d.UI.Theme != "glass" || d.Notify.Toast ||
		d.Downloader.Mode != ModeManual || d.Downloader.TorrentDir != "" || d.Open.FolderOnStart || d.UI.SetupDone ||
		d.Library.Folders == nil || len(d.Library.Folders) != 0 {
		t.Fatalf("defaults differ from SPEC section 3 / v1.1: %+v", d)
	}
	if errs := d.Validate(); len(errs) != 0 {
		t.Fatalf("defaults do not validate: %v", errs)
	}
	b, _ := json.Marshal(d)
	for _, key := range []string{`"scan_interval_min":10`, `"save_path_style":"show_folder"`, `"poll_interval_min":15`, `"player_titles":[`,
		`"reduced_motion":false`, `"folders":[]`, `"downloader":{"mode":"manual","torrent_dir":""}`, `"folder_on_start":false`, `"setup_done":false`, `"max_release_gib":4`, `"min_free_gib":5`, `"size_preference":"small"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("JSON missing %s: %s", key, b)
		}
	}
	for _, gone := range []string{`"root"`, `"extra"`, `"enabled":false,"url"`} {
		if strings.Contains(string(b), gone) {
			t.Errorf("JSON still has legacy %s: %s", gone, b)
		}
	}
}

func TestValidation(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	os.WriteFile(file, []byte("x"), 0o644)

	cases := []struct {
		name  string
		mut   func(*Settings)
		field string
	}{
		{"port low", func(s *Settings) { s.Server.Port = 80 }, "server.port"},
		{"port high", func(s *Settings) { s.Server.Port = 70000 }, "server.port"},
		{"fraction low", func(s *Settings) { s.Watched.Fraction = 0.01 }, "watched.fraction"},
		{"fraction high", func(s *Settings) { s.Watched.Fraction = 1.5 }, "watched.fraction"},
		{"scan interval", func(s *Settings) { s.Library.ScanIntervalMin = 0 }, "library.scan_interval_min"},
		{"poll interval", func(s *Settings) { s.Conveyor.PollIntervalMin = 0 }, "conveyor.poll_interval_min"},
		{"poll sec", func(s *Settings) { s.Watched.PollSec = 0 }, "watched.poll_sec"},
		{"relative folder", func(s *Settings) { s.Library.Folders = []Folder{{Path: "anime", Active: true}} }, "library.folders"},
		{"qbit url", func(s *Settings) { s.Qbit.URL = "ftp://x" }, "qbit.url"},
		{"qbit url empty in qbit mode", func(s *Settings) { s.Qbit.URL = ""; s.Downloader.Mode = ModeQbit }, "qbit.url"},
		{"nyaa url", func(s *Settings) { s.Conveyor.NyaaBase = "::bad" }, "conveyor.nyaa_base"},
		{"mode", func(s *Settings) { s.Watched.Mode = "sometimes" }, "watched.mode"},
		{"language", func(s *Settings) { s.Titles.Language = "french" }, "titles.language"},
		{"theme planned", func(s *Settings) { s.UI.Theme = "light" }, "ui.theme"},
		{"save path style", func(s *Settings) { s.Qbit.SavePathStyle = "x" }, "qbit.save_path_style"},
		{"resolution", func(s *Settings) { s.Conveyor.Resolution = "hd" }, "conveyor.resolution"},
		{"max release size", func(s *Settings) { s.Conveyor.MaxReleaseGiB = -0.5 }, "conveyor.max_release_gib"},
		{"min free", func(s *Settings) { s.Conveyor.MinFreeGiB = -1 }, "conveyor.min_free_gib"},
		{"size preference", func(s *Settings) { s.Conveyor.SizePreference = "biggest" }, "conveyor.size_preference"},
		{"exts", func(s *Settings) { s.Library.VideoExts = nil }, "library.video_exts"},
		{"downloader mode", func(s *Settings) { s.Downloader.Mode = "aria2" }, "downloader.mode"},
		{"torrent_dir required", func(s *Settings) { s.Downloader.Mode = ModeTorrentFile }, "downloader.torrent_dir"},
		{"torrent_dir missing", func(s *Settings) {
			s.Downloader.Mode = ModeTorrentFile
			s.Downloader.TorrentDir = filepath.Join(dir, "nope")
		}, "downloader.torrent_dir"},
		{"torrent_dir is file", func(s *Settings) { s.Downloader.Mode = ModeTorrentFile; s.Downloader.TorrentDir = file }, "downloader.torrent_dir"},
		{"torrent_dir relative in magnet mode", func(s *Settings) { s.Downloader.Mode = ModeMagnet; s.Downloader.TorrentDir = "watch" }, "downloader.torrent_dir"},
	}
	for _, c := range cases {
		s := Defaults()
		c.mut(&s)
		s.Normalize()
		errs := s.Validate()
		if _, ok := errs[c.field]; !ok || len(errs) != 1 {
			t.Errorf("%s: want only %s, got %v", c.name, c.field, errs)
		}
	}
	nolimit := Defaults()
	nolimit.Conveyor.MaxReleaseGiB = 0 // 0 = no limit
	nolimit.Conveyor.MinFreeGiB = 0    // 0 = off
	nolimit.Conveyor.SizePreference = SizeAny
	if errs := nolimit.Validate(); len(errs) != 0 {
		t.Errorf("max_release_gib 0 should pass: %v", errs)
	}
	ok := Defaults()
	// A missing (unplugged) library folder must not block saving.
	ok.Library.Folders = []Folder{{Path: dir, Active: true, Download: true}, {Path: filepath.Join(dir, "unplugged"), Active: true}}
	ok.Qbit.URL = ""
	ok.Downloader.Mode = ModeTorrentFile
	ok.Downloader.TorrentDir = dir
	ok.Normalize()
	if errs := ok.Validate(); len(errs) != 0 {
		t.Errorf("existing folders + torrent dir + empty qbit url outside qbit mode should pass: %v", errs)
	}
	ok.Downloader.Mode = ModeMagnet
	ok.Downloader.TorrentDir = filepath.Join(dir, "not-there-yet") // absolute: existence not checked outside torrent_file
	if errs := ok.Validate(); len(errs) != 0 {
		t.Errorf("absolute missing torrent_dir in magnet mode should pass: %v", errs)
	}
}

func TestNormalize(t *testing.T) {
	s := Defaults()
	s.Library.VideoExts = []string{".MKV", " mp4 ", ""}
	s.Qbit.URL = "http://localhost:8080/"
	s.Normalize()
	if strings.Join(s.Library.VideoExts, ",") != "mkv,mp4" || s.Qbit.URL != "http://localhost:8080" {
		t.Fatalf("normalize: %+v %+v", s.Library, s.Qbit)
	}
}

func TestNormalizeFolders(t *testing.T) {
	base := t.TempDir()
	a, b, c := filepath.Join(base, "a"), filepath.Join(base, "b"), filepath.Join(base, "c")
	s := Defaults()
	s.Library.Folders = []Folder{
		{Path: " " + a + string(filepath.Separator) + " ", Label: "  A  ", Active: true},
		{Path: ""},
		{Path: filepath.Join(a, "x", ".."), Download: true}, // duplicate of a: flags merge into the first
		{Path: b, Active: true, Download: true},             // second active download folder: cleared
		{Path: c, Active: false, Download: true},            // inactive: never the download folder
	}
	s.Downloader.TorrentDir = " " + filepath.Join(base, "w", ".") + " "
	s.Normalize()
	f := s.Library.Folders
	if len(f) != 3 || f[0].Path != a || f[0].Label != "A" || !f[0].Active || !f[0].Download || f[1].Download || f[2].Download || f[0].AddedAt == 0 {
		t.Fatalf("folders = %+v", f)
	}
	if s.Library.DownloadDir() != a || strings.Join(s.Library.ActiveDirs(), "|") != a+"|"+b {
		t.Fatalf("DownloadDir=%q ActiveDirs=%v", s.Library.DownloadDir(), s.Library.ActiveDirs())
	}
	if s.Downloader.TorrentDir != filepath.Join(base, "w") {
		t.Fatalf("torrent_dir = %q", s.Downloader.TorrentDir)
	}
	if i, ok := s.Library.Find(b + string(filepath.Separator)); !ok || i != 1 {
		t.Fatalf("Find(b/) = %d %v", i, ok)
	}
	if _, ok := s.Library.Find(filepath.Join(base, "zzz")); ok {
		t.Fatal("Find hit an unknown path")
	}
}

// A v1 blob (library.root + extra, qbit.enabled) is folded into folders and a downloader mode.
func TestLegacyFold(t *testing.T) {
	base := t.TempDir()
	root, extra := filepath.Join(base, "Anime"), filepath.Join(base, "Old")
	blob, _ := json.Marshal(map[string]any{
		"library": map[string]any{"root": root, "extra": []string{extra, " ", root}, "scan_interval_min": 5},
		"qbit":    map[string]any{"enabled": true, "url": "http://127.0.0.1:8080"},
	})
	kv := memKV{Key: string(blob)}
	s, err := Load(kv)
	if err != nil {
		t.Fatal(err)
	}
	f := s.Library.Folders
	if len(f) != 2 || f[0].Path != root || !f[0].Download || !f[0].Active || f[1].Path != extra || f[1].Download || !f[1].Active {
		t.Fatalf("folders = %+v", f)
	}
	if s.Library.Root != "" || s.Library.Extra != nil || s.Library.ScanIntervalMin != 5 {
		t.Fatalf("legacy fields not cleared: %+v", s.Library)
	}
	if s.Downloader.Mode != ModeQbit || s.Qbit.Enabled {
		t.Fatalf("qbit.enabled -> mode: %+v %+v", s.Downloader, s.Qbit)
	}
	out, _ := json.Marshal(s)
	if strings.Contains(string(out), `"root"`) || strings.Contains(string(out), `"extra"`) || strings.Contains(string(out), `"enabled":true,"url"`) {
		t.Fatalf("legacy keys written back: %s", out)
	}

	// qbit.enabled false (or absent) -> manual; a v1.1 blob keeps its own mode.
	kv[Key] = `{"library":{"root":""},"qbit":{"enabled":false}}`
	if s, _ := Load(kv); s.Downloader.Mode != ModeManual || len(s.Library.Folders) != 0 {
		t.Fatalf("disabled qbit: %+v %+v", s.Downloader, s.Library.Folders)
	}
	kv[Key] = `{"downloader":{"mode":"magnet"},"qbit":{"enabled":true}}`
	if s, _ := Load(kv); s.Downloader.Mode != ModeMagnet || s.Qbit.Enabled {
		t.Fatalf("explicit mode must win: %+v", s.Downloader)
	}
	// Root that is already a folder is not added twice.
	s = Defaults()
	s.Library.Folders = []Folder{{Path: root, Active: true}}
	s.Library.Root = root
	s.Normalize()
	if len(s.Library.Folders) != 1 || s.Library.Folders[0].Download {
		t.Fatalf("root already known: %+v", s.Library.Folders)
	}
}

func TestLoadSaveAndManager(t *testing.T) {
	kv := memKV{}
	s, err := Load(kv)
	if err != nil || s.Server.Port != 7700 || s.Downloader.Mode != ModeManual {
		t.Fatalf("empty load: %v %v %q", s.Server.Port, err, s.Downloader.Mode)
	}
	// Partial blob: missing keys keep defaults.
	kv[Key] = `{"server":{"port":8000},"watched":{"fraction":0.8}}`
	s, err = Load(kv)
	if err != nil || s.Server.Port != 8000 || s.Watched.Fraction != 0.8 || s.Watched.PollSec != 5 || s.Conveyor.PollIntervalMin != 15 {
		t.Fatalf("partial load: %+v %v", s, err)
	}

	m, err := NewManager(kv)
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	var gotOld, gotNew Settings
	m.OnChange(func(o, n Settings) { calls++; gotOld, gotNew = o, n })

	bad := m.Get()
	bad.Server.Port = 1
	_, err = m.Update(bad)
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Fields["server.port"] == "" {
		t.Fatalf("want ValidationError for port, got %v", err)
	}
	if calls != 0 || m.Get().Server.Port != 8000 {
		t.Fatal("invalid update must not apply or fire hooks")
	}

	next := m.Get()
	next.Conveyor.PollIntervalMin = 30
	if _, err := m.Update(next); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || gotOld.Conveyor.PollIntervalMin != 15 || gotNew.Conveyor.PollIntervalMin != 30 {
		t.Fatalf("hook: calls=%d old=%d new=%d", calls, gotOld.Conveyor.PollIntervalMin, gotNew.Conveyor.PollIntervalMin)
	}
	reloaded, _ := Load(kv)
	if reloaded.Conveyor.PollIntervalMin != 30 {
		t.Fatal("update not persisted")
	}
	// Get returns a copy: mutating it must not change the manager.
	g := m.Get()
	g.Conveyor.GroupOrder[0] = "X"
	if m.Get().Conveyor.GroupOrder[0] == "X" {
		t.Fatal("Get leaked internal slice")
	}
	next = m.Get()
	next.Library.Folders = []Folder{{Path: t.TempDir(), Active: true, Download: true}}
	m.Update(next)
	g = m.Get()
	g.Library.Folders[0].Download = false
	if m.Get().Library.DownloadDir() == "" {
		t.Fatal("Get leaked the folders slice")
	}

	kv[Key] = `{not json`
	if _, err := Load(kv); err == nil {
		t.Fatal("corrupt blob should error")
	}
}

// SPEC v1.1 section 8: "" or an unknown scan value becomes "shows".
func TestNormalizeScan(t *testing.T) {
	base := t.TempDir()
	s := Defaults()
	s.Library.Folders = []Folder{
		{Path: filepath.Join(base, "a"), Active: true},
		{Path: filepath.Join(base, "b"), Active: true, Scan: " DEEP "},
		{Path: filepath.Join(base, "c"), Active: true, Scan: "flat"},
		{Path: filepath.Join(base, "d"), Active: true, Scan: "recursive"},
	}
	s.Normalize()
	got := []string{}
	depths := []int{}
	for _, f := range s.Library.Folders {
		got = append(got, f.Scan)
		depths = append(depths, f.ScanDepth())
	}
	if strings.Join(got, ",") != "shows,deep,flat,shows" {
		t.Fatalf("scan = %v", got)
	}
	if depths[0] != 1 || depths[1] != -1 || depths[2] != 0 || depths[3] != 1 {
		t.Fatalf("depths = %v", depths)
	}
	if (Folder{}).ScanDepth() != 1 {
		t.Fatal("empty scan must count as shows")
	}
}

// The download folder may not be "flat" while qBittorrent saves into show folders.
func TestValidateScanDepth(t *testing.T) {
	base := t.TempDir()
	mk := func(style, dlScan, otherScan string) Settings {
		s := Defaults()
		s.Downloader.Mode = "qbittorrent"
		s.Qbit.SavePathStyle = style
		s.Library.Folders = []Folder{
			{Path: filepath.Join(base, "dl"), Active: true, Download: true, Scan: dlScan},
			{Path: filepath.Join(base, "old"), Active: true, Scan: otherScan},
		}
		s.Normalize()
		return s
	}
	want := "the download folder needs at least 'show folders' because episodes are saved one level down"
	if got := mk("show_folder", "flat", "shows").Validate()["library.folders"]; got != want {
		t.Fatalf("flat download folder + show_folder: %q", got)
	}
	manual := mk("show_folder", "flat", "flat")
	manual.Downloader.Mode = "manual" // other modes: the torrent app picks the layout, flat is fine
	for _, s := range []Settings{mk("root", "flat", "flat"), mk("show_folder", "shows", "flat"), mk("show_folder", "deep", "flat"), mk("show_folder", "", "flat"), manual} {
		if errs := s.Validate(); len(errs) != 0 {
			t.Fatalf("%s/%+v: %v", s.Qbit.SavePathStyle, s.Library.Folders, errs)
		}
	}
	// A past (inactive) folder never has Download, so its depth is free.
	s := mk("show_folder", "shows", "flat")
	s.Library.Folders[1].Active = false
	if errs := s.Validate(); len(errs) != 0 {
		t.Fatal(errs)
	}
}

// SPEC v1.1 section 10: min_free_gib and size_preference.
func TestFreeSpaceAndSizePreference(t *testing.T) {
	kv := memKV{Key: `{"conveyor":{"min_seeders":3}}`}
	s, err := Load(kv)
	if err != nil || s.Conveyor.MinFreeGiB != 5 || s.Conveyor.SizePreference != SizeSmall || s.Conveyor.MinSeeders != 3 {
		t.Fatalf("blob without the keys: %+v %v", s.Conveyor, err)
	}
	for in, want := range map[string]string{" ANY ": SizeAny, "": SizeSmall, "Small": SizeSmall} {
		s := Defaults()
		s.Conveyor.SizePreference = in
		s.Normalize()
		if s.Conveyor.SizePreference != want || len(s.Validate()) != 0 {
			t.Errorf("size_preference %q -> %q (%v), want %q", in, s.Conveyor.SizePreference, s.Validate(), want)
		}
	}
	nan := Defaults()
	nan.Conveyor.MinFreeGiB = math.NaN()
	if _, ok := nan.Validate()["conveyor.min_free_gib"]; !ok {
		t.Error("NaN min_free_gib must not validate")
	}
}
