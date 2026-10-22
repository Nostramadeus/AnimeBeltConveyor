// Package mal is a minimal MyAnimeList API v2 client: OAuth2 with PKCE
// (plain challenge, as MAL requires), list import and progress push
// (SPEC 4, MyAnimeList).
package mal

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Default endpoints.
const (
	DefaultBaseURL  = "https://api.myanimelist.net/v2"
	AuthorizeURL    = "https://myanimelist.net/v1/oauth2/authorize"
	DefaultTokenURL = "https://myanimelist.net/v1/oauth2/token"
)

// pageLimit caps list paging (1000 entries per page).
const pageLimit = 50

// Token is an OAuth token. ExpiresIn is the lifetime in seconds as sent;
// ExpiresAt = exchange time + ExpiresIn, unix seconds (accounts.expires_at).
type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	ExpiresAt    int64  `json:"expires_at"`
}

// ListEntry is one anime on the user's MAL list. Status is MAL's
// (watching, completed, on_hold, dropped, plan_to_watch); see LocalStatus.
// Score is 0-10, 0 = unscored.
type ListEntry struct {
	MalID    int    `json:"mal_id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Progress int    `json:"progress"`
	Score    int    `json:"score"`
}

// User is the account behind a token.
type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Error is an HTTP error from MAL, with MAL's error code when given.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	s := fmt.Sprintf("mal: HTTP %d", e.Status)
	if e.Code != "" {
		s += ": " + e.Code
	}
	if e.Message != "" {
		s += ": " + e.Message
	}
	return s
}

// Client talks to MAL. Create it with New.
type Client struct {
	BaseURL  string
	TokenURL string
	HTTP     *http.Client
	now      func() time.Time
}

// New returns a client for the real MAL endpoints.
func New() *Client {
	return &Client{
		BaseURL:  DefaultBaseURL,
		TokenURL: DefaultTokenURL,
		HTTP:     &http.Client{Timeout: 30 * time.Second},
		now:      time.Now,
	}
}

// NewVerifier returns a random PKCE code verifier: 86 characters from the
// unreserved set [A-Za-z0-9-_] (RFC 7636 allows 43-128). A second call is a
// good OAuth `state` value too.
func NewVerifier() string {
	b := make([]byte, 64)
	rand.Read(b) // never fails since Go 1.24
	return base64.RawURLEncoding.EncodeToString(b)
}

// Challenge is the PKCE code challenge for method "plain": the verifier
// itself. MAL supports only plain.
func Challenge(verifier string) string { return verifier }

// AuthURL is the page the user opens to authorize the app.
func AuthURL(clientID, redirect, verifier, state string) string {
	v := url.Values{}
	v.Set("response_type", "code")
	v.Set("client_id", clientID)
	v.Set("code_challenge", Challenge(verifier))
	v.Set("code_challenge_method", "plain")
	if redirect != "" {
		v.Set("redirect_uri", redirect)
	}
	if state != "" {
		v.Set("state", state)
	}
	return AuthorizeURL + "?" + v.Encode()
}

// Exchange trades the callback code for a token. clientSecret may be empty
// (MAL app type "other" has none).
func (c *Client) Exchange(ctx context.Context, clientID, clientSecret, redirect, code, verifier string) (Token, error) {
	f := url.Values{}
	f.Set("client_id", clientID)
	if clientSecret != "" {
		f.Set("client_secret", clientSecret)
	}
	f.Set("grant_type", "authorization_code")
	f.Set("code", code)
	f.Set("code_verifier", verifier)
	if redirect != "" {
		f.Set("redirect_uri", redirect)
	}
	return c.token(ctx, f)
}

// Refresh gets a new token from a refresh token.
func (c *Client) Refresh(ctx context.Context, clientID, clientSecret, refreshToken string) (Token, error) {
	f := url.Values{}
	f.Set("client_id", clientID)
	if clientSecret != "" {
		f.Set("client_secret", clientSecret)
	}
	f.Set("grant_type", "refresh_token")
	f.Set("refresh_token", refreshToken)
	return c.token(ctx, f)
}

func (c *Client) token(ctx context.Context, form url.Values) (Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var t struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := c.do(req, &t); err != nil {
		return Token{}, err
	}
	if t.AccessToken == "" {
		return Token{}, &Error{Status: http.StatusOK, Message: "no access_token in response"}
	}
	tok := Token{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, TokenType: t.TokenType, ExpiresIn: t.ExpiresIn}
	if t.ExpiresIn > 0 {
		tok.ExpiresAt = c.now().Unix() + t.ExpiresIn
	}
	return tok, nil
}

// Me returns the user a token belongs to.
func (c *Client) Me(ctx context.Context, token string) (User, error) {
	req, err := c.apiRequest(ctx, http.MethodGet, c.BaseURL+"/users/@me", token, nil)
	if err != nil {
		return User{}, err
	}
	var u User
	err = c.do(req, &u)
	return u, err
}

// UserList returns the token owner's whole anime list, following
// paging.next (1000 per page). Paging links to another host are refused so
// the token is never sent elsewhere.
func (c *Client) UserList(ctx context.Context, token string) ([]ListEntry, error) {
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, err
	}
	next := c.BaseURL + "/users/@me/animelist?fields=list_status&limit=1000&nsfw=true"
	var res []ListEntry
	for page := 0; next != "" && page < pageLimit; page++ {
		u, err := url.Parse(next)
		if err != nil || u.Scheme != base.Scheme || u.Host != base.Host {
			return nil, fmt.Errorf("mal: refusing paging link %q", next)
		}
		req, err := c.apiRequest(ctx, http.MethodGet, next, token, nil)
		if err != nil {
			return nil, err
		}
		var body struct {
			Data []struct {
				Node struct {
					ID    int    `json:"id"`
					Title string `json:"title"`
				} `json:"node"`
				ListStatus struct {
					Status             string `json:"status"`
					Score              int    `json:"score"`
					NumEpisodesWatched int    `json:"num_episodes_watched"`
				} `json:"list_status"`
			} `json:"data"`
			Paging struct {
				Next string `json:"next"`
			} `json:"paging"`
		}
		if err := c.do(req, &body); err != nil {
			return nil, err
		}
		for _, d := range body.Data {
			res = append(res, ListEntry{
				MalID:    d.Node.ID,
				Title:    d.Node.Title,
				Status:   d.ListStatus.Status,
				Progress: d.ListStatus.NumEpisodesWatched,
				Score:    d.ListStatus.Score,
			})
		}
		next = body.Paging.Next
	}
	return res, nil
}

// PushProgress sets num_watched_episodes for malID on the token owner's
// list (PATCH /anime/{id}/my_list_status).
func (c *Client) PushProgress(ctx context.Context, token string, malID, watched int) error {
	if token == "" {
		return fmt.Errorf("mal: PushProgress needs a token")
	}
	f := url.Values{}
	f.Set("num_watched_episodes", strconv.Itoa(watched))
	req, err := c.apiRequest(ctx, http.MethodPatch, c.BaseURL+"/anime/"+strconv.Itoa(malID)+"/my_list_status", token, strings.NewReader(f.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(req, nil)
}

// LocalStatus maps a MAL list status to shows.list_status.
func LocalStatus(s string) string {
	switch s {
	case "watching":
		return "watching"
	case "completed":
		return "completed"
	case "on_hold":
		return "paused"
	case "dropped":
		return "dropped"
	}
	return "planning" // plan_to_watch and anything unknown
}

func (c *Client) apiRequest(ctx context.Context, method, u, token string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// do sends req and decodes a JSON body into out (nil = discard).
func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
			Hint    string `json:"hint"`
		}
		_ = json.Unmarshal(data, &e)
		msg := e.Message
		if msg == "" {
			msg = e.Hint
		}
		return &Error{Status: resp.StatusCode, Code: e.Error, Message: msg}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("mal: bad JSON: %w", err)
	}
	return nil
}
