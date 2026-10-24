package sched

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/match"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

type EpisodeCheck struct {
	Releases    []store.Release `json:"releases"`
	Outcome     string          `json:"outcome"`
	Explanation string          `json:"explanation"`
}

func (s *Sched) CheckEpisode(ctx context.Context, showID int64, number int, download bool) (EpisodeCheck, error) {
	result := EpisodeCheck{Releases: []store.Release{}, Outcome: "not_found", Explanation: "No matching release found for this episode."}
	sh, err := s.st.GetShow(ctx, showID)
	if err != nil {
		return result, err
	}
	episode, err := s.st.GetEpisode(ctx, showID, number)
	if err != nil {
		return result, err
	}
	c := s.set.Get()
	var items []NyaaItem
	queries := episodeQueries(sh, number)
	seen := map[string]bool{}
	for index, query := range queries {
		if index > 0 && !sleep(ctx, s.nyaaSpace) {
			return result, ctx.Err()
		}
		found, err := s.brain.NyaaSearch(ctx, c.Conveyor.NyaaBase, query, c.Conveyor.NyaaCategory)
		if err != nil {
			return result, fmt.Errorf("episode search incomplete (%s): %w", query, err)
		}
		for _, item := range found {
			key := fmt.Sprintf("%d:%s:%s", item.ID, item.InfoHash, item.Title)
			if !seen[key] {
				seen[key] = true
				items = append(items, item)
			}
		}
	}
	titles := s.titles(ctx, []store.Show{sh})
	filtered := make([]NyaaItem, 0, len(items))
	for _, item := range items {
		parsed := s.brain.Parse(item.Title)
		sid, ep, ok, _, shift := s.brain.Match(parsed, titles)
		if !ok || int64(sid) != showID {
			continue
		}
		if ep != number && !(parsed.IsBatch && (ep == 0 || number >= ep && number <= parsed.EpisodeEnd-shift)) {
			continue
		}
		filtered = append(filtered, item)
	}
	s.addMu.Lock()
	defer s.addMu.Unlock()
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	currentShow, err := s.st.GetShow(ctx, showID)
	if err != nil {
		return result, err
	}
	currentEpisode, err := s.st.GetEpisode(ctx, showID, number)
	if err != nil {
		return result, err
	}
	currentSettings := s.set.Get()
	if !reflect.DeepEqual(sh, currentShow) || !reflect.DeepEqual(c, currentSettings) {
		result.Outcome, result.Explanation = "waiting", "Show or settings changed during the search. Check this episode again."
		return result, nil
	}
	episode = currentEpisode
	s.storeCandidatesWithCutoff(ctx, c, filtered, titles, map[int64]store.Show{sh.ID: sh}, false)
	result.Releases, err = s.st.ListReleases(ctx, showID, &number)
	if err != nil {
		return result, err
	}
	if len(result.Releases) > 0 {
		result.Outcome, result.Explanation = "found", "Releases found. Nothing was downloaded by this search."
	}
	if !download {
		return result, nil
	}
	files, err := s.st.FilesForShow(ctx, showID)
	if err != nil {
		return result, err
	}
	for _, file := range files {
		if file.Episode != nil && *file.Episode == number && file.Present {
			if info, err := os.Stat(file.Path); err == nil && info.Mode().IsRegular() {
				result.Outcome, result.Explanation = "already_on_disk", "This episode is already on disk."
				return result, nil
			}
		}
	}
	var candidates []Candidate
	for _, rel := range result.Releases {
		if rel.Status == store.RelAdded || rel.Status == store.RelDownloading {
			result.Outcome, result.Explanation = "already_downloading", "A release already covers this episode; no duplicate was added."
			return result, nil
		}
		if !rel.IsBatch && (rel.Status == store.RelCandidate || rel.Status == store.RelChosen || rel.Status == store.RelSkipped || rel.Status == store.RelFailed || rel.Status == store.RelDone) {
			candidates = append(candidates, candidateOf(rel))
		}
	}
	pick, reason := s.brain.Pick(candidates, rulesFor(c, sh, nil), s.now(), airedTime(episode))
	if pick == nil {
		if reason != "" {
			result.Outcome, result.Explanation = "waiting", reason
		}
		return result, nil
	}
	for _, rel := range result.Releases {
		if rel.ID != pick.ID {
			continue
		}
		if rel.Status == store.RelDone {
			if err := s.st.SetReleaseStatus(ctx, rel.ID, store.RelCandidate, ""); err != nil {
				return result, err
			}
		}
		if !s.roomFor(c, sh, number, rel, s.newBudget(ctx, c)) {
			result.Outcome, result.Explanation = "waiting", "Waiting for enough free disk space."
			return result, nil
		}
		updated, err := s.addReleaseLocked(ctx, c, sh, rel, nil, true)
		if err != nil {
			return result, err
		}
		if updated.Status == store.RelAdded {
			result.Outcome, result.Explanation = "added", "Release handed to the configured downloader."
		} else {
			result.Outcome, result.Explanation = "waiting", "Release chosen. Use its Magnet button to add it manually."
		}
		if updated.StatusNote != "" {
			result.Explanation += " " + updated.StatusNote
		}
		s.clearNote(showID, number)
		break
	}
	result.Releases, err = s.st.ListReleases(ctx, showID, &number)
	return result, err
}

func episodeQueries(show store.Show, number int) []string {
	season := match.SeasonNumber(show.TitleRomaji)
	if season == 0 {
		season = match.SeasonNumber(show.TitleEnglish)
	}
	if season == 0 {
		season = 1
	}
	clause := func(episode int, withSeason bool) string {
		forms := []string{fmt.Sprint(episode), fmt.Sprintf("%02d*", episode), fmt.Sprintf("%03d*", episode)}
		if withSeason {
			forms = append(forms, fmt.Sprintf("S%02dE%02d*", season, episode))
		}
		unique := []string{}
		for _, form := range forms {
			if !slices.Contains(unique, form) {
				unique = append(unique, form)
			}
		}
		return "(" + strings.Join(unique, "|") + ")"
	}
	title := NyaaQuery(show)
	queries := []string{"(" + title + ") " + clause(number, true)}
	if offset := store.EffectiveOffset(show); offset > 0 {
		queries = append(queries, "("+title+") "+clause(number+offset, false))
	}
	return append(queries, title)
}
