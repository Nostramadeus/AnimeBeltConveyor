package sched

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/events"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// pusher sends watched progress to AniList / MAL. Jobs live in memory; a job is
// retried after s.pushRetry * tries and dropped after 3 failures with push.failed.
type pusher struct {
	s     *Sched
	mu    sync.Mutex
	jobs  map[string]*pushJob // key service:showID (one pending job per show and service)
	wake  chan struct{}
	tick  time.Duration
	limit int

	queued int // enqueue calls that queued or refreshed a job (tests check "one push per bulk action")
}

type pushJob struct {
	service string
	showID  int64
	tries   int
	nextAt  time.Time
}

func newPusher(s *Sched) *pusher {
	return &pusher{s: s, jobs: map[string]*pushJob{}, wake: make(chan struct{}, 1), tick: 15 * time.Second, limit: 3}
}

// enqueue queues a push for every enabled, connected service.
func (p *pusher) enqueue(showID int64) {
	c := p.s.set.Get()
	var services []string
	if c.Push.AniList {
		services = append(services, "anilist")
	}
	if c.Push.MAL {
		services = append(services, "mal")
	}
	if len(services) == 0 {
		return
	}
	p.mu.Lock()
	p.queued++
	for _, svc := range services {
		key := svc + ":" + itoa64(showID)
		if j, ok := p.jobs[key]; ok {
			j.nextAt = time.Time{} // newer progress: send now
			continue
		}
		p.jobs[key] = &pushJob{service: svc, showID: showID}
	}
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *pusher) run(ctx context.Context) {
	t := time.NewTicker(p.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
		case <-t.C:
		}
		p.flush(ctx)
	}
}

// flush sends every due job once.
func (p *pusher) flush(ctx context.Context) {
	now := p.s.now()
	p.mu.Lock()
	var due []*pushJob
	for _, j := range p.jobs {
		if !j.nextAt.After(now) {
			due = append(due, j)
		}
	}
	p.mu.Unlock()
	for _, j := range due {
		if ctx.Err() != nil {
			return
		}
		err := p.send(ctx, j)
		p.mu.Lock()
		key := j.service + ":" + itoa64(j.showID)
		if err == nil || errors.Is(err, errNothingToPush) {
			delete(p.jobs, key)
			p.mu.Unlock()
			continue
		}
		j.tries++
		drop := j.tries >= p.limit
		if drop {
			delete(p.jobs, key)
		} else {
			j.nextAt = now.Add(p.s.pushRetry * time.Duration(j.tries))
		}
		p.mu.Unlock()
		if drop {
			sh, _ := p.s.st.GetShow(ctx, j.showID)
			p.s.ev.Log(events.PushFailed, j.showID, 0, fmt.Sprintf("%s: progress push for %s failed %d times: %v", serviceName(j.service), title(sh), j.tries, err),
				map[string]any{"service": j.service})
		}
	}
}

var errNothingToPush = errors.New("nothing to push")

func serviceName(svc string) string {
	if svc == "mal" {
		return "MyAnimeList"
	}
	return "AniList"
}

// send pushes the show's highest watched episode number.
func (p *pusher) send(ctx context.Context, j *pushJob) error {
	s := p.s
	acc, err := s.st.GetAccount(ctx, j.service)
	if errors.Is(err, store.ErrNotFound) || (err == nil && acc.AccessToken == "") {
		return errNothingToPush // not connected: nothing to retry
	}
	if err != nil {
		return err
	}
	sh, err := s.st.GetShow(ctx, j.showID)
	if errors.Is(err, store.ErrNotFound) {
		return errNothingToPush
	}
	if err != nil {
		return err
	}
	progress, err := s.st.MaxWatched(ctx, j.showID)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	switch j.service {
	case "anilist":
		if sh.AniListID == 0 {
			return errNothingToPush
		}
		err = s.brain.AniListPush(cctx, acc.AccessToken, int(sh.AniListID), progress)
	case "mal":
		if sh.MALID == 0 {
			return errNothingToPush
		}
		err = s.brain.MALPush(cctx, acc.AccessToken, int(sh.MALID), progress)
	default:
		return errNothingToPush
	}
	if err != nil {
		return err
	}
	s.ev.Log(events.PushOK, j.showID, progress, fmt.Sprintf("%s: %s progress set to %d", serviceName(j.service), title(sh), progress),
		map[string]any{"service": j.service, "progress": progress})
	return nil
}
