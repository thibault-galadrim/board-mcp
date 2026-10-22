package board

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type fakeLiveness map[string]bool

func (f fakeLiveness) Alive(identity string) bool { return f[identity] }

type harness struct {
	svc   *Service
	db    *sql.DB
	path  string
	clock *fakeClock
	alive fakeLiveness
}

func newHarness(t *testing.T, mutate func(*Settings)) *harness {
	t.Helper()
	path := filepath.Join(t.TempDir(), "board.db")
	db, err := OpenDB(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	settings := DefaultSettings()
	settings.DefaultMode = "demande"
	settings.QueueRequests = true
	settings.WritesPerMinute = 10_000
	if mutate != nil {
		mutate(&settings)
	}
	clock := &fakeClock{t: time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)}
	alive := fakeLiveness{}
	svc, err := NewService(db, settings, alive, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{svc: svc, db: db, path: path, clock: clock, alive: alive}
	h.project(t, "atlas")
	h.project(t, "perso")
	return h
}

// project creates a project the way the owner does, from their terminal.
func (h *harness) project(t *testing.T, slug string) {
	t.Helper()
	owner := Caller{Identity: "terminal", Name: TerminalName, Terminal: true, Vouched: true}
	if _, err := h.svc.CreateProject(context.Background(), owner, slug, strings.ToUpper(slug)); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) approve(t *testing.T, out Outcome) Outcome {
	t.Helper()
	if out.Status != StatusPendingApproval {
		t.Fatalf("want a pending request, got %+v", out)
	}
	res, err := h.svc.Approve(context.Background(), out.Request.ID, "touchid")
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (h *harness) caller(t *testing.T, name string) Caller {
	t.Helper()
	identity := "claude:" + name
	h.alive[identity] = true
	c, pending, err := h.svc.Bind(context.Background(), identity, false, false, name)
	if err != nil || pending != nil {
		t.Fatalf("bind %s: %v %+v", name, err, pending)
	}
	return c
}

func (h *harness) add(t *testing.T, c Caller, in AddInput) Ticket {
	t.Helper()
	out, err := h.svc.Add(context.Background(), c, in)
	if err != nil {
		t.Fatal(err)
	}
	return *out.Ticket
}

// todo creates a task already triaged to todo, as the owner does.
func (h *harness) todo(t *testing.T, project, title string, priority int, due string) Ticket {
	t.Helper()
	owner := Caller{Identity: "terminal", Name: TerminalName, Terminal: true, Vouched: true}
	out, err := h.svc.Add(context.Background(), owner, AddInput{Project: project, Title: title, Priority: &priority, Due: due, Todo: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status == StatusPendingApproval {
		out = h.approve(t, out)
	}
	if out.Ticket.State != StateTodo {
		t.Fatalf("want todo, got %s", out.Ticket.State)
	}
	return *out.Ticket
}

func (h *harness) ticket(t *testing.T, id int64) Ticket {
	t.Helper()
	tk, err := h.svc.getTicket(context.Background(), h.db, id)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

var terminal = Caller{Identity: "terminal", Name: TerminalName, Terminal: true}

func autoMode(s *Settings) { s.DefaultMode = "auto" }

func TestDatabaseIsExclusiveWhileOpen(t *testing.T) {
	h := newHarness(t, nil)
	other, err := sql.Open("sqlite", "file:"+h.path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var n int
	err = other.QueryRow("SELECT COUNT(*) FROM tickets").Scan(&n)
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("a second connection must be locked out, got err=%v", err)
	}
	if _, err := other.Exec("INSERT INTO projects (slug, name, created_at, created_by) VALUES ('x', 'x', '', '')"); err == nil {
		t.Fatal("a second connection must not write")
	}
}

func TestAgentFindingLandsInInbox(t *testing.T) {
	h := newHarness(t, nil)
	agent := h.caller(t, "fix-login")
	tk := h.add(t, agent, AddInput{Project: "atlas", Title: "flaky test in planning"})
	if tk.State != StateInbox || tk.CreatedBy != "fix-login" || tk.Priority != 2 {
		t.Fatalf("got %+v", tk)
	}
	circle, _ := circleOf(context.Background(), h.db, tk.ID)
	if len(circle) != 1 || circle[0] != "fix-login" {
		t.Fatalf("creator must be in the circle, got %v", circle)
	}
}

func TestAskingForTodoFilesTriageRequest(t *testing.T) {
	h := newHarness(t, nil)
	agent := h.caller(t, "coord-atlas")
	out, err := h.svc.Add(context.Background(), agent, AddInput{Project: "atlas", Title: "relancer le client", Todo: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Ticket.State != StateInbox || out.Request.Action != ActTriage {
		t.Fatalf("want inbox + triage request, got %+v", out)
	}
	res := h.approve(t, out)
	if res.Ticket.State != StateTodo || res.Request.State != RequestApproved {
		t.Fatalf("got %+v", res)
	}
	d, _ := h.svc.Show(context.Background(), out.Ticket.ID)
	last := d.Events[len(d.Events)-1]
	if last.Kind != "request_approved" || last.Proof != "touchid" {
		t.Fatalf("approval must be journaled with its proof, got %+v", last)
	}
}

func TestToutPermisSendsTasksStraightToTodo(t *testing.T) {
	h := newHarness(t, func(s *Settings) { s.ProjectModes["perso"] = "tout_permis" })
	agent := h.caller(t, "side")
	if tk := h.add(t, agent, AddInput{Project: "perso", Title: "x"}); tk.State != StateTodo {
		t.Fatalf("got %s", tk.State)
	}
	if tk := h.add(t, agent, AddInput{Project: "atlas", Title: "y"}); tk.State != StateInbox {
		t.Fatalf("atlas stays in demande, got %s", tk.State)
	}
}

func TestUnknownProjectListsExistingOnes(t *testing.T) {
	h := newHarness(t, nil)
	_, err := h.svc.Add(context.Background(), h.caller(t, "a1"), AddInput{Project: "zephyr", Title: "x"})
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "atlas, perso") {
		t.Fatalf("got %v", err)
	}
}

func TestReminderIsNeverTaken(t *testing.T) {
	h := newHarness(t, autoMode)
	agent := h.caller(t, "coord-atlas")
	r := h.add(t, agent, AddInput{Kind: KindReminder, Title: "appeler le comptable", Due: "2026-10-23"})
	if r.State != StateTodo || r.Project != "" {
		t.Fatalf("got %+v", r)
	}
	if _, err := h.svc.Claim(context.Background(), agent, r.ID, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v", err)
	}
}

func TestDemandeTakeWaitsForApprovalAndNextSkipsIt(t *testing.T) {
	h := newHarness(t, nil)
	a := h.todo(t, "atlas", "a", 1, "")
	b := h.todo(t, "atlas", "b", 2, "")
	s1, s2 := h.caller(t, "s1"), h.caller(t, "s2")

	out, err := h.svc.Next(context.Background(), s1, "atlas", "")
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != StatusPendingApproval || out.Ticket.ID != a.ID || h.ticket(t, a.ID).State != StateTodo {
		t.Fatalf("got %+v", out)
	}
	out2, err := h.svc.Next(context.Background(), s2, "atlas", "")
	if err != nil {
		t.Fatal(err)
	}
	if out2.Ticket.ID != b.ID {
		t.Fatalf("next must skip a ticket with a pending take, got #%d", out2.Ticket.ID)
	}
	res := h.approve(t, out)
	if res.Ticket.State != StateInProgress || res.Ticket.Owner != "s1" {
		t.Fatalf("got %+v", res.Ticket)
	}
}

func TestConcurrentNextNeverGivesATicketTwice(t *testing.T) {
	h := newHarness(t, autoMode)
	for i := range 10 {
		h.todo(t, "atlas", fmt.Sprintf("t%d", i), i%4, "")
	}
	callers := make([]Caller, 20)
	for i := range callers {
		callers[i] = h.caller(t, fmt.Sprintf("worker-%02d", i))
	}
	var wg sync.WaitGroup
	results := make([]Outcome, len(callers))
	errs := make([]error, len(callers))
	for i, c := range callers {
		wg.Go(func() { results[i], errs[i] = h.svc.Next(context.Background(), c, "atlas", "") })
	}
	wg.Wait()

	owners := map[int64]string{}
	empty := 0
	for i, out := range results {
		if errs[i] != nil {
			t.Fatalf("worker %d: %v", i, errs[i])
		}
		if out.Ticket == nil {
			empty++
			continue
		}
		if prev, dup := owners[out.Ticket.ID]; dup {
			t.Fatalf("ticket #%d given to %s and %s", out.Ticket.ID, prev, out.Ticket.Owner)
		}
		owners[out.Ticket.ID] = out.Ticket.Owner
	}
	if len(owners) != 10 || empty != 10 {
		t.Fatalf("want 10 taken and 10 empty, got %d and %d", len(owners), empty)
	}
}

func TestNextPrefersOverdueThenPriority(t *testing.T) {
	h := newHarness(t, autoMode)
	h.todo(t, "atlas", "p0 later", 0, "2026-12-01")
	overdue := h.todo(t, "atlas", "p3 overdue", 3, "2026-10-01")
	h.todo(t, "atlas", "p1", 1, "")
	out, err := h.svc.Next(context.Background(), h.caller(t, "s1"), "atlas", "")
	if err != nil {
		t.Fatal(err)
	}
	if out.Ticket.ID != overdue.ID || !out.Ticket.Overdue {
		t.Fatalf("want the overdue ticket first, got %+v", out.Ticket)
	}
}

func TestCircleDecidesInAutoOthersAsk(t *testing.T) {
	h := newHarness(t, autoMode)
	tk := h.todo(t, "atlas", "fix", 1, "")
	owner, outsider := h.caller(t, "owner-s"), h.caller(t, "outsider")
	ctx := context.Background()

	if _, err := h.svc.Claim(ctx, owner, tk.ID, ""); err != nil {
		t.Fatal(err)
	}
	if out, err := h.svc.Note(ctx, outsider, tk.ID, "j'ai vu un souci", "", nil); err != nil || out.Status != StatusOK {
		t.Fatalf("anyone may comment: %v %+v", err, out)
	}
	if out, _ := h.svc.Note(ctx, outsider, tk.ID, "", StateDone, nil); out.Status != StatusPendingApproval {
		t.Fatalf("an outsider closing must ask, got %+v", out)
	}
	if out, _ := h.svc.Note(ctx, owner, tk.ID, "prêt", StateToValidate, []string{"https://github.com/x/pr/1"}); out.Status != StatusOK || out.Ticket.State != StateToValidate {
		t.Fatalf("got %+v", out)
	}
	out, _ := h.svc.Note(ctx, owner, tk.ID, "", StateDone, nil)
	if out.Status != StatusOK || out.Ticket.State != StateDone || len(out.Ticket.Links) != 1 {
		t.Fatalf("got %+v", out)
	}
}

func TestDispatchForAnotherSession(t *testing.T) {
	h := newHarness(t, autoMode)
	tk := h.todo(t, "atlas", "split me", 1, "")
	coord := h.caller(t, "coord-atlas")
	ctx := context.Background()

	out, err := h.svc.Claim(ctx, coord, tk.ID, "worker-1")
	if err != nil || out.Ticket.Owner != "worker-1" {
		t.Fatalf("%v %+v", err, out)
	}
	worker := h.caller(t, "worker-1")
	if out, _ := h.svc.Note(ctx, worker, tk.ID, "question", StateBlocked, nil); out.Status != StatusOK {
		t.Fatalf("the worker is in the circle: %+v", out)
	}
	if out, _ := h.svc.Assign(ctx, coord, tk.ID, "worker-2", "worker-1 ne répond plus"); out.Status != StatusOK || out.Ticket.Owner != "worker-2" {
		t.Fatalf("the dispatcher is in the circle: %+v", out)
	}
	sub := h.add(t, coord, AddInput{Project: "atlas", Title: "sub", ParentID: tk.ID})
	if sub.ParentID != tk.ID {
		t.Fatalf("got %+v", sub)
	}
	if _, err := h.svc.Add(ctx, coord, AddInput{Project: "perso", Title: "sub", ParentID: tk.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a sub-ticket stays in its parent's project, got %v", err)
	}
}

func TestTerminalIsNeverInTheCircle(t *testing.T) {
	h := newHarness(t, autoMode)
	tk := h.add(t, terminal, AddInput{Project: "atlas", Title: "from my terminal"})
	if tk.State != StateInbox || tk.CreatedBy != TerminalName {
		t.Fatalf("got %+v", tk)
	}
	out, err := h.svc.Note(context.Background(), terminal, tk.ID, "", StateDropped, nil)
	if err != nil || out.Status != StatusPendingApproval {
		t.Fatalf("a terminal decision must go through the owner, got %v %+v", err, out)
	}
	if _, err := h.svc.Claim(context.Background(), terminal, h.todo(t, "atlas", "x", 1, "").ID, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a terminal takes only for a named session, got %v", err)
	}
}

func TestApprovalFailsIfTicketMovedMeanwhile(t *testing.T) {
	h := newHarness(t, nil)
	tk := h.todo(t, "atlas", "contested", 1, "")
	s1, s2 := h.caller(t, "s1"), h.caller(t, "s2")
	ctx := context.Background()
	r1, _ := h.svc.Claim(ctx, s1, tk.ID, "")
	r2, _ := h.svc.Claim(ctx, s2, tk.ID, "")
	h.approve(t, r1)

	res, err := h.svc.Approve(ctx, r2.Request.ID, "touchid")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFailed || res.Request.State != RequestFailed {
		t.Fatalf("got %+v", res)
	}
	if got := h.ticket(t, tk.ID); got.Owner != "s1" {
		t.Fatalf("the first approval must stand, owner=%s", got.Owner)
	}
	if _, err := h.svc.Approve(ctx, r2.Request.ID, "touchid"); !errors.Is(err, ErrNotPending) {
		t.Fatalf("a decided request cannot be replayed, got %v", err)
	}
}

func TestRepeatedRequestIsDeduplicated(t *testing.T) {
	h := newHarness(t, nil)
	tk := h.todo(t, "atlas", "x", 1, "")
	s1 := h.caller(t, "s1")
	a, _ := h.svc.Claim(context.Background(), s1, tk.ID, "")
	b, _ := h.svc.Claim(context.Background(), s1, tk.ID, "")
	if a.Request.ID != b.Request.ID {
		t.Fatalf("got #%d and #%d", a.Request.ID, b.Request.ID)
	}
}

func TestRefuseLeavesTicketAlone(t *testing.T) {
	h := newHarness(t, nil)
	tk := h.todo(t, "atlas", "x", 1, "")
	out, _ := h.svc.Claim(context.Background(), h.caller(t, "s1"), tk.ID, "")
	res, err := h.svc.Refuse(context.Background(), out.Request.ID, "pas maintenant", "touchid")
	if err != nil || res.Request.State != RequestRefused || h.ticket(t, tk.ID).State != StateTodo {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestNamesFollowTheirProcess(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	h.alive["claude:A"] = true
	h.alive["claude:B"] = true

	if _, p, err := h.svc.Bind(ctx, "claude:A", false, false, "coord-atlas"); err != nil || p != nil {
		t.Fatalf("%v %+v", err, p)
	}
	if _, p, err := h.svc.Bind(ctx, "claude:A", false, false, "worker-x"); err != nil || p != nil {
		t.Fatalf("one process may hold several names (subagents): %v %+v", err, p)
	}
	_, pending, err := h.svc.Bind(ctx, "claude:B", false, false, "coord-atlas")
	if err != nil || pending == nil || pending.Request.Action != ActRebindName {
		t.Fatalf("taking a live session's name must ask, got %v %+v", err, pending)
	}

	h.alive["claude:A"] = false
	if _, p, err := h.svc.Bind(ctx, "claude:C", false, false, "coord-atlas"); err != nil || p != nil {
		t.Fatalf("a dead session's name is free, got %v %+v", err, p)
	}
	for _, bad := range []string{"terminal", "Thibault", "x", "UPPER", "has space"} {
		if _, _, err := h.svc.Bind(ctx, "claude:D", false, false, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q should be refused, got %v", bad, err)
		}
	}
}

func TestWhatsNewMovesCursorAndFlagsSilence(t *testing.T) {
	h := newHarness(t, autoMode)
	ctx := context.Background()
	coord, worker := h.caller(t, "coord-atlas"), h.caller(t, "worker-1")
	tk := h.todo(t, "atlas", "long job", 1, "")
	h.add(t, terminal, AddInput{Kind: KindReminder, Project: "atlas", Title: "recette", Due: "2026-10-23"})
	h.add(t, terminal, AddInput{Kind: KindReminder, Project: "atlas", Title: "plus tard", Due: "2026-11-30"})

	first, err := h.svc.WhatsNew(ctx, coord, "atlas")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Reminders) != 1 || first.Reminders[0].Title != "recette" {
		t.Fatalf("only the reminder due within the lead shows, got %+v", first.Reminders)
	}

	if _, err := h.svc.Claim(ctx, worker, tk.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Note(ctx, coord, tk.ID, "own note", "", nil); err != nil {
		t.Fatal(err)
	}
	second, err := h.svc.WhatsNew(ctx, coord, "atlas")
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Events) != 1 || second.Events[0].Kind != "claimed" || second.Events[0].Actor != "worker-1" {
		t.Fatalf("want only the worker's claim since last look, got %+v", second.Events)
	}
	if third, _ := h.svc.WhatsNew(ctx, coord, "atlas"); len(third.Events) != 0 {
		t.Fatalf("cursor must have moved, got %+v", third.Events)
	}

	h.clock.advance(3 * time.Hour)
	later, _ := h.svc.WhatsNew(ctx, coord, "atlas")
	if len(later.Silent) != 1 || later.Silent[0].ID != tk.ID {
		t.Fatalf("want the ticket flagged silent, got %+v", later.Silent)
	}
	mine, _ := h.svc.WhatsNew(ctx, worker, "")
	if len(mine.MyTickets) != 1 || mine.MyTickets[0].ID != tk.ID {
		t.Fatalf("got %+v", mine.MyTickets)
	}
}

func TestRateLimitPerIdentity(t *testing.T) {
	h := newHarness(t, func(s *Settings) { s.WritesPerMinute = 3 })
	agent := h.caller(t, "chatty")
	for i := range 3 {
		if _, err := h.svc.Add(context.Background(), agent, AddInput{Project: "atlas", Title: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.svc.Add(context.Background(), agent, AddInput{Project: "atlas", Title: "x"}); !errors.Is(err, ErrRateLimit) {
		t.Fatalf("got %v", err)
	}
	h.clock.advance(time.Minute + time.Second)
	if _, err := h.svc.Add(context.Background(), agent, AddInput{Project: "atlas", Title: "y"}); err != nil {
		t.Fatalf("the window must slide, got %v", err)
	}
}

func TestInputLimits(t *testing.T) {
	h := newHarness(t, nil)
	agent := h.caller(t, "a1")
	bad := []AddInput{
		{Project: "atlas", Title: ""},
		{Project: "atlas", Title: strings.Repeat("x", maxTitle+1)},
		{Project: "atlas", Title: "x", Details: strings.Repeat("x", maxText+1)},
		{Project: "atlas", Title: "x", Due: "demain"},
		{Project: "atlas", Title: "x", Priority: ptr(7)},
		{Title: "a task without project"},
	}
	for _, in := range bad {
		if _, err := h.svc.Add(context.Background(), agent, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: want invalid, got %v", in, err)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestTransitions(t *testing.T) {
	cases := []struct {
		kind     Kind
		from, to State
		want     Action
	}{
		{KindTask, StateInbox, StateTodo, ActTriage},
		{KindTask, StateInbox, StateDropped, ActDrop},
		{KindTask, StateInProgress, StateBlocked, ActProgress},
		{KindTask, StateBlocked, StateInProgress, ActProgress},
		{KindTask, StateInProgress, StateToValidate, ActProgress},
		{KindTask, StateToValidate, StateDone, ActClose},
		{KindTask, StateDone, StateTodo, ActTriage},
		{KindReminder, StateTodo, StateDone, ActClose},
		{KindTask, StateTodo, StateInProgress, ""},
		{KindTask, StateInProgress, StateTodo, ""},
		{KindTask, StateTodo, StateDone, ""},
		{KindTask, StateDone, StateDropped, ""},
		{KindReminder, StateTodo, StateInProgress, ""},
	}
	for _, c := range cases {
		got, err := transitionAction(c.kind, c.from, c.to)
		if got != c.want || (c.want == "") != (err != nil) {
			t.Errorf("%s %s→%s: got %q, %v", c.kind, c.from, c.to, got, err)
		}
	}
}

func TestDefaultModeIsAuto(t *testing.T) {
	s := DefaultSettings()
	if s.DefaultMode != "auto" {
		t.Fatalf("arbitrated default is auto, got %s", s.DefaultMode)
	}
	for _, a := range []Action{ActTriage, ActCreateProject, ActRebindName} {
		if s.rule("any", a) != RuleApproval {
			t.Errorf("%s must still need the owner in auto", a)
		}
	}
}

func TestSettingsValidation(t *testing.T) {
	s := DefaultSettings()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	delete(s.Modes["auto"], ActClose)
	if err := s.Validate(); err == nil {
		t.Fatal("a mode missing a rule must be rejected")
	}
	s = DefaultSettings()
	s.ProjectModes["atlas"] = "yolo"
	if err := s.Validate(); err == nil {
		t.Fatal("an undefined mode must be rejected")
	}
}

func TestMigrationFromSchemaOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO meta (key, value) VALUES ('schema_version', '1')"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	upgraded, err := OpenDB(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	var version string
	if err := upgraded.QueryRow("SELECT value FROM meta WHERE key = 'schema_version'").Scan(&version); err != nil || version != fmt.Sprint(schemaVersion) {
		t.Fatalf("version %q, err %v", version, err)
	}
	if _, err := upgraded.Exec("INSERT INTO notified (ticket_id, kind, day) VALUES (1, 'x', 'y')"); err == nil {
		t.Fatal("the foreign key to tickets must hold in the migrated table")
	}
}

func TestDeadlinesAreNotifiedOncePerDay(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	r := h.add(t, terminal, AddInput{Kind: KindReminder, Title: "recette", Due: "2026-10-23"})
	h.add(t, terminal, AddInput{Kind: KindReminder, Title: "plus tard", Due: "2026-11-30"})

	due, err := h.svc.DueOn(ctx, "2026-10-22", "2026-10-23")
	if err != nil || len(due) != 1 || due[0].ID != r.ID {
		t.Fatalf("%v %+v", err, due)
	}
	first, _ := h.svc.MarkNotified(ctx, r.ID, "due_tomorrow", "2026-10-22")
	again, _ := h.svc.MarkNotified(ctx, r.ID, "due_tomorrow", "2026-10-22")
	if !first || again {
		t.Fatalf("first=%v again=%v", first, again)
	}
}

func TestFeedAndSnapshot(t *testing.T) {
	h := newHarness(t, autoMode)
	ctx := context.Background()
	start, _ := h.svc.LastEventID(ctx)
	tk := h.todo(t, "atlas", "x", 1, "")
	worker := h.caller(t, "worker-1")
	h.svc.Claim(ctx, worker, tk.ID, "")
	h.svc.Note(ctx, worker, tk.ID, "fini", StateToValidate, nil)
	h.svc.Note(ctx, worker, tk.ID, "", StateDone, nil)

	feed, err := h.svc.Feed(ctx, start, 100)
	if err != nil {
		t.Fatal(err)
	}
	last := feed[len(feed)-1]
	if last.Event.Kind != "state" || last.Title != "x" || last.Project != "atlas" || last.State != StateDone {
		t.Fatalf("got %+v", last)
	}
	snap, err := h.svc.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Closed) != 1 || len(snap.Projects) != 2 {
		t.Fatalf("got %+v", snap)
	}
	h.clock.advance(4 * 24 * time.Hour)
	if snap, _ := h.svc.Snapshot(ctx); len(snap.Closed) != 0 {
		t.Fatalf("closed tickets leave the page after three days, got %+v", snap.Closed)
	}
}

func TestDeclaringWorkAlreadyStarted(t *testing.T) {
	h := newHarness(t, autoMode)
	ctx := context.Background()
	agent := h.caller(t, "atlas-planning")
	out, err := h.svc.Add(ctx, agent, AddInput{Project: "atlas", Title: "refonte du solveur", Doing: true})
	if err != nil || out.Status != StatusOK {
		t.Fatalf("%v %+v", err, out)
	}
	if tk := out.Ticket; tk.State != StateInProgress || tk.Owner != "atlas-planning" || tk.OwnerActivityAt == "" {
		t.Fatalf("declared work is in progress and owned by its declarer, got %+v", tk)
	}
	if next, _ := h.svc.Next(ctx, h.caller(t, "other"), "atlas", ""); next.Ticket != nil {
		t.Fatalf("no other session can pick declared work, got %+v", next.Ticket)
	}
	if _, err := h.svc.Add(ctx, terminal, AddInput{Project: "atlas", Title: "x", Doing: true}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a terminal does not declare work, got %v", err)
	}
	if _, err := h.svc.Add(ctx, agent, AddInput{Kind: KindReminder, Title: "x", Doing: true}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a reminder is never in progress, got %v", err)
	}

	strict := newHarness(t, nil)
	pending, err := strict.svc.Add(ctx, strict.caller(t, "atlas-planning"), AddInput{Project: "atlas", Title: "y", Doing: true})
	if err != nil || pending.Status != StatusPendingApproval || pending.Request.Action != ActDeclare {
		t.Fatalf("declaring needs the owner in demande, got %v %+v", err, pending)
	}
}

func TestMovingATicketBetweenProjects(t *testing.T) {
	h := newHarness(t, func(s *Settings) { s.DefaultMode = "auto"; s.ProjectModes["perso"] = "demande" })
	h.project(t, "outillage")
	ctx := context.Background()
	author := h.caller(t, "board-design")
	misfiled := h.add(t, author, AddInput{Project: "outillage", Title: "Grafana", Doing: true})
	sub := h.add(t, author, AddInput{Project: "outillage", Title: "sous-tâche", ParentID: misfiled.ID})

	out, err := h.svc.Move(ctx, author, misfiled.ID, "atlas", "mal rangé")
	if err != nil || out.Status != StatusOK || out.Ticket.Project != "atlas" || out.Ticket.State != StateInProgress {
		t.Fatalf("the circle moves its ticket in auto: %v %+v", err, out)
	}
	if got := h.ticket(t, sub.ID); got.Project != "atlas" {
		t.Fatalf("sub-tickets follow their parent, got %s", got.Project)
	}
	d, _ := h.svc.Show(ctx, misfiled.ID)
	if last := d.Events[len(d.Events)-1]; last.Kind != "moved" || last.Body != "mal rangé" {
		t.Fatalf("got %+v", last)
	}

	if out, _ := h.svc.Move(ctx, h.caller(t, "outsider"), misfiled.ID, "outillage", ""); out.Status != StatusPendingApproval {
		t.Fatalf("an outsider asks the owner, got %+v", out)
	}
	if out, _ := h.svc.Move(ctx, author, misfiled.ID, "perso", ""); out.Status != StatusPendingApproval {
		t.Fatalf("moving into a demande project follows the stricter mode, got %+v", out)
	}
	for name, err := range map[string]error{
		"sub-ticket": second(h.svc.Move(ctx, author, sub.ID, "outillage", "")),
		"same":       second(h.svc.Move(ctx, author, misfiled.ID, "atlas", "")),
		"no project": second(h.svc.Move(ctx, author, misfiled.ID, "", "")),
		"unknown":    second(h.svc.Move(ctx, author, misfiled.ID, "nope", "")),
	} {
		if !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: want a refusal, got %v", name, err)
		}
	}
	reminder := h.add(t, author, AddInput{Kind: KindReminder, Project: "atlas", Title: "rappel"})
	if out, err := h.svc.Move(ctx, author, reminder.ID, "", ""); err != nil || out.Ticket.Project != "" {
		t.Fatalf("a reminder can leave every project: %v %+v", err, out)
	}
}

func second[T any](_ T, err error) error { return err }

func TestSendBackAndBackup(t *testing.T) {
	h := newHarness(t, autoMode)
	ctx := context.Background()
	agent := h.caller(t, "worker-1")
	tk := h.add(t, agent, AddInput{Project: "atlas", Title: "x", Doing: true})
	if _, err := h.svc.SendBack(ctx, terminal, tk.ID, "trop tôt"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("only a ticket awaiting validation is sent back, got %v", err)
	}
	h.svc.Note(ctx, agent, tk.ID, "", StateToValidate, nil)
	if _, err := h.svc.SendBack(ctx, terminal, tk.ID, "  "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a send-back needs a reason, got %v", err)
	}
	h.clock.advance(3 * time.Hour)
	out, err := h.svc.SendBack(ctx, terminal, tk.ID, "il manque le test")
	if err != nil || out.Ticket.State != StateInProgress || out.Ticket.Silent {
		t.Fatalf("a sent-back ticket resumes and its silence restarts: %v %+v", err, out.Ticket)
	}

	path := filepath.Join(t.TempDir(), "copy.db")
	if err := h.svc.Backup(ctx, path); err != nil {
		t.Fatal(err)
	}
	copyDB, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	var n int
	if err := copyDB.QueryRow("SELECT COUNT(*) FROM tickets").Scan(&n); err != nil || n != 1 {
		t.Fatalf("the backup is a readable copy: %v n=%d", err, n)
	}
	if err := h.svc.Backup(ctx, path); err == nil {
		t.Fatal("a backup never overwrites an existing file")
	}
}

func TestTheOwnersWordIsEnough(t *testing.T) {
	h := newHarness(t, func(s *Settings) { s.QueueRequests = false })
	ctx := context.Background()
	tk := h.todo(t, "atlas", "x", 1, "")
	agent := h.caller(t, "worker-1")

	_, err := h.svc.Claim(ctx, agent, tk.ID, "")
	if !errors.Is(err, ErrNeedsOwner) || !strings.Contains(err.Error(), "owner_asked=true") {
		t.Fatalf("without the owner's word nothing queues: the session is told to ask, got %v", err)
	}
	if pending, _ := h.svc.PendingRequests(ctx); len(pending) != 0 {
		t.Fatalf("no request piles up, got %+v", pending)
	}
	agent.Vouched = true
	out, err := h.svc.Claim(ctx, agent, tk.ID, "")
	if err != nil || out.Ticket.Owner != "worker-1" {
		t.Fatalf("on the owner's word it applies at once: %v %+v", err, out)
	}
	d, _ := h.svc.Show(ctx, tk.ID)
	if last := d.Events[len(d.Events)-1]; last.Kind != "claimed" || last.Proof != "owner_asked" {
		t.Fatalf("the journal says whose word it was, got %+v", last)
	}

	found := h.add(t, h.caller(t, "worker-2"), AddInput{Project: "atlas", Title: "trouvaille", Todo: true})
	if found.State != StateInbox {
		t.Fatalf("an unvouched todo stays in the inbox, got %s", found.State)
	}
	owner := Caller{Identity: "terminal", Name: TerminalName, Terminal: true, Vouched: true}
	if out, err := h.svc.Note(ctx, owner, found.ID, "", StateTodo, nil); err != nil || out.Ticket.State != StateTodo {
		t.Fatalf("the owner triages from their terminal directly: %v %+v", err, out)
	}
}
