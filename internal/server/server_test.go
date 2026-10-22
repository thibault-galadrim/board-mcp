package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"board/internal/board"
	"board/internal/identity"
)

// portIdentities stands in for the OS: it attributes each client
// connection to the identity its test client was created with.
type portIdentities struct {
	mu    sync.Mutex
	ports map[int]string
}

func (p *portIdentities) Resolve(_, remote net.Addr) identity.Identity {
	p.mu.Lock()
	defer p.mu.Unlock()
	if key, ok := p.ports[remote.(*net.TCPAddr).Port]; ok {
		return identity.Identity{Key: key}
	}
	return identity.Identity{Key: identity.Terminal}
}

type alive struct{}

func (alive) Alive(string) bool { return true }

type fakeNotifier struct {
	mu   sync.Mutex
	sent []string
}

func (f *fakeNotifier) Notify(title, subtitle, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, title+" | "+subtitle+" | "+body)
}

func (f *fakeNotifier) all() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.sent...)
}

type env struct {
	srv   *Server
	notes *fakeNotifier
	svc   *board.Service
	url   string
	base  string
	token string
	ids   *portIdentities
}

// quiet never prompts and starts unlocked, for tests about the tools alone.
var quiet = Options{ApprovalWait: time.Second, UnlockWait: time.Second}

func newEnv(t *testing.T) *env { return newEnvWith(t, &fakeApprover{}, quiet) }

func newEnvWith(t *testing.T, approver Approver, opts Options, mutate ...func(*board.Settings)) *env {
	t.Helper()
	ctx := context.Background()
	db, err := board.OpenDB(ctx, filepath.Join(t.TempDir(), "board.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	settings := board.DefaultSettings()
	for _, m := range mutate {
		m(&settings)
	}
	svc, err := board.NewService(db, settings, alive{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	owner := board.Caller{Identity: identity.Terminal, Name: board.TerminalName, Terminal: true}
	out, err := svc.CreateProject(ctx, owner, "atlas", "Atlas")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Approve(ctx, out.Request.ID, "touchid"); err != nil {
		t.Fatal(err)
	}

	ids := &portIdentities{ports: map[int]string{}}
	notes := &fakeNotifier{}
	srv := New(svc, ids, approver, notes, "secret-token", opts, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpSrv := srv.HTTPServer(ln)
	go httpSrv.Serve(ln)
	t.Cleanup(func() { httpSrv.Close() })
	base := "http://" + ln.Addr().String()
	return &env{srv: srv, notes: notes, svc: svc, url: base + "/mcp", base: base, token: "secret-token", ids: ids}
}

type headers struct {
	base  http.RoundTripper
	extra map[string]string
}

func (h headers) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range h.extra {
		r.Header.Set(k, v)
	}
	return h.base.RoundTrip(r)
}

// httpClient talks to the server as the given identity; extra headers are
// sent verbatim.
func (e *env) httpClient(identityKey string, extra map[string]string) *http.Client {
	dialer := &net.Dialer{}
	transport := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := dialer.DialContext(ctx, network, addr)
		if err == nil {
			e.ids.mu.Lock()
			e.ids.ports[c.LocalAddr().(*net.TCPAddr).Port] = identityKey
			e.ids.mu.Unlock()
		}
		return c, err
	}}
	all := map[string]string{"Authorization": "Bearer " + e.token}
	for k, v := range extra {
		all[k] = v
	}
	return &http.Client{Transport: headers{base: transport, extra: all}}
}

func (e *env) client(t *testing.T, identityKey string, extra map[string]string) *mcp.ClientSession {
	t.Helper()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(context.Background(),
		&mcp.StreamableClientTransport{Endpoint: e.url, HTTPClient: e.httpClient(identityKey, extra)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func call[T any](t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) (T, *mcp.CallToolResult) {
	t.Helper()
	var out T
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		raw, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
	}
	return out, res
}

func errorText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

func TestRejectsMissingTokenForeignHostAndCrossOrigin(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		name   string
		header map[string]string
		want   int
	}{
		{"no token", nil, http.StatusUnauthorized},
		{"dns rebinding", map[string]string{"Authorization": "Bearer " + e.token, "Host": "evil.example"}, http.StatusForbidden},
		{"cross origin", map[string]string{"Authorization": "Bearer " + e.token, "Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
	}
	for _, c := range cases {
		req, _ := http.NewRequest(http.MethodPost, e.url, strings.NewReader("{}"))
		for k, v := range c.header {
			if k == "Host" {
				req.Host = v
				continue
			}
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != c.want {
			t.Errorf("%s: got %d, want %d", c.name, res.StatusCode, c.want)
		}
	}
}

func TestInstructionsAreDeliveredAndFit(t *testing.T) {
	e := newEnv(t)
	cs := e.client(t, "claude:A", nil)
	got := cs.InitializeResult().Instructions
	if got != instructions || len(got) == 0 || len(got) > 4096 {
		t.Fatalf("instructions: %d chars", len(got))
	}
}

func TestSessionsCoordinateThroughTheBoard(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	coord := e.client(t, "claude:A", nil)
	worker := e.client(t, "claude:B", nil)

	added, _ := call[board.Outcome](t, coord, "board_add", map[string]any{"as": "coord-atlas", "project": "atlas", "title": "migrer le planning", "todo": true, "priority": 1})
	if added.Status != board.StatusPendingApproval || added.Ticket.State != board.StateInbox {
		t.Fatalf("got %+v", added)
	}
	if _, err := e.svc.Approve(ctx, added.Request.ID, "touchid"); err != nil {
		t.Fatal(err)
	}
	call[board.WhatsNew](t, coord, "board_whatsnew", map[string]any{"as": "coord-atlas", "project": "atlas"})

	taken, _ := call[board.Outcome](t, worker, "board_next", map[string]any{"as": "worker-1", "project": "atlas"})
	if taken.Status != board.StatusPendingApproval || taken.Request.Action != board.ActTake {
		t.Fatalf("demande mode: taking waits for the owner, got %+v", taken)
	}
	approved, err := e.svc.Approve(ctx, taken.Request.ID, "touchid")
	if err != nil || approved.Ticket.Owner != "worker-1" {
		t.Fatalf("%v %+v", err, approved)
	}

	news, _ := call[board.WhatsNew](t, coord, "board_whatsnew", map[string]any{"as": "coord-atlas", "project": "atlas"})
	kinds := []string{}
	for _, ev := range news.Events {
		kinds = append(kinds, ev.Actor+":"+ev.Kind)
	}
	if !strings.Contains(strings.Join(kinds, ","), "worker-1:claimed") {
		t.Fatalf("the coordinator must see the worker's claim, got %v", kinds)
	}

	_, res := call[board.Outcome](t, coord, "board_next", map[string]any{"as": "worker-1", "project": "atlas"})
	if !res.IsError || !strings.Contains(errorText(res), "held by another live session") {
		t.Fatalf("a session cannot borrow a live session's name, got %q", errorText(res))
	}
}

func TestForgedIdentityHeaderIsIgnored(t *testing.T) {
	e := newEnv(t)
	cs := e.client(t, identity.Terminal, map[string]string{identityHeader: "claude:A"})
	out, _ := call[board.Outcome](t, cs, "board_add", map[string]any{"as": "coord-atlas", "project": "atlas", "title": "x"})
	if out.Ticket == nil || out.Ticket.CreatedBy != board.TerminalName {
		t.Fatalf("the header must be overwritten by the OS identity, got %+v", out.Ticket)
	}
}

func TestToolErrorsReachTheAgent(t *testing.T) {
	e := newEnv(t)
	cs := e.client(t, "claude:A", nil)
	_, res := call[board.Outcome](t, cs, "board_add", map[string]any{"as": "a1", "project": "nope", "title": "x"})
	if !res.IsError || !strings.Contains(errorText(res), "existing projects: atlas") {
		t.Fatalf("got %q", errorText(res))
	}
}
