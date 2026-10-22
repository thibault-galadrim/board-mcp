package board

import (
	"context"
	"strings"
	"time"
)

// FeedItem is a journal event with the ticket it concerns.
type FeedItem struct {
	Event
	Title   string
	Project string
	Owner   string
	State   State
	Kind    Kind
}

func (s *Service) LastEventID(ctx context.Context) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM events").Scan(&id)
	return id, err
}

// Feed returns the journal after an event id, oldest first.
func (s *Service) Feed(ctx context.Context, afterID int64, limit int) ([]FeedItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.id, COALESCE(e.ticket_id, 0), e.at, e.actor, e.kind, e.body, e.data, COALESCE(e.proof, ''),
		COALESCE(t.title, ''), COALESCE(t.project, ''), COALESCE(t.owner, ''), COALESCE(t.state, ''), COALESCE(t.kind, '')
		FROM events e LEFT JOIN tickets t ON t.id = e.ticket_id WHERE e.id > ? ORDER BY e.id LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FeedItem{}
	for rows.Next() {
		var f FeedItem
		if err := rows.Scan(&f.ID, &f.TicketID, &f.At, &f.Actor, &f.Event.Kind, &f.Body, &f.Data, &f.Proof,
			&f.Title, &f.Project, &f.Owner, &f.State, &f.Kind); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// DueOn returns the open tickets due on one of the given days.
func (s *Service) DueOn(ctx context.Context, days ...string) ([]Ticket, error) {
	if len(days) == 0 {
		return []Ticket{}, nil
	}
	where := "state NOT IN ('done', 'dropped') AND due IN (?" + strings.Repeat(", ?", len(days)-1) + ") ORDER BY due, priority"
	args := make([]any, len(days))
	for i, d := range days {
		args[i] = d
	}
	return s.queryTickets(ctx, s.db, where, args...)
}

// MarkNotified records that a notification went out, and reports whether
// it is the first time: a restart never repeats one.
func (s *Service) MarkNotified(ctx context.Context, ticketID int64, kind, day string) (bool, error) {
	res, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO notified (ticket_id, kind, day) VALUES (?, ?, ?)", ticketID, kind, day)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// Backup writes a consistent copy of the database to path, which must not
// exist yet.
func (s *Service) Backup(ctx context.Context, path string) error {
	_, err := s.db.ExecContext(ctx, "VACUUM INTO ?", path)
	return err
}

type Snapshot struct {
	GeneratedAt string    `json:"generated_at"`
	Projects    []Project `json:"projects"`
	Open        []Ticket  `json:"open"`
	Closed      []Ticket  `json:"recently_closed"`
	Requests    []Request `json:"pending_requests"`
}

const recentlyClosed = 72 * time.Hour

// Snapshot is the whole board as the owner's page shows it.
func (s *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	snap := Snapshot{GeneratedAt: s.stamp()}
	var err error
	if snap.Projects, err = s.Projects(ctx); err != nil {
		return snap, err
	}
	if snap.Open, err = s.queryTickets(ctx, s.db, "state NOT IN ('done', 'dropped')"+listOrder+" LIMIT 500", s.today()); err != nil {
		return snap, err
	}
	cutoff := s.now().Add(-recentlyClosed).UTC().Format(time.RFC3339)
	if snap.Closed, err = s.queryTickets(ctx, s.db, "state IN ('done', 'dropped') AND updated_at >= ? ORDER BY updated_at DESC LIMIT 30", cutoff); err != nil {
		return snap, err
	}
	for _, list := range [][]Ticket{snap.Open, snap.Closed} {
		for i := range list {
			list[i].Details = ""
		}
	}
	snap.Requests, err = s.PendingRequests(ctx)
	return snap, err
}
