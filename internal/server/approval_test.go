package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"board/internal/board"
	"board/internal/identity"
	"board/internal/touchid"
)

// fakeApprover answers prompts from a script; once it runs out, prompts go
// unanswered until their deadline.
type fakeApprover struct {
	mu      sync.Mutex
	answers []touchid.Decision
	reasons []string
}

func (f *fakeApprover) Confirm(ctx context.Context, reason string) (touchid.Decision, error) {
	f.mu.Lock()
	f.reasons = append(f.reasons, reason)
	if len(f.answers) > 0 {
		d := f.answers[0]
		f.answers = f.answers[1:]
		f.mu.Unlock()
		return d, nil
	}
	f.mu.Unlock()
	<-ctx.Done()
	return touchid.Undecided, nil
}

func (f *fakeApprover) Proof() string { return "fake-touchid" }

func (f *fakeApprover) script(d ...touchid.Decision) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, d...)
}

func (f *fakeApprover) lastReason() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reasons) == 0 {
		return ""
	}
	return f.reasons[len(f.reasons)-1]
}

var prompting = Options{Prompt: PromptAlways, ApprovalWait: 300 * time.Millisecond, UnlockWait: 300 * time.Millisecond}

func (e *env) post(t *testing.T, identityKey, path string) (int, map[string]any) {
	t.Helper()
	res, err := e.httpClient(identityKey, nil).Post(e.base+path, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	return res.StatusCode, body
}

func TestOwnerApprovesWithinTheCall(t *testing.T) {
	approver := &fakeApprover{}
	e := newEnvWith(t, approver, prompting)
	cs := e.client(t, "claude:A", nil)

	approver.script(touchid.Approved)
	out, _ := call[board.Outcome](t, cs, "board_add", map[string]any{"as": "coord-atlas", "project": "atlas", "title": "relancer le client", "todo": true})
	if out.Status != board.StatusOK || out.Ticket.State != board.StateTodo {
		t.Fatalf("an approved prompt must complete the call, got %+v", out)
	}
	if r := approver.lastReason(); !strings.Contains(r, "de coord-atlas : valider la trouvaille") {
		t.Fatalf("the prompt must say who asks what, got %q", r)
	}
	d, err := e.svc.Show(context.Background(), out.Ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if last := d.Events[len(d.Events)-1]; last.Kind != "request_approved" || last.Proof != "fake-touchid" {
		t.Fatalf("got %+v", last)
	}
}

func TestCancelledPromptRefusesTheRequest(t *testing.T) {
	approver := &fakeApprover{}
	e := newEnvWith(t, approver, prompting)
	approver.script(touchid.Approved)
	cs := e.client(t, "claude:A", nil)
	added, _ := call[board.Outcome](t, cs, "board_add", map[string]any{"as": "coord-atlas", "project": "atlas", "title": "x", "todo": true})

	approver.script(touchid.Refused)
	out, _ := call[board.Outcome](t, cs, "board_claim", map[string]any{"as": "coord-atlas", "id": added.Ticket.ID})
	if out.Status != board.StatusRefused || out.Request.State != board.RequestRefused {
		t.Fatalf("got %+v", out)
	}
	if d, _ := e.svc.Show(context.Background(), added.Ticket.ID); d.Ticket.State != board.StateTodo {
		t.Fatalf("a refused take leaves the ticket alone, got %s", d.Ticket.State)
	}
}

func TestUnansweredRequestWaitsForTheOwnersTerminal(t *testing.T) {
	approver := &fakeApprover{}
	e := newEnvWith(t, approver, prompting)
	approver.script(touchid.Approved)
	cs := e.client(t, "claude:A", nil)
	added, _ := call[board.Outcome](t, cs, "board_add", map[string]any{"as": "worker-1", "project": "atlas", "title": "x", "todo": true})

	pending, _ := call[board.Outcome](t, cs, "board_claim", map[string]any{"as": "worker-1", "id": added.Ticket.ID})
	if pending.Status != board.StatusPendingApproval || !strings.Contains(pending.Message, "board wait") {
		t.Fatalf("got %+v", pending)
	}
	id := pending.Request.ID

	waited := make(chan board.Request, 1)
	go func() {
		res, err := e.httpClient("claude:A", nil).Get(e.base + "/api/requests/" + itoa(id) + "/wait?timeout=5s")
		if err != nil {
			t.Error(err)
			return
		}
		defer res.Body.Close()
		var r board.Request
		_ = json.NewDecoder(res.Body).Decode(&r)
		waited <- r
	}()
	time.Sleep(100 * time.Millisecond)

	if status, _ := e.post(t, "claude:A", "/api/requests/"+itoa(id)+"/approve"); status != http.StatusForbidden {
		t.Fatalf("a Claude session cannot approve, got %d", status)
	}
	approver.script(touchid.Refused)
	if status, _ := e.post(t, identity.Terminal, "/api/requests/"+itoa(id)+"/approve"); status != http.StatusRequestTimeout {
		t.Fatalf("cancelling a prompt one asked for keeps the request pending, got %d", status)
	}
	approver.script(touchid.Approved)
	status, body := e.post(t, identity.Terminal, "/api/requests/"+itoa(id)+"/approve")
	if status != http.StatusOK || body["status"] != board.StatusOK {
		t.Fatalf("got %d %v", status, body)
	}
	select {
	case r := <-waited:
		if r.State != board.RequestApproved {
			t.Fatalf("the waiter must see the decision, got %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the waiter was not woken up")
	}
	if status, _ := e.post(t, identity.Terminal, "/api/requests/"+itoa(id)+"/approve"); status != http.StatusConflict {
		t.Fatalf("a decided request cannot be approved again, got %d", status)
	}
}

func TestOwnerRefusesFromTerminal(t *testing.T) {
	approver := &fakeApprover{}
	e := newEnvWith(t, approver, prompting)
	cs := e.client(t, "claude:A", nil)
	out, _ := call[board.Outcome](t, cs, "board_add", map[string]any{"as": "worker-1", "project": "atlas", "title": "x", "todo": true})
	id := out.Request.ID
	if status, _ := e.post(t, "claude:A", "/api/requests/"+itoa(id)+"/refuse"); status != http.StatusForbidden {
		t.Fatalf("a Claude session cannot refuse, got %d", status)
	}
	status, body := e.post(t, identity.Terminal, "/api/requests/"+itoa(id)+"/refuse?reason=doublon")
	if status != http.StatusOK || body["status"] != board.StatusRefused {
		t.Fatalf("got %d %v", status, body)
	}
}

func TestBoardStaysLockedUntilTheOwnerUnlocks(t *testing.T) {
	approver := &fakeApprover{}
	opts := prompting
	opts.UnlockAtStart = true
	e := newEnvWith(t, approver, opts)
	cs := e.client(t, "claude:A", nil)

	_, res := call[board.Outcome](t, cs, "board_list", map[string]any{"as": "a1"})
	if !res.IsError || !strings.Contains(errorText(res), "locked") {
		t.Fatalf("got %q", errorText(res))
	}
	if status, _ := e.post(t, "claude:A", "/api/unlock"); status != http.StatusForbidden {
		t.Fatalf("a Claude session cannot unlock, got %d", status)
	}
	approver.script(touchid.Refused)
	if _, body := e.post(t, identity.Terminal, "/api/unlock"); body["unlocked"] != false {
		t.Fatalf("a refused unlock keeps the board closed, got %v", body)
	}
	approver.script(touchid.Approved)
	if _, body := e.post(t, identity.Terminal, "/api/unlock"); body["unlocked"] != true {
		t.Fatalf("got %v", body)
	}
	if !strings.Contains(approver.lastReason(), "déverrouiller le board (mode par défaut : demande") {
		t.Fatalf("the unlock prompt must show the active settings, got %q", approver.lastReason())
	}
	if _, res := call[board.Outcome](t, cs, "board_list", map[string]any{"as": "a1"}); res.IsError {
		t.Fatalf("unlocked board must answer, got %q", errorText(res))
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestOnlyOwnerTriggeredRequestsPrompt(t *testing.T) {
	approver := &fakeApprover{}
	opts := prompting
	opts.Prompt = PromptOwner
	opts.Notify = DefaultOptions().Notify
	e := newEnvWith(t, approver, opts)
	agent := e.client(t, "claude:A", nil)

	queued, _ := call[board.Outcome](t, agent, "board_add", map[string]any{"as": "worker-1", "project": "atlas", "title": "spontané", "todo": true})
	if queued.Status != board.StatusPendingApproval || approver.lastReason() != "" {
		t.Fatalf("a request the owner did not ask for must queue silently, got %+v / prompt %q", queued, approver.lastReason())
	}
	if sent := e.notes.all(); len(sent) != 1 || !strings.HasPrefix(sent[0], "Demande en attente") {
		t.Fatalf("the queued request must be notified, got %q", sent)
	}

	approver.script(touchid.Approved)
	asked, _ := call[board.Outcome](t, agent, "board_add", map[string]any{"as": "worker-1", "project": "atlas", "title": "dicté", "todo": true, "owner_asked": true})
	if asked.Status != board.StatusOK || asked.Ticket.State != board.StateTodo {
		t.Fatalf("an action the owner just dictated prompts at once, got %+v", asked)
	}

	approver.script(touchid.Approved)
	owner := e.client(t, identity.Terminal, nil)
	mine, _ := call[board.Outcome](t, owner, "board_add", map[string]any{"as": "cli", "project": "atlas", "title": "de mon terminal", "todo": true})
	if mine.Status != board.StatusOK {
		t.Fatalf("the owner's terminal prompts at once, got %+v", mine)
	}
}
