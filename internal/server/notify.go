package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"board/internal/board"
)

type Notifier interface {
	Notify(title, subtitle, body string)
}

// NotifyOptions chooses what reaches the owner as a notification.
type NotifyOptions struct {
	Blocked        bool
	ToValidate     bool
	Done           bool
	Inbox          bool
	PendingRequest bool
	Deadline       bool
	// DeadlineHour is the local hour from which due-date notifications go out.
	DeadlineHour int
}

const (
	maxNotificationsPerScan = 3
	feedPage                = 200
	deadlineCheckEvery      = 15 * time.Minute
)

// changed tells the page and the notifier that the board moved.
func (s *Server) changed() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
	s.pages.publish()
}

// Run delivers notifications until ctx ends. It starts from the current
// end of the journal, so a restart never replays old notifications.
func (s *Server) Run(ctx context.Context) {
	last, err := s.svc.LastEventID(ctx)
	if err != nil {
		s.log.Error("notifier cannot read the journal", "err", err)
		return
	}
	ticker := time.NewTicker(deadlineCheckEvery)
	defer ticker.Stop()
	s.checkDeadlines(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
			last = s.scan(ctx, last)
		case <-ticker.C:
			s.checkDeadlines(ctx)
		}
	}
}

type note struct{ title, subtitle, body string }

func (s *Server) scan(ctx context.Context, last int64) int64 {
	var notes []note
	for {
		feed, err := s.svc.Feed(ctx, last, feedPage)
		if err != nil {
			s.log.Error("notifier cannot read the journal", "err", err)
			return last
		}
		for _, item := range feed {
			if n, ok := s.noteFor(item); ok {
				notes = append(notes, n)
			}
			last = item.ID
		}
		if len(feed) < feedPage {
			break
		}
	}
	for i, n := range notes {
		if i == maxNotificationsPerScan {
			s.notifier.Notify("Board", fmt.Sprintf("et %d autre(s)", len(notes)-i), "board news pour le détail")
			break
		}
		s.notifier.Notify(n.title, n.subtitle, n.body)
	}
	return last
}

func (s *Server) noteFor(item board.FeedItem) (note, bool) {
	// The owner knows what they just did themselves.
	if item.Actor == board.TerminalName {
		return note{}, false
	}
	ref := fmt.Sprintf("#%d %s", item.TicketID, item.Title)
	switch item.Event.Kind {
	case "created":
		var data struct {
			State board.State `json:"state"`
		}
		_ = json.Unmarshal([]byte(item.Data), &data)
		if s.options().Notify.Inbox && data.State == board.StateInbox {
			return note{"Trouvaille" + inProject(item.Project), ref, "déposée par " + item.Actor}, true
		}
	case "state":
		var data struct {
			To board.State `json:"to"`
		}
		_ = json.Unmarshal([]byte(item.Data), &data)
		body := item.Actor
		if item.Body != "" {
			body += " : " + clip(item.Body, 180)
		}
		switch {
		case data.To == board.StateBlocked && s.options().Notify.Blocked:
			return note{"Bloqué" + inProject(item.Project), ref, body}, true
		case data.To == board.StateToValidate && s.options().Notify.ToValidate:
			return note{"À valider" + inProject(item.Project), ref, body}, true
		case data.To == board.StateDone && s.options().Notify.Done:
			return note{"Fait" + inProject(item.Project), ref, body}, true
		}
	}
	return note{}, false
}

func (s *Server) notifyPending(r board.Request) {
	if s.options().Notify.PendingRequest {
		s.notifier.Notify("Demande en attente", fmt.Sprintf("#%d de %s", r.ID, r.Actor), clip(r.Summary, 180)+" - board ok "+fmt.Sprint(r.ID))
	}
}

func (s *Server) checkDeadlines(ctx context.Context) {
	now := s.clock()
	if !s.options().Notify.Deadline || now.Hour() < s.options().Notify.DeadlineHour {
		return
	}
	today := now.Format(time.DateOnly)
	tomorrow := now.AddDate(0, 0, 1).Format(time.DateOnly)
	due, err := s.svc.DueOn(ctx, today, tomorrow)
	if err != nil {
		s.log.Error("deadline check failed", "err", err)
		return
	}
	for _, t := range due {
		kind, title := "due_tomorrow", "Échéance demain"
		if t.Due == today {
			kind, title = "due_today", "Échéance aujourd'hui"
		}
		first, err := s.svc.MarkNotified(ctx, t.ID, kind, today)
		if err != nil {
			s.log.Error("deadline check failed", "err", err)
			return
		}
		if !first {
			continue
		}
		who := t.Owner
		if t.Kind == board.KindReminder {
			who = "rappel pour toi"
		} else if who == "" {
			who = "personne ne l'a pris"
		}
		s.notifier.Notify(title+inProject(t.Project), fmt.Sprintf("#%d %s", t.ID, t.Title), who)
	}
}

func inProject(slug string) string {
	if slug == "" {
		return ""
	}
	return " (" + slug + ")"
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
