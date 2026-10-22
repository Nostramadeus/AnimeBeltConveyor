// Package store owns the SQLite database: opening, migrations and typed queries.
package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"time"

	_ "modernc.org/sqlite" // driver "sqlite"
)

//go:embed *.sql
var sqlFS embed.FS

// SchemaVersion is the version schema.sql creates. Later versions add
// migrate_N.sql files (N = 2, 3, ...) next to schema.sql; they are applied in order.
const SchemaVersion = 1

// ErrNotFound is returned by Get* functions when no row matches.
var ErrNotFound = errors.New("not found")

// DB wraps *sql.DB with the typed query methods.
type DB struct {
	db *sql.DB
}

// Open opens (or creates) the database file at path with WAL and foreign keys
// on, then applies schema.sql and any pending migrations.
func Open(path string) (*DB, error) {
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	sdb.SetMaxOpenConns(4)
	d := &DB{db: sdb}
	if err := d.migrate(context.Background()); err != nil {
		sdb.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}

// Close closes the database.
func (d *DB) Close() error { return d.db.Close() }

// SQL exposes the raw handle (tests, one-off queries).
func (d *DB) SQL() *sql.DB { return d.db }

func (d *DB) migrate(ctx context.Context) error {
	schema, err := sqlFS.ReadFile("schema.sql")
	if err != nil {
		return err
	}
	if _, err := d.db.ExecContext(ctx, string(schema)); err != nil {
		return fmt.Errorf("schema.sql: %w", err)
	}
	v, ok, err := d.Meta(ctx, "schema_version")
	if err != nil {
		return err
	}
	cur := SchemaVersion
	if ok {
		if cur, err = strconv.Atoi(v); err != nil {
			return fmt.Errorf("bad schema_version %q", v)
		}
	} else if err := d.SetMeta(ctx, "schema_version", strconv.Itoa(SchemaVersion)); err != nil {
		return err
	}
	for n := cur + 1; ; n++ {
		body, err := sqlFS.ReadFile(fmt.Sprintf("migrate_%d.sql", n))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		tx, err := d.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migrate_%d.sql: %w", n, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES('schema_version',?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.Itoa(n)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
}

// Meta reads a meta value.
func (d *DB) Meta(ctx context.Context, key string) (string, bool, error) {
	return d.kvGet(ctx, "meta", key)
}

// SetMeta writes a meta value.
func (d *DB) SetMeta(ctx context.Context, key, value string) error {
	return d.kvPut(ctx, "meta", key, value)
}

// GetSetting reads a raw settings value (JSON text). Implements settings.KV.
func (d *DB) GetSetting(key string) (string, bool, error) {
	return d.kvGet(context.Background(), "settings", key)
}

// PutSetting writes a raw settings value (JSON text). Implements settings.KV.
func (d *DB) PutSetting(key, value string) error {
	return d.kvPut(context.Background(), "settings", key, value)
}

func (d *DB) kvGet(ctx context.Context, table, key string) (string, bool, error) {
	var v string
	err := d.db.QueryRowContext(ctx, "SELECT value FROM "+table+" WHERE key=?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func (d *DB) kvPut(ctx context.Context, table, key, value string) error {
	_, err := d.db.ExecContext(ctx, "INSERT INTO "+table+"(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
	return err
}

// Now is the clock used for timestamps written by the store. Tests may replace it.
var Now = defaultNow

func defaultNow() int64 { return time.Now().Unix() }

// ---- NULL helpers ----

func nstr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nid(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func nint(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func nint64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func nfloat(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func nbool(p *bool) any {
	if p == nil {
		return nil
	}
	if *p {
		return 1
	}
	return 0
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func njsonList(v []string) any {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func jsonList(v []string) string {
	if v == nil {
		v = []string{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func ptrInt(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

func ptrInt64(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func ptrFloat(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}

func ptrBool(n sql.NullInt64) *bool {
	if !n.Valid {
		return nil
	}
	v := n.Int64 != 0
	return &v
}

func parseList(s sql.NullString) []string {
	if !s.Valid {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s.String), &out); err != nil {
		return nil
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// Ptr returns a pointer to v (handy for nullable fields).
func Ptr[T any](v T) *T { return &v }

type scanner interface {
	Scan(dest ...any) error
}
