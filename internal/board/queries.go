package board

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const ticketColumns = `id, COALESCE(project, ''), kind, title, details, priority, COALESCE(due, ''), state,
	COALESCE(owner, ''), COALESCE(parent_id, 0), links, created_by, created_at, updated_at, COALESCE(owner_activity_at, '')`

type scanner interface{ Scan(dest ...any) error }

func (s *Service) scanTicket(row scanner) (Ticket, error) {
	var t Ticket
	var links string
	if err := row.Scan(&t.ID, &t.Project, &t.Kind, &t.Title, &t.Details, &t.Priority, &t.Due, &t.State,
		&t.Owner, &t.ParentID, &links, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt, &t.ownerActivityAt); err != nil {
		return Ticket{}, err
	}
	if err := json.Unmarshal([]byte(links), &t.Links); err != nil {
		return Ticket{}, fmt.Errorf("ticket #%d links: %w", t.ID, err)
	}
	t.Overdue = t.Due != "" && !t.State.closed() && t.Due < s.today()
	if t.State == StateInProgress && t.ownerActivityAt != "" {
		if at, err := time.Parse(time.RFC3339, t.ownerActivityAt); err == nil {
			t.Silent = s.now().Sub(at) > s.settings.SilenceAfter
		}
	}
	return t, nil
}

func (s *Service) getTicket(ctx context.Context, q querier, id int64) (Ticket, error) {
	t, err := s.scanTicket(q.QueryRowContext(ctx, "SELECT "+ticketColumns+" FROM tickets WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, fmt.Errorf("%w: ticket #%d", ErrNotFound, id)
	}
	return t, err
}

func (s *Service) queryTickets(ctx context.Context, q querier, where string, args ...any) ([]Ticket, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+ticketColumns+" FROM tickets WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Ticket{}
	for rows.Next() {
		t, err := s.scanTicket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

const requestColumns = `id, COALESCE(ticket_id, 0), action, actor, identity, op, summary, state, outcome, created_at, COALESCE(decided_at, '')`

func scanRequest(row scanner) (Request, error) {
	var r Request
	err := row.Scan(&r.ID, &r.TicketID, &r.Action, &r.Actor, &r.identity, &r.op, &r.Summary, &r.State, &r.Outcome, &r.CreatedAt, &r.DecidedAt)
	return r, err
}

func getRequest(ctx context.Context, q querier, id int64) (Request, error) {
	r, err := scanRequest(q.QueryRowContext(ctx, "SELECT "+requestColumns+" FROM requests WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Request{}, fmt.Errorf("%w: request #%d", ErrNotFound, id)
	}
	return r, err
}

func queryRequests(ctx context.Context, q querier, where string, args ...any) ([]Request, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+requestColumns+" FROM requests WHERE "+where+" ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func inCircle(ctx context.Context, q querier, ticketID int64, name string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM ticket_circle WHERE ticket_id = ? AND name = ?", ticketID, name).Scan(&n)
	return n > 0, err
}

func addCircle(ctx context.Context, q querier, ticketID int64, names ...string) error {
	for _, n := range names {
		if n == "" || n == TerminalName {
			continue
		}
		if _, err := q.ExecContext(ctx, "INSERT OR IGNORE INTO ticket_circle (ticket_id, name) VALUES (?, ?)", ticketID, n); err != nil {
			return err
		}
	}
	return nil
}

func circleOf(ctx context.Context, q querier, ticketID int64) ([]string, error) {
	rows, err := q.QueryContext(ctx, "SELECT name FROM ticket_circle WHERE ticket_id = ? ORDER BY name", ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func projectSlugs(ctx context.Context, q querier) ([]string, error) {
	rows, err := q.QueryContext(ctx, "SELECT slug FROM projects ORDER BY slug")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, err
		}
		out = append(out, slug)
	}
	return out, rows.Err()
}

func (s *Service) Projects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT slug, name FROM projects ORDER BY slug")
	if err != nil {
		return nil, err
	}
	projects := []Project{}
	index := map[string]int{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.Slug, &p.Name); err != nil {
			rows.Close()
			return nil, err
		}
		p.Mode = s.settings.modeOf(p.Slug)
		p.Count = map[State]int{}
		index[p.Slug] = len(projects)
		projects = append(projects, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	counts, err := s.db.QueryContext(ctx,
		"SELECT project, state, COUNT(*) FROM tickets WHERE project IS NOT NULL AND state NOT IN ('done', 'dropped') GROUP BY project, state")
	if err != nil {
		return nil, err
	}
	defer counts.Close()
	for counts.Next() {
		var slug string
		var state State
		var n int
		if err := counts.Scan(&slug, &state, &n); err != nil {
			return nil, err
		}
		if i, ok := index[slug]; ok {
			projects[i].Count[state] = n
		}
	}
	return projects, counts.Err()
}

type ListFilter struct {
	Project       string
	State         State
	Owner         string
	Kind          Kind
	IncludeClosed bool
	Limit         int
}

const listOrder = ` ORDER BY CASE state WHEN 'blocked' THEN 0 WHEN 'to_validate' THEN 1 WHEN 'in_progress' THEN 2
	WHEN 'todo' THEN 3 WHEN 'inbox' THEN 4 ELSE 5 END, (due IS NOT NULL AND due < ?) DESC, priority, id`

func (s *Service) List(ctx context.Context, f ListFilter) ([]Ticket, error) {
	where := []string{"1 = 1"}
	args := []any{}
	if f.Project != "" {
		where = append(where, "project = ?")
		args = append(args, f.Project)
	}
	if f.State != "" {
		if !f.State.valid() {
			return nil, invalidf("unknown state %q", f.State)
		}
		where = append(where, "state = ?")
		args = append(args, f.State)
	} else if !f.IncludeClosed {
		where = append(where, "state NOT IN ('done', 'dropped')")
	}
	if f.Owner != "" {
		where = append(where, "owner = ?")
		args = append(args, f.Owner)
	}
	if f.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, f.Kind)
	}
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	args = append(args, s.today(), limit)
	tickets, err := s.queryTickets(ctx, s.db, strings.Join(where, " AND ")+listOrder+" LIMIT ?", args...)
	if err != nil {
		return nil, err
	}
	for i := range tickets {
		tickets[i].Details = ""
	}
	return tickets, nil
}

type TicketDetail struct {
	Ticket   Ticket    `json:"ticket"`
	Circle   []string  `json:"circle"`
	Children []Ticket  `json:"sub_tickets,omitempty"`
	Requests []Request `json:"pending_requests,omitempty"`
	Events   []Event   `json:"events"`
}

func (s *Service) Show(ctx context.Context, id int64) (TicketDetail, error) {
	t, err := s.getTicket(ctx, s.db, id)
	if err != nil {
		return TicketDetail{}, err
	}
	d := TicketDetail{Ticket: t}
	if d.Circle, err = circleOf(ctx, s.db, id); err != nil {
		return d, err
	}
	if d.Children, err = s.queryTickets(ctx, s.db, "parent_id = ? ORDER BY id", id); err != nil {
		return d, err
	}
	for i := range d.Children {
		d.Children[i].Details = ""
	}
	if d.Requests, err = queryRequests(ctx, s.db, "ticket_id = ? AND state = 'pending'", id); err != nil {
		return d, err
	}
	d.Events, err = queryEvents(ctx, s.db, "SELECT * FROM (SELECT e.* FROM events e WHERE e.ticket_id = ? ORDER BY e.id DESC LIMIT 200) ORDER BY id", id)
	return d, err
}

func queryEvents(ctx context.Context, q querier, query string, args ...any) ([]Event, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var ticketID sql.NullInt64
		var proof sql.NullString
		if err := rows.Scan(&e.ID, &ticketID, &e.At, &e.Actor, &e.Kind, &e.Body, &e.Data, &proof); err != nil {
			return nil, err
		}
		e.TicketID = ticketID.Int64
		e.Proof = proof.String
		if e.Data == "{}" {
			e.Data = ""
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type WhatsNew struct {
	Scope           string    `json:"scope"`
	Events          []Event   `json:"events"`
	Truncated       bool      `json:"truncated,omitempty"`
	MyTickets       []Ticket  `json:"my_tickets"`
	Silent          []Ticket  `json:"silent_tickets"`
	Reminders       []Ticket  `json:"reminders_due"`
	PendingRequests []Request `json:"my_pending_requests"`
	InboxCount      int       `json:"inbox_count"`
}

const (
	whatsNewLimit   = 200
	whatsNewInitial = 20
)

// WhatsNew returns what others did since the caller's last look at this
// scope (a project, or everything), then moves the caller's cursor.
func (s *Service) WhatsNew(ctx context.Context, c Caller, project string) (WhatsNew, error) {
	scope := project
	if scope == "" {
		scope = "*"
	} else if err := s.requireProject(ctx, s.db, project); err != nil {
		return WhatsNew{}, err
	}
	w := WhatsNew{Scope: scope}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var cursor int64
		err := tx.QueryRowContext(ctx, "SELECT event_id FROM cursors WHERE actor = ? AND scope = ?", c.Name, scope).Scan(&cursor)
		first := errors.Is(err, sql.ErrNoRows)
		if err != nil && !first {
			return err
		}

		from := "events e"
		cond := []string{"e.actor != ?"}
		args := []any{c.Name}
		if project != "" {
			from = "events e JOIN tickets t ON t.id = e.ticket_id"
			cond = append(cond, "t.project = ?")
			args = append(args, project)
		}
		var q string
		if first {
			q = fmt.Sprintf("SELECT * FROM (SELECT e.* FROM %s WHERE %s ORDER BY e.id DESC LIMIT %d) ORDER BY id",
				from, strings.Join(cond, " AND "), whatsNewInitial)
		} else {
			cond = append(cond, "e.id > ?")
			args = append(args, cursor)
			q = fmt.Sprintf("SELECT e.* FROM %s WHERE %s ORDER BY e.id LIMIT %d", from, strings.Join(cond, " AND "), whatsNewLimit+1)
		}
		if w.Events, err = queryEvents(ctx, tx, q, args...); err != nil {
			return err
		}
		if len(w.Events) > whatsNewLimit {
			w.Events = w.Events[:whatsNewLimit]
			w.Truncated = true
		}

		next := cursor
		if w.Truncated {
			next = w.Events[len(w.Events)-1].ID
		} else if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM events").Scan(&next); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO cursors (actor, scope, event_id) VALUES (?, ?, ?) ON CONFLICT (actor, scope) DO UPDATE SET event_id = excluded.event_id",
			c.Name, scope, next); err != nil {
			return err
		}

		inScope := "1 = 1"
		scopeArgs := []any{}
		if project != "" {
			inScope = "project = ?"
			scopeArgs = append(scopeArgs, project)
		}
		if !c.Terminal {
			if w.MyTickets, err = s.queryTickets(ctx, tx, "owner = ? AND state NOT IN ('done', 'dropped') ORDER BY id", c.Name); err != nil {
				return err
			}
		}
		inProgress, err := s.queryTickets(ctx, tx, inScope+" AND state = 'in_progress' ORDER BY id", scopeArgs...)
		if err != nil {
			return err
		}
		w.Silent = []Ticket{}
		for _, t := range inProgress {
			if t.Silent {
				w.Silent = append(w.Silent, t)
			}
		}
		horizon := s.now().Add(s.settings.ReminderLead).Format(time.DateOnly)
		reminderArgs := append(append([]any{}, scopeArgs...), horizon)
		if w.Reminders, err = s.queryTickets(ctx, tx,
			inScope+" AND kind = 'reminder' AND state = 'todo' AND due IS NOT NULL AND due <= ? ORDER BY due, priority", reminderArgs...); err != nil {
			return err
		}
		if w.PendingRequests, err = queryRequests(ctx, tx, "actor = ? AND state = 'pending'", c.Name); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM tickets WHERE "+inScope+" AND state = 'inbox'", scopeArgs...).Scan(&w.InboxCount)
	})
	return w, err
}

func (s *Service) PendingRequests(ctx context.Context) ([]Request, error) {
	return queryRequests(ctx, s.db, "state = 'pending'")
}
