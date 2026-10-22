package sched

import (
	"context"
	"errors"
	"fmt"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// ImportAniList imports a public AniList list (POST /api/import/anilist).
// New shows are created (one AniList Media call each), existing shows get list
// status and score; episodes 1..progress are marked watched (source import).
// TODO(spec): runs synchronously; a large list takes ~1 s per new show (rate limit).
func (s *Sched) ImportAniList(ctx context.Context, userName string) (added, updated int, err error) {
	entries, err := s.brain.AniListUserList(ctx, userName)
	if err != nil {
		return 0, 0, fmt.Errorf("AniList: %w", err)
	}
	return s.importEntries(ctx, entries, func(e ListEntry) (int, error) { return e.MediaID, nil })
}

// ImportMAL imports the connected MAL account's list (POST /api/import/mal).
// MAL ids are mapped to AniList with Media(idMal:) lookups.
func (s *Sched) ImportMAL(ctx context.Context) (added, updated int, err error) {
	acc, err := s.st.GetAccount(ctx, "mal")
	if errors.Is(err, store.ErrNotFound) || (err == nil && acc.AccessToken == "") {
		return 0, 0, errors.New("connect MyAnimeList first")
	}
	if err != nil {
		return 0, 0, err
	}
	entries, err := s.brain.MALUserList(ctx, acc.AccessToken)
	if err != nil {
		return 0, 0, fmt.Errorf("MyAnimeList: %w", err)
	}
	return s.importEntries(ctx, entries, func(e ListEntry) (int, error) {
		if e.MediaID != 0 {
			return e.MediaID, nil
		}
		m, err := s.brain.MediaByMAL(ctx, e.MALID)
		return m.ID, err
	})
}

func (s *Sched) importEntries(ctx context.Context, entries []ListEntry, anilistID func(ListEntry) (int, error)) (added, updated int, err error) {
	var firstErr error
	for _, e := range entries {
		if ctx.Err() != nil {
			return added, updated, ctx.Err()
		}
		id, err := anilistID(e)
		if err != nil || id == 0 {
			if firstErr == nil && err != nil {
				firstErr = err
			}
			continue
		}
		status := ListStatusFrom(e.Status)
		var score *float64
		if e.Score > 0 {
			v := e.Score
			score = &v
		}
		sh, err := s.st.GetShowByAniList(ctx, int64(id))
		switch {
		case errors.Is(err, store.ErrNotFound):
			m, err := s.brain.Media(ctx, id)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if sh, err = s.createFromMedia(ctx, m, status, score); err != nil {
				return added, updated, err
			}
			added++
		case err != nil:
			return added, updated, err
		default:
			sh.ListStatus = status
			if score != nil {
				sh.Score = score
			}
			if err := s.st.UpdateShowPrefs(ctx, &sh); err != nil {
				return added, updated, err
			}
			updated++
		}
		now := s.now().Unix()
		for n := 1; n <= e.Progress; n++ {
			if _, err := s.st.SetWatched(ctx, sh.ID, n, now, "import"); err != nil {
				return added, updated, err
			}
		}
	}
	if added+updated > 0 {
		s.rescanSoon()
	}
	if added+updated == 0 && firstErr != nil {
		return 0, 0, firstErr
	}
	return added, updated, nil
}
