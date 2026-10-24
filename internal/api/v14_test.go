package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/sched"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

func TestV14EpisodeActionsAndSubtitlePATCH(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	show := store.Show{TitleRomaji: "Frieren"}
	if err := env.db.CreateShow(ctx, &show); err != nil {
		t.Fatal(err)
	}
	env.db.UpsertEpisodeAiring(ctx, show.ID, 1, nil)
	path := filepath.Join(env.root, "[SubsPlease] Frieren - 01 (1080p).mkv")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	env.db.UpsertFiles(ctx, []store.File{{Path: path, ShowID: &show.ID, Episode: store.Ptr(1), Size: 1}})
	base := fmt.Sprintf("/api/shows/%d", show.ID)
	for _, body := range []any{map[string]any{"subtitle_delay_sec": 0.825, "subtitle_scale": 0.625}, map[string]any{"subtitle_delay_sec": 0, "subtitle_scale": 1}, map[string]any{"subtitle_delay_sec": nil, "subtitle_scale": nil}} {
		if status, result := env.do(t, "PATCH", base, body); status != 200 {
			t.Fatalf("patch %d %s", status, result)
		}
		status, result := env.do(t, "GET", base, nil)
		var detail struct {
			Show ShowFull `json:"show"`
		}
		detail = decodeT[struct {
			Show ShowFull `json:"show"`
		}](t, result)
		if status != 200 {
			t.Fatalf("get show %d %s", status, result)
		}
		if value := body.(map[string]any)["subtitle_delay_sec"]; value == nil {
			if detail.Show.SubtitleDelaySec != nil || detail.Show.SubtitleScale != nil {
				t.Fatal("null overrides lost", detail.Show)
			}
		} else if detail.Show.SubtitleDelaySec == nil || detail.Show.SubtitleScale == nil {
			t.Fatal("override fields absent from view", detail.Show)
		} else {
			wantDelay, wantScale := 0.0, 1.0
			if number, ok := value.(float64); ok {
				wantDelay = number
				wantScale = body.(map[string]any)["subtitle_scale"].(float64)
			}
			if *detail.Show.SubtitleDelaySec != wantDelay || *detail.Show.SubtitleScale != wantScale {
				t.Fatal("override values changed in view", detail.Show)
			}
		}
	}
	for _, body := range []any{map[string]any{"subtitle_delay_sec": 61}, map[string]any{"subtitle_scale": 0}, map[string]any{"subtitle_scale": "1"}} {
		if status, _ := env.do(t, "PATCH", base, body); status != 400 {
			t.Fatalf("invalid patch %d", status)
		}
	}
	if status, body := env.do(t, "POST", base+"/episodes/1/check", map[string]any{"download": false}); status != 200 {
		t.Fatalf("check %d %s", status, body)
	}
	if status, _ := env.do(t, "POST", base+"/episodes/1/check", map[string]any{}); status != 400 {
		t.Fatal("missing download accepted")
	}
	if status, _ := env.do(t, "POST", base+"/episodes/1/delete", map[string]any{"token": "bad"}, "Origin", "https://foreign.invalid"); status != 403 {
		t.Fatal("origin bypass")
	}
	status, body := env.do(t, "GET", base+"/episodes/1/delete", nil)
	preview := decodeT[sched.DeletePreview](t, body)
	if runtime.GOOS != "windows" {
		if preview.Allowed {
			t.Fatal("non-Windows deletion must refuse")
		}
		return
	}
	if status != 200 || !preview.Allowed {
		t.Fatalf("preview %d %s", status, body)
	}
	if status, _ := env.do(t, "POST", base+"/episodes/1/delete", map[string]any{"token": preview.Token, "path": path}); status != 409 {
		t.Fatal("caller path accepted")
	}
	if status, body := env.do(t, "POST", base+"/episodes/1/delete", map[string]any{"token": preview.Token}); status != 200 {
		t.Fatalf("delete %d %s", status, body)
	}
	if status, _ := env.do(t, "POST", base+"/episodes/1/delete", map[string]any{"token": preview.Token}); status != 409 {
		t.Fatal("token replay accepted")
	}
}
