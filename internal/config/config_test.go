package config

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"board/internal/board"
	"board/internal/server"
)

const home = "/Users/someone"

func TestTemplateParsesToTheDefaults(t *testing.T) {
	c, err := Parse([]byte(Template), home)
	if err != nil {
		t.Fatal(err)
	}
	if lines := Diff(Defaults(home), c); len(lines) != 0 {
		t.Fatalf("the template must describe the defaults, differs on %q", lines)
	}
	for _, a := range board.Actions() {
		if !strings.Contains(Template, string(a)) {
			t.Errorf("the template must list action %q", a)
		}
	}
}

func TestOverrides(t *testing.T) {
	c, err := Parse([]byte(`
default_mode = "demande"
[projects]
perso = "tout_permis"
[modes.auto]
close = "approval"
[timing]
silence_after = "90m"
[approvals]
prompt = "never"
biometrics_only = true
[notifications]
done = false
deadline_hour = 8
[identity]
claude_prefixes = ["~/bin/claude/"]
`), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.Board.DefaultMode != "demande" || c.Board.ProjectModes["perso"] != "tout_permis" ||
		c.Board.Modes["auto"][board.ActClose] != board.RuleApproval || c.Board.Modes["auto"][board.ActTake] != board.RuleFree {
		t.Fatalf("board settings: %+v", c.Board)
	}
	if c.Board.SilenceAfter != 90*time.Minute || c.Server.Prompt != server.PromptNever || !c.BiometricsOnly ||
		c.Server.Notify.Done || !c.Server.Notify.Blocked || c.Server.Notify.DeadlineHour != 8 {
		t.Fatalf("got %+v", c)
	}
	if !slices.Equal(c.ClaudePrefixes, []string{"/Users/someone/bin/claude/"}) {
		t.Fatalf("got %v", c.ClaudePrefixes)
	}
}

func TestInvalidFilesAreRejected(t *testing.T) {
	for name, content := range map[string]string{
		"typo":         "defaut_mode = \"auto\"",
		"unknown mode": "default_mode = \"yolo\"",
		"bad action":   "[modes.auto]\nclosee = \"free\"",
		"bad rule":     "[modes.auto]\nclose = \"maybe\"",
		"bad prompt":   "[approvals]\nprompt = \"sometimes\"",
		"bad hour":     "[notifications]\ndeadline_hour = 25",
		"bad duration": "[timing]\nsilence_after = \"soon\"",
		"new mode gap": "[modes.strict]\nclose = \"approval\"\n[projects]\natlas = \"strict\"",
		"syntax":       "default_mode = ",
	} {
		if _, err := Parse([]byte(content), home); err == nil {
			t.Errorf("%s: must be rejected", name)
		}
	}
}

func TestDiffSpeaksTheOwnersLanguage(t *testing.T) {
	old := Defaults(home)
	next, err := Parse([]byte("default_mode = \"demande\"\n[projects]\natlas = \"tout_permis\"\n[approvals]\nbiometrics_only = true"), home)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"mode par défaut : auto → demande",
		"projet atlas : défaut → tout_permis",
		"Touch ID seul (au redémarrage) : non → oui",
	}
	if got := Diff(old, next); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestLoadWritesTheTemplateOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if _, _, err := Load(path, home); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("template must be written owner-only: %v %v", info, err)
	}
	if err := os.WriteFile(path, []byte("default_mode = \"demande\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, data, err := Load(path, home)
	if err != nil || c.Board.DefaultMode != "demande" || string(data) != "default_mode = \"demande\"\n" {
		t.Fatalf("an existing file is read, never overwritten: %v %q", err, data)
	}
}
