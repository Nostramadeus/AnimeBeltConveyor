package sched

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
)

func TestLoopErrorMessagePlainWords(t *testing.T) {
	win := errors.New(`dial tcp 186.2.163.20:443: connectex: A connection attempt failed because the connected party did not properly respond after a period of time, or established connection failed because connected host has failed to respond.`)
	get := &url.Error{Op: "Get", URL: "https://nyaa.si/?c=1_2&f=0&page=rss&q=black+clover", Err: win}
	one := fmt.Errorf("nyaa %q: %w", "black clover", get)
	three := fmt.Errorf("%w (and 2 more)", one)

	cases := []struct {
		name  string
		err   error
		retry time.Duration
		want  string
	}{
		{"conveyor", one, 30 * time.Minute,
			`Release search failed while searching "black clover": nyaa.si did not answer in time. Check your internet connection or VPN; the site may also be down. Next try in 30 min.`},
		{"conveyor", three, 30 * time.Minute,
			`Release search failed while searching "black clover": nyaa.si did not answer in time. Check your internet connection or VPN; the site may also be down. Next try in 30 min (and 2 more).`},
		{"conveyor", three, 0,
			`Release search failed while searching "black clover": nyaa.si did not answer in time. Check your internet connection or VPN; the site may also be down. (and 2 more).`},
		{"airing", &url.Error{Op: "Post", URL: "https://graphql.anilist.co", Err: errors.New("dial tcp: lookup graphql.anilist.co: no such host")}, 6 * time.Hour,
			`Airing update (AniList) failed: graphql.anilist.co could not be looked up (DNS failed). Check your internet connection or VPN. Next try in 6 h.`},
		{"qbit", errors.New(`Post "http://127.0.0.1:8080/api/v2/auth/login": dial tcp 127.0.0.1:8080: connectex: No connection could be made because the target machine actively refused it.`), time.Minute,
			`qBittorrent sync failed: 127.0.0.1:8080 refused the connection. The service is down or the address in Settings is wrong. Next try in 1 min.`},
		{"conveyor", errors.New("nyaa: nyaa.si returned 503 Service Unavailable"), 30 * time.Minute,
			`Release search failed: nyaa.si is having trouble on its side (HTTP 503). Next try in 30 min.`},
		{"conveyor", errors.New("nyaa: nyaa.si returned 403 Forbidden"), 30 * time.Minute,
			`Release search failed: nyaa.si refused the request (HTTP 403). It may be blocking automated requests for a while. Next try in 30 min.`},
		{"scan", errors.New("boom"), 0, `Library scan: boom`},
		{"x", errors.New("boom (and 4 more)"), 0, `x: boom (and 4 more)`},
	}
	for _, c := range cases {
		if got := loopErrorMessage(c.name, c.err, c.retry); got != c.want {
			t.Errorf("%s / %v:\n got %q\nwant %q", c.name, c.err, got, c.want)
		}
	}
}

func TestLoopErrorEventKeepsRawDetail(t *testing.T) {
	e := newEnv(t)
	raw := `Get "https://nyaa.si/?q=x": dial tcp 1.2.3.4:443: i/o timeout`
	l := &loop{name: "conveyor", run: func(context.Context) error { return errors.New(raw) }, interval: func(settings.Settings) time.Duration { return 5 * time.Minute }}
	e.s.tryRun(l)
	evs, _ := e.db.ListEvents(context.Background(), 0, 1000)
	var found bool
	for _, ev := range evs {
		if ev.Kind != "error" {
			continue
		}
		found = true
		if strings.Contains(ev.Message, "i/o timeout") || !strings.Contains(ev.Message, "did not answer in time") || !strings.Contains(ev.Message, "Next try in 5 min") {
			t.Fatalf("message = %q", ev.Message)
		}
		if !strings.Contains(string(ev.Data), "i/o timeout") || !strings.Contains(string(ev.Data), `"retry_sec":300`) {
			t.Fatalf("data = %s", ev.Data)
		}
	}
	if !found {
		t.Fatal("no error event")
	}
}
