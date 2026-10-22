package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"board/internal/board"
	"board/internal/config"
	"board/internal/server"
)

type fakeApp struct {
	approve bool
	asked   []string
	notes   []string
	options *server.Options
}

func (f *fakeApp) ConfirmSettings(_ context.Context, reason string) (bool, error) {
	f.asked = append(f.asked, reason)
	return f.approve, nil
}

func (f *fakeApp) Notify(title, _, body string) { f.notes = append(f.notes, title+" : "+body) }

func (f *fakeApp) SetOptions(o server.Options) { f.options = &o }

type fakeBoard struct{ settings *board.Settings }

func (f *fakeBoard) SetSettings(s board.Settings) error {
	f.settings = &s
	return s.Validate()
}

func newWatcher(t *testing.T, approve bool) (*configWatcher, *fakeApp, *fakeBoard) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(config.Template), 0o600); err != nil {
		t.Fatal(err)
	}
	app, b := &fakeApp{approve: approve}, &fakeBoard{}
	return &configWatcher{path: path, home: "/h", current: config.Defaults("/h"), seen: []byte(config.Template), app: app, board: b}, app, b
}

func (w *configWatcher) write(t *testing.T, content string) {
	t.Helper()
	if err := os.WriteFile(w.path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestApprovedEditIsApplied(t *testing.T) {
	w, app, b := newWatcher(t, true)
	w.write(t, "default_mode = \"demande\"\n[approvals]\nprompt = \"always\"\n")
	w.check(context.Background())
	if len(app.asked) != 1 || !strings.Contains(app.asked[0], "mode par défaut : auto → demande") {
		t.Fatalf("the owner must see the difference, got %q", app.asked)
	}
	if b.settings == nil || b.settings.DefaultMode != "demande" || app.options == nil || app.options.Prompt != server.PromptAlways {
		t.Fatalf("approved settings must be applied: %+v %+v", b.settings, app.options)
	}
	w.check(context.Background())
	if len(app.asked) != 1 {
		t.Fatal("an unchanged file is not proposed twice")
	}
}

func TestRefusedEditIsNotProposedAgainUntilTheFileChanges(t *testing.T) {
	w, app, b := newWatcher(t, false)
	w.write(t, "default_mode = \"tout_permis\"\n")
	w.check(context.Background())
	w.check(context.Background())
	if len(app.asked) != 1 || b.settings != nil || !strings.Contains(strings.Join(app.notes, "\n"), "non appliqués") {
		t.Fatalf("asked %q notes %q settings %+v", app.asked, app.notes, b.settings)
	}
	w.write(t, "default_mode = \"tout_permis\"\n# encore\n")
	w.check(context.Background())
	if len(app.asked) != 2 {
		t.Fatal("a new edit is proposed again")
	}
}

func TestInvalidOrCosmeticEditsNeverPrompt(t *testing.T) {
	w, app, _ := newWatcher(t, true)
	w.write(t, "defaut_mode = \"auto\"\n")
	w.check(context.Background())
	if len(app.asked) != 0 || !strings.Contains(strings.Join(app.notes, "\n"), "invalide") {
		t.Fatalf("an invalid file is reported, never proposed: %q %q", app.asked, app.notes)
	}
	w.write(t, config.Template+"\n# juste un commentaire\n")
	w.check(context.Background())
	if len(app.asked) != 0 {
		t.Fatalf("a comment-only edit changes nothing, got %q", app.asked)
	}
}

func TestAgentPlistIsValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), agentLabel+".plist")
	content := agentPlist("/Users/a & b/.local/bin/board", "/Users/a & b/.board", "127.0.0.1:7391")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("plutil", "-lint", path).CombinedOutput(); err != nil {
		t.Fatalf("plutil: %v %s", err, out)
	}
	if !strings.Contains(content, "/Users/a &amp; b/.board/daemon.log") || !strings.Contains(content, "<string>Aqua</string>") {
		t.Fatalf("paths must be escaped and the agent bound to the GUI session:\n%s", content)
	}
}
