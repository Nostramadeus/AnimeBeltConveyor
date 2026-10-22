package diskfree

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFreeTempDir(t *testing.T) {
	dir := t.TempDir()
	n, err := Free(dir)
	if err != nil || n <= 0 {
		t.Fatalf("Free(temp) = %d, %v; want > 0", n, err)
	}
	// A folder that does not exist yet reads its nearest existing ancestor.
	m, err := Free(filepath.Join(dir, "not", "yet"))
	if err != nil || m <= 0 {
		t.Fatalf("Free(missing child) = %d, %v; want > 0", m, err)
	}
	// A file reads its folder.
	f := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if k, err := Free(f); err != nil || k <= 0 {
		t.Fatalf("Free(file) = %d, %v", k, err)
	}
	if got, _ := Existing(filepath.Join(dir, "not", "yet")); got != dir {
		t.Errorf("Existing = %q, want %q", got, dir)
	}
	if _, err := Free(""); err == nil {
		t.Error("Free(\"\") should fail")
	}
	if Volume(dir) == "" {
		t.Error("Volume(temp) is empty")
	}
}
