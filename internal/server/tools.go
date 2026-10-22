package server

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"board/internal/board"
	"board/internal/identity"
)

type projectsIn struct {
	As         string `json:"as" jsonschema:"your stable session name, e.g. coord-atlas"`
	CreateSlug string `json:"create_slug,omitempty" jsonschema:"only when the owner asked for a new project: its slug (a-z, 0-9, -)"`
	CreateName string `json:"create_name,omitempty" jsonschema:"display name of the project to create"`
}

type projectsOut struct {
	Projects []board.Project `json:"projects"`
	Created  *board.Outcome  `json:"created,omitempty"`
}

type listIn struct {
	As            string `json:"as" jsonschema:"your stable session name, e.g. coord-atlas"`
	Project       string `json:"project,omitempty" jsonschema:"project slug"`
	State         string `json:"state,omitempty" jsonschema:"inbox, todo, in_progress, blocked, to_validate, done or dropped"`
	Owner         string `json:"owner,omitempty" jsonschema:"session name owning the tickets"`
	Mine          bool   `json:"mine,omitempty" jsonschema:"only tickets you own"`
	Kind          string `json:"kind,omitempty" jsonschema:"task or reminder"`
	IncludeClosed bool   `json:"include_closed,omitempty" jsonschema:"also list done and dropped tickets"`
}

type listOut struct {
	Tickets []board.Ticket `json:"tickets"`
}

type showIn struct {
	As string `json:"as" jsonschema:"your stable session name, e.g. coord-atlas"`
	ID int64  `json:"id" jsonschema:"ticket id"`
}

type whatsNewIn struct {
	As      string `json:"as" jsonschema:"your stable session name, e.g. coord-atlas"`
	Project string `json:"project,omitempty" jsonschema:"project slug; empty for the whole board"`
}

type nextIn struct {
	As      string `json:"as" jsonschema:"your stable session name, e.g. coord-atlas"`
	Project string `json:"project" jsonschema:"project slug"`
	For     string `json:"for,omitempty" jsonschema:"take it for another session (its name) instead of yourself"`
}

type claimIn struct {
	As  string `json:"as" jsonschema:"your stable session name, e.g. coord-atlas"`
	ID  int64  `json:"id" jsonschema:"ticket id"`
	For string `json:"for,omitempty" jsonschema:"take it for another session (its name) instead of yourself"`
}

type assignIn struct {
	As     string `json:"as" jsonschema:"your stable session name, e.g. coord-atlas"`
	ID     int64  `json:"id" jsonschema:"ticket id"`
	To     string `json:"to,omitempty" jsonschema:"session to pass the ticket to; empty releases it back to todo"`
	Reason string `json:"reason" jsonschema:"why, kept in the journal"`
}

type noteIn struct {
	As    string   `json:"as" jsonschema:"your stable session name, e.g. coord-atlas"`
	ID    int64    `json:"id" jsonschema:"ticket id"`
	Text  string   `json:"text,omitempty" jsonschema:"note for the journal: progress, question, summary"`
	State string   `json:"state,omitempty" jsonschema:"optional new state: blocked, in_progress, to_validate, done, dropped, or todo (triage)"`
	Links []string `json:"links,omitempty" jsonschema:"links to attach (PR, branch, doc)"`
}

type addIn struct {
	As       string   `json:"as" jsonschema:"your stable session name, e.g. coord-atlas"`
	Title    string   `json:"title" jsonschema:"short title"`
	Project  string   `json:"project,omitempty" jsonschema:"project slug; required for tasks, optional for reminders"`
	Kind     string   `json:"kind,omitempty" jsonschema:"task (default) or reminder (a task of the owner that you only surface)"`
	Details  string   `json:"details,omitempty" jsonschema:"markdown details"`
	Priority *int     `json:"priority,omitempty" jsonschema:"0 (urgent) to 3; default 2"`
	Due      string   `json:"due,omitempty" jsonschema:"due date YYYY-MM-DD"`
	ParentID int64    `json:"parent_id,omitempty" jsonschema:"parent ticket id, to make a sub-ticket"`
	Todo     bool     `json:"todo,omitempty" jsonschema:"put it straight in todo (the owner may have to approve)"`
	Links    []string `json:"links,omitempty" jsonschema:"links to attach"`
}

func (s *Server) registerTools() {
	mcp.AddTool(s.mcp, &mcp.Tool{Name: "board_projects", Description: "List the board's projects with their mode and open ticket counts. Creating a project is only for when the owner asked for it."},
		func(ctx context.Context, req *mcp.CallToolRequest, in projectsIn) (*mcp.CallToolResult, projectsOut, error) {
			c, err := s.caller(ctx, req, in.As)
			if err != nil {
				return nil, projectsOut{}, err
			}
			var out projectsOut
			if in.CreateSlug != "" {
				created, err := s.svc.CreateProject(ctx, c, in.CreateSlug, in.CreateName)
				if err != nil {
					return nil, projectsOut{}, err
				}
				out.Created = &created
			}
			out.Projects, err = s.svc.Projects(ctx)
			return nil, out, err
		})

	mcp.AddTool(s.mcp, &mcp.Tool{Name: "board_list", Description: "List tickets (without details), most pressing first. Closed tickets are hidden unless asked."},
		func(ctx context.Context, req *mcp.CallToolRequest, in listIn) (*mcp.CallToolResult, listOut, error) {
			c, err := s.caller(ctx, req, in.As)
			if err != nil {
				return nil, listOut{}, err
			}
			f := board.ListFilter{Project: in.Project, State: board.State(in.State), Owner: in.Owner, Kind: board.Kind(in.Kind), IncludeClosed: in.IncludeClosed}
			if in.Mine {
				f.Owner = c.Name
			}
			tickets, err := s.svc.List(ctx, f)
			return nil, listOut{Tickets: tickets}, err
		})

	mcp.AddTool(s.mcp, &mcp.Tool{Name: "board_show", Description: "Show one ticket with its details, journal, circle, sub-tickets and pending requests."},
		func(ctx context.Context, req *mcp.CallToolRequest, in showIn) (*mcp.CallToolResult, board.TicketDetail, error) {
			if _, err := s.caller(ctx, req, in.As); err != nil {
				return nil, board.TicketDetail{}, err
			}
			d, err := s.svc.Show(ctx, in.ID)
			return nil, d, err
		})

	mcp.AddTool(s.mcp, &mcp.Tool{Name: "board_whatsnew", Description: "What others did since your last look at this project (or the whole board), plus your tickets, silent tickets, reminders due and your pending requests."},
		func(ctx context.Context, req *mcp.CallToolRequest, in whatsNewIn) (*mcp.CallToolResult, board.WhatsNew, error) {
			c, err := s.caller(ctx, req, in.As)
			if err != nil {
				return nil, board.WhatsNew{}, err
			}
			w, err := s.svc.WhatsNew(ctx, c, in.Project)
			return nil, w, err
		})

	mcp.AddTool(s.mcp, &mcp.Tool{Name: "board_next", Description: "Take the most urgent free todo task of a project (overdue first, then priority) and mark it in progress for you."},
		func(ctx context.Context, req *mcp.CallToolRequest, in nextIn) (*mcp.CallToolResult, board.Outcome, error) {
			return s.outcome(ctx, req, in.As, func(c board.Caller) (board.Outcome, error) {
				return s.svc.Next(ctx, c, in.Project, in.For)
			})
		})

	mcp.AddTool(s.mcp, &mcp.Tool{Name: "board_claim", Description: "Take a specific todo ticket, for yourself or for another session."},
		func(ctx context.Context, req *mcp.CallToolRequest, in claimIn) (*mcp.CallToolResult, board.Outcome, error) {
			return s.outcome(ctx, req, in.As, func(c board.Caller) (board.Outcome, error) {
				return s.svc.Claim(ctx, c, in.ID, in.For)
			})
		})

	mcp.AddTool(s.mcp, &mcp.Tool{Name: "board_assign", Description: "Pass a ticket someone is working on to another session, or release it back to todo. A reason is required."},
		func(ctx context.Context, req *mcp.CallToolRequest, in assignIn) (*mcp.CallToolResult, board.Outcome, error) {
			return s.outcome(ctx, req, in.As, func(c board.Caller) (board.Outcome, error) {
				return s.svc.Assign(ctx, c, in.ID, in.To, in.Reason)
			})
		})

	mcp.AddTool(s.mcp, &mcp.Tool{Name: "board_note", Description: "Write in a ticket's journal and/or change its state (blocked with your question, to_validate with your summary and PR)."},
		func(ctx context.Context, req *mcp.CallToolRequest, in noteIn) (*mcp.CallToolResult, board.Outcome, error) {
			return s.outcome(ctx, req, in.As, func(c board.Caller) (board.Outcome, error) {
				return s.svc.Note(ctx, c, in.ID, in.Text, board.State(in.State), in.Links)
			})
		})

	mcp.AddTool(s.mcp, &mcp.Tool{Name: "board_add", Description: "Add a task (lands in the owner's inbox unless todo is allowed), a reminder for the owner, or a sub-ticket."},
		func(ctx context.Context, req *mcp.CallToolRequest, in addIn) (*mcp.CallToolResult, board.Outcome, error) {
			return s.outcome(ctx, req, in.As, func(c board.Caller) (board.Outcome, error) {
				return s.svc.Add(ctx, c, board.AddInput{
					Project: in.Project, Kind: board.Kind(in.Kind), Title: in.Title, Details: in.Details,
					Priority: in.Priority, Due: in.Due, ParentID: in.ParentID, Todo: in.Todo, Links: in.Links,
				})
			})
		})
}

func (s *Server) outcome(ctx context.Context, req *mcp.CallToolRequest, as string, fn func(board.Caller) (board.Outcome, error)) (*mcp.CallToolResult, board.Outcome, error) {
	c, err := s.caller(ctx, req, as)
	if err != nil {
		return nil, board.Outcome{}, err
	}
	out, err := fn(c)
	s.log.Info("tool", "tool", req.Params.Name, "actor", c.Name, "identity", c.Identity, "status", out.Status, "err", err)
	return nil, out, err
}

func (s *Server) caller(ctx context.Context, req *mcp.CallToolRequest, as string) (board.Caller, error) {
	key := identity.Terminal
	if req.Extra != nil {
		if h := req.Extra.Header.Get(identityHeader); h != "" {
			key = h
		}
	}
	c, pending, err := s.svc.Bind(ctx, key, key == identity.Terminal, as)
	if err != nil {
		return board.Caller{}, err
	}
	if pending != nil {
		return board.Caller{}, fmt.Errorf("name %q is held by another live session; request #%d asks %s to let you take it. Pick another name, or wait",
			as, pending.Request.ID, s.svc.Settings().OwnerName)
	}
	return c, nil
}
