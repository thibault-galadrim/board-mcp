// Package server exposes the board over MCP (streamable HTTP) on loopback,
// plus the owner's approval routes.
package server

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"board/internal/board"
	"board/internal/identity"
	"board/internal/touchid"
)

//go:embed instructions.md
var instructions string

// identityHeader carries the OS-verified caller to the handlers. It is
// always overwritten, so a client cannot supply its own.
const identityHeader = "X-Board-Identity"

// Approver obtains the owner's live consent. It is the only authority in
// the board: nothing stored grants a right.
type Approver interface {
	Confirm(ctx context.Context, reason string) (touchid.Decision, error)
	Proof() string
}

// PromptPolicy decides which fresh requests show the Touch ID prompt at
// once, while the session's call waits up to ApprovalWait. The others wait
// in the queue: the owner is notified and decides from the page or the CLI.
type PromptPolicy string

const (
	// PromptOwner prompts only for what the owner triggered: their terminal,
	// or a session passing owner_asked for an action they just dictated.
	PromptOwner  PromptPolicy = "owner"
	PromptAlways PromptPolicy = "always"
	PromptNever  PromptPolicy = "never"
)

type Options struct {
	Prompt       PromptPolicy
	ApprovalWait time.Duration
	// UnlockAtStart keeps the board closed until the owner confirms the
	// active settings at startup.
	UnlockAtStart bool
	UnlockWait    time.Duration
	Notify        NotifyOptions
}

func DefaultOptions() Options {
	return Options{
		Prompt: PromptOwner, ApprovalWait: 45 * time.Second, UnlockAtStart: true, UnlockWait: 2 * time.Minute,
		Notify: NotifyOptions{Blocked: true, ToValidate: true, Done: true, Inbox: true, PendingRequest: true, Deadline: true, DeadlineHour: 9},
	}
}

type Server struct {
	svc      *board.Service
	resolver identity.Resolver
	approver Approver
	notifier Notifier
	token    string
	opts     atomic.Pointer[Options]
	log      *slog.Logger
	mcp      *mcp.Server
	locked   atomic.Bool
	hub      hub
	pages    broadcaster
	web      webAuth
	wake     chan struct{}
	clock    func() time.Time
}

func New(svc *board.Service, resolver identity.Resolver, approver Approver, notifier Notifier, token string, opts Options, log *slog.Logger) *Server {
	s := &Server{
		svc: svc, resolver: resolver, approver: approver, notifier: notifier, token: token, log: log,
		hub:   hub{waiters: map[int64][]chan struct{}{}},
		pages: broadcaster{subs: map[chan struct{}]struct{}{}},
		web:   webAuth{links: map[string]time.Time{}, sessions: map[string]webSession{}},
		wake:  make(chan struct{}, 1),
		clock: time.Now,
	}
	s.opts.Store(&opts)
	s.locked.Store(opts.UnlockAtStart)
	s.mcp = mcp.NewServer(&mcp.Implementation{Name: "board", Version: "0.2.0"}, &mcp.ServerOptions{Instructions: instructions})
	s.registerTools()
	return s
}

func (s *Server) options() Options { return *s.opts.Load() }

// SetOptions swaps the options while the board runs. The caller is
// responsible for the owner's consent.
func (s *Server) SetOptions(o Options) {
	s.opts.Store(&o)
	s.changed()
}

// ConfirmSettings asks the owner to approve a settings change.
func (s *Server) ConfirmSettings(ctx context.Context, reason string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, s.options().UnlockWait)
	defer cancel()
	d, err := s.approver.Confirm(ctx, clip(reason, 400))
	return d == touchid.Approved, err
}

func (s *Server) Notify(title, subtitle, body string) { s.notifier.Notify(title, subtitle, body) }

// HTTPServer serves the board on ln, which must be a loopback listener.
func (s *Server) HTTPServer(ln net.Listener) *http.Server {
	return &http.Server{
		Handler:           s.Handler(ln.Addr().String()),
		ConnContext:       withConn,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

func (s *Server) Handler(addr string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.mcp }, &mcp.StreamableHTTPOptions{
		SessionTimeout: time.Hour,
	}))
	s.registerAPI(mux)
	s.registerWeb(mux)
	var h http.Handler = mux
	h = s.identify(h)
	h = s.authenticate(h)
	h = http.NewCrossOriginProtection().Handler(h)
	return requireHost(addr, h)
}

// Unlock asks the owner to confirm the active settings. Until then every
// call is refused.
func (s *Server) Unlock(ctx context.Context) (bool, error) {
	if !s.locked.Load() {
		return true, nil
	}
	ctx, cancel := context.WithTimeout(ctx, s.options().UnlockWait)
	defer cancel()
	d, err := s.approver.Confirm(ctx, s.unlockReason())
	if err != nil {
		return false, err
	}
	if d != touchid.Approved {
		return false, nil
	}
	s.locked.Store(false)
	s.log.Info("board unlocked", "proof", s.approver.Proof())
	s.changed()
	return true, nil
}

func (s *Server) unlockReason() string {
	settings := s.svc.Settings()
	overrides := []string{}
	for project, mode := range settings.ProjectModes {
		overrides = append(overrides, project+" : "+mode)
	}
	slices.Sort(overrides)
	reason := "déverrouiller le board (mode par défaut : " + settings.DefaultMode
	if len(overrides) > 0 {
		reason += " ; " + strings.Join(overrides, ", ")
	}
	return reason + ")"
}

func (s *Server) lockedError() error {
	return fmt.Errorf("the board is locked: ask %s to run `board unlock` (Touch ID)", s.svc.Settings().OwnerName)
}

// confirmRequest shows the owner one request and applies the answer.
// decided is false when the owner did not answer in time. A cancelled
// prompt refuses the request only when cancelRefuses is set: cancelling a
// prompt one asked for oneself just means "not now".
func (s *Server) confirmRequest(ctx context.Context, r board.Request, cancelRefuses bool) (out board.Outcome, decided bool) {
	pctx, cancel := context.WithTimeout(ctx, s.options().ApprovalWait)
	defer cancel()
	d, err := s.approver.Confirm(pctx, reasonFor(r))
	if err != nil {
		s.log.Warn("owner authentication failed", "request", r.ID, "err", err)
		return board.Outcome{}, false
	}
	bg := context.WithoutCancel(ctx)
	switch {
	case d == touchid.Approved:
		out, err = s.svc.Approve(bg, r.ID, s.approver.Proof())
	case d == touchid.Refused && cancelRefuses:
		out, err = s.svc.Refuse(bg, r.ID, "refusé à l'invite", "")
	default:
		return board.Outcome{}, false
	}
	s.hub.notify(r.ID)
	s.changed()
	if err != nil {
		return board.Outcome{Status: board.StatusFailed, Message: err.Error()}, true
	}
	s.log.Info("request decided", "request", r.ID, "status", out.Status)
	return out, true
}

func reasonFor(r board.Request) string {
	who := r.Actor
	if who == board.TerminalName {
		who = "ton terminal"
	}
	return fmt.Sprintf("valider la demande #%d de %s : %s", r.ID, who, r.Summary)
}

func (s *Server) shouldPrompt(c board.Caller, ownerAsked bool) bool {
	switch s.options().Prompt {
	case PromptAlways:
		return true
	case PromptOwner:
		return c.Terminal || ownerAsked
	}
	return false
}

// settle gives the owner the chance to answer a fresh request while the
// session's call waits, or queues it with a notification.
func (s *Server) settle(ctx context.Context, out board.Outcome, prompt bool) board.Outcome {
	if out.Status != board.StatusPendingApproval || out.Request == nil {
		return out
	}
	if prompt {
		if decided, ok := s.confirmRequest(ctx, *out.Request, true); ok {
			return decided
		}
	}
	s.notifyPending(*out.Request)
	out.Message = fmt.Sprintf("%s has not decided yet. Request #%d waits for them (they are notified): run `board wait %d` in the background, it exits once they decide. Do not retry.",
		s.svc.Settings().OwnerName, out.Request.ID, out.Request.ID)
	return out
}

// requireHost rejects any Host other than the loopback address we listen
// on, which defeats DNS rebinding for every route, not only /mcp.
func requireHost(addr string, next http.Handler) http.Handler {
	_, port, _ := net.SplitHostPort(addr)
	allowed := map[string]bool{addr: true, "127.0.0.1:" + port: true, "localhost:" + port: true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.Host] {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type connKey struct{}

type connInfo struct {
	local, remote net.Addr
	once          sync.Once
	id            identity.Identity
}

func withConn(ctx context.Context, c net.Conn) context.Context {
	return context.WithValue(ctx, connKey{}, &connInfo{local: c.LocalAddr(), remote: c.RemoteAddr()})
}

// identify resolves the caller once per TCP connection.
func (s *Server) identify(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := identity.Identity{Key: identity.Terminal}
		if info, ok := r.Context().Value(connKey{}).(*connInfo); ok {
			info.once.Do(func() { info.id = s.resolver.Resolve(info.local, info.remote) })
			id = info.id
		}
		r.Header.Set(identityHeader, id.Key)
		next.ServeHTTP(w, r)
	})
}

// hub wakes the callers waiting on a request's decision.
type hub struct {
	mu      sync.Mutex
	waiters map[int64][]chan struct{}
}

func (h *hub) subscribe(id int64) chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan struct{})
	h.waiters[id] = append(h.waiters[id], ch)
	return ch
}

func (h *hub) unsubscribe(id int64, ch chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.waiters[id] = slices.DeleteFunc(h.waiters[id], func(c chan struct{}) bool { return c == ch })
	if len(h.waiters[id]) == 0 {
		delete(h.waiters, id)
	}
}

func (h *hub) notify(id int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.waiters[id] {
		close(ch)
	}
	delete(h.waiters, id)
}
