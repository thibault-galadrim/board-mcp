package board

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Liveness tells whether the process behind an identity is still running.
type Liveness interface {
	Alive(identity string) bool
}

type Service struct {
	db       *sql.DB
	settings Settings
	alive    Liveness
	now      func() time.Time
	limiter  *limiter
}

func NewService(db *sql.DB, settings Settings, alive Liveness, now func() time.Time) (*Service, error) {
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	return &Service{
		db:       db,
		settings: settings,
		alive:    alive,
		now:      now,
		limiter:  &limiter{perMinute: settings.WritesPerMinute, hits: map[string][]time.Time{}},
	}, nil
}

func (s *Service) Settings() Settings { return s.settings }

type opKind string

const (
	opAdd           opKind = "add"
	opNote          opKind = "note"
	opClaim         opKind = "claim"
	opAssign        opKind = "assign"
	opCreateProject opKind = "create_project"
	opRebind        opKind = "rebind"
)

// op is a mutation, serialisable so that a request can replay it verbatim
// once the owner approves.
type op struct {
	Kind     opKind    `json:"kind"`
	TicketID int64     `json:"ticket_id,omitempty"`
	Add      *AddInput `json:"add,omitempty"`
	Text     string    `json:"text,omitempty"`
	State    State     `json:"state,omitempty"`
	Links    []string  `json:"links,omitempty"`
	For      string    `json:"for,omitempty"`
	To       string    `json:"to,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	Slug     string    `json:"slug,omitempty"`
	Name     string    `json:"name,omitempty"`
	Identity string    `json:"identity,omitempty"`
}

type AddInput struct {
	Project  string   `json:"project,omitempty"`
	Kind     Kind     `json:"kind,omitempty"`
	Title    string   `json:"title"`
	Details  string   `json:"details,omitempty"`
	Priority *int     `json:"priority,omitempty"`
	Due      string   `json:"due,omitempty"`
	ParentID int64    `json:"parent_id,omitempty"`
	Todo     bool     `json:"todo,omitempty"`
	Links    []string `json:"links,omitempty"`
}

type plan struct {
	action  Action
	project string
	ticket  *Ticket
	summary string
}

func (s *Service) Add(ctx context.Context, c Caller, in AddInput) (Outcome, error) {
	return s.run(ctx, c, op{Kind: opAdd, Add: &in})
}

func (s *Service) Note(ctx context.Context, c Caller, id int64, text string, state State, links []string) (Outcome, error) {
	return s.run(ctx, c, op{Kind: opNote, TicketID: id, Text: text, State: state, Links: links})
}

func (s *Service) Claim(ctx context.Context, c Caller, id int64, forName string) (Outcome, error) {
	return s.run(ctx, c, op{Kind: opClaim, TicketID: id, For: forName})
}

func (s *Service) Assign(ctx context.Context, c Caller, id int64, to, reason string) (Outcome, error) {
	return s.run(ctx, c, op{Kind: opAssign, TicketID: id, To: to, Reason: reason})
}

func (s *Service) CreateProject(ctx context.Context, c Caller, slug, name string) (Outcome, error) {
	return s.run(ctx, c, op{Kind: opCreateProject, Slug: slug, Name: name})
}

// Next takes the most urgent free task of a project. Choosing and taking
// happen in one transaction, so concurrent sessions never race for it.
func (s *Service) Next(ctx context.Context, c Caller, project, forName string) (Outcome, error) {
	if err := s.requireProject(ctx, s.db, project); err != nil {
		return Outcome{}, err
	}
	none := Outcome{Status: StatusOK, Message: fmt.Sprintf("no free todo task left in project %s", project)}
	return s.runWith(ctx, c, &none, func(tx *sql.Tx) (op, bool, error) {
		var id int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM tickets t
			WHERE project = ? AND kind = 'task' AND state = 'todo' AND owner IS NULL
			AND NOT EXISTS (SELECT 1 FROM requests r WHERE r.ticket_id = t.id AND r.state = 'pending' AND r.action = 'take')
			ORDER BY (due IS NOT NULL AND due < ?) DESC, priority ASC, due IS NULL, due ASC, id ASC LIMIT 1`,
			project, s.today()).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return op{}, false, nil
		}
		return op{Kind: opClaim, TicketID: id, For: forName}, err == nil, err
	})
}

func (s *Service) run(ctx context.Context, c Caller, o op) (Outcome, error) {
	return s.runWith(ctx, c, nil, func(*sql.Tx) (op, bool, error) { return o, true, nil })
}

// runWith builds the operation inside the transaction that executes it;
// when build finds nothing to do, empty is returned.
func (s *Service) runWith(ctx context.Context, c Caller, empty *Outcome, build func(*sql.Tx) (op, bool, error)) (Outcome, error) {
	if !s.limiter.allow(c.Identity, s.now()) {
		return Outcome{}, fmt.Errorf("%w: more than %d writes per minute", ErrRateLimit, s.settings.WritesPerMinute)
	}
	var out Outcome
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		o, found, err := build(tx)
		if err != nil {
			return err
		}
		if !found {
			out = *empty
			return nil
		}
		p, err := s.plan(ctx, tx, c, o)
		if err != nil {
			return err
		}
		member := false
		if p.ticket != nil && !c.Terminal {
			if member, err = inCircle(ctx, tx, p.ticket.ID, c.Name); err != nil {
				return err
			}
		}
		switch decide(s.settings.rule(p.project, p.action), member) {
		case allow:
			out, err = s.apply(ctx, tx, c, o, p, "")
		case needApproval:
			out, err = s.request(ctx, tx, c, o, p)
		default:
			err = fmt.Errorf("%w: %s is not allowed in mode %s", ErrDenied, p.action, s.settings.modeOf(p.project))
		}
		return err
	})
	return out, err
}

func (s *Service) plan(ctx context.Context, tx *sql.Tx, c Caller, o op) (plan, error) {
	switch o.Kind {
	case opAdd:
		if o.Add == nil {
			return plan{}, invalidf("missing ticket")
		}
		if err := s.validateAdd(ctx, tx, o.Add); err != nil {
			return plan{}, err
		}
		return plan{action: ActAdd, project: o.Add.Project, summary: fmt.Sprintf("ajouter « %s »%s", o.Add.Title, inProject(o.Add.Project))}, nil

	case opNote:
		t, err := s.getTicket(ctx, tx, o.TicketID)
		if err != nil {
			return plan{}, err
		}
		if err := validateText(o.Text); err != nil {
			return plan{}, err
		}
		if _, err := mergeLinks(t.Links, o.Links); err != nil {
			return plan{}, err
		}
		if o.State == "" {
			if strings.TrimSpace(o.Text) == "" && len(o.Links) == 0 {
				return plan{}, invalidf("a note needs text, links or a state")
			}
			return plan{action: ActNote, project: t.Project, ticket: &t, summary: fmt.Sprintf("commenter %s", t.ref())}, nil
		}
		if !o.State.valid() {
			return plan{}, invalidf("unknown state %q", o.State)
		}
		a, err := transitionAction(t.Kind, t.State, o.State)
		if err != nil {
			return plan{}, err
		}
		summary := fmt.Sprintf("passer %s de « %s » à « %s »", t.ref(), stateLabel[t.State], stateLabel[o.State])
		switch {
		case a == ActTriage && t.State == StateInbox:
			summary = fmt.Sprintf("valider la trouvaille %s%s", t.ref(), inProject(t.Project))
		case a == ActClose:
			summary = fmt.Sprintf("clore %s comme fait", t.ref())
		case a == ActDrop:
			summary = fmt.Sprintf("abandonner %s", t.ref())
		}
		return plan{action: a, project: t.Project, ticket: &t, summary: summary}, nil

	case opClaim:
		t, err := s.getTicket(ctx, tx, o.TicketID)
		if err != nil {
			return plan{}, err
		}
		if t.Kind == KindReminder {
			return plan{}, invalidf("%s is a reminder for %s: surface it, never take it", t.ref(), s.settings.OwnerName)
		}
		if t.State != StateTodo || t.Owner != "" {
			who := ""
			if t.Owner != "" {
				who = " by " + t.Owner
			}
			return plan{}, fmt.Errorf("%w: ticket #%d is %s%s", ErrConflict, t.ID, t.State, who)
		}
		owner := o.For
		if owner == "" {
			if c.Terminal {
				return plan{}, invalidf("from a terminal, pass `for` with the session that will do it")
			}
			owner = c.Name
		}
		if err := s.validateName(owner); err != nil {
			return plan{}, err
		}
		summary := fmt.Sprintf("prendre %s%s", t.ref(), inProject(t.Project))
		if owner != c.Name {
			summary = fmt.Sprintf("donner %s à %s%s", t.ref(), owner, inProject(t.Project))
		}
		return plan{action: ActTake, project: t.Project, ticket: &t, summary: summary}, nil

	case opAssign:
		t, err := s.getTicket(ctx, tx, o.TicketID)
		if err != nil {
			return plan{}, err
		}
		if !t.State.owned() {
			return plan{}, invalidf("ticket #%d is %s: only a ticket someone is working on can be passed or released", t.ID, t.State)
		}
		if strings.TrimSpace(o.Reason) == "" {
			return plan{}, invalidf("a reason is required")
		}
		if err := validateText(o.Reason); err != nil {
			return plan{}, err
		}
		if o.To == "" {
			return plan{action: ActReassign, project: t.Project, ticket: &t,
				summary: fmt.Sprintf("relâcher %s (tenu par %s) : %s", t.ref(), t.Owner, o.Reason)}, nil
		}
		if err := s.validateName(o.To); err != nil {
			return plan{}, err
		}
		if o.To == t.Owner {
			return plan{}, invalidf("%s already owns ticket #%d", o.To, t.ID)
		}
		return plan{action: ActReassign, project: t.Project, ticket: &t,
			summary: fmt.Sprintf("passer %s de %s à %s : %s", t.ref(), t.Owner, o.To, o.Reason)}, nil

	case opCreateProject:
		if !slugPattern.MatchString(o.Slug) {
			return plan{}, invalidf("project slug must match %s", slugPattern)
		}
		name := strings.TrimSpace(o.Name)
		if name == "" || len(name) > maxTitle {
			return plan{}, invalidf("project name must be 1 to %d characters", maxTitle)
		}
		var exists int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects WHERE slug = ?", o.Slug).Scan(&exists); err != nil {
			return plan{}, err
		}
		if exists > 0 {
			return plan{}, fmt.Errorf("%w: project %s already exists", ErrConflict, o.Slug)
		}
		return plan{action: ActCreateProject, project: o.Slug, summary: fmt.Sprintf("créer le projet %s « %s »", o.Slug, name)}, nil

	case opRebind:
		return plan{action: ActRebindName, summary: fmt.Sprintf("laisser une nouvelle session reprendre le nom %s, encore tenu par une session active", o.Name)}, nil
	}
	return plan{}, invalidf("unknown operation %q", o.Kind)
}

func (s *Service) apply(ctx context.Context, tx *sql.Tx, c Caller, o op, p plan, proof string) (Outcome, error) {
	now := s.stamp()
	switch o.Kind {
	case opAdd:
		return s.applyAdd(ctx, tx, c, o.Add, proof)

	case opNote:
		t := p.ticket
		links, _ := mergeLinks(t.Links, o.Links)
		linksJSON, _ := json.Marshal(links)
		activity := ""
		if c.Name == t.Owner {
			activity = now
		}
		if o.State == "" {
			if _, err := tx.ExecContext(ctx,
				`UPDATE tickets SET links = ?, updated_at = ?, owner_activity_at = COALESCE(NULLIF(?, ''), owner_activity_at) WHERE id = ?`,
				string(linksJSON), now, activity, t.ID); err != nil {
				return Outcome{}, err
			}
			if err := s.event(ctx, tx, t.ID, c.Name, "note", o.Text, map[string]any{"links": o.Links}, proof); err != nil {
				return Outcome{}, err
			}
		} else {
			owner := sql.NullString{String: t.Owner, Valid: t.Owner != ""}
			if o.State == StateTodo {
				owner = sql.NullString{}
			}
			res, err := tx.ExecContext(ctx,
				`UPDATE tickets SET state = ?, owner = ?, links = ?, updated_at = ?,
				owner_activity_at = COALESCE(NULLIF(?, ''), owner_activity_at) WHERE id = ? AND state = ?`,
				o.State, owner, string(linksJSON), now, activity, t.ID, t.State)
			if err := expectOne(res, err, t.ID); err != nil {
				return Outcome{}, err
			}
			kind := "state"
			if p.action == ActTriage && t.State == StateInbox {
				kind = "triaged"
			}
			if err := s.event(ctx, tx, t.ID, c.Name, kind, o.Text, map[string]any{"from": t.State, "to": o.State, "links": o.Links}, proof); err != nil {
				return Outcome{}, err
			}
		}
		return s.okTicket(ctx, tx, t.ID, "")

	case opClaim:
		owner := o.For
		if owner == "" {
			owner = c.Name
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE tickets SET state = 'in_progress', owner = ?, updated_at = ?, owner_activity_at = ?
			WHERE id = ? AND state = 'todo' AND owner IS NULL`, owner, now, now, p.ticket.ID)
		if err := expectOne(res, err, p.ticket.ID); err != nil {
			return Outcome{}, err
		}
		if err := addCircle(ctx, tx, p.ticket.ID, c.Name, owner); err != nil {
			return Outcome{}, err
		}
		if err := s.event(ctx, tx, p.ticket.ID, c.Name, "claimed", "", map[string]any{"owner": owner}, proof); err != nil {
			return Outcome{}, err
		}
		return s.okTicket(ctx, tx, p.ticket.ID, "")

	case opAssign:
		t := p.ticket
		var res sql.Result
		var err error
		if o.To == "" {
			res, err = tx.ExecContext(ctx,
				`UPDATE tickets SET state = 'todo', owner = NULL, updated_at = ? WHERE id = ? AND owner = ? AND state = ?`,
				now, t.ID, t.Owner, t.State)
		} else {
			res, err = tx.ExecContext(ctx,
				`UPDATE tickets SET owner = ?, updated_at = ?, owner_activity_at = ? WHERE id = ? AND owner = ? AND state = ?`,
				o.To, now, now, t.ID, t.Owner, t.State)
		}
		if err := expectOne(res, err, t.ID); err != nil {
			return Outcome{}, err
		}
		kind := "released"
		if o.To != "" {
			kind = "assigned"
			if err := addCircle(ctx, tx, t.ID, c.Name, o.To); err != nil {
				return Outcome{}, err
			}
		}
		if err := s.event(ctx, tx, t.ID, c.Name, kind, o.Reason, map[string]any{"from": t.Owner, "to": o.To}, proof); err != nil {
			return Outcome{}, err
		}
		return s.okTicket(ctx, tx, t.ID, "")

	case opCreateProject:
		if _, err := tx.ExecContext(ctx, "INSERT INTO projects (slug, name, created_at, created_by) VALUES (?, ?, ?, ?)",
			o.Slug, strings.TrimSpace(o.Name), now, c.Name); err != nil {
			return Outcome{}, err
		}
		if err := s.event(ctx, tx, 0, c.Name, "project_created", "", map[string]any{"slug": o.Slug, "name": o.Name}, proof); err != nil {
			return Outcome{}, err
		}
		return Outcome{Status: StatusOK, Message: fmt.Sprintf("project %s created", o.Slug)}, nil

	case opRebind:
		res, err := tx.ExecContext(ctx, "UPDATE actors SET identity = ?, bound_at = ?, last_seen_at = ? WHERE name = ?",
			o.Identity, now, now, o.Name)
		if err := expectOne(res, err, 0); err != nil {
			return Outcome{}, err
		}
		if err := s.event(ctx, tx, 0, o.Name, "name_rebound", "", map[string]any{"name": o.Name}, proof); err != nil {
			return Outcome{}, err
		}
		return Outcome{Status: StatusOK, Message: fmt.Sprintf("name %s now belongs to the requesting session", o.Name)}, nil
	}
	return Outcome{}, invalidf("unknown operation %q", o.Kind)
}

func (s *Service) applyAdd(ctx context.Context, tx *sql.Tx, c Caller, in *AddInput, proof string) (Outcome, error) {
	now := s.stamp()
	state := StateInbox
	if in.Kind == KindReminder {
		state = StateTodo
	}
	links, _ := json.Marshal(in.Links)
	res, err := tx.ExecContext(ctx,
		`INSERT INTO tickets (project, kind, title, details, priority, due, state, parent_id, links, created_by, created_at, updated_at)
		VALUES (NULLIF(?, ''), ?, ?, ?, ?, NULLIF(?, ''), ?, NULLIF(?, 0), ?, ?, ?, ?)`,
		in.Project, in.Kind, in.Title, in.Details, *in.Priority, in.Due, state, in.ParentID, string(links), c.Name, now, now)
	if err != nil {
		return Outcome{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Outcome{}, err
	}
	if !c.Terminal {
		if err := addCircle(ctx, tx, id, c.Name); err != nil {
			return Outcome{}, err
		}
	}
	if err := s.event(ctx, tx, id, c.Name, "created", "", map[string]any{"state": state}, proof); err != nil {
		return Outcome{}, err
	}
	if in.Kind == KindReminder {
		return s.okTicket(ctx, tx, id, "")
	}

	// A task lands in the inbox unless its author may triage it: then it
	// goes straight to todo. Asking for todo without that right files a
	// triage request for the owner.
	triage := s.settings.rule(in.Project, ActTriage)
	wantTodo := in.Todo || triage == RuleFree
	if !wantTodo {
		return s.okTicket(ctx, tx, id, "")
	}
	triageOp := op{Kind: opNote, TicketID: id, State: StateTodo}
	t, err := s.getTicket(ctx, tx, id)
	if err != nil {
		return Outcome{}, err
	}
	p := plan{action: ActTriage, project: in.Project, ticket: &t, summary: fmt.Sprintf("valider la trouvaille %s%s", t.ref(), inProject(in.Project))}
	switch decide(triage, !c.Terminal) {
	case allow:
		return s.apply(ctx, tx, c, triageOp, p, proof)
	case needApproval:
		if !in.Todo {
			return s.okTicket(ctx, tx, id, "")
		}
		return s.request(ctx, tx, c, triageOp, p)
	default:
		return s.okTicket(ctx, tx, id, "triage is not allowed in this mode: the ticket stays in the inbox")
	}
}

func (s *Service) request(ctx context.Context, tx *sql.Tx, c Caller, o op, p plan) (Outcome, error) {
	opJSON, err := json.Marshal(o)
	if err != nil {
		return Outcome{}, err
	}
	var ticketID int64
	if p.ticket != nil {
		ticketID = p.ticket.ID
	}
	var existing int64
	err = tx.QueryRowContext(ctx, "SELECT id FROM requests WHERE state = 'pending' AND actor = ? AND identity = ? AND op = ?",
		c.Name, c.Identity, string(opJSON)).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Outcome{}, err
	}
	if existing == 0 {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO requests (ticket_id, action, actor, identity, op, summary, state, created_at)
			VALUES (NULLIF(?, 0), ?, ?, ?, ?, ?, 'pending', ?)`,
			ticketID, p.action, c.Name, c.Identity, string(opJSON), p.summary, s.stamp())
		if err != nil {
			return Outcome{}, err
		}
		if existing, err = res.LastInsertId(); err != nil {
			return Outcome{}, err
		}
		if err := s.event(ctx, tx, ticketID, c.Name, "request_created", p.summary, map[string]any{"request_id": existing, "action": p.action}, ""); err != nil {
			return Outcome{}, err
		}
	}
	r, err := getRequest(ctx, tx, existing)
	if err != nil {
		return Outcome{}, err
	}
	out := Outcome{
		Status:  StatusPendingApproval,
		Message: fmt.Sprintf("waiting for %s's approval (request #%d). Tell them; do not retry.", s.settings.OwnerName, r.ID),
		Request: &r,
	}
	if ticketID != 0 {
		t, err := s.getTicket(ctx, tx, ticketID)
		if err != nil {
			return Outcome{}, err
		}
		out.Ticket = &t
	}
	return out, nil
}

// Approve runs a pending request on behalf of the session that asked for
// it. The state is checked again: if the ticket moved meanwhile the request
// fails instead of overwriting anything.
func (s *Service) Approve(ctx context.Context, id int64, proof string) (Outcome, error) {
	var out Outcome
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		r, err := getRequest(ctx, tx, id)
		if err != nil {
			return err
		}
		if r.State != RequestPending {
			return fmt.Errorf("%w: request #%d is %s", ErrNotPending, id, r.State)
		}
		var o op
		if err := json.Unmarshal([]byte(r.op), &o); err != nil {
			return fmt.Errorf("request #%d: %w", id, err)
		}
		c := Caller{Identity: r.identity, Name: r.Actor, Terminal: r.Actor == TerminalName}

		if _, err := tx.ExecContext(ctx, "SAVEPOINT approve"); err != nil {
			return err
		}
		p, perr := s.plan(ctx, tx, c, o)
		if perr == nil {
			out, perr = s.apply(ctx, tx, c, o, p, proof)
		}
		if perr != nil {
			if !errors.Is(perr, ErrInvalid) && !errors.Is(perr, ErrConflict) && !errors.Is(perr, ErrNotFound) {
				return perr
			}
			if _, err := tx.ExecContext(ctx, "ROLLBACK TO approve"); err != nil {
				return err
			}
			if err := s.decideRequest(ctx, tx, r, RequestFailed, perr.Error(), proof); err != nil {
				return err
			}
			r, err = getRequest(ctx, tx, id)
			out = Outcome{Status: StatusFailed, Message: perr.Error(), Request: &r}
			return err
		}
		if err := s.decideRequest(ctx, tx, r, RequestApproved, "", proof); err != nil {
			return err
		}
		r, err = getRequest(ctx, tx, id)
		out.Request = &r
		return err
	})
	return out, err
}

func (s *Service) Refuse(ctx context.Context, id int64, reason, proof string) (Outcome, error) {
	var out Outcome
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		r, err := getRequest(ctx, tx, id)
		if err != nil {
			return err
		}
		if r.State != RequestPending {
			return fmt.Errorf("%w: request #%d is %s", ErrNotPending, id, r.State)
		}
		if err := s.decideRequest(ctx, tx, r, RequestRefused, reason, proof); err != nil {
			return err
		}
		r, err = getRequest(ctx, tx, id)
		out = Outcome{Status: StatusRefused, Request: &r}
		return err
	})
	return out, err
}

func (s *Service) decideRequest(ctx context.Context, tx *sql.Tx, r Request, state RequestState, outcome, proof string) error {
	if _, err := tx.ExecContext(ctx, "UPDATE requests SET state = ?, outcome = ?, decided_at = ? WHERE id = ?",
		state, outcome, s.stamp(), r.ID); err != nil {
		return err
	}
	return s.event(ctx, tx, r.TicketID, s.settings.OwnerName, "request_"+string(state), outcome, map[string]any{"request_id": r.ID, "action": r.Action}, proof)
}

// Bind resolves the session name a caller asks for against its verified
// identity. A name follows its process: free when the process is gone,
// approval needed to take it from a live one.
func (s *Service) Bind(ctx context.Context, identity string, terminal bool, as string) (Caller, *Outcome, error) {
	if terminal {
		return Caller{Identity: identity, Name: TerminalName, Terminal: true}, nil, nil
	}
	name := strings.TrimSpace(as)
	if err := s.validateName(name); err != nil {
		return Caller{}, nil, err
	}
	c := Caller{Identity: identity, Name: name}
	var pending *Outcome
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		now := s.stamp()
		var bound string
		err := tx.QueryRowContext(ctx, "SELECT identity FROM actors WHERE name = ?", name).Scan(&bound)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err := tx.ExecContext(ctx, "INSERT INTO actors (name, identity, bound_at, last_seen_at) VALUES (?, ?, ?, ?)",
				name, identity, now, now); err != nil {
				return err
			}
			return s.event(ctx, tx, 0, name, "name_bound", "", map[string]any{"identity": identity}, "")
		case err != nil:
			return err
		case bound == identity:
			_, err := tx.ExecContext(ctx, "UPDATE actors SET last_seen_at = ? WHERE name = ?", now, name)
			return err
		case s.alive == nil || !s.alive.Alive(bound):
			if _, err := tx.ExecContext(ctx, "UPDATE actors SET identity = ?, bound_at = ?, last_seen_at = ? WHERE name = ?",
				identity, now, now, name); err != nil {
				return err
			}
			return s.event(ctx, tx, 0, name, "name_rebound", "previous session ended", map[string]any{"identity": identity}, "")
		}
		o := op{Kind: opRebind, Name: name, Identity: identity}
		p, err := s.plan(ctx, tx, c, o)
		if err != nil {
			return err
		}
		switch decide(s.settings.rule("", ActRebindName), false) {
		case allow:
			_, err = s.apply(ctx, tx, c, o, p, "")
			return err
		case needApproval:
			out, err := s.request(ctx, tx, c, o, p)
			pending = &out
			return err
		}
		return fmt.Errorf("%w: name %s is held by another live session; pick another name", ErrNameInUse, name)
	})
	if err != nil {
		return Caller{}, nil, err
	}
	return c, pending, nil
}

func (s *Service) validateName(name string) error {
	if !namePattern.MatchString(name) {
		return invalidf("session name %q must be 2-48 characters of a-z, 0-9, '.', '_' or '-', starting with a letter or digit", name)
	}
	if s.settings.reserved(name) {
		return invalidf("session name %q is reserved", name)
	}
	return nil
}

func (s *Service) validateAdd(ctx context.Context, tx *sql.Tx, in *AddInput) error {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" || len(in.Title) > maxTitle {
		return invalidf("title must be 1 to %d characters", maxTitle)
	}
	if err := validateText(in.Details); err != nil {
		return err
	}
	if in.Kind == "" {
		in.Kind = KindTask
	}
	if in.Kind != KindTask && in.Kind != KindReminder {
		return invalidf("kind must be task or reminder")
	}
	if in.Priority == nil {
		p := 2
		in.Priority = &p
	}
	if *in.Priority < 0 || *in.Priority > 3 {
		return invalidf("priority goes from 0 (urgent) to 3")
	}
	due, err := s.normalizeDue(in.Due)
	if err != nil {
		return err
	}
	in.Due = due
	if _, err := mergeLinks(nil, in.Links); err != nil {
		return err
	}
	if in.Project == "" {
		if in.Kind == KindTask {
			return invalidf("a task needs a project")
		}
	} else if err := s.requireProject(ctx, tx, in.Project); err != nil {
		return err
	}
	if in.ParentID != 0 {
		parent, err := s.getTicket(ctx, tx, in.ParentID)
		if err != nil {
			return err
		}
		if parent.Project != in.Project {
			return invalidf("sub-ticket must be in the parent's project (%s)", parent.Project)
		}
	}
	return nil
}

func (s *Service) normalizeDue(due string) (string, error) {
	due = strings.TrimSpace(due)
	if due == "" {
		return "", nil
	}
	if datePattern.MatchString(due) {
		if _, err := time.Parse(time.DateOnly, due); err != nil {
			return "", invalidf("due date %q: %v", due, err)
		}
		return due, nil
	}
	t, err := time.Parse(time.RFC3339, due)
	if err != nil {
		return "", invalidf("due must be YYYY-MM-DD or RFC 3339")
	}
	return t.In(s.now().Location()).Format(time.DateOnly), nil
}

func (s *Service) requireProject(ctx context.Context, q querier, slug string) error {
	var n int
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects WHERE slug = ?", slug).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	slugs, err := projectSlugs(ctx, q)
	if err != nil {
		return err
	}
	existing := "none yet"
	if len(slugs) > 0 {
		existing = strings.Join(slugs, ", ")
	}
	return fmt.Errorf("%w: unknown project %q; existing projects: %s. Only %s creates projects",
		ErrNotFound, slug, existing, s.settings.OwnerName)
}

func validateText(text string) error {
	if len(text) > maxText {
		return invalidf("text is limited to %d characters", maxText)
	}
	return nil
}

func mergeLinks(existing, added []string) ([]string, error) {
	out := append([]string{}, existing...)
	for _, l := range added {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if len(l) > maxLinkLen {
			return nil, invalidf("links are limited to %d characters", maxLinkLen)
		}
		dup := false
		for _, e := range out {
			if e == l {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, l)
		}
	}
	if len(out) > maxLinks {
		return nil, invalidf("a ticket keeps at most %d links", maxLinks)
	}
	return out, nil
}

func (s *Service) okTicket(ctx context.Context, q querier, id int64, msg string) (Outcome, error) {
	t, err := s.getTicket(ctx, q, id)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{Status: StatusOK, Message: msg, Ticket: &t}, nil
}

func (s *Service) event(ctx context.Context, tx *sql.Tx, ticketID int64, actor, kind, body string, data map[string]any, proof string) error {
	raw := "{}"
	if len(data) > 0 {
		b, err := json.Marshal(data)
		if err != nil {
			return err
		}
		raw = string(b)
	}
	_, err := tx.ExecContext(ctx,
		"INSERT INTO events (ticket_id, at, actor, kind, body, data, proof) VALUES (NULLIF(?, 0), ?, ?, ?, ?, ?, NULLIF(?, ''))",
		ticketID, s.stamp(), actor, kind, body, raw, proof)
	return err
}

func (s *Service) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) stamp() string { return s.now().UTC().Format(time.RFC3339) }

func (s *Service) today() string { return s.now().Format(time.DateOnly) }

func expectOne(res sql.Result, err error, id int64) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("%w: ticket #%d changed meanwhile", ErrConflict, id)
	}
	return nil
}

var stateLabel = map[State]string{
	StateInbox: "à trier", StateTodo: "à faire", StateInProgress: "en cours", StateBlocked: "bloqué",
	StateToValidate: "à valider", StateDone: "fait", StateDropped: "abandonné",
}

func (t Ticket) ref() string { return fmt.Sprintf("#%d « %s »", t.ID, t.Title) }

func inProject(slug string) string {
	if slug == "" {
		return ""
	}
	return " (projet " + slug + ")"
}

type limiter struct {
	mu        sync.Mutex
	perMinute int
	hits      map[string][]time.Time
}

func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-time.Minute)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.perMinute {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}
