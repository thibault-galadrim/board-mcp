// Package server exposes the board over MCP (streamable HTTP) on loopback.
package server

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"board/internal/board"
	"board/internal/identity"
)

//go:embed instructions.md
var instructions string

// identityHeader carries the OS-verified caller to the tool handlers. It is
// always overwritten, so a client cannot supply its own.
const identityHeader = "X-Board-Identity"

type Server struct {
	svc      *board.Service
	resolver identity.Resolver
	token    string
	log      *slog.Logger
	mcp      *mcp.Server
}

func New(svc *board.Service, resolver identity.Resolver, token string, log *slog.Logger) *Server {
	s := &Server{svc: svc, resolver: resolver, token: token, log: log}
	s.mcp = mcp.NewServer(&mcp.Implementation{Name: "board", Version: "0.1.0"}, &mcp.ServerOptions{Instructions: instructions})
	s.registerTools()
	return s
}

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
	var h http.Handler = mux
	h = s.identify(h)
	h = requireToken(s.token, h)
	h = http.NewCrossOriginProtection().Handler(h)
	return requireHost(addr, h)
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

func requireToken(token string, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
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
