package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Event is one row of the events table (the conveyor feed).
type Event struct {
	ID      int64           `json:"id"`
	TS      int64           `json:"ts"`
	Kind    string          `json:"kind"`
	ShowID  *int64          `json:"show_id"`
	Episode *int            `json:"episode"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// InsertEvent writes e and fills e.ID (and e.TS when zero).
func (d *DB) InsertEvent(ctx context.Context, e *Event) error {
	if e.TS == 0 {
		e.TS = Now()
	}
	if len(e.Data) == 0 {
		e.Data = json.RawMessage("{}")
	}
	res, err := d.db.ExecContext(ctx, `INSERT INTO events(ts, kind, show_id, episode, message, data) VALUES(?,?,?,?,?,?)`,
		e.TS, e.Kind, nint64(e.ShowID), nint(e.Episode), e.Message, string(e.Data))
	if err != nil {
		return err
	}
	e.ID, err = res.LastInsertId()
	return err
}

// ListEvents returns events with id > after, newest first, at most limit (default 100, max 1000).
func (d *DB) ListEvents(ctx context.Context, after int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := d.db.QueryContext(ctx, `SELECT id, ts, kind, show_id, episode, message, data FROM events
		WHERE id > ? ORDER BY id DESC LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var show, ep sql.NullInt64
		var data string
		if err := rows.Scan(&e.ID, &e.TS, &e.Kind, &show, &ep, &e.Message, &data); err != nil {
			return nil, err
		}
		e.ShowID, e.Episode = ptrInt64(show), ptrInt(ep)
		if data == "" {
			data = "{}"
		}
		e.Data = json.RawMessage(data)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Account is one row of the accounts table. Tokens never leave the server in JSON.
type Account struct {
	Service      string `json:"service"`
	UserID       string `json:"user_id"`
	UserName     string `json:"user_name"`
	AccessToken  string `json:"-"`
	RefreshToken string `json:"-"`
	ExpiresAt    *int64 `json:"expires_at"`
	PushEnabled  bool   `json:"push_enabled"`
}

const accountCols = `service, user_id, user_name, access_token, refresh_token, expires_at, push_enabled`

func scanAccount(r scanner) (Account, error) {
	var a Account
	var uid, uname, at, rt sql.NullString
	var exp sql.NullInt64
	var push int
	if err := r.Scan(&a.Service, &uid, &uname, &at, &rt, &exp, &push); err != nil {
		return a, err
	}
	a.UserID, a.UserName, a.AccessToken, a.RefreshToken = uid.String, uname.String, at.String, rt.String
	a.ExpiresAt = ptrInt64(exp)
	a.PushEnabled = push != 0
	return a, nil
}

// GetAccount returns the account for service (anilist, mal) or ErrNotFound.
func (d *DB) GetAccount(ctx context.Context, service string) (Account, error) {
	a, err := scanAccount(d.db.QueryRowContext(ctx, `SELECT `+accountCols+` FROM accounts WHERE service=?`, service))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// ListAccounts returns all connected accounts.
func (d *DB) ListAccounts(ctx context.Context) ([]Account, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT `+accountCols+` FROM accounts ORDER BY service`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpsertAccount writes a by service.
func (d *DB) UpsertAccount(ctx context.Context, a Account) error {
	_, err := d.db.ExecContext(ctx, `INSERT INTO accounts(`+accountCols+`) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(service) DO UPDATE SET user_id=excluded.user_id, user_name=excluded.user_name,
			access_token=excluded.access_token, refresh_token=excluded.refresh_token,
			expires_at=excluded.expires_at, push_enabled=excluded.push_enabled`,
		a.Service, nstr(a.UserID), nstr(a.UserName), nstr(a.AccessToken), nstr(a.RefreshToken), nint64(a.ExpiresAt), b2i(a.PushEnabled))
	return err
}

// SetAccountPush sets push_enabled for a service (no-op when not connected).
func (d *DB) SetAccountPush(ctx context.Context, service string, on bool) error {
	_, err := d.db.ExecContext(ctx, `UPDATE accounts SET push_enabled=? WHERE service=?`, b2i(on), service)
	return err
}

// DeleteAccount removes the account for service or returns ErrNotFound.
func (d *DB) DeleteAccount(ctx context.Context, service string) error {
	return d.exec1(ctx, `DELETE FROM accounts WHERE service=?`, service)
}
