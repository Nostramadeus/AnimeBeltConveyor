//go:build windows

package sched

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestV14DeleteRejectsJunction(t *testing.T) {
	env, show, file := episodeFixture(t)
	outside := t.TempDir()
	old := env.root + ".old"
	if err := os.Rename(env.root, old); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("cmd.exe", "/c", "mklink", "/J", env.root, outside)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(output), err)
	}
	protected := filepath.Join(outside, filepath.Base(file.Path))
	if err := os.WriteFile(protected, []byte("protected"), 0o600); err != nil {
		t.Fatal(err)
	}
	preview, err := env.s.PreviewEpisodeDelete(context.Background(), show.ID, 1)
	if err != nil || preview.Allowed {
		t.Fatal(preview, err)
	}
	if body, err := os.ReadFile(protected); err != nil || string(body) != "protected" {
		t.Fatal("outside file changed", err)
	}
}
