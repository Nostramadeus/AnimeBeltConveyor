package store

import (
	"database/sql"
	"encoding/json"
	"os"
)

// PeekServerPort reads Settings > Server > port from the database at path
// WITHOUT opening it for real (no migration, read-only). 0 when the file,
// the settings row or the value is missing. cmd/abc uses it to notice an
// already running ABC before store.Open would migrate the shared database.
func PeekServerPort(path string) int {
	if _, err := os.Stat(path); err != nil {
		return 0
	}
	sdb, err := sql.Open("sqlite", path+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return 0
	}
	defer sdb.Close()
	var blob string
	if err := sdb.QueryRow(`SELECT value FROM settings WHERE key='app'`).Scan(&blob); err != nil {
		return 0
	}
	var v struct {
		Server struct {
			Port int `json:"port"`
		} `json:"server"`
	}
	if json.Unmarshal([]byte(blob), &v) != nil {
		return 0
	}
	return v.Server.Port
}
