// Package events writes the conveyor feed (events table) and fans new events out
// to live subscribers (one channel per SSE client).
package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// Event kinds (SPEC section 7).
const (
	ShowAdded        = "show.added"
	ShowRemoved      = "show.removed"
	AiringUpdated    = "airing.updated"
	ReleaseFound     = "release.found"
	ReleaseAdded     = "release.added"
	ReleaseWaiting   = "release.waiting"
	ReleaseSkipped   = "release.skipped"
	DownloadDone     = "download.done"
	DownloadFailed   = "download.failed"
	FileFound        = "file.found"
	FileMissing      = "file.missing"
	EpisodeWatched   = "episode.watched"
	EpisodeUnwatched = "episode.unwatched"
	PushOK           = "push.ok"
	PushFailed       = "push.failed"
	ScanDone         = "scan.done"
	Error            = "error"
)

// Store is what the logger needs from the database.
type Store interface {
	InsertEvent(ctx context.Context, e *store.Event) error
}

// Logger writes events and fans them out.
type Logger struct {
	st   Store
	mu   sync.Mutex
	subs map[*Sub]struct{}
}

// Sub is one live subscriber. Read from C; call Close when done.
type Sub struct {
	C  <-chan store.Event
	ch chan store.Event
	l  *Logger
}

// New returns a Logger writing to st.
func New(st Store) *Logger {
	return &Logger{st: st, subs: map[*Sub]struct{}{}}
}

// Log writes an event row and sends it to every subscriber. showID 0 and
// episode 0 are stored as NULL. data is marshalled to JSON (nil -> {}).
// A failed insert is logged and the event is still broadcast with ID 0.
func (l *Logger) Log(kind string, showID int64, episode int, message string, data any) store.Event {
	e := store.Event{Kind: kind, Message: message, Data: json.RawMessage("{}")}
	if showID != 0 {
		e.ShowID = &showID
	}
	if episode != 0 {
		e.Episode = &episode
	}
	if data != nil {
		if b, err := json.Marshal(data); err == nil && string(b) != "null" {
			e.Data = b
		}
	}
	// Hold the lock across insert and fan-out so subscribers see events in id order.
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.st.InsertEvent(context.Background(), &e); err != nil {
		slog.Error("event insert failed", "kind", kind, "err", err)
	}
	lvl := slog.LevelInfo
	if kind == Error || kind == DownloadFailed || kind == PushFailed {
		lvl = slog.LevelWarn
	}
	slog.Log(context.Background(), lvl, message, "event", kind, "show", showID, "episode", episode)
	for s := range l.subs {
		send(s.ch, e)
	}
	return e
}

// send never blocks: when the buffer is full the oldest queued event is dropped.
func send(ch chan store.Event, e store.Event) {
	for {
		select {
		case ch <- e:
			return
		default:
		}
		select {
		case <-ch:
		default:
		}
	}
}

// Subscribe registers a subscriber with a buffer of buf events (min 1).
func (l *Logger) Subscribe(buf int) *Sub {
	if buf < 1 {
		buf = 1
	}
	ch := make(chan store.Event, buf)
	s := &Sub{C: ch, ch: ch, l: l}
	l.mu.Lock()
	l.subs[s] = struct{}{}
	l.mu.Unlock()
	return s
}

// Close unregisters the subscriber. The channel is not closed (Log may hold a
// reference mid-send); readers stop by their own context.
func (s *Sub) Close() {
	s.l.mu.Lock()
	delete(s.l.subs, s)
	s.l.mu.Unlock()
}

// Subscribers returns the number of live subscribers.
func (l *Logger) Subscribers() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.subs)
}
