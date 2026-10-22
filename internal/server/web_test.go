package server

import (
	"bufio"
	"context"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"board/internal/board"
	"board/internal/identity"
)

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func autoBoard(s *board.Settings) { s.DefaultMode = "auto" }

// browser is a client without the bearer token, as a web page would be.
func (e *env) browser(t *testing.T) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func TestPageSignInFlow(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)

	res, _ := b.Get(e.base + "/")
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized || !strings.Contains(res.Header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("got %d, csp %q", res.StatusCode, res.Header.Get("Content-Security-Policy"))
	}
	if status, _ := e.post(t, "claude:A", "/api/login-link"); status != http.StatusForbidden {
		t.Fatalf("a Claude session cannot mint a login link, got %d", status)
	}
	status, body := e.post(t, identity.Terminal, "/api/login-link")
	link, _ := body["url"].(string)
	if status != http.StatusOK || !strings.Contains(link, "/login?t=") {
		t.Fatalf("got %d %v", status, body)
	}

	res, err := b.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || res.Request.URL.Path != "/" {
		t.Fatalf("the link must sign in and land on the board, got %d %s", res.StatusCode, res.Request.URL)
	}
	var cookie *http.Cookie
	for _, c := range res.Request.Response.Cookies() {
		if c.Name == sessionCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("got cookie %+v", cookie)
	}
	if again, _ := http.Get(link); again.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a login link works once, got %d", again.StatusCode)
	}

	res, _ = b.Get(e.base + "/api/board")
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("the session reads the board, got %d", res.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodPost, e.base+"/api/requests/1/refuse", nil)
	res, _ = b.Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("a POST without the CSRF token must fail, got %d", res.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodPost, e.base+"/mcp", strings.NewReader("{}"))
	res, _ = b.Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the page cookie never opens /mcp, got %d", res.StatusCode)
	}
}

func TestPageIsPushedEveryChange(t *testing.T) {
	e := newEnv(t)
	req, _ := http.NewRequest(http.MethodGet, e.base+"/api/events", nil)
	res, err := e.httpClient(identity.Terminal, nil).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()

	cs := e.client(t, "claude:A", nil)
	call[board.Outcome](t, cs, "board_add", map[string]any{"as": "a1", "project": "atlas", "title": "x"})
	deadline := time.After(3 * time.Second)
	for {
		select {
		case l := <-lines:
			if l == "event: changed" {
				return
			}
		case <-deadline:
			t.Fatal("no change event reached the page")
		}
	}
}

func TestNotificationsFollowTheJournal(t *testing.T) {
	opts := quiet
	opts.Notify = DefaultOptions().Notify
	e := newEnvWith(t, &fakeApprover{}, opts, autoBoard)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.srv.Run(ctx)
	time.Sleep(50 * time.Millisecond)

	worker := e.client(t, "claude:A", nil)
	found, _ := call[board.Outcome](t, worker, "board_add", map[string]any{"as": "worker-1", "project": "atlas", "title": "fuite mémoire"})
	eventually(t, "the finding notification", func() bool {
		return contains(e.notes.all(), "Trouvaille (atlas) | #"+itoa(found.Ticket.ID)+" fuite mémoire | déposée par worker-1")
	})

	todo, _ := call[board.Outcome](t, worker, "board_add", map[string]any{"as": "worker-1", "project": "atlas", "title": "migrer", "todo": true})
	if todo.Status == board.StatusPendingApproval {
		if _, err := e.svc.Approve(ctx, todo.Request.ID, "test"); err != nil {
			t.Fatal(err)
		}
	}
	call[board.Outcome](t, worker, "board_claim", map[string]any{"as": "worker-1", "id": todo.Ticket.ID})
	call[board.Outcome](t, worker, "board_note", map[string]any{"as": "worker-1", "id": todo.Ticket.ID, "state": "blocked", "text": "quelle base ?"})
	eventually(t, "the blocked notification", func() bool {
		return contains(e.notes.all(), "Bloqué (atlas) | #"+itoa(todo.Ticket.ID)+" migrer | worker-1 : quelle base ?")
	})
	call[board.Outcome](t, worker, "board_note", map[string]any{"as": "worker-1", "id": todo.Ticket.ID, "state": "in_progress"})
	call[board.Outcome](t, worker, "board_note", map[string]any{"as": "worker-1", "id": todo.Ticket.ID, "state": "to_validate", "text": "PR prête"})
	eventually(t, "the to-validate notification", func() bool {
		return contains(e.notes.all(), "À valider (atlas) | #"+itoa(todo.Ticket.ID)+" migrer | worker-1 : PR prête")
	})

	owner := e.client(t, identity.Terminal, nil)
	call[board.Outcome](t, owner, "board_add", map[string]any{"as": "cli", "project": "atlas", "title": "de mon terminal"})
	time.Sleep(150 * time.Millisecond)
	for _, n := range e.notes.all() {
		if strings.Contains(n, "de mon terminal") {
			t.Fatalf("the owner's own additions are not notified: %q", n)
		}
	}
}

func TestNotificationBurstIsCapped(t *testing.T) {
	opts := quiet
	opts.Notify = DefaultOptions().Notify
	e := newEnvWith(t, &fakeApprover{}, opts)
	ctx := context.Background()
	start, _ := e.svc.LastEventID(ctx)
	worker := board.Caller{Identity: "claude:A", Name: "worker-1"}
	for i := range 5 {
		if _, err := e.svc.Add(ctx, worker, board.AddInput{Project: "atlas", Title: "trouvaille " + itoa(int64(i))}); err != nil {
			t.Fatal(err)
		}
	}
	e.srv.scan(ctx, start)
	sent := e.notes.all()
	if len(sent) != 4 || !strings.HasPrefix(sent[3], "Board | et 2 autre(s)") {
		t.Fatalf("want three notifications and a summary, got %q", sent)
	}
}

func TestUnansweredRequestIsNotified(t *testing.T) {
	opts := quiet
	opts.Notify = DefaultOptions().Notify
	e := newEnvWith(t, &fakeApprover{}, opts)
	cs := e.client(t, "claude:A", nil)
	out, _ := call[board.Outcome](t, cs, "board_add", map[string]any{"as": "coord-atlas", "project": "atlas", "title": "x", "todo": true})
	want := "Demande en attente | #" + itoa(out.Request.ID) + " de coord-atlas"
	if sent := e.notes.all(); len(sent) != 1 || !strings.HasPrefix(sent[0], want) || !strings.HasSuffix(sent[0], "board ok "+itoa(out.Request.ID)) {
		t.Fatalf("got %q", sent)
	}
}

func TestDeadlinesNotifyOncePerDayFromNine(t *testing.T) {
	opts := quiet
	opts.Notify = DefaultOptions().Notify
	e := newEnvWith(t, &fakeApprover{}, opts)
	ctx := context.Background()
	owner := board.Caller{Identity: identity.Terminal, Name: board.TerminalName, Terminal: true}
	if _, err := e.svc.Add(ctx, owner, board.AddInput{Kind: board.KindReminder, Title: "appeler le comptable", Due: "2026-10-23"}); err != nil {
		t.Fatal(err)
	}

	e.srv.clock = func() time.Time { return time.Date(2026, 10, 22, 8, 30, 0, 0, time.Local) }
	e.srv.checkDeadlines(ctx)
	if len(e.notes.all()) != 0 {
		t.Fatal("nothing before nine")
	}
	e.srv.clock = func() time.Time { return time.Date(2026, 10, 22, 9, 5, 0, 0, time.Local) }
	e.srv.checkDeadlines(ctx)
	e.srv.checkDeadlines(ctx)
	if sent := e.notes.all(); len(sent) != 1 || !strings.HasPrefix(sent[0], "Échéance demain | #1 appeler le comptable | rappel pour toi") {
		t.Fatalf("got %q", sent)
	}
	e.srv.clock = func() time.Time { return time.Date(2026, 10, 23, 9, 0, 0, 0, time.Local) }
	e.srv.checkDeadlines(ctx)
	if sent := e.notes.all(); len(sent) != 2 || !strings.HasPrefix(sent[1], "Échéance aujourd'hui") {
		t.Fatalf("got %q", sent)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
