package api

import (
	"context"
	"crypto/rand"
	"errors"
	"html"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/sched"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// redirectURI is the OAuth callback for service, on 127.0.0.1 and the port this
// request came in on (it must match what the user registered with the provider).
func redirectURI(r *http.Request, service string) string {
	port := "7700"
	if _, p, err := net.SplitHostPort(r.Host); err == nil && p != "" {
		port = p
	}
	return "http://127.0.0.1:" + port + "/api/auth/" + service + "/callback"
}

const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"

func randomString(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = unreserved[int(b[i])%len(unreserved)]
	}
	return string(b)
}

// authPage answers the browser tab the provider redirected back to.
func authPage(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>ABC</title>` +
		`<body style="font-family:Inter,'Segoe UI',system-ui,sans-serif;background:#0f1117;color:#e8eaf0;padding:48px">` +
		`<p>` + html.EscapeString(msg) + `</p><p><a style="color:#9ecbff" href="/#/settings">Back to ABC settings</a></p></body>`))
}

func (s *Server) anilistStart(w http.ResponseWriter, r *http.Request) {
	c := s.set.Get()
	if c.Push.AniListClientID == "" || c.Push.AniListClientSecret == "" {
		writeErr(w, http.StatusBadRequest, "Paste the AniList client ID and secret in Settings and save first.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": s.brain.AniListAuthURL(c.Push.AniListClientID, redirectURI(r, "anilist"))})
}

func (s *Server) anilistCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		authPage(w, http.StatusBadRequest, "AniList did not send a code: "+r.URL.Query().Get("error"))
		return
	}
	c := s.set.Get()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	tok, err := s.brain.AniListExchange(ctx, c.Push.AniListClientID, c.Push.AniListClientSecret, redirectURI(r, "anilist"), code)
	if err != nil {
		authPage(w, http.StatusBadGateway, "AniList login failed: "+err.Error())
		return
	}
	s.saveAccount(w, r, "anilist", tok, c.Push.AniList, s.brain.AniListViewer)
}

func (s *Server) malStart(w http.ResponseWriter, r *http.Request) {
	c := s.set.Get()
	if c.Push.MALClientID == "" {
		writeErr(w, http.StatusBadRequest, "Paste the MyAnimeList client ID in Settings and save first.")
		return
	}
	verifier, state := randomString(96), randomString(24)
	now := s.now()
	s.authMu.Lock()
	for k, v := range s.malStates {
		if now.After(v.expires) {
			delete(s.malStates, k)
		}
	}
	s.malStates[state] = malState{verifier: verifier, expires: now.Add(15 * time.Minute)}
	s.authMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"url": s.brain.MALAuthURL(c.Push.MALClientID, verifier, state, redirectURI(r, "mal"))})
}

func (s *Server) malCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	code, state := q.Get("code"), q.Get("state")
	s.authMu.Lock()
	st, ok := s.malStates[state]
	delete(s.malStates, state)
	s.authMu.Unlock()
	if code == "" || !ok || s.now().After(st.expires) {
		authPage(w, http.StatusBadRequest, "MyAnimeList login expired or was not started here. Press Connect again.")
		return
	}
	c := s.set.Get()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	tok, err := s.brain.MALExchange(ctx, c.Push.MALClientID, st.verifier, code, redirectURI(r, "mal"))
	if err != nil {
		authPage(w, http.StatusBadGateway, "MyAnimeList login failed: "+err.Error())
		return
	}
	s.saveAccount(w, r, "mal", tok, c.Push.MAL, s.brain.MALViewer)
}

func (s *Server) saveAccount(w http.ResponseWriter, r *http.Request, service string, tok sched.Token, push bool,
	viewer func(context.Context, string) (string, string, error)) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	uid, name, err := viewer(ctx, tok.AccessToken)
	if err != nil {
		authPage(w, http.StatusBadGateway, "Logged in, but reading the account failed: "+err.Error())
		return
	}
	a := store.Account{Service: service, UserID: uid, UserName: name, AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, PushEnabled: push}
	if tok.ExpiresIn > 0 {
		exp := s.now().Unix() + tok.ExpiresIn
		a.ExpiresAt = &exp
	}
	if err := s.st.UpsertAccount(r.Context(), a); err != nil {
		authPage(w, http.StatusInternalServerError, "Could not save the account: "+err.Error())
		return
	}
	label := map[string]string{"anilist": "AniList", "mal": "MyAnimeList"}[service]
	authPage(w, http.StatusOK, label+" connected as "+name+". You can close this tab.")
}

func (s *Server) importAniList(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserName string `json:"user_name"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	body.UserName = strings.TrimSpace(body.UserName)
	if body.UserName == "" {
		writeErr(w, http.StatusBadRequest, "user_name is required")
		return
	}
	added, updated, err := s.sch.ImportAniList(r.Context(), body.UserName)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"added": added, "updated": updated})
}

func (s *Server) importMAL(w http.ResponseWriter, r *http.Request) {
	if _, err := s.st.GetAccount(r.Context(), "mal"); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusUnauthorized, "MyAnimeList is not connected. Connect it in Settings first.")
		return
	}
	added, updated, err := s.sch.ImportMAL(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"added": added, "updated": updated})
}

func (s *Server) listAccounts(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListAccounts(r.Context())
	if err != nil {
		writeStoreErr(w, err, "accounts")
		return
	}
	type acc struct {
		Service     string `json:"service"`
		UserName    string `json:"user_name"`
		PushEnabled bool   `json:"push_enabled"`
	}
	out := make([]acc, 0, len(list))
	for _, a := range list {
		out = append(out, acc{a.Service, a.UserName, a.PushEnabled})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DeleteAccount(r.Context(), r.PathValue("service")); err != nil {
		writeStoreErr(w, err, "account")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
