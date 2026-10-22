package events

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

func TestLogWritesAndFansOut(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "abc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	l := New(db)
	a := l.Subscribe(10)
	b := l.Subscribe(10)
	e := l.Log(FileFound, 3, 5, "Frieren 05 on disk", map[string]any{"path": "x.mkv"})
	if e.ID == 0 || *e.ShowID != 3 || *e.Episode != 5 || string(e.Data) != `{"path":"x.mkv"}` {
		t.Fatalf("event = %+v", e)
	}
	for _, s := range []*Sub{a, b} {
		select {
		case got := <-s.C:
			if got.ID != e.ID || got.Kind != FileFound {
				t.Fatalf("subscriber got %+v", got)
			}
		case <-time.After(time.Second):
			t.Fatal("subscriber did not receive")
		}
	}
	rows, _ := db.ListEvents(context.Background(), 0, 10)
	if len(rows) != 1 || rows[0].Message != "Frieren 05 on disk" {
		t.Fatalf("rows = %+v", rows)
	}
	e2 := l.Log(ScanDone, 0, 0, "scan", nil)
	if e2.ShowID != nil || e2.Episode != nil || string(e2.Data) != "{}" {
		t.Fatalf("zero ids should be NULL: %+v", e2)
	}
	a.Close()
	b.Close()
	if l.Subscribers() != 0 {
		t.Fatal("Close did not unregister")
	}
}

type nopStore struct{ n int64 }

func (s *nopStore) InsertEvent(_ context.Context, e *store.Event) error {
	s.n++
	e.ID = s.n
	return nil
}

func TestSlowSubscriberDropsOldest(t *testing.T) {
	l := New(&nopStore{})
	s := l.Subscribe(3)
	for i := 0; i < 10; i++ {
		l.Log(ScanDone, 0, 0, "x", nil) // must never block
	}
	var ids []int64
	for len(s.C) > 0 {
		ids = append(ids, (<-s.C).ID)
	}
	if len(ids) != 3 || ids[0] != 8 || ids[2] != 10 {
		t.Fatalf("want newest 3 (8,9,10), got %v", ids)
	}
}
