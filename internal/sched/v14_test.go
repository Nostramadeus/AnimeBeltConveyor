package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/qbit"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

const v14Hash = "0123456789012345678901234567890123456789"

func episodeFixture(t *testing.T) (*env, store.Show, store.File) {
	t.Helper()
	e := newEnv(t)
	ctx := context.Background()
	show := e.show(t, store.Show{TitleRomaji: "Frieren", Status: "FINISHED", EpisodesTotal: store.Ptr(2)})
	for number := 1; number <= 2; number++ {
		if _, err := e.db.UpsertEpisodeAiring(ctx, show.ID, number, store.Ptr(e.clock.Add(-time.Hour).Unix())); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(e.root, "[SubsPlease] Frieren - 01 (1080p).mkv")
	touch(t, path)
	files := []store.File{{Path: path, ShowID: &show.ID, Episode: store.Ptr(1), Size: 1}}
	if err := e.db.UpsertFiles(ctx, files); err != nil {
		t.Fatal(err)
	}
	return e, show, files[0]
}

type deleteQbit struct {
	mu        sync.Mutex
	torrents  []qbit.Torrent
	files     map[string][]qbit.TorrentFile
	fail      bool
	unchanged bool
	removes   int
	badDelete bool
	adds      int
	onRemove  func()
}

func (client *deleteQbit) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	client.mu.Lock()
	defer client.mu.Unlock()
	switch request.URL.Path {
	case "/api/v2/torrents/info":
		filtered := []qbit.Torrent{}
		for _, torrent := range client.torrents {
			if hash := request.URL.Query().Get("hashes"); hash == "" || hash == torrent.Hash {
				filtered = append(filtered, torrent)
			}
		}
		json.NewEncoder(w).Encode(filtered)
	case "/api/v2/torrents/files":
		json.NewEncoder(w).Encode(client.files[request.URL.Query().Get("hash")])
	case "/api/v2/torrents/delete":
		request.ParseForm()
		client.badDelete = request.Form.Get("deleteFiles") != "false" || request.Form.Get("hashes") != v14Hash
		client.removes++
		if client.onRemove != nil {
			client.onRemove()
		}
		if client.fail {
			http.Error(w, "no", 500)
			return
		}
		if !client.unchanged {
			client.torrents = nil
		}
	case "/api/v2/torrents/add":
		client.adds++
		fmt.Fprint(w, "Ok.")
	default:
		fmt.Fprint(w, "Ok.")
	}
}

func managedFixture(t *testing.T) (*env, store.Show, store.File, *deleteQbit) {
	t.Helper()
	e, show, file := episodeFixture(t)
	client := &deleteQbit{torrents: []qbit.Torrent{{Hash: v14Hash, Name: filepath.Base(file.Path), SavePath: e.root, ContentPath: file.Path, Size: 1}}, files: map[string][]qbit.TorrentFile{v14Hash: {{Name: filepath.Base(file.Path), Size: 1}}}}
	server := httptest.NewServer(client)
	t.Cleanup(server.Close)
	e.update(t, func(config *settings.Settings) {
		config.Downloader.Mode = settings.ModeQbit
		config.Qbit.URL = server.URL
	})
	release := store.Release{ShowID: show.ID, Episode: store.Ptr(1), Title: filepath.Base(file.Path), QbitHash: v14Hash, InfoHash: v14Hash, Status: store.RelDone}
	if _, err := e.db.UpsertRelease(context.Background(), &release); err != nil {
		t.Fatal(err)
	}
	return e, show, file, client
}

func TestV14DeleteManualHistoryReplay(t *testing.T) {
	if !episodeDeletionSupported {
		t.Skip("Windows-only deletion")
	}
	e, show, file := episodeFixture(t)
	ctx := context.Background()
	e.s.SetWatched(ctx, show.ID, 1, true, "manual")
	preview, err := e.s.PreviewEpisodeDelete(ctx, show.ID, 1)
	if err != nil || !preview.Allowed || len(preview.Files) != 1 || len(preview.Torrents) != 0 {
		t.Fatalf("preview %+v %v", preview, err)
	}
	result, err := e.s.DeleteEpisode(ctx, show.ID, 1, preview.Token)
	if err != nil || len(result.Remaining) != 0 || len(result.DeletedFiles) != 1 {
		t.Fatalf("delete %+v %v", result, err)
	}
	if _, err := os.Stat(file.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("file remains", err)
	}
	stored, _ := e.db.GetFile(ctx, file.ID)
	episode, _ := e.db.GetEpisode(ctx, show.ID, 1)
	if stored.Present || episode.WatchedAt == nil || episode.Auto == nil || *episode.Auto != 0 {
		t.Fatalf("state %+v %+v", stored, episode)
	}
	if _, err := e.s.DeleteEpisode(ctx, show.ID, 1, preview.Token); !errors.Is(err, ErrDeleteStale) {
		t.Fatal("replay accepted", err)
	}
}

func TestV14DeleteRefusesMutations(t *testing.T) {
	if !episodeDeletionSupported {
		t.Skip("Windows-only deletion")
	}
	for _, kind := range []string{"file", "parent", "settings", "expiry", "episode", "show", "directory"} {
		t.Run(kind, func(t *testing.T) {
			e, show, file := episodeFixture(t)
			ctx := context.Background()
			preview, _ := e.s.PreviewEpisodeDelete(ctx, show.ID, 1)
			number := 1
			showID := show.ID
			switch kind {
			case "file":
				os.Rename(file.Path, file.Path+".old")
				touch(t, file.Path)
			case "parent":
				os.Rename(e.root, e.root+".old")
				os.MkdirAll(e.root, 0o755)
				touch(t, file.Path)
			case "settings":
				e.update(t, func(config *settings.Settings) {
					config.Library.Folders[0].Active = false
					config.Library.Folders[0].Download = false
				})
			case "expiry":
				e.clock = e.clock.Add(6 * time.Minute)
			case "episode":
				number = 2
			case "show":
				showID = e.show(t, store.Show{TitleRomaji: "Other"}).ID
			case "directory":
				os.Remove(file.Path)
				os.Mkdir(file.Path, 0o755)
			}
			if _, err := e.s.DeleteEpisode(ctx, showID, number, preview.Token); !errors.Is(err, ErrDeleteStale) {
				t.Fatalf("mutation accepted: %v", err)
			}
			if _, err := os.Stat(file.Path); err != nil {
				t.Fatal("replacement deleted", err)
			}
		})
	}
}

func TestV14DeleteUnsafePreview(t *testing.T) {
	if !episodeDeletionSupported {
		t.Skip("Windows-only deletion")
	}
	for _, kind := range []string{"outside", "directory", "symlink", "inactive"} {
		t.Run(kind, func(t *testing.T) {
			e, show, file := episodeFixture(t)
			ctx := context.Background()
			switch kind {
			case "outside":
				file.Path = filepath.Join(t.TempDir(), filepath.Base(file.Path))
				touch(t, file.Path)
				e.db.UpsertFiles(ctx, []store.File{file})
			case "directory":
				os.Remove(file.Path)
				os.Mkdir(file.Path, 0o755)
			case "symlink":
				outside := filepath.Join(t.TempDir(), "outside.mkv")
				touch(t, outside)
				os.Remove(file.Path)
				if err := os.Symlink(outside, file.Path); err != nil {
					t.Skip("symlink privilege unavailable", err)
				}
			case "inactive":
				e.update(t, func(config *settings.Settings) {
					config.Library.Folders[0].Active = false
					config.Library.Folders[0].Download = false
				})
			}
			preview, err := e.s.PreviewEpisodeDelete(ctx, show.ID, 1)
			if err != nil || preview.Allowed {
				t.Fatalf("unsafe preview %+v %v", preview, err)
			}
		})
	}
}

func TestV14DeleteManagedAndSharedRefusals(t *testing.T) {
	if !episodeDeletionSupported {
		t.Skip("Windows-only deletion")
	}
	for _, kind := range []string{"single", "multi", "shared", "batch", "candidate_batch", "other_torrent", "missing", "unavailable", "failure", "unchanged"} {
		t.Run(kind, func(t *testing.T) {
			e, show, file, client := managedFixture(t)
			ctx := context.Background()
			switch kind {
			case "multi":
				client.files[v14Hash] = append(client.files[v14Hash], qbit.TorrentFile{Name: "notes.txt", Size: 1})
			case "shared":
				_, err := e.db.UpsertRelease(ctx, &store.Release{ShowID: show.ID, Episode: store.Ptr(2), Title: "other", QbitHash: v14Hash})
				if err != nil {
					t.Fatal(err)
				}
			case "batch", "candidate_batch":
				hash := v14Hash
				if kind == "candidate_batch" {
					hash = ""
				}
				_, err := e.db.UpsertRelease(ctx, &store.Release{ShowID: show.ID, IsBatch: true, Title: "whole season", QbitHash: hash})
				if err != nil {
					t.Fatal(err)
				}
			case "other_torrent":
				client.torrents = append(client.torrents, qbit.Torrent{Hash: "other", Name: "Other", SavePath: e.root})
				client.files["other"] = client.files[v14Hash]
			case "missing":
				client.torrents = nil
			case "unavailable":
				e.update(t, func(config *settings.Settings) { config.Qbit.URL = "http://127.0.0.1:1" })
			case "failure":
				client.fail = true
			case "unchanged":
				client.unchanged = true
			}
			preview, err := e.s.PreviewEpisodeDelete(ctx, show.ID, 1)
			if err != nil {
				t.Fatal(err)
			}
			allowed := kind == "single" || kind == "candidate_batch" || kind == "missing" || kind == "failure" || kind == "unchanged"
			if preview.Allowed != allowed {
				t.Fatalf("preview %+v", preview)
			}
			if !allowed {
				if client.removes != 0 {
					t.Fatal("removed refused torrent")
				}
				return
			}
			result, err := e.s.DeleteEpisode(ctx, show.ID, 1, preview.Token)
			if err != nil || client.badDelete {
				t.Fatalf("delete %+v %v bad=%v", result, err, client.badDelete)
			}
			if kind == "failure" || kind == "unchanged" {
				if len(result.Remaining) < 2 {
					t.Fatal("false success", result)
				}
				if _, err := os.Stat(file.Path); err != nil {
					t.Fatal("file removed after torrent refusal")
				}
			} else if _, err := os.Stat(file.Path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("file remains", err)
			}
		})
	}
}

func TestV14DeleteConcurrentAndScan(t *testing.T) {
	if !episodeDeletionSupported {
		t.Skip("Windows-only deletion")
	}
	e, show, _, client := managedFixture(t)
	ctx := context.Background()
	preview, _ := e.s.PreviewEpisodeDelete(ctx, show.ID, 1)
	start := make(chan struct{})
	results := make(chan error, 3)
	for index := 0; index < 2; index++ {
		go func() { <-start; _, err := e.s.DeleteEpisode(ctx, show.ID, 1, preview.Token); results <- err }()
	}
	go func() { <-start; _, err := e.s.scanLocked(ctx, false); results <- err }()
	close(start)
	stale := 0
	for index := 0; index < 3; index++ {
		select {
		case err := <-results:
			if errors.Is(err, ErrDeleteStale) {
				stale++
			} else if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("deadlock")
		}
	}
	if stale != 1 || client.removes != 1 {
		t.Fatalf("stale=%d removes=%d", stale, client.removes)
	}
	episode, _ := e.db.GetEpisode(ctx, show.ID, 1)
	if episode.Auto == nil || *episode.Auto != 0 {
		t.Fatal("automatic re-download remains enabled")
	}
}

func TestV14DeletePinnedLeaf(t *testing.T) {
	if !episodeDeletionSupported {
		t.Skip("Windows-only deletion")
	}
	e, show, file, client := managedFixture(t)
	ctx := context.Background()
	var replaced bool
	client.onRemove = func() {
		if err := os.Rename(file.Path, file.Path+".old"); err == nil {
			replaced = true
			os.WriteFile(file.Path, []byte("protected replacement"), 0o644)
		}
	}
	preview, _ := e.s.PreviewEpisodeDelete(ctx, show.ID, 1)
	result, err := e.s.DeleteEpisode(ctx, show.ID, 1, preview.Token)
	if err != nil {
		t.Fatal(err)
	}
	if replaced {
		body, err := os.ReadFile(file.Path)
		if err != nil || string(body) != "protected replacement" || len(result.Remaining) == 0 {
			t.Fatalf("replacement removed: %s %+v %v", body, result, err)
		}
	} else if len(result.DeletedFiles) != 1 {
		t.Fatalf("pinned delete %+v", result)
	}
}

type queryBrain struct {
	*fakeBrain
	queries []string
}

func (brain *queryBrain) NyaaSearch(_ context.Context, _, query, _ string) ([]NyaaItem, error) {
	brain.queries = append(brain.queries, query)
	if strings.Contains(query, "S01E01*") {
		return []NyaaItem{{ID: 777, Title: "[SubsPlease] Frieren - 01v2 (1080p).mkv", InfoHash: v14Hash, Seeders: 20, SizeBytes: 1, PublishedAt: time.Unix(1, 0)}}, nil
	}
	return []NyaaItem{{ID: 999, Title: "[SubsPlease] Frieren - 02 (1080p).mkv", InfoHash: v14Hash, Seeders: 20}}, nil
}

func TestV14CheckSearchOnlyAndExplicitDownload(t *testing.T) {
	e, show, file, client := managedFixture(t)
	ctx := context.Background()
	e.db.SQL().ExecContext(ctx, "DELETE FROM releases")
	os.Remove(file.Path)
	e.db.MarkFilesMissing(ctx, []int64{file.ID})
	e.db.SetEpisodeAuto(ctx, show.ID, 1, store.Ptr(0))
	show.ListStatus = "paused"
	show.AutoDownload = store.Ptr(false)
	e.db.UpdateShowPrefs(ctx, &show)
	e.update(t, func(config *settings.Settings) { config.Conveyor.Enabled = false })
	brain := &queryBrain{fakeBrain: e.brain}
	e.s.brain = brain
	result, err := e.s.CheckEpisode(ctx, show.ID, 1, false)
	if err != nil || result.Outcome != "found" || len(result.Releases) != 1 || client.adds != 0 || len(brain.queries) != 2 {
		t.Fatalf("search %+v %v queries=%v adds=%d", result, err, brain.queries, client.adds)
	}
	all, _ := e.db.ListReleases(ctx, 0, nil)
	if len(all) != 1 || all[0].Episode == nil || *all[0].Episode != 1 {
		t.Fatalf("stored wrong episode %+v", all)
	}
	result, err = e.s.CheckEpisode(ctx, show.ID, 1, true)
	if err != nil || result.Outcome != "added" || client.adds != 1 {
		t.Fatalf("download %+v %v adds=%d", result, err, client.adds)
	}
	result, err = e.s.CheckEpisode(ctx, show.ID, 1, true)
	if err != nil || result.Outcome != "already_downloading" || client.adds != 1 {
		t.Fatalf("duplicate %+v %v adds=%d", result, err, client.adds)
	}
	episode, _ := e.db.GetEpisode(ctx, show.ID, 1)
	if episode.Auto == nil || *episode.Auto != 0 {
		t.Fatal("manual check enabled auto")
	}
}

func TestV14AutomaticAddRespectsDeleteAutoOff(t *testing.T) {
	e, show, _, client := managedFixture(t)
	ctx := context.Background()
	e.db.SQL().ExecContext(ctx, "DELETE FROM releases")
	e.db.SetEpisodeAuto(ctx, show.ID, 1, store.Ptr(0))
	for _, batch := range []bool{false, true} {
		rel := store.Release{ShowID: show.ID, Title: "Frieren", InfoHash: v14Hash, Magnet: e.brain.Magnet(v14Hash, "Frieren"), IsBatch: batch}
		if !batch {
			rel.Episode = store.Ptr(1)
		}
		e.db.UpsertRelease(ctx, &rel)
		if _, err := e.s.addRelease(ctx, e.set.Get(), show, rel, nil); err != nil {
			t.Fatal(err)
		}
	}
	if client.adds != 0 {
		t.Fatal("automatic re-download after deletion")
	}
}

func TestV14SubtitleArgsAndSMPlayerProfile(t *testing.T) {
	options := SubtitleOptions{Delay: 0.825, Scale: 0.625, Requested: true}
	for _, name := range []string{"mpv", "mpvnet", "celluloid", "iina-cli"} {
		command := playbackArgs(name, "file.mkv", 10, options)
		joined := strings.Join(command.args, " ")
		if !command.subtitles || !command.skipped || !strings.Contains(joined, "sub-delay=0.825") || !strings.Contains(joined, "sub-scale=0.625") || !strings.Contains(joined, "start=10") {
			t.Fatalf("%s %+v", name, command)
		}
	}
	if command := playbackArgs("vlc", "file.mkv", 10, options); command.subtitles || !command.skipped || command.note == "" {
		t.Fatalf("unsupported %+v", command)
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "smplayer.exe")
	source := filepath.Join(dir, "smplayer.ini")
	touch(t, filepath.Join(dir, "mpv", "mpv.exe"))
	media := filepath.Join(dir, "episode.mkv")
	touch(t, media)
	original := "[%General]\nmplayer_bin=mpv/mpv.exe\nremember_time_pos=true\n[advanced]\nmplayer_additional_options=\"--volume=25 --title=\\\"Test title\\\"\"\n[custom]\nuntouched=yes\n"
	if err := os.WriteFile(source, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	profile, cleanup, err := smplayerProfileFrom(exe, media, options, []string{source})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(profile, "smplayer.ini"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"single_instance_enabled=false", "remember_media_settings=true", "remember_time_pos=false", "initial_sub_scale_ass=0.625", "initial_sub_scale=3.125", "untouched=yes", "--volume=25", "sub-delay=0.825", "sub-ass-override=scale"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s: %s", want, text)
		}
	}
	lines := strings.Split(text, "\n")
	raw, _ := iniKey(lines, "advanced", "mplayer_additional_options")
	decoded, err := iniValue(raw)
	if err != nil || !strings.Contains(decoded, `--title="Test title"`) {
		t.Fatalf("options corrupted %q %v", decoded, err)
	}
	if strings.Contains(text, "\r") || strings.HasPrefix(text, "\ufeff") {
		t.Fatal("generated INI encoding")
	}
	unchanged, _ := os.ReadFile(source)
	if string(unchanged) != original {
		t.Fatal("real config changed")
	}
	cleanup()
	if _, err := os.Stat(profile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("profile leaked")
	}
	if _, err := iniValue("@ByteArray(unsupported)"); err == nil {
		t.Fatal("encoded options accepted")
	}
	config := settings.Defaults()
	config.Open.SubtitleDelaySec = 0.8
	config.Open.SubtitleScale = 0.6
	show := store.Show{SubtitleDelaySec: store.Ptr(0.0), SubtitleScale: store.Ptr(1.0)}
	if effective := subtitlesFor(config, &show); effective.Delay != 0 || effective.Scale != 1 || !effective.Requested {
		t.Fatal(effective)
	}
}

func TestV14TrackedPlayerMarksWatchedWithoutWindow(t *testing.T) {
	e, show, file := episodeFixture(t)
	ctx := context.Background()
	e.update(t, func(config *settings.Settings) { config.Open.IntroSkipSec = 0 })
	launcher := e.s.launch.(*fakeLauncher)
	launcher.done = make(chan struct{})
	result, err := e.s.OpenFileAt(ctx, file.ID)
	if err != nil || result.WantedSec != 0 {
		t.Fatal(result, err)
	}
	for seconds := 0; seconds <= 12*60; seconds += 5 {
		if seconds > 0 {
			e.clock = e.clock.Add(5 * time.Second)
		}
		if err := e.s.playerRun(ctx); err != nil {
			t.Fatal(err)
		}
	}
	episode, _ := e.db.GetEpisode(ctx, show.ID, 1)
	if episode.WatchedAt == nil {
		t.Fatal("tracked player did not mark watched at 12-minute threshold")
	}
	close(launcher.done)
}
