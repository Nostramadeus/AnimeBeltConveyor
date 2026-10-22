package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDataDirPrecedence(t *testing.T) {
	base := t.TempDir()
	exe := filepath.Join(base, "app")
	cfg := filepath.Join(base, "config")
	os.MkdirAll(exe, 0o755)
	userCfg := func() (string, error) { return cfg, nil }

	// no portable folder: per-user config folder
	dir, src, err := dataDir("", exe, userCfg)
	if err != nil || dir != filepath.Join(cfg, "abc") || src != srcUser {
		t.Fatalf("user config: %q %q %v", dir, src, err)
	}
	// a "data" folder next to abc.exe makes it portable
	os.MkdirAll(filepath.Join(exe, "data"), 0o755)
	dir, src, err = dataDir("", exe, userCfg)
	if err != nil || dir != filepath.Join(exe, "data") || src != srcPortable {
		t.Fatalf("portable: %q %q %v", dir, src, err)
	}
	// the flag wins over both
	flagDir := filepath.Join(base, "custom")
	dir, src, err = dataDir(flagDir, exe, userCfg)
	if err != nil || dir != flagDir || src != srcFlag {
		t.Fatalf("flag: %q %q %v", dir, src, err)
	}
	// a file named "data" is not a portable folder
	exe2 := filepath.Join(base, "app2")
	os.MkdirAll(exe2, 0o755)
	os.WriteFile(filepath.Join(exe2, "data"), []byte("x"), 0o644)
	if dir, src, _ := dataDir("", exe2, userCfg); src != srcUser || dir != filepath.Join(cfg, "abc") {
		t.Fatalf("file named data: %q %q", dir, src)
	}
	// no config folder and nothing else: a clear error
	if _, _, err := dataDir("", "", func() (string, error) { return "", errors.New("no HOME") }); err == nil {
		t.Fatal("want an error without any data folder")
	}
}
