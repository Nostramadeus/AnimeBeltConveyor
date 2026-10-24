package sched

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/qbit"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

var ErrDeleteStale = errors.New("delete preview changed or expired; request a new preview")

type DeleteFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type DeleteTorrent struct {
	Hash string `json:"hash"`
	Name string `json:"name"`
}

type DeletePreview struct {
	Allowed  bool            `json:"allowed"`
	Reason   string          `json:"reason"`
	Token    string          `json:"token"`
	Files    []DeleteFile    `json:"files"`
	Torrents []DeleteTorrent `json:"torrents"`
}

type pathIdentity struct {
	path string
	info os.FileInfo
}

type deleteTarget struct {
	path    string
	fileID  int64
	info    os.FileInfo
	parents []pathIdentity
}

type episodeDeletePlan struct {
	preview         DeletePreview
	showID          int64
	number          int
	settings        string
	config          settings.Settings
	expires         time.Time
	targets         []deleteTarget
	releases        []int64
	torrentMetadata string
}

type EpisodeDeleteResult struct {
	DeletedFiles    []string `json:"deleted_files"`
	RemovedTorrents []string `json:"removed_torrents"`
	Remaining       []string `json:"remaining"`
	Explanation     string   `json:"explanation"`
}

func (s *Sched) PreviewEpisodeDelete(ctx context.Context, showID int64, number int) (DeletePreview, error) {
	s.addMu.Lock()
	defer s.addMu.Unlock()
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	plan, err := s.episodeDeletePlan(ctx, showID, number)
	if err != nil {
		return DeletePreview{}, err
	}
	if !plan.preview.Allowed {
		return plan.preview, nil
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return DeletePreview{}, err
	}
	plan.preview.Token = hex.EncodeToString(random[:])
	plan.expires = s.now().Add(5 * time.Minute)
	s.deleteMu.Lock()
	defer s.deleteMu.Unlock()
	if s.deletePlans == nil {
		s.deletePlans = map[string]*episodeDeletePlan{}
	}
	for token, prior := range s.deletePlans {
		if !s.now().Before(prior.expires) {
			delete(s.deletePlans, token)
		}
	}
	if len(s.deletePlans) >= 256 {
		return DeletePreview{}, errors.New("too many pending previews; retry after five minutes")
	}
	s.deletePlans[plan.preview.Token] = plan
	return plan.preview, nil
}

func safePath(path string, c settings.Settings) (os.FileInfo, []pathIdentity, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, nil, errors.New("path is not a clean absolute path")
	}
	inside := false
	for _, folder := range c.Library.Folders {
		if !folder.Active {
			continue
		}
		rel, err := filepath.Rel(filepath.Clean(folder.Path), path)
		if err == nil && rel != "." && filepath.IsLocal(rel) {
			inside = true
			break
		}
	}
	if !inside {
		return nil, nil, errors.New("file is outside an active Library folder")
	}
	var parents []pathIdentity
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot verify parent folder: %w", err)
		}
		if !info.IsDir() || unsafeReparse(info) {
			return nil, nil, errors.New("symlink or reparse traversal is not allowed")
		}
		parents = append(parents, pathIdentity{parent, info})
		if filepath.Dir(parent) == parent {
			break
		}
	}
	slices.Reverse(parents)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, parents, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || unsafeReparse(info) {
		return nil, nil, errors.New("target is not an exact regular video file")
	}
	return info, parents, nil
}

func (s *Sched) episodeDeletePlan(ctx context.Context, showID int64, number int) (*episodeDeletePlan, error) {
	plan := &episodeDeletePlan{showID: showID, number: number, preview: DeletePreview{Files: []DeleteFile{}, Torrents: []DeleteTorrent{}}}
	sh, err := s.st.GetShow(ctx, showID)
	if err != nil {
		return nil, err
	}
	if _, err := s.st.GetEpisode(ctx, showID, number); err != nil {
		return nil, err
	}
	c := s.set.Get()
	plan.config = c
	encoded, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	plan.settings = string(encoded)
	blocked := func(reason string) (*episodeDeletePlan, error) { plan.preview.Reason = reason; return plan, nil }
	if !episodeDeletionSupported {
		return blocked("Episode deletion is currently supported only on Windows; manage this file/torrent in your file manager and torrent client.")
	}
	shows, err := s.st.ListShows(ctx, "")
	if err != nil {
		return nil, err
	}
	titles := s.titles(ctx, shows)
	paths := map[string]int64{}
	files, err := s.st.FilesForShow(ctx, showID)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if file.Present && file.Episode != nil && *file.Episode == number {
			paths[file.Path] = file.ID
		}
	}
	releases, err := s.st.ListReleases(ctx, 0, nil)
	if err != nil {
		return nil, err
	}
	hashes := map[string]bool{}
	for _, rel := range releases {
		if rel.ShowID != showID || !rel.Covers(number) {
			continue
		}
		if rel.IsBatch && rel.QbitHash == "" && rel.Status != store.RelAdded && rel.Status != store.RelDownloading && rel.Status != store.RelDone {
			continue
		}
		plan.releases = append(plan.releases, rel.ID)
		if rel.IsBatch || rel.EpisodeEnd != nil && rel.Episode != nil && *rel.EpisodeEnd != *rel.Episode {
			return blocked("Torrent/release " + rel.Title + " includes or may include other episodes. Manage it in qBittorrent.")
		}
		if rel.QbitHash != "" {
			hashes[strings.ToLower(rel.QbitHash)] = true
		}
		if c.Downloader.Mode != settings.ModeQbit && (rel.QbitHash != "" || rel.Status == store.RelAdded || rel.Status == store.RelDownloading || rel.Status == store.RelDone) {
			return blocked("This release was handed to a torrent client (" + c.Downloader.Mode + "). ABC cannot remove its torrent; manage it in that client.")
		}
	}
	for _, rel := range releases {
		if hashes[strings.ToLower(rel.QbitHash)] && (rel.ShowID != showID || rel.Episode == nil || *rel.Episode != number || rel.IsBatch) {
			return blocked("Torrent " + rel.Title + " is shared with another show or episode. Manage it in qBittorrent.")
		}
	}
	var torrents []qbit.Torrent
	metadata := map[string]any{}
	if len(hashes) > 0 {
		qctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		torrents, err = s.qbitClient(c).Info(qctx, "")
		if err != nil {
			return blocked("Cannot verify qBittorrent torrents: " + err.Error())
		}
		for _, torrent := range torrents {
			if !hashes[strings.ToLower(torrent.Hash)] {
				continue
			}
			contents, err := s.qbitClient(c).Files(qctx, torrent.Hash)
			if err != nil {
				return blocked("Cannot verify torrent " + torrent.Name + ": " + err.Error())
			}
			if len(contents) != 1 {
				return blocked("Torrent " + torrent.Name + " contains multiple or unknown files and may include other episodes. Manage it in qBittorrent.")
			}
			content := contents[0]
			if !filepath.IsLocal(filepath.FromSlash(content.Name)) {
				return blocked("Torrent " + torrent.Name + " has an unsafe content path. Manage it in qBittorrent.")
			}
			path := filepath.Clean(filepath.Join(torrent.SavePath, filepath.FromSlash(content.Name)))
			if torrent.ContentPath != "" && !samePath(strings.TrimSuffix(path, ".!qB"), strings.TrimSuffix(filepath.Clean(torrent.ContentPath), ".!qB")) {
				return blocked("Torrent " + torrent.Name + " has unverifiable content paths. Manage it in qBittorrent.")
			}
			paths[path] = paths[path]
			partial := path + ".!qB"
			if _, err := os.Lstat(partial); err == nil {
				paths[partial] = 0
			} else if !errors.Is(err, os.ErrNotExist) {
				return blocked("Cannot verify partial file for " + torrent.Name)
			}
			plan.preview.Torrents = append(plan.preview.Torrents, DeleteTorrent{torrent.Hash, torrent.Name})
			metadata[torrent.Hash] = []any{torrent.Name, torrent.SavePath, torrent.ContentPath, torrent.Size, contents}
		}
		for _, torrent := range torrents {
			if hashes[strings.ToLower(torrent.Hash)] {
				continue
			}
			contents, err := s.qbitClient(c).Files(qctx, torrent.Hash)
			if err != nil {
				return blocked("Cannot verify ownership against torrent " + torrent.Name + ". Manage it in qBittorrent.")
			}
			for _, content := range contents {
				other := filepath.Clean(filepath.Join(torrent.SavePath, filepath.FromSlash(content.Name)))
				for path := range paths {
					if samePath(strings.TrimSuffix(path, ".!qB"), strings.TrimSuffix(other, ".!qB")) {
						return blocked("Torrent " + torrent.Name + " shares this file. Manage it in qBittorrent.")
					}
				}
			}
		}
	}
	meta, _ := json.Marshal(metadata)
	plan.torrentMetadata = string(meta)
	var ordered []string
	for path := range paths {
		ordered = append(ordered, path)
	}
	slices.Sort(ordered)
	for _, path := range ordered {
		name := strings.TrimSuffix(filepath.Base(path), ".!qB")
		if !slices.Contains(c.Library.VideoExts, strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")) {
			return blocked("Target is not a configured video file: " + path)
		}
		parsed := s.brain.Parse(name)
		sid, ep, ok := s.matchFile(parsed, path, titles)
		if !ok || int64(sid) != sh.ID || ep != number || parsed.IsBatch {
			return blocked("File cannot be verified as only the selected episode: " + path)
		}
		info, parents, err := safePath(path, c)
		if err != nil {
			return blocked(path + ": " + err.Error())
		}
		plan.targets = append(plan.targets, deleteTarget{path, paths[path], info, parents})
		if info != nil {
			plan.preview.Files = append(plan.preview.Files, DeleteFile{path, info.Size()})
		}
	}
	if len(plan.preview.Files) == 0 && len(plan.preview.Torrents) == 0 {
		return blocked("No verified episode files or managed torrents to delete.")
	}
	plan.preview.Allowed = true
	return plan, nil
}

func samePath(first, second string) bool {
	return strings.EqualFold(filepath.Clean(first), filepath.Clean(second))
}

func sameDeletePlan(prior, current *episodeDeletePlan) bool {
	if !current.preview.Allowed && current.preview.Reason != "No verified episode files or managed torrents to delete." || prior.settings != current.settings || prior.torrentMetadata != current.torrentMetadata || !reflect.DeepEqual(prior.releases, current.releases) || len(prior.targets) != len(current.targets) {
		return false
	}
	for index, before := range prior.targets {
		after := current.targets[index]
		if before.path != after.path || before.fileID != after.fileID || len(before.parents) != len(after.parents) {
			return false
		}
		for parent, identity := range before.parents {
			if !os.SameFile(identity.info, after.parents[parent].info) {
				return false
			}
		}
		if before.info == nil && after.info != nil || before.info != nil && after.info != nil && (!os.SameFile(before.info, after.info) || before.info.Size() != after.info.Size() || !before.info.ModTime().Equal(after.info.ModTime())) {
			return false
		}
	}
	return true
}

func (s *Sched) DeleteEpisode(ctx context.Context, showID int64, number int, token string) (EpisodeDeleteResult, error) {
	result := EpisodeDeleteResult{DeletedFiles: []string{}, RemovedTorrents: []string{}, Remaining: []string{}}
	s.deleteMu.Lock()
	prior := s.deletePlans[token]
	delete(s.deletePlans, token)
	s.deleteMu.Unlock()
	if prior == nil || prior.showID != showID || prior.number != number || !s.now().Before(prior.expires) {
		return result, ErrDeleteStale
	}
	s.addMu.Lock()
	defer s.addMu.Unlock()
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	current, err := s.episodeDeletePlan(ctx, showID, number)
	if err != nil {
		return result, err
	}
	if !sameDeletePlan(prior, current) {
		return result, ErrDeleteStale
	}
	var pinned []pinnedDelete
	defer func() {
		for _, target := range pinned {
			target.close()
		}
	}()
	for _, target := range current.targets {
		if target.info == nil {
			continue
		}
		handle, err := pinDelete(target)
		if err != nil {
			return result, fmt.Errorf("file changed or cannot be pinned; request a new preview: %w", err)
		}
		pinned = append(pinned, handle)
	}
	if err := s.st.SetEpisodeAuto(ctx, showID, number, store.Ptr(0)); err != nil {
		return result, err
	}
	for _, id := range current.releases {
		if err := s.st.SetReleaseStatus(ctx, id, store.RelSkipped, "deleted by user"); err != nil {
			return result, err
		}
	}
	s.clearNote(showID, number)
	qb := s.qbitClient(current.config)
	for _, torrent := range current.preview.Torrents {
		s.dropProgress(torrent.Hash)
		qctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		err := qb.Remove(qctx, torrent.Hash)
		if err == nil {
			for {
				_, _, _, statusErr := qb.Status(qctx, torrent.Hash)
				if errors.Is(statusErr, qbit.ErrNotFound) {
					break
				}
				if statusErr != nil {
					err = statusErr
					break
				}
				if !sleep(qctx, 100*time.Millisecond) {
					err = errors.New("torrent still present after removal request")
					break
				}
			}
		}
		cancel()
		if err != nil {
			result.Remaining = append(result.Remaining, "Torrent: "+torrent.Name)
			for _, target := range current.targets {
				if target.info != nil {
					result.Remaining = append(result.Remaining, target.path)
				}
			}
			result.Explanation = "Automatic download is off. No local files were deleted: " + err.Error()
			return result, nil
		}
		result.RemovedTorrents = append(result.RemovedTorrents, torrent.Name)
	}
	for _, target := range pinned {
		if err := target.remove(); err != nil && !errors.Is(err, os.ErrNotExist) {
			result.Remaining = append(result.Remaining, target.path+": "+err.Error())
			continue
		}
		result.DeletedFiles = append(result.DeletedFiles, target.path)
	}
	var missing []int64
	for _, target := range current.targets {
		if _, err := os.Lstat(target.path); errors.Is(err, os.ErrNotExist) && target.fileID != 0 {
			missing = append(missing, target.fileID)
		}
	}
	if err := s.st.MarkFilesMissing(ctx, missing); err != nil {
		result.Remaining = append(result.Remaining, "Database file-state update: "+err.Error())
	}
	result.Explanation = "Deleted. Watched history stays; automatic download is off for this episode."
	if len(result.RemovedTorrents) == 0 {
		result.Explanation = "Deleted files. No linked torrent was removed. Watched history stays; automatic download is off for this episode."
	}
	if len(result.Remaining) > 0 {
		result.Explanation = "Partly deleted. Automatic download is off. Remaining work is listed."
	}
	return result, nil
}
