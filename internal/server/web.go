package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"board/internal/board"
)

//go:embed web
var webFiles embed.FS

const (
	linkTTL       = time.Minute
	sessionTTL    = 12 * time.Hour
	sessionCookie = "board_session"
	csrfHeader    = "X-Board-CSRF"
	heartbeat     = 30 * time.Second
	pageCSP       = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
)

type webSession struct {
	csrf    string
	expires time.Time
}

// webAuth holds the one-time login links and the browser sessions, in
// memory only: a daemon restart signs the page out.
type webAuth struct {
	mu       sync.Mutex
	links    map[string]time.Time
	sessions map[string]webSession
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (a *webAuth) newLink(now time.Time) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	tok := randomToken()
	a.links[tok] = now.Add(linkTTL)
	return tok
}

func (a *webAuth) consumeLink(tok string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	expires, ok := a.links[tok]
	delete(a.links, tok)
	return ok && now.Before(expires)
}

func (a *webAuth) newSession(now time.Time) (string, webSession) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id, sess := randomToken(), webSession{csrf: randomToken(), expires: now.Add(sessionTTL)}
	a.sessions[id] = sess
	return id, sess
}

func (a *webAuth) session(id string, now time.Time) (webSession, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	sess, ok := a.sessions[id]
	if ok && !now.Before(sess.expires) {
		delete(a.sessions, id)
		return webSession{}, false
	}
	return sess, ok
}

type sessionKey struct{}

// authenticate accepts the bearer token (sessions, CLI) or a browser
// session cookie (the page). A cookie never reaches /mcp, and every
// non-GET request made with one must carry the session's CSRF token.
func (s *Server) authenticate(next http.Handler) http.Handler {
	want := []byte("Bearer " + s.token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			next.ServeHTTP(w, r)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) == 1 {
			next.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie(sessionCookie); err == nil && r.URL.Path != "/mcp" {
			if sess, ok := s.web.session(c.Value, s.clock()); ok {
				if r.Method != http.MethodGet && r.Method != http.MethodHead &&
					subtle.ConstantTimeCompare([]byte(r.Header.Get(csrfHeader)), []byte(sess.csrf)) != 1 {
					http.Error(w, "missing CSRF token", http.StatusForbidden)
					return
				}
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, sess)))
				return
			}
		}
		if r.URL.Path == "/" {
			pageHeaders(w)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><title>Board</title><p>Lance <code>board open</code> dans ton terminal pour ouvrir le board.</p>`)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

func pageHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", pageCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
}

func (s *Server) registerWeb(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/login-link", s.ownerOnly(s.apiLoginLink))
	mux.HandleFunc("GET /login", s.webLogin)
	mux.HandleFunc("GET /{$}", s.static("web/index.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /app.js", s.static("web/app.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /app.css", s.static("web/app.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /api/session", s.apiSession)
	mux.HandleFunc("GET /api/board", s.apiBoard)
	mux.HandleFunc("GET /api/tickets/{id}", s.apiTicket)
	mux.HandleFunc("GET /api/events", s.apiEvents)
}

func (s *Server) apiLoginLink(w http.ResponseWriter, r *http.Request) {
	tok := s.web.newLink(s.clock())
	writeJSON(w, http.StatusOK, map[string]string{"url": "http://" + r.Host + "/login?t=" + tok})
}

func (s *Server) webLogin(w http.ResponseWriter, r *http.Request) {
	if !s.web.consumeLink(r.URL.Query().Get("t"), s.clock()) {
		pageHeaders(w)
		http.Error(w, "lien expiré ou déjà utilisé : relance board open", http.StatusUnauthorized)
		return
	}
	id, _ := s.web.newSession(s.clock())
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: id, Path: "/", MaxAge: int(sessionTTL.Seconds()),
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) static(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := webFiles.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		pageHeaders(w)
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(b)
	}
}

func (s *Server) apiSession(w http.ResponseWriter, r *http.Request) {
	sess, ok := r.Context().Value(sessionKey{}).(webSession)
	if !ok {
		writeError(w, http.StatusBadRequest, "no browser session")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"csrf": sess.csrf, "owner": s.svc.Settings().OwnerName})
}

type boardView struct {
	Locked bool `json:"locked"`
	board.Snapshot
}

func (s *Server) apiBoard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.locked.Load() {
		writeJSON(w, http.StatusOK, boardView{Locked: true})
		return
	}
	snap, err := s.svc.Snapshot(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, boardView{Snapshot: snap})
}

func (s *Server) apiTicket(w http.ResponseWriter, r *http.Request) {
	if s.locked.Load() {
		writeError(w, http.StatusLocked, s.lockedError().Error())
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ticket id")
		return
	}
	d, err := s.svc.Show(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// apiEvents pushes a "changed" event whenever the board moves; the page
// then fetches a fresh snapshot. Nothing polls.
func (s *Server) apiEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	ch := s.pages.subscribe()
	defer s.pages.unsubscribe(ch)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, "retry: 3000\n\n")
	flusher.Flush()
	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			fmt.Fprint(w, "event: changed\ndata: {}\n\n")
		case <-tick.C:
			fmt.Fprint(w, ": ping\n\n")
		}
		flusher.Flush()
	}
}

type broadcaster struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func (b *broadcaster) subscribe() chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan struct{}, 1)
	b.subs[ch] = struct{}{}
	return ch
}

func (b *broadcaster) unsubscribe(ch chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.subs, ch)
}

func (b *broadcaster) publish() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
