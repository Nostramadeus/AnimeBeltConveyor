package sched

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/events"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/scan"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// airingRun refreshes AniList data for releasing / not-yet-released shows, then dub times.
func (s *Sched) airingRun(ctx context.Context) error {
	shows, err := s.st.ListShows(ctx, "")
	if err != nil {
		return err
	}
	var errs []error
	for _, sh := range shows {
		if sh.AniListID == 0 || (sh.Status != "RELEASING" && sh.Status != "NOT_YET_RELEASED" && sh.Status != "") {
			continue
		}
		if _, err := s.refreshShow(ctx, sh); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", title(sh), err))
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if err := s.dubRefresh(ctx, shows); err != nil {
		errs = append(errs, fmt.Errorf("dubs: %w", err))
	}
	if len(errs) > 0 {
		return firstErrs(errs)
	}
	return nil
}

func firstErrs(errs []error) error {
	if len(errs) == 1 {
		return errs[0]
	}
	return fmt.Errorf("%w (and %d more)", errs[0], len(errs)-1)
}

// applyMedia copies AniList fields onto sh. Returns true when a field that matters changed.
func applyMedia(sh *store.Show, m Media) bool {
	before := *sh
	sh.AniListID = int64(m.ID)
	if m.IDMal != 0 {
		sh.MALID = int64(m.IDMal)
	}
	if m.TitleRomaji != "" {
		sh.TitleRomaji = m.TitleRomaji
	}
	sh.TitleEnglish, sh.TitleNative = m.TitleEnglish, m.TitleNative
	sh.Synonyms = append([]string{}, m.Synonyms...)
	sh.CoverURL, sh.BannerURL, sh.Color = m.CoverURL, m.BannerURL, m.Color
	sh.Format, sh.Status, sh.Season = m.Format, m.Status, m.Season
	sh.SeasonYear = optInt(m.SeasonYear)
	sh.EpisodesTotal = optInt(m.Episodes)
	sh.DurationMin = optInt(m.Duration)
	sh.NextAiringEp = optInt(m.NextAiringEp)
	sh.NextAiringAt = nil
	if m.NextAiringAt != 0 {
		v := m.NextAiringAt
		sh.NextAiringAt = &v
	}
	return before.Status != sh.Status || !eqInt(before.EpisodesTotal, sh.EpisodesTotal) ||
		!eqInt(before.NextAiringEp, sh.NextAiringEp) || !eqInt64(before.NextAiringAt, sh.NextAiringAt) ||
		before.TitleRomaji != sh.TitleRomaji || before.TitleEnglish != sh.TitleEnglish ||
		before.CoverURL != sh.CoverURL || before.BannerURL != sh.BannerURL || !slices.Equal(before.Synonyms, sh.Synonyms)
}

func optInt(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

func eqInt(a, b *int) bool     { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
func eqInt64(a, b *int64) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// syncEpisodes upserts aired_at from the airing schedule and makes sure rows
// 1..episodes_total exist. Returns the number of rows created or changed.
func (s *Sched) syncEpisodes(ctx context.Context, sh store.Show, m Media) (int, error) {
	changed := 0
	for _, a := range m.Airing {
		if a.Episode <= 0 {
			continue
		}
		at := a.AiringAt
		ch, err := s.st.UpsertEpisodeAiring(ctx, sh.ID, a.Episode, &at)
		if err != nil {
			return changed, err
		}
		if ch {
			changed++
		}
	}
	n, err := s.st.EnsureEpisodes(ctx, sh.ID, m.Episodes)
	return changed + n, err
}

// refreshShow re-fetches one show from AniList and writes meta + episodes.
func (s *Sched) refreshShow(ctx context.Context, sh store.Show) (store.Show, error) {
	m, err := s.brain.Media(ctx, int(sh.AniListID))
	if err != nil {
		return sh, err
	}
	metaChanged := applyMedia(&sh, m)
	if err := s.st.UpdateShowMeta(ctx, &sh); err != nil {
		return sh, err
	}
	n, err := s.syncEpisodes(ctx, sh, m)
	if err != nil {
		return sh, err
	}
	if metaChanged || n > 0 {
		msg := title(sh) + ": airing data updated"
		if sh.NextAiringEp != nil && sh.NextAiringAt != nil {
			msg = fmt.Sprintf("%s: next is ep %d", title(sh), *sh.NextAiringEp)
		}
		s.ev.Log(events.AiringUpdated, sh.ID, 0, msg, map[string]any{"episodes_changed": n, "status": sh.Status})
	}
	return sh, nil
}

// AddShow tracks an AniList media: fetches it, creates the show and its episodes,
// then queues a conveyor pass and a rescan. An already tracked show is returned
// unchanged with created=false.
func (s *Sched) AddShow(ctx context.Context, anilistID int, listStatus string) (store.Show, bool, error) {
	if existing, err := s.st.GetShowByAniList(ctx, int64(anilistID)); err == nil {
		return existing, false, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return existing, false, err
	}
	m, err := s.brain.Media(ctx, anilistID)
	if err != nil {
		return store.Show{}, false, fmt.Errorf("AniList: %w", err)
	}
	sh, err := s.createFromMedia(ctx, m, listStatus, nil)
	if err != nil {
		return sh, false, err
	}
	s.RunConveyorForShow(sh.ID)
	s.rescanSoon()
	return sh, true, nil
}

func (s *Sched) createFromMedia(ctx context.Context, m Media, listStatus string, score *float64) (store.Show, error) {
	if listStatus == "" {
		listStatus = "watching"
	}
	sh := store.Show{ListStatus: listStatus, Score: score}
	applyMedia(&sh, m)
	if sh.TitleRomaji == "" {
		sh.TitleRomaji = fmt.Sprintf("AniList %d", m.ID)
	}
	if err := s.st.CreateShow(ctx, &sh); err != nil {
		return sh, err
	}
	if _, err := s.syncEpisodes(ctx, sh, m); err != nil {
		return sh, err
	}
	s.ev.Log(events.ShowAdded, sh.ID, 0, "Tracking "+title(sh), map[string]any{"anilist_id": m.ID, "list_status": listStatus})
	return sh, nil
}

// RefreshShow re-fetches AniList data now and queues a conveyor pass for the show.
func (s *Sched) RefreshShow(ctx context.Context, id int64) (store.Show, error) {
	sh, err := s.st.GetShow(ctx, id)
	if err != nil {
		return sh, err
	}
	if sh.AniListID != 0 {
		if sh, err = s.refreshShow(ctx, sh); err != nil {
			return sh, err
		}
	}
	s.RunConveyorForShow(id)
	return sh, nil
}

// ShowPrefsChanged is called after PATCH /api/shows/{id}: matching inputs may
// have changed, so files are re-matched and the conveyor runs for the show.
func (s *Sched) ShowPrefsChanged(id int64) {
	s.rescanSoon()
	s.RunConveyorForShow(id)
}

// DeleteShow stops tracking a show. With deleteFiles, its present files inside
// the download folder are deleted from disk (never anything outside it).
func (s *Sched) DeleteShow(ctx context.Context, id int64, deleteFiles bool) (deleted int, err error) {
	sh, err := s.st.GetShow(ctx, id)
	if err != nil {
		return 0, err
	}
	if deleteFiles {
		root := s.set.Get().Library.DownloadDir()
		files, err := s.st.FilesForShow(ctx, id)
		if err != nil {
			return 0, err
		}
		var gone []int64
		for _, f := range files {
			if !f.Present || !scan.Under(f.Path, root) || f.Path == root {
				continue
			}
			if err := os.Remove(f.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
				s.ev.Log(events.Error, id, deref(f.Episode), "could not delete "+f.Path+": "+err.Error(), nil)
				continue
			}
			gone = append(gone, f.ID)
		}
		if err := s.st.MarkFilesMissing(ctx, gone); err != nil {
			return 0, err
		}
		deleted = len(gone)
	}
	if err := s.st.DeleteShow(ctx, id); err != nil {
		return deleted, err
	}
	msg := "Stopped tracking " + title(sh)
	if deleted > 0 {
		msg += fmt.Sprintf(" (%d files deleted)", deleted)
	}
	s.ev.Log(events.ShowRemoved, 0, 0, msg, map[string]any{"show_id": id, "files_deleted": deleted})
	return deleted, nil
}

// dubRefresh pulls this week's dub times from animeschedule.net when enabled.
func (s *Sched) dubRefresh(ctx context.Context, shows []store.Show) error {
	c := s.set.Get()
	if !c.Dubs.Enabled || c.Dubs.Token == "" {
		return nil
	}
	year, week := s.now().UTC().ISOWeek()
	entries, err := s.brain.DubTimes(ctx, c.Dubs.Token, year, week)
	if err != nil {
		return err
	}
	byAniList := map[int64]store.Show{}
	byTitle := map[string]store.Show{}
	for _, sh := range shows {
		if sh.AniListID != 0 {
			byAniList[sh.AniListID] = sh
		}
		byTitle[normTitle(sh.TitleRomaji)] = sh
	}
	for _, e := range entries {
		sh, ok := byAniList[int64(e.AniListID)]
		if !ok || e.AniListID == 0 {
			sh, ok = byTitle[normTitle(strings.TrimSpace(e.Title))]
		}
		if !ok || e.Episode <= 0 {
			continue
		}
		at := e.AiringAt
		if _, err := s.st.SetDubAired(ctx, sh.ID, e.Episode, &at); err != nil {
			return err
		}
	}
	return nil
}
