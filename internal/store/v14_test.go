package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestV14MigrateFromFourSubtitleOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abc.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"schema.sql", "migrate_2.sql", "migrate_3.sql", "migrate_4.sql"} {
		body, _ := sqlFS.ReadFile(name)
		if _, err := raw.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec("INSERT INTO meta(key,value) VALUES('schema_version','4'); INSERT INTO shows(id,title_romaji,added_at,updated_at) VALUES(1,'Example',1,1)"); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	show, err := database.GetShow(context.Background(), 1)
	if err != nil || show.SubtitleDelaySec != nil || show.SubtitleScale != nil {
		t.Fatal(show, err)
	}
	show.SubtitleDelaySec = Ptr(0.0)
	show.SubtitleScale = Ptr(1.0)
	if err := database.UpdateShowPrefs(context.Background(), &show); err != nil {
		t.Fatal(err)
	}
	show, _ = database.GetShow(context.Background(), 1)
	if show.SubtitleDelaySec == nil || *show.SubtitleDelaySec != 0 || show.SubtitleScale == nil || *show.SubtitleScale != 1 {
		t.Fatal(show)
	}
	show.SubtitleDelaySec = Ptr(0.825)
	show.SubtitleScale = nil
	database.UpdateShowPrefs(context.Background(), &show)
	show, _ = database.GetShow(context.Background(), 1)
	if *show.SubtitleDelaySec != 0.825 || show.SubtitleScale != nil {
		t.Fatal(show)
	}
}
