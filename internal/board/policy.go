package board

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

type Action string

const (
	ActAdd           Action = "add"
	ActNote          Action = "note"
	ActProgress      Action = "progress"
	ActTriage        Action = "triage"
	ActTake          Action = "take"
	ActReassign      Action = "reassign"
	ActClose         Action = "close"
	ActDrop          Action = "drop"
	ActCreateProject Action = "create_project"
	ActRebindName    Action = "rebind_name"
)

var allActions = []Action{
	ActAdd, ActNote, ActProgress, ActTriage, ActTake, ActReassign,
	ActClose, ActDrop, ActCreateProject, ActRebindName,
}

type Rule string

const (
	RuleFree Rule = "free"
	// RuleCircle lets the ticket's circle act alone; anyone else needs approval.
	RuleCircle   Rule = "circle"
	RuleApproval Rule = "approval"
	RuleDeny     Rule = "deny"
)

func (r Rule) valid() bool {
	return r == RuleFree || r == RuleCircle || r == RuleApproval || r == RuleDeny
}

// Settings holds every tunable decision. Defaults reproduce the choices
// arbitrated on 2026-10-22; nothing here is hardcoded elsewhere.
type Settings struct {
	OwnerName       string
	DefaultMode     string
	ProjectModes    map[string]string
	Modes           map[string]map[Action]Rule
	SilenceAfter    time.Duration
	ReminderLead    time.Duration
	ReservedNames   []string
	WritesPerMinute int
}

func DefaultSettings() Settings {
	demande := map[Action]Rule{
		ActAdd: RuleFree, ActNote: RuleFree, ActProgress: RuleCircle,
		ActTriage: RuleApproval, ActTake: RuleApproval, ActReassign: RuleApproval,
		ActClose: RuleApproval, ActDrop: RuleApproval,
		ActCreateProject: RuleApproval, ActRebindName: RuleApproval,
	}
	auto := cloneRules(demande)
	auto[ActTake] = RuleFree
	auto[ActReassign] = RuleCircle
	auto[ActClose] = RuleCircle
	auto[ActDrop] = RuleCircle
	toutPermis := cloneRules(auto)
	toutPermis[ActTriage] = RuleFree

	return Settings{
		OwnerName:   "Thibault",
		DefaultMode: "demande",
		Modes: map[string]map[Action]Rule{
			"demande":     demande,
			"auto":        auto,
			"tout_permis": toutPermis,
		},
		ProjectModes:    map[string]string{},
		SilenceAfter:    2 * time.Hour,
		ReminderLead:    24 * time.Hour,
		ReservedNames:   []string{TerminalName, "owner", "human", "thibault"},
		WritesPerMinute: 60,
	}
}

func cloneRules(m map[Action]Rule) map[Action]Rule {
	out := make(map[Action]Rule, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (s Settings) Validate() error {
	if _, ok := s.Modes[s.DefaultMode]; !ok {
		return fmt.Errorf("default mode %q is not defined", s.DefaultMode)
	}
	for name, rules := range s.Modes {
		for _, a := range allActions {
			r, ok := rules[a]
			if !ok {
				return fmt.Errorf("mode %q has no rule for %q", name, a)
			}
			if !r.valid() {
				return fmt.Errorf("mode %q: unknown rule %q for %q", name, r, a)
			}
		}
	}
	for project, mode := range s.ProjectModes {
		if _, ok := s.Modes[mode]; !ok {
			return fmt.Errorf("project %q uses undefined mode %q", project, mode)
		}
	}
	if s.SilenceAfter <= 0 || s.ReminderLead < 0 || s.WritesPerMinute <= 0 {
		return fmt.Errorf("silence delay, reminder lead and write rate must be positive")
	}
	return nil
}

func (s Settings) modeOf(project string) string {
	if m, ok := s.ProjectModes[project]; ok {
		return m
	}
	return s.DefaultMode
}

func (s Settings) rule(project string, a Action) Rule {
	return s.Modes[s.modeOf(project)][a]
}

func (s Settings) reserved(name string) bool {
	return slices.Contains(s.ReservedNames, strings.ToLower(name))
}

type decision int

const (
	allow decision = iota
	needApproval
	deny
)

func decide(r Rule, inCircle bool) decision {
	switch r {
	case RuleFree:
		return allow
	case RuleCircle:
		if inCircle {
			return allow
		}
		return needApproval
	case RuleApproval:
		return needApproval
	default:
		return deny
	}
}

// transitionAction maps a state change requested through a note to the
// action the policy governs. Taking and releasing go through claim/assign.
func transitionAction(kind Kind, from, to State) (Action, error) {
	if from == to {
		return "", invalidf("ticket is already %s", to)
	}
	if kind == KindReminder {
		switch {
		case to == StateDone && !from.closed():
			return ActClose, nil
		case to == StateDropped && !from.closed():
			return ActDrop, nil
		case to == StateTodo && from.closed():
			return ActTriage, nil
		}
		return "", invalidf("a reminder only moves between todo, done and dropped")
	}
	switch to {
	case StateTodo:
		if from == StateInbox || from.closed() {
			return ActTriage, nil
		}
		if from.owned() {
			return "", invalidf("use board_assign with an empty `to` to release a ticket")
		}
	case StateInProgress:
		if from == StateBlocked || from == StateToValidate {
			return ActProgress, nil
		}
		if from == StateTodo {
			return "", invalidf("use board_claim or board_next to take a ticket")
		}
	case StateBlocked, StateToValidate:
		if from.owned() {
			return ActProgress, nil
		}
	case StateDone:
		if from.owned() {
			return ActClose, nil
		}
	case StateDropped:
		if !from.closed() {
			return ActDrop, nil
		}
	}
	return "", invalidf("cannot move a ticket from %s to %s", from, to)
}
