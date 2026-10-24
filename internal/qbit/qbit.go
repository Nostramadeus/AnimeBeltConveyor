// Package qbit is a small qBittorrent WebUI API v2 client.
package qbit

import (
	"bytes"
	"context"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ErrLogin is returned when qBittorrent rejects the credentials.
var ErrLogin = errors.New("qbittorrent login failed (check username/password)")

// ErrNotFound is returned by Status when qBittorrent does not know the hash.
var ErrNotFound = errors.New("torrent not found in qbittorrent")

// Client talks to one qBittorrent instance. Safe for concurrent use.
type Client struct {
	base     string
	user     string
	pass     string
	hc       *http.Client
	mu       sync.Mutex
	loggedIn bool
}

// Torrent is the subset of /api/v2/torrents/info fields the app uses.
type Torrent struct {
	Hash        string  `json:"hash"`
	Name        string  `json:"name"`
	Progress    float64 `json:"progress"`
	State       string  `json:"state"`
	ContentPath string  `json:"content_path"`
	SavePath    string  `json:"save_path"`
	Category    string  `json:"category"`
	Size        int64   `json:"size"`
}

type TorrentFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

func (c *Client) Files(ctx context.Context, hash string) ([]TorrentFile, error) {
	body, err := c.get(ctx, "/api/v2/torrents/files", url.Values{"hash": {hash}})
	if err != nil {
		return nil, err
	}
	var files []TorrentFile
	if err := json.Unmarshal(body, &files); err != nil {
		return nil, err
	}
	return files, nil
}

func (c *Client) Remove(ctx context.Context, hash string) error {
	_, err := c.postForm(ctx, "/api/v2/torrents/delete", url.Values{"hashes": {hash}, "deleteFiles": {"false"}})
	return err
}

// New returns a client for baseURL (e.g. "http://127.0.0.1:8080").
func New(baseURL, username, password string) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{
		base: strings.TrimRight(baseURL, "/"),
		user: username,
		pass: password,
		hc:   &http.Client{Jar: jar, Timeout: 15 * time.Second},
	}
}

// Login authenticates and stores the SID cookie.
func (c *Client) Login(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loginLocked(ctx)
}

func (c *Client) loginLocked(ctx context.Context) error {
	form := url.Values{"username": {c.user}, "password": {c.pass}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/v2/auth/login", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.headers(req)
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("qbittorrent refused login (IP banned after too many failures?): %s", strings.TrimSpace(string(body)))
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("qbittorrent login: HTTP %d", resp.StatusCode)
	}
	if strings.TrimSpace(string(body)) == "Fails." {
		return ErrLogin
	}
	c.loggedIn = true
	return nil
}

// headers sets the Referer/Origin qBittorrent's CSRF check wants.
func (c *Client) headers(req *http.Request) {
	req.Header.Set("Referer", c.base)
	req.Header.Set("Origin", c.base)
}

// do sends a request built by mk. It logs in first when credentials are set and no
// session exists, and retries once after re-login on HTTP 403.
func (c *Client) do(ctx context.Context, mk func() (*http.Request, error)) ([]byte, error) {
	c.mu.Lock()
	if !c.loggedIn && c.user != "" {
		if err := c.loginLocked(ctx); err != nil {
			c.mu.Unlock()
			return nil, err
		}
	}
	c.mu.Unlock()
	for attempt := 0; ; attempt++ {
		req, err := mk()
		if err != nil {
			return nil, err
		}
		c.headers(req)
		resp, err := c.hc.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusForbidden && attempt == 0 {
			c.mu.Lock()
			c.loggedIn = false
			err := c.loginLocked(ctx)
			c.mu.Unlock()
			if err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return body, fmt.Errorf("qbittorrent %s %s: HTTP %d %s", req.Method, req.URL.Path, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return body, nil
	}
}

func (c *Client) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	return c.do(ctx, func() (*http.Request, error) {
		u := c.base + path
		if len(q) > 0 {
			u += "?" + q.Encode()
		}
		return http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	})
}

func (c *Client) postForm(ctx context.Context, path string, form url.Values) ([]byte, error) {
	return c.do(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, strings.NewReader(form.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		return req, err
	})
}

// Version returns the qBittorrent application version (for the Settings "Test" button).
func (c *Client) Version(ctx context.Context) (string, error) {
	b, err := c.get(ctx, "/api/v2/app/version", nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// EnsureCategory creates the category if it does not exist (409 = exists, ignored).
func (c *Client) EnsureCategory(ctx context.Context, category string) error {
	if category == "" {
		return nil
	}
	_, err := c.postForm(ctx, "/api/v2/torrents/createCategory", url.Values{"category": {category}})
	if err != nil && strings.Contains(err.Error(), "HTTP 409") {
		return nil
	}
	return err
}

// Add adds a magnet with the given save path and category and returns its
// info hash (lowercase hex, parsed from the magnet). savePath "" lets qBittorrent
// use its default. Automatic torrent management is turned off so savepath is honoured.
func (c *Client) Add(ctx context.Context, magnet, savePath, category string) (string, error) {
	hash, err := HashFromMagnet(magnet)
	if err != nil {
		return "", err
	}
	if err := c.EnsureCategory(ctx, category); err != nil {
		return "", fmt.Errorf("create category: %w", err)
	}
	fields := [][2]string{{"urls", magnet}, {"category", category}, {"autoTMM", "false"}}
	if savePath != "" {
		fields = append(fields, [2]string{"savepath", savePath})
	}
	body, err := c.do(ctx, func() (*http.Request, error) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		for _, f := range fields {
			if err := mw.WriteField(f[0], f[1]); err != nil {
				return nil, err
			}
		}
		if err := mw.Close(); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/v2/torrents/add", &buf)
		if err == nil {
			req.Header.Set("Content-Type", mw.FormDataContentType())
		}
		return req, err
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(body)) == "Fails." {
		return "", errors.New("qbittorrent rejected the magnet")
	}
	return hash, nil
}

// Info lists torrents in a category ("" = all).
func (c *Client) Info(ctx context.Context, category string) ([]Torrent, error) {
	q := url.Values{}
	if category != "" {
		q.Set("category", category)
	}
	return c.info(ctx, q)
}

func (c *Client) info(ctx context.Context, q url.Values) ([]Torrent, error) {
	b, err := c.get(ctx, "/api/v2/torrents/info", q)
	if err != nil {
		return nil, err
	}
	var out []Torrent
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("torrents/info: %w", err)
	}
	return out, nil
}

// Status returns progress (0..1), state and content path of one torrent.
// TODO(spec): SPEC lists torrents/properties, but that endpoint has no progress or
// state; torrents/info?hashes= returns all three, so Status uses it.
func (c *Client) Status(ctx context.Context, hash string) (progress float64, state, contentPath string, err error) {
	ts, err := c.info(ctx, url.Values{"hashes": {strings.ToLower(hash)}})
	if err != nil {
		return 0, "", "", err
	}
	for _, t := range ts {
		if strings.EqualFold(t.Hash, hash) {
			return t.Progress, t.State, t.ContentPath, nil
		}
	}
	return 0, "", "", ErrNotFound
}

// FailedState reports whether a qBittorrent state means the download cannot finish on its own.
func FailedState(state string) bool {
	return state == "error" || state == "missingFiles"
}

// HashFromMagnet extracts the BitTorrent v1 info hash from a magnet link as
// lowercase 40-char hex. 32-char base32 hashes are converted.
func HashFromMagnet(magnet string) (string, error) {
	u, err := url.Parse(magnet)
	if err != nil || u.Scheme != "magnet" {
		return "", fmt.Errorf("not a magnet link")
	}
	for _, xt := range u.Query()["xt"] {
		const p = "urn:btih:"
		if len(xt) < len(p) || !strings.EqualFold(xt[:len(p)], p) {
			continue
		}
		return NormalizeHash(xt[len(p):])
	}
	return "", fmt.Errorf("magnet has no urn:btih hash")
}

// NormalizeHash turns a 40-hex or 32-base32 info hash into lowercase hex.
func NormalizeHash(h string) (string, error) {
	switch len(h) {
	case 40:
		if _, err := hex.DecodeString(h); err != nil {
			return "", fmt.Errorf("bad hex info hash")
		}
		return strings.ToLower(h), nil
	case 32:
		b, err := base32.StdEncoding.DecodeString(strings.ToUpper(h))
		if err != nil {
			return "", fmt.Errorf("bad base32 info hash")
		}
		return hex.EncodeToString(b), nil
	}
	return "", fmt.Errorf("info hash has length %d, want 40 or 32", len(h))
}
