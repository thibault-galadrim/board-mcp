package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"board/internal/board"
)

type bearer struct {
	token string
	base  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

type client struct {
	cs *mcp.ClientSession
	as string
}

func connect(ctx context.Context) (*client, error) {
	home, err := boardHome()
	if err != nil {
		return nil, err
	}
	addr, err := boardAddr()
	if err != nil {
		return nil, err
	}
	token, err := readToken(home)
	if err != nil {
		return nil, err
	}
	transport := &mcp.StreamableClientTransport{
		Endpoint:   "http://" + addr + "/mcp",
		HTTPClient: &http.Client{Transport: bearer{token: token, base: http.DefaultTransport}, Timeout: 30 * time.Second},
		MaxRetries: 1,
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "board-cli", Version: "0.1.0"}, nil).Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot reach the daemon on %s: %w", addr, err)
	}
	return &client{cs: cs, as: "cli"}, nil
}

func (c *client) call(ctx context.Context, tool string, args map[string]any, out any) error {
	args["as"] = c.as
	res, err := c.cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return err
	}
	if res.IsError {
		for _, content := range res.Content {
			if t, ok := content.(*mcp.TextContent); ok {
				return errors.New(t.Text)
			}
		}
		return errors.New("tool error")
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// parseArgs accepts flags anywhere among the positional arguments.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func runCLI(cmd string, args []string) error {
	if handled, err := runApprovals(cmd, args); handled {
		return err
	}
	ctx := context.Background()
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	switch cmd {
	case "projects":
		c, err := connect(ctx)
		if err != nil {
			return err
		}
		var out struct {
			Projects []board.Project `json:"projects"`
		}
		if err := c.call(ctx, "board_projects", map[string]any{}, &out); err != nil {
			return err
		}
		for _, p := range out.Projects {
			counts := []string{}
			for _, s := range []board.State{board.StateInbox, board.StateTodo, board.StateInProgress, board.StateBlocked, board.StateToValidate} {
				if n := p.Count[s]; n > 0 {
					counts = append(counts, fmt.Sprintf("%d %s", n, label(s)))
				}
			}
			fmt.Printf("%-12s %-24s mode %-12s %s\n", p.Slug, p.Name, p.Mode, strings.Join(counts, ", "))
		}
		return nil

	case "project-add":
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if len(pos) < 2 {
			return errors.New("usage: board project-add <slug> <nom>")
		}
		c, err := connect(ctx)
		if err != nil {
			return err
		}
		var out struct {
			Created *board.Outcome `json:"created"`
		}
		if err := c.call(ctx, "board_projects", map[string]any{"create_slug": pos[0], "create_name": strings.Join(pos[1:], " ")}, &out); err != nil {
			return err
		}
		printOutcome(*out.Created)
		return nil

	case "ls":
		project := fs.String("p", "", "projet")
		state := fs.String("s", "", "état")
		owner := fs.String("o", "", "session")
		kind := fs.String("k", "", "task ou reminder")
		all := fs.Bool("a", false, "inclure fait et abandonné")
		if _, err := parseArgs(fs, args); err != nil {
			return err
		}
		c, err := connect(ctx)
		if err != nil {
			return err
		}
		var out struct {
			Tickets []board.Ticket `json:"tickets"`
		}
		if err := c.call(ctx, "board_list", map[string]any{"project": *project, "state": *state, "owner": *owner, "kind": *kind, "include_closed": *all}, &out); err != nil {
			return err
		}
		if len(out.Tickets) == 0 {
			fmt.Println("aucun ticket")
		}
		for _, t := range out.Tickets {
			fmt.Println(line(t))
		}
		return nil

	case "show":
		id, err := ticketID(fs, args)
		if err != nil {
			return err
		}
		c, err := connect(ctx)
		if err != nil {
			return err
		}
		var d board.TicketDetail
		if err := c.call(ctx, "board_show", map[string]any{"id": id}, &d); err != nil {
			return err
		}
		printDetail(d)
		return nil

	case "news":
		project := fs.String("p", "", "projet")
		if _, err := parseArgs(fs, args); err != nil {
			return err
		}
		c, err := connect(ctx)
		if err != nil {
			return err
		}
		var w board.WhatsNew
		if err := c.call(ctx, "board_whatsnew", map[string]any{"project": *project}, &w); err != nil {
			return err
		}
		printNews(w)
		return nil

	case "add":
		project := fs.String("p", "", "projet")
		priority := fs.Int("P", 2, "priorité 0 (urgent) à 3")
		due := fs.String("d", "", "échéance AAAA-MM-JJ")
		details := fs.String("m", "", "détails")
		reminder := fs.Bool("r", false, "rappel pour toi")
		todo := fs.Bool("t", false, "directement à faire")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return errors.New("usage: board add [-p projet] <titre>")
		}
		kind := board.KindTask
		if *reminder {
			kind = board.KindReminder
		}
		c, err := connect(ctx)
		if err != nil {
			return err
		}
		var out board.Outcome
		if err := c.call(ctx, "board_add", map[string]any{"project": *project, "title": strings.Join(pos, " "), "priority": *priority,
			"due": *due, "details": *details, "kind": kind, "todo": *todo}, &out); err != nil {
			return err
		}
		printOutcome(out)
		return nil

	case "note":
		state := fs.String("s", "", "nouvel état")
		link := fs.String("l", "", "lien")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return errors.New("usage: board note <id> [-s état] <texte>")
		}
		id, err := strconv.ParseInt(pos[0], 10, 64)
		if err != nil {
			return fmt.Errorf("ticket id %q: %w", pos[0], err)
		}
		args := map[string]any{"id": id, "text": strings.Join(pos[1:], " "), "state": *state}
		if *link != "" {
			args["links"] = []string{*link}
		}
		return callOutcome(ctx, "board_note", args)

	case "claim":
		forName := fs.String("for", "", "session qui fera le travail")
		id, err := ticketID(fs, args)
		if err != nil {
			return err
		}
		return callOutcome(ctx, "board_claim", map[string]any{"id": id, "for": *forName})

	case "assign":
		to := fs.String("to", "", "session qui reprend (vide : relâcher)")
		reason := fs.String("r", "", "motif")
		id, err := ticketID(fs, args)
		if err != nil {
			return err
		}
		return callOutcome(ctx, "board_assign", map[string]any{"id": id, "to": *to, "reason": *reason})
	}
	return fmt.Errorf("unknown command %q (board help)", cmd)
}

func ticketID(fs *flag.FlagSet, args []string) (int64, error) {
	pos, err := parseArgs(fs, args)
	if err != nil {
		return 0, err
	}
	if len(pos) != 1 {
		return 0, fmt.Errorf("usage: board %s <id>", fs.Name())
	}
	return strconv.ParseInt(pos[0], 10, 64)
}

func callOutcome(ctx context.Context, tool string, args map[string]any) error {
	c, err := connect(ctx)
	if err != nil {
		return err
	}
	var out board.Outcome
	if err := c.call(ctx, tool, args, &out); err != nil {
		return err
	}
	printOutcome(out)
	return nil
}

var labels = map[board.State]string{
	board.StateInbox: "à trier", board.StateTodo: "à faire", board.StateInProgress: "en cours", board.StateBlocked: "bloqué",
	board.StateToValidate: "à valider", board.StateDone: "fait", board.StateDropped: "abandonné",
}

func label(s board.State) string {
	if l, ok := labels[s]; ok {
		return l
	}
	return string(s)
}

func line(t board.Ticket) string {
	project := t.Project
	if t.Kind == board.KindReminder {
		project = "rappel " + project
	}
	b := fmt.Sprintf("#%-4d P%d  %-10s %-16s %s", t.ID, t.Priority, label(t.State), project, t.Title)
	if t.Owner != "" {
		b += "  (" + t.Owner + ")"
	}
	if t.Due != "" {
		b += "  échéance " + t.Due
	}
	if t.Overdue {
		b += "  [en retard]"
	}
	if t.Silent {
		b += "  [silencieux]"
	}
	return b
}

func printOutcome(o board.Outcome) {
	switch o.Status {
	case board.StatusPendingApproval:
		fmt.Printf("en attente de ta validation : demande #%d, %s\n", o.Request.ID, o.Request.Summary)
	case board.StatusOK:
		if o.Ticket != nil {
			fmt.Println(line(*o.Ticket))
		}
		if o.Message != "" {
			fmt.Println(o.Message)
		}
	default:
		fmt.Printf("%s : %s\n", o.Status, o.Message)
	}
}

func localTime(stamp string) string {
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return stamp
	}
	return t.Local().Format("2006-01-02 15:04")
}

func printDetail(d board.TicketDetail) {
	fmt.Println(line(d.Ticket))
	if d.Ticket.Details != "" {
		fmt.Println()
		fmt.Println(d.Ticket.Details)
	}
	fmt.Println()
	fmt.Printf("cercle : %s\n", strings.Join(d.Circle, ", "))
	for _, l := range d.Ticket.Links {
		fmt.Printf("lien : %s\n", l)
	}
	for _, c := range d.Children {
		fmt.Println("  sous-ticket " + line(c))
	}
	for _, r := range d.Requests {
		fmt.Printf("demande #%d en attente (%s) : %s\n", r.ID, r.Actor, r.Summary)
	}
	fmt.Println()
	for _, e := range d.Events {
		printEvent(e)
	}
}

func printEvent(e board.Event) {
	proof := ""
	if e.Proof != "" {
		proof = " [" + e.Proof + "]"
	}
	ticket := ""
	if e.TicketID != 0 {
		ticket = fmt.Sprintf(" #%d", e.TicketID)
	}
	fmt.Printf("%s  %-14s %-16s%s%s %s\n", localTime(e.At), e.Actor, e.Kind, ticket, proof, e.Body)
}

func printNews(w board.WhatsNew) {
	if len(w.Events) == 0 {
		fmt.Println("rien de neuf")
	}
	for _, e := range w.Events {
		printEvent(e)
	}
	if w.Truncated {
		fmt.Println("(suite au prochain appel)")
	}
	for _, t := range w.Silent {
		fmt.Println("silencieux : " + line(t))
	}
	for _, t := range w.Reminders {
		fmt.Println("rappel : " + line(t))
	}
	if w.InboxCount > 0 {
		fmt.Printf("%d trouvaille(s) à trier\n", w.InboxCount)
	}
	if len(w.PendingRequests) > 0 {
		fmt.Fprintf(os.Stdout, "%d demande(s) en attente\n", len(w.PendingRequests))
	}
}
