package board

import (
	"errors"
	"fmt"
	"regexp"
)

type State string

const (
	StateInbox      State = "inbox"
	StateTodo       State = "todo"
	StateInProgress State = "in_progress"
	StateBlocked    State = "blocked"
	StateToValidate State = "to_validate"
	StateDone       State = "done"
	StateDropped    State = "dropped"
)

func (s State) valid() bool {
	switch s {
	case StateInbox, StateTodo, StateInProgress, StateBlocked, StateToValidate, StateDone, StateDropped:
		return true
	}
	return false
}

func (s State) closed() bool { return s == StateDone || s == StateDropped }

// owned reports the states in which a ticket has someone working on it.
func (s State) owned() bool {
	return s == StateInProgress || s == StateBlocked || s == StateToValidate
}

type Kind string

const (
	KindTask Kind = "task"
	// KindReminder is one of the owner's own tasks: sessions surface it, never execute it.
	KindReminder Kind = "reminder"
)

type Ticket struct {
	ID        int64    `json:"id"`
	Project   string   `json:"project,omitempty"`
	Kind      Kind     `json:"kind"`
	Title     string   `json:"title"`
	Details   string   `json:"details,omitempty"`
	Priority  int      `json:"priority"`
	Due       string   `json:"due,omitempty"`
	State     State    `json:"state"`
	Owner     string   `json:"owner,omitempty"`
	ParentID  int64    `json:"parent_id,omitempty"`
	Links     []string `json:"links,omitempty"`
	CreatedBy string   `json:"created_by"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
	Overdue   bool     `json:"overdue,omitempty"`
	Silent    bool     `json:"silent,omitempty"`

	OwnerActivityAt string `json:"owner_activity_at,omitempty"`
}

type Event struct {
	ID       int64  `json:"id"`
	TicketID int64  `json:"ticket_id,omitempty"`
	At       string `json:"at"`
	Actor    string `json:"actor"`
	Kind     string `json:"kind"`
	Body     string `json:"body,omitempty"`
	Data     string `json:"data,omitempty"`
	Proof    string `json:"proof,omitempty"`
}

type Project struct {
	Slug  string        `json:"slug"`
	Name  string        `json:"name"`
	Mode  string        `json:"mode"`
	Count map[State]int `json:"open_counts,omitempty"`
}

type RequestState string

const (
	RequestPending  RequestState = "pending"
	RequestApproved RequestState = "approved"
	RequestRefused  RequestState = "refused"
	RequestFailed   RequestState = "failed"
)

// Request is an action waiting for the owner. It grants nothing by itself:
// the action only runs at the moment the owner approves it.
type Request struct {
	ID        int64        `json:"id"`
	TicketID  int64        `json:"ticket_id,omitempty"`
	Action    Action       `json:"action"`
	Actor     string       `json:"actor"`
	Summary   string       `json:"summary"`
	State     RequestState `json:"state"`
	Outcome   string       `json:"outcome,omitempty"`
	CreatedAt string       `json:"created_at"`
	DecidedAt string       `json:"decided_at,omitempty"`

	identity string
	op       string
}

const (
	StatusOK              = "ok"
	StatusPendingApproval = "pending_approval"
	StatusFailed          = "failed"
	StatusRefused         = "refused"
)

type Outcome struct {
	Status  string   `json:"status"`
	Message string   `json:"message,omitempty"`
	Ticket  *Ticket  `json:"ticket,omitempty"`
	Request *Request `json:"request,omitempty"`
}

// Caller is the verified actor behind a call. Identity comes from the
// operating system, never from the client; Name is the session name bound
// to that identity.
type Caller struct {
	Identity string
	Name     string
	Terminal bool
	// Vouched means the owner gave their word for this call: they are at
	// their terminal, or a session acts on what they just asked in the
	// conversation. An action that needs the owner then applies directly.
	Vouched bool
}

// TerminalName is the actor recorded for calls that no Claude session owns
// (the owner's terminal, or any detached process). It is never part of a
// ticket's circle, so everything beyond free actions goes through approval.
const TerminalName = "terminal"

var (
	ErrNotFound   = errors.New("not found")
	ErrInvalid    = errors.New("invalid")
	ErrConflict   = errors.New("conflict")
	ErrDenied     = errors.New("denied")
	ErrRateLimit  = errors.New("rate limited")
	ErrNameInUse  = errors.New("name in use")
	ErrNotPending = errors.New("request is not pending")
	ErrNeedsOwner = errors.New("needs the owner's word")
)

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

var (
	slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,47}$`)
	datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

const (
	maxTitle   = 200
	maxText    = 20_000
	maxLinks   = 20
	maxLinkLen = 500
)
