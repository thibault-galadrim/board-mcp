// Package config reads ~/.board/config.toml, the single place where every
// board decision is tuned.
package config

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"board/internal/board"
	"board/internal/server"
)

type Config struct {
	Board          board.Settings
	Server         server.Options
	ClaudePrefixes []string
	BiometricsOnly bool
}

func Defaults(home string) Config {
	return Config{
		Board:          board.DefaultSettings(),
		Server:         server.DefaultOptions(),
		ClaudePrefixes: []string{filepath.Join(home, ".local/share/claude/versions") + "/"},
	}
}

type duration struct{ time.Duration }

func (d *duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	d.Duration = v
	return err
}

// file mirrors config.toml; a missing key keeps its default.
type file struct {
	OwnerName   *string                      `toml:"owner_name"`
	DefaultMode *string                      `toml:"default_mode"`
	Projects    map[string]string            `toml:"projects"`
	Modes       map[string]map[string]string `toml:"modes"`
	Timing      struct {
		SilenceAfter    *duration `toml:"silence_after"`
		ReminderLead    *duration `toml:"reminder_lead"`
		WritesPerMinute *int      `toml:"writes_per_minute"`
	} `toml:"timing"`
	Approvals struct {
		Prompt         *string   `toml:"prompt"`
		Wait           *duration `toml:"wait"`
		UnlockAtStart  *bool     `toml:"unlock_at_start"`
		UnlockWait     *duration `toml:"unlock_wait"`
		BiometricsOnly *bool     `toml:"biometrics_only"`
	} `toml:"approvals"`
	Notifications struct {
		Blocked        *bool `toml:"blocked"`
		ToValidate     *bool `toml:"to_validate"`
		Done           *bool `toml:"done"`
		Inbox          *bool `toml:"inbox"`
		PendingRequest *bool `toml:"pending_request"`
		Deadline       *bool `toml:"deadline"`
		DeadlineHour   *int  `toml:"deadline_hour"`
	} `toml:"notifications"`
	Identity struct {
		ClaudePrefixes []string `toml:"claude_prefixes"`
	} `toml:"identity"`
}

func set[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}

func setDuration(dst *time.Duration, src *duration) {
	if src != nil {
		*dst = src.Duration
	}
}

// Parse reads a config file over the defaults. Unknown keys are errors: a
// typo must not silently leave a setting at its default.
func Parse(data []byte, home string) (Config, error) {
	var f file
	md, err := toml.NewDecoder(bytes.NewReader(data)).Decode(&f)
	if err != nil {
		return Config{}, err
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return Config{}, fmt.Errorf("unknown keys: %s", strings.Join(keys, ", "))
	}

	c := Defaults(home)
	b := &c.Board
	set(&b.OwnerName, f.OwnerName)
	set(&b.DefaultMode, f.DefaultMode)
	maps.Copy(b.ProjectModes, f.Projects)
	known := board.Actions()
	for mode, rules := range f.Modes {
		target, ok := b.Modes[mode]
		if !ok {
			target = map[board.Action]board.Rule{}
			b.Modes[mode] = target
		}
		for action, rule := range rules {
			if !slices.Contains(known, board.Action(action)) {
				return Config{}, fmt.Errorf("modes.%s: unknown action %q", mode, action)
			}
			target[board.Action(action)] = board.Rule(rule)
		}
	}
	setDuration(&b.SilenceAfter, f.Timing.SilenceAfter)
	setDuration(&b.ReminderLead, f.Timing.ReminderLead)
	set(&b.WritesPerMinute, f.Timing.WritesPerMinute)

	o := &c.Server
	if f.Approvals.Prompt != nil {
		o.Prompt = server.PromptPolicy(*f.Approvals.Prompt)
	}
	setDuration(&o.ApprovalWait, f.Approvals.Wait)
	set(&o.UnlockAtStart, f.Approvals.UnlockAtStart)
	setDuration(&o.UnlockWait, f.Approvals.UnlockWait)
	set(&c.BiometricsOnly, f.Approvals.BiometricsOnly)
	n := &o.Notify
	set(&n.Blocked, f.Notifications.Blocked)
	set(&n.ToValidate, f.Notifications.ToValidate)
	set(&n.Done, f.Notifications.Done)
	set(&n.Inbox, f.Notifications.Inbox)
	set(&n.PendingRequest, f.Notifications.PendingRequest)
	set(&n.Deadline, f.Notifications.Deadline)
	set(&n.DeadlineHour, f.Notifications.DeadlineHour)
	if len(f.Identity.ClaudePrefixes) > 0 {
		c.ClaudePrefixes = nil
		for _, p := range f.Identity.ClaudePrefixes {
			if rest, ok := strings.CutPrefix(p, "~/"); ok {
				p = filepath.Join(home, rest) + "/"
			}
			c.ClaudePrefixes = append(c.ClaudePrefixes, p)
		}
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if err := c.Board.Validate(); err != nil {
		return err
	}
	switch c.Server.Prompt {
	case server.PromptOwner, server.PromptAlways, server.PromptNever:
	default:
		return fmt.Errorf("approvals.prompt must be owner, always or never, got %q", c.Server.Prompt)
	}
	if c.Server.ApprovalWait <= 0 || c.Server.UnlockWait <= 0 {
		return fmt.Errorf("approvals.wait and approvals.unlock_wait must be positive")
	}
	if h := c.Server.Notify.DeadlineHour; h < 0 || h > 23 {
		return fmt.Errorf("notifications.deadline_hour must be between 0 and 23, got %d", h)
	}
	for _, p := range c.ClaudePrefixes {
		if !filepath.IsAbs(p) {
			return fmt.Errorf("identity.claude_prefixes must be absolute paths, got %q", p)
		}
	}
	return nil
}

// Load reads the config file, writing the commented template first when it
// does not exist yet.
func Load(path, home string) (Config, []byte, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		data = []byte(Template)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return Config{}, nil, err
		}
	} else if err != nil {
		return Config{}, nil, err
	}
	c, err := Parse(data, home)
	return c, data, err
}

func yesNo(b bool) string {
	if b {
		return "oui"
	}
	return "non"
}

// Diff lists, in the owner's words, what changes from old to next. Lines
// marked "(au redémarrage)" only take effect when the daemon restarts.
func Diff(old, next Config) []string {
	var out []string
	add := func(label string, a, b any) {
		if fmt.Sprint(a) != fmt.Sprint(b) {
			out = append(out, fmt.Sprintf("%s : %v → %v", label, a, b))
		}
	}
	ob, nb := old.Board, next.Board
	add("propriétaire", ob.OwnerName, nb.OwnerName)
	add("mode par défaut", ob.DefaultMode, nb.DefaultMode)
	for _, p := range sortedKeys(ob.ProjectModes, nb.ProjectModes) {
		add("projet "+p, orDefault(ob.ProjectModes[p]), orDefault(nb.ProjectModes[p]))
	}
	for _, m := range sortedKeys(ob.Modes, nb.Modes) {
		for _, a := range board.Actions() {
			add("mode "+m+", "+string(a), ob.Modes[m][a], nb.Modes[m][a])
		}
	}
	add("silence après", ob.SilenceAfter, nb.SilenceAfter)
	add("avance des rappels", ob.ReminderLead, nb.ReminderLead)
	add("écritures par minute", ob.WritesPerMinute, nb.WritesPerMinute)
	oo, no := old.Server, next.Server
	add("invites", oo.Prompt, no.Prompt)
	add("attente pendant l'appel", oo.ApprovalWait, no.ApprovalWait)
	add("verrou au démarrage (au redémarrage)", yesNo(oo.UnlockAtStart), yesNo(no.UnlockAtStart))
	add("attente du déverrouillage", oo.UnlockWait, no.UnlockWait)
	add("Touch ID seul (au redémarrage)", yesNo(old.BiometricsOnly), yesNo(next.BiometricsOnly))
	on, nn := oo.Notify, no.Notify
	add("notif bloqué", yesNo(on.Blocked), yesNo(nn.Blocked))
	add("notif à valider", yesNo(on.ToValidate), yesNo(nn.ToValidate))
	add("notif fait", yesNo(on.Done), yesNo(nn.Done))
	add("notif trouvaille", yesNo(on.Inbox), yesNo(nn.Inbox))
	add("notif demande en attente", yesNo(on.PendingRequest), yesNo(nn.PendingRequest))
	add("notif échéance", yesNo(on.Deadline), yesNo(nn.Deadline))
	add("heure des échéances", on.DeadlineHour, nn.DeadlineHour)
	add("binaires Claude (au redémarrage)", strings.Join(old.ClaudePrefixes, ", "), strings.Join(next.ClaudePrefixes, ", "))
	return out
}

func orDefault(mode string) string {
	if mode == "" {
		return "défaut"
	}
	return mode
}

func sortedKeys[V any](a, b map[string]V) []string {
	keys := slices.Collect(maps.Keys(a))
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}

// Template is written on first start. Every key is optional.
const Template = `# Réglages du board.
# Une modification pendant que le démon tourne t'est présentée avec
# Touch ID avant d'être appliquée. Une clé absente garde sa valeur par
# défaut, indiquée en commentaire.

# owner_name = "Thibault"

# Mode par défaut des projets : demande, auto ou tout_permis.
default_mode = "auto"

# Mode d'un projet, à la place du mode par défaut.
[projects]
# atlas = "demande"

[timing]
# silence_after = "2h"        # un ticket en cours sans note devient silencieux
# reminder_lead = "24h"       # avance des rappels dans le « quoi de neuf »
# writes_per_minute = 60      # plafond d'écritures par session

[approvals]
# prompt = "owner"            # owner : invite pour ce que tu déclenches ; always ; never
# wait = "45s"                # attente de ta réponse pendant l'appel d'une session
# unlock_at_start = true      # board fermé jusqu'à ton Touch ID au démarrage
# unlock_wait = "2m"
# biometrics_only = false     # true : Touch ID seul, sans repli sur le mot de passe

[notifications]
# blocked = true
# to_validate = true
# done = true
# inbox = true
# pending_request = true
# deadline = true
# deadline_hour = 9

[identity]
# claude_prefixes = ["~/.local/share/claude/versions/"]

# Matrice des modes : une règle par action (free, circle, approval, deny).
# Actions : add, declare, note, progress, triage, take, reassign, close,
# drop, move, create_project, rebind_name. Exemple :
# [modes.auto]
# close = "approval"
`
