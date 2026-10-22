// Package settings holds the typed app settings, their defaults, validation,
// persistence (one JSON blob under settings.key='app') and change hooks.
package settings

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Key is the settings-table key the blob is stored under.
const Key = "app"

// Settings is the whole app configuration. JSON keys are snake_case.
type Settings struct {
	Server     Server     `json:"server"`
	Library    Library    `json:"library"`
	Downloader Downloader `json:"downloader"`
	Qbit       Qbit       `json:"qbit"`
	Conveyor   Conveyor   `json:"conveyor"`
	Watched    Watched    `json:"watched"`
	Titles     Titles     `json:"titles"`
	Dubs       Dubs       `json:"dubs"`
	Push       Push       `json:"push"`
	Open       Open       `json:"open"`
	UI         UI         `json:"ui"`
	Notify     Notify     `json:"notify"`
}

type Server struct {
	Port int `json:"port"`
}

// Folder is one library folder. Paths are absolute and filepath.Clean'ed.
type Folder struct {
	Path     string `json:"path"`
	Label    string `json:"label"`    // optional, shown instead of the path's last segment
	Download bool   `json:"download"` // new episodes are saved here; at most one ACTIVE folder has it
	Active   bool   `json:"active"`   // scanned. false = "past folder": remembered, not scanned
	AddedAt  int64  `json:"added_at"` // unix seconds
	Scan     string `json:"scan"`     // "flat" | "shows" | "deep" (SPEC v1.1 section 8)
}

// Scan depths of a library folder.
const (
	ScanFlat  = "flat"  // the folder only
	ScanShows = "shows" // the folder and one level of subfolders (default)
	ScanDeep  = "deep"  // every subfolder
)

// ValidScan reports whether v is one of the three scan depths.
func ValidScan(v string) bool { return v == ScanFlat || v == ScanShows || v == ScanDeep }

// ScanDepth is how many folder levels below the folder are scanned: 0 flat,
// 1 shows, -1 (unlimited) deep. An unknown value counts as shows.
func (f Folder) ScanDepth() int {
	switch f.Scan {
	case ScanFlat:
		return 0
	case ScanDeep:
		return -1
	}
	return 1
}

type Library struct {
	Folders         []Folder `json:"folders"`
	Root            string   `json:"root,omitempty"`  // LEGACY (v1 blob). Normalize folds it into Folders then clears it.
	Extra           []string `json:"extra,omitempty"` // LEGACY, same.
	ScanIntervalMin int      `json:"scan_interval_min"`
	VideoExts       []string `json:"video_exts"`
}

// DownloadDir is the path of the active folder with Download=true, "" if none.
func (l Library) DownloadDir() string {
	for _, f := range l.Folders {
		if f.Active && f.Download {
			return f.Path
		}
	}
	return ""
}

// ActiveDirs returns the paths of active folders, in list order, no duplicates.
func (l Library) ActiveDirs() []string {
	var out []string
	for _, f := range l.Folders {
		if !f.Active || f.Path == "" {
			continue
		}
		dup := false
		for _, o := range out {
			if SamePath(o, f.Path) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, f.Path)
		}
	}
	return out
}

// Find returns the index of the folder with this path (cleaned; case-insensitive on Windows).
func (l Library) Find(path string) (int, bool) {
	for i, f := range l.Folders {
		if SamePath(f.Path, path) {
			return i, true
		}
	}
	return -1, false
}

// CleanPath trims and filepath.Cleans a path; "" stays "".
func CleanPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	return filepath.Clean(p)
}

// SamePath compares two paths after cleaning, ignoring case on Windows.
func SamePath(a, b string) bool {
	a, b = CleanPath(a), CleanPath(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Downloader picks how a chosen release reaches a torrent app.
type Downloader struct {
	Mode       string `json:"mode"`        // "qbittorrent" | "magnet" | "torrent_file" | "manual"
	TorrentDir string `json:"torrent_dir"` // torrent_file mode: the folder the torrent app watches for .torrent files
}

// Downloader modes.
const (
	ModeQbit        = "qbittorrent"
	ModeMagnet      = "magnet"
	ModeTorrentFile = "torrent_file"
	ModeManual      = "manual"
)

type Qbit struct {
	Enabled       bool   `json:"enabled,omitempty"` // LEGACY: Normalize maps it to Downloader.Mode, then clears it
	URL           string `json:"url"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	Category      string `json:"category"`
	SavePathStyle string `json:"save_path_style"` // "show_folder" | "root"
}

type Conveyor struct {
	Enabled                bool     `json:"enabled"`
	PollIntervalMin        int      `json:"poll_interval_min"`
	NyaaBase               string   `json:"nyaa_base"`
	NyaaCategory           string   `json:"nyaa_category"`
	GroupOrder             []string `json:"group_order"`
	Resolution             string   `json:"resolution"`
	FallbackHours          float64  `json:"fallback_hours"`
	SkipBatchesWhileAiring bool     `json:"skip_batches_while_airing"`
	AutoReplaceV2          bool     `json:"auto_replace_v2"`
	MinSeeders             int      `json:"min_seeders"`
	LookbackDays           int      `json:"lookback_days"`   // nyaa upload window AND auto-download air window (SPEC v1.1 section 9)
	MaxReleaseGiB          float64  `json:"max_release_gib"` // auto picks skip bigger releases; 0 = no limit
	MinFreeGiB             float64  `json:"min_free_gib"`    // automatic hand-offs keep this much free on the download drive; 0 = off (SPEC v1.1 section 10)
	SizePreference         string   `json:"size_preference"` // "small" | "any": Pick tie-break inside a tier (SPEC v1.1 section 10)
}

type Watched struct {
	Mode              string   `json:"mode"` // "fraction" | "on_open" | "manual"
	Fraction          float64  `json:"fraction"`
	DefaultRuntimeMin int      `json:"default_runtime_min"`
	PollSec           int      `json:"poll_sec"`
	PlayerTitles      []string `json:"player_titles"`
}

type Titles struct {
	Language string `json:"language"` // "romaji" | "english"
}

type Dubs struct {
	Enabled        bool   `json:"enabled"`
	ShowInCalendar bool   `json:"show_in_calendar"`
	Token          string `json:"token"` // animeschedule.net API token (SPEC section 4)
}

// Push holds the push switches plus the OAuth client credentials the user pastes
// in Settings (SPEC section 4). TODO(spec): the spec names the credentials but not
// their settings fields; they live here.
type Push struct {
	AniList             bool   `json:"anilist"`
	MAL                 bool   `json:"mal"`
	AniListClientID     string `json:"anilist_client_id"`
	AniListClientSecret string `json:"anilist_client_secret"`
	MALClientID         string `json:"mal_client_id"`
}

type Open struct {
	PlayerPath    string `json:"player_path"`     // "" = OS default handler
	FolderOnStart bool   `json:"folder_on_start"` // open the download folder in the file manager when ABC starts
}

type UI struct {
	Theme         string `json:"theme"` // "glass" | "floating" | "light" (only glass in v1)
	Accent        string `json:"accent"`
	ReducedMotion bool   `json:"reduced_motion"`
	SetupDone     bool   `json:"setup_done"` // the setup guide was finished or skipped
}

type Notify struct {
	Toast bool `json:"toast"`
}

// Conveyor.SizePreference values.
const (
	SizeSmall = "small" // within an otherwise equal tier, the smallest known size wins
	SizeAny   = "any"   // most seeders wins (v1 behaviour)
)

// Defaults returns the default settings (SPEC section 3 + v1.1 section 1).
func Defaults() Settings {
	var s Settings
	s.Server.Port = 7700
	s.Library = Library{Folders: []Folder{}, ScanIntervalMin: 10, VideoExts: []string{"mkv", "mp4", "avi", "webm", "ts", "m4v"}}
	s.Downloader = Downloader{Mode: ModeManual}
	s.Qbit = Qbit{URL: "http://127.0.0.1:8080", Category: "abc", SavePathStyle: "show_folder"}
	s.Conveyor = Conveyor{Enabled: true, PollIntervalMin: 15, NyaaBase: "https://nyaa.si", NyaaCategory: "1_2",
		GroupOrder: []string{"SubsPlease", "Erai-raws"}, Resolution: "1080p", FallbackHours: 12,
		SkipBatchesWhileAiring: true, AutoReplaceV2: true, MinSeeders: 1, LookbackDays: 14, MaxReleaseGiB: 4,
		MinFreeGiB: 5, SizePreference: SizeSmall}
	s.Watched = Watched{Mode: "fraction", Fraction: 0.5, DefaultRuntimeMin: 24, PollSec: 5,
		PlayerTitles: []string{"mpv", "VLC media player", "MPC-HC", "MPC-BE", "PotPlayer"}}
	s.Titles.Language = "romaji"
	s.Dubs = Dubs{Enabled: false, ShowInCalendar: true}
	s.UI.Theme = "glass"
	return s
}

// Clone returns a deep copy (slices are not shared).
func (s Settings) Clone() Settings {
	c := s
	c.Library.Folders = append([]Folder{}, s.Library.Folders...)
	if s.Library.Extra != nil {
		c.Library.Extra = append([]string{}, s.Library.Extra...)
	}
	c.Library.VideoExts = append([]string{}, s.Library.VideoExts...)
	c.Conveyor.GroupOrder = append([]string{}, s.Conveyor.GroupOrder...)
	c.Watched.PlayerTitles = append([]string{}, s.Watched.PlayerTitles...)
	return c
}

// nowUnix is the clock Normalize uses for AddedAt (tests may replace it).
var nowUnix = func() int64 { return time.Now().Unix() }

// Normalize trims whitespace, drops empty list entries, lowercases extensions
// without the leading dot, makes nil lists empty, folds the v1 library and
// qBittorrent fields into Folders / Downloader and enforces the single download
// folder. It does not validate.
func (s *Settings) Normalize() {
	l := &s.Library
	now := nowUnix()
	// 1. Legacy fold.
	if root := CleanPath(l.Root); root != "" {
		if _, ok := l.Find(root); !ok {
			l.Folders = append(l.Folders, Folder{Path: root, Download: true, Active: true, AddedAt: now})
		}
	}
	for _, x := range l.Extra {
		if x = CleanPath(x); x != "" {
			l.Folders = append(l.Folders, Folder{Path: x, Active: true, AddedAt: now})
		}
	}
	l.Root, l.Extra = "", nil
	// 2. Legacy downloader switch.
	s.Downloader.Mode = strings.TrimSpace(s.Downloader.Mode)
	if s.Downloader.Mode == "" {
		s.Downloader.Mode = ModeManual
		if s.Qbit.Enabled {
			s.Downloader.Mode = ModeQbit
		}
	}
	s.Qbit.Enabled = false
	// 3. Clean and de-duplicate folders (first wins; the Download and Active flags are merged).
	folders := make([]Folder, 0, len(l.Folders))
	for _, f := range l.Folders {
		f.Path = CleanPath(f.Path)
		f.Label = strings.TrimSpace(f.Label)
		if f.Scan = strings.ToLower(strings.TrimSpace(f.Scan)); !ValidScan(f.Scan) {
			f.Scan = ScanShows
		}
		if f.Path == "" {
			continue
		}
		if f.AddedAt == 0 {
			f.AddedAt = now
		}
		dup := false
		for i := range folders {
			if SamePath(folders[i].Path, f.Path) {
				folders[i].Download = folders[i].Download || f.Download
				folders[i].Active = folders[i].Active || f.Active
				dup = true
				break
			}
		}
		if !dup {
			folders = append(folders, f)
		}
	}
	// 4. At most one active download folder; an inactive folder never has the flag.
	seenDownload := false
	for i := range folders {
		f := &folders[i]
		if !f.Active {
			f.Download = false
			continue
		}
		if f.Download {
			if seenDownload {
				f.Download = false
			}
			seenDownload = true
		}
	}
	l.Folders = folders
	s.Downloader.TorrentDir = CleanPath(s.Downloader.TorrentDir)

	exts := cleanList(l.VideoExts)
	for i, e := range exts {
		exts[i] = strings.ToLower(strings.TrimPrefix(e, "."))
	}
	l.VideoExts = exts
	s.Qbit.URL = strings.TrimRight(strings.TrimSpace(s.Qbit.URL), "/")
	s.Qbit.Category = strings.TrimSpace(s.Qbit.Category)
	s.Conveyor.NyaaBase = strings.TrimRight(strings.TrimSpace(s.Conveyor.NyaaBase), "/")
	s.Conveyor.GroupOrder = cleanList(s.Conveyor.GroupOrder)
	s.Conveyor.Resolution = strings.ToLower(strings.TrimSpace(s.Conveyor.Resolution))
	if s.Conveyor.SizePreference = strings.ToLower(strings.TrimSpace(s.Conveyor.SizePreference)); s.Conveyor.SizePreference == "" {
		s.Conveyor.SizePreference = SizeSmall
	}
	s.Watched.PlayerTitles = cleanList(s.Watched.PlayerTitles)
	s.Open.PlayerPath = strings.TrimSpace(s.Open.PlayerPath)
	s.Push.AniListClientID = strings.TrimSpace(s.Push.AniListClientID)
	s.Push.AniListClientSecret = strings.TrimSpace(s.Push.AniListClientSecret)
	s.Push.MALClientID = strings.TrimSpace(s.Push.MALClientID)
	s.Dubs.Token = strings.TrimSpace(s.Dubs.Token)
}

func cleanList(in []string) []string {
	out := []string{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

var resolutionRe = regexp.MustCompile(`^[0-9]{3,4}p$`)

// Validate returns field-level errors keyed by "group.field" (JSON names), e.g.
// {"server.port": "must be between 1024 and 65535"}. Empty map = valid.
// Library folder existence is not checked (an unplugged drive must never block saving).
func (s Settings) Validate() map[string]string {
	e := map[string]string{}
	if s.Server.Port < 1024 || s.Server.Port > 65535 {
		e["server.port"] = "must be between 1024 and 65535"
	}
	for _, f := range s.Library.Folders {
		if !filepath.IsAbs(f.Path) {
			e["library.folders"] = "paths must be absolute"
			break
		}
	}
	if _, bad := e["library.folders"]; !bad && s.Downloader.Mode == "qbittorrent" && s.Qbit.SavePathStyle == "show_folder" {
		for _, f := range s.Library.Folders {
			if f.Active && f.Download && f.Scan == ScanFlat {
				e["library.folders"] = "the download folder needs at least 'show folders' because episodes are saved one level down"
				break
			}
		}
	}
	if s.Library.ScanIntervalMin < 1 {
		e["library.scan_interval_min"] = "must be at least 1"
	}
	if len(s.Library.VideoExts) == 0 {
		e["library.video_exts"] = "list at least one extension"
	}
	switch s.Downloader.Mode {
	case ModeQbit, ModeMagnet, ModeTorrentFile, ModeManual:
	default:
		e["downloader.mode"] = `must be "qbittorrent", "magnet", "torrent_file" or "manual"`
	}
	if td := s.Downloader.TorrentDir; s.Downloader.Mode == ModeTorrentFile {
		if td == "" {
			e["downloader.torrent_dir"] = "required"
		} else if !filepath.IsAbs(td) {
			e["downloader.torrent_dir"] = "must be an absolute path"
		} else if fi, err := os.Stat(td); err != nil {
			e["downloader.torrent_dir"] = "folder does not exist"
		} else if !fi.IsDir() {
			e["downloader.torrent_dir"] = "not a folder"
		}
	} else if td != "" && !filepath.IsAbs(td) {
		e["downloader.torrent_dir"] = "must be an absolute path"
	}
	if s.Qbit.URL != "" || s.Downloader.Mode == ModeQbit {
		if msg := checkURL(s.Qbit.URL); msg != "" {
			e["qbit.url"] = msg
		}
	}
	if s.Qbit.SavePathStyle != "show_folder" && s.Qbit.SavePathStyle != "root" {
		e["qbit.save_path_style"] = `must be "show_folder" or "root"`
	}
	if s.Conveyor.PollIntervalMin < 1 {
		e["conveyor.poll_interval_min"] = "must be at least 1"
	}
	if msg := checkURL(s.Conveyor.NyaaBase); msg != "" {
		e["conveyor.nyaa_base"] = msg
	}
	if s.Conveyor.Resolution != "" && !resolutionRe.MatchString(s.Conveyor.Resolution) {
		e["conveyor.resolution"] = `must look like "1080p" or be empty`
	}
	if s.Conveyor.FallbackHours < 0 {
		e["conveyor.fallback_hours"] = "must be 0 or more"
	}
	if s.Conveyor.MinSeeders < 0 {
		e["conveyor.min_seeders"] = "must be 0 or more"
	}
	if s.Conveyor.LookbackDays < 1 {
		e["conveyor.lookback_days"] = "must be at least 1"
	}
	if !(s.Conveyor.MaxReleaseGiB >= 0) || math.IsInf(s.Conveyor.MaxReleaseGiB, 1) {
		e["conveyor.max_release_gib"] = "must be 0 or more"
	}
	if !(s.Conveyor.MinFreeGiB >= 0) || math.IsInf(s.Conveyor.MinFreeGiB, 1) {
		e["conveyor.min_free_gib"] = "must be 0 or more"
	}
	if s.Conveyor.SizePreference != SizeSmall && s.Conveyor.SizePreference != SizeAny {
		e["conveyor.size_preference"] = `must be "small" or "any"`
	}
	switch s.Watched.Mode {
	case "fraction", "on_open", "manual":
	default:
		e["watched.mode"] = `must be "fraction", "on_open" or "manual"`
	}
	if s.Watched.Fraction < 0.05 || s.Watched.Fraction > 1 {
		e["watched.fraction"] = "must be between 0.05 and 1"
	}
	if s.Watched.DefaultRuntimeMin < 1 {
		e["watched.default_runtime_min"] = "must be at least 1"
	}
	if s.Watched.PollSec < 1 {
		e["watched.poll_sec"] = "must be at least 1"
	}
	if s.Titles.Language != "romaji" && s.Titles.Language != "english" {
		e["titles.language"] = `must be "romaji" or "english"`
	}
	switch s.UI.Theme {
	case "glass":
	case "floating", "light":
		e["ui.theme"] = "planned, not available in v1"
	default:
		e["ui.theme"] = `must be "glass"`
	}
	return e
}

func checkURL(raw string) string {
	if raw == "" {
		return "required"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "not a valid URL"
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "must start with http:// or https://"
	}
	if u.Host == "" {
		return "missing host"
	}
	return ""
}

// KV is the persistence the settings need (store.DB implements it).
type KV interface {
	GetSetting(key string) (string, bool, error)
	PutSetting(key, value string) error
}

// Load reads the settings blob; missing keys keep their defaults. A missing blob
// returns Defaults().
func Load(kv KV) (Settings, error) {
	s := Defaults()
	// A v1 blob has no "downloader" key: leave Mode empty so Normalize can map the
	// legacy qbit.enabled switch (SPEC v1.1 section 1, step 2).
	loadBase := Defaults()
	loadBase.Downloader.Mode = ""
	raw, ok, err := kv.GetSetting(Key)
	if err != nil {
		return s, err
	}
	if !ok {
		return s, nil
	}
	s = loadBase
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return Defaults(), fmt.Errorf("settings blob: %w", err)
	}
	s.Normalize()
	return s, nil
}

// Save writes s as the settings blob (no validation; use Manager.Update for that).
func Save(kv KV, s Settings) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return kv.PutSetting(Key, string(b))
}

// ValidationError carries field-level messages.
type ValidationError struct {
	Fields map[string]string
}

func (v *ValidationError) Error() string {
	parts := make([]string, 0, len(v.Fields))
	for k, m := range v.Fields {
		parts = append(parts, k+": "+m)
	}
	return "invalid settings: " + strings.Join(parts, "; ")
}

// Manager holds the current settings in memory, persists updates and fires hooks.
type Manager struct {
	kv    KV
	mu    sync.RWMutex
	cur   Settings
	hmu   sync.Mutex
	hooks []func(old, new Settings)
}

// NewManager loads settings from kv. A corrupt blob falls back to defaults and returns the error alongside.
func NewManager(kv KV) (*Manager, error) {
	s, err := Load(kv)
	return &Manager{kv: kv, cur: s}, err
}

// Get returns a copy of the current settings.
func (m *Manager) Get() Settings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cur.Clone()
}

// OnChange registers fn, called (synchronously, in registration order) after every successful Update.
func (m *Manager) OnChange(fn func(old, new Settings)) {
	m.hmu.Lock()
	m.hooks = append(m.hooks, fn)
	m.hmu.Unlock()
}

// Update normalizes and validates s, saves it, swaps it in and fires hooks.
// Validation failures return *ValidationError.
func (m *Manager) Update(s Settings) (Settings, error) {
	s = s.Clone()
	s.Normalize()
	if errs := s.Validate(); len(errs) > 0 {
		return s, &ValidationError{Fields: errs}
	}
	if err := Save(m.kv, s); err != nil {
		return s, err
	}
	m.mu.Lock()
	old := m.cur
	m.cur = s.Clone()
	m.mu.Unlock()
	m.hmu.Lock()
	hooks := append([]func(old, new Settings){}, m.hooks...)
	m.hmu.Unlock()
	for _, h := range hooks {
		h(old.Clone(), s.Clone())
	}
	return s, nil
}
