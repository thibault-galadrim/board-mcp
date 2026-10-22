package main

import (
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
	notes   []string
	options *server.Options
}

func (f *fakeApp) Notify(title, _, body string) { f.notes = append(f.notes, title+" : "+body) }

func (f *fakeApp) SetOptions(o server.Options) { f.options = &o }

type fakeBoard struct{ settings *board.Settings }

func (f *fakeBoard) SetSettings(s board.Settings) error {
	f.settings = &s
	return s.Validate()
}

func newWatcher(t *testing.T) (*configWatcher, *fakeApp, *fakeBoard) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(config.Template), 0o600); err != nil {
		t.Fatal(err)
	}
	app, b := &fakeApp{}, &fakeBoard{}
	return &configWatcher{path: path, home: "/h", current: config.Defaults("/h"), seen: []byte(config.Template), app: app, board: b}, app, b
}

func (w *configWatcher) write(t *testing.T, content string) {
	t.Helper()
	if err := os.WriteFile(w.path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEditIsAppliedAndAnnounced(t *testing.T) {
	w, app, b := newWatcher(t)
	w.write(t, "default_mode = \"demande\"\n[approvals]\nqueue = true\n")
	w.check()
	if b.settings == nil || b.settings.DefaultMode != "demande" || !b.settings.QueueRequests || app.options == nil {
		t.Fatalf("an edit applies at once: %+v %+v", b.settings, app.options)
	}
	if len(app.notes) != 1 || !strings.Contains(app.notes[0], "mode par défaut : auto → demande") {
		t.Fatalf("the owner is told what changed, got %q", app.notes)
	}
	w.check()
	if len(app.notes) != 1 {
		t.Fatal("an unchanged file is not announced twice")
	}
}

func TestInvalidOrCosmeticEditsChangeNothing(t *testing.T) {
	w, app, b := newWatcher(t)
	w.write(t, "defaut_mode = \"auto\"\n")
	w.check()
	if b.settings != nil || len(app.notes) != 1 || !strings.Contains(app.notes[0], "invalide") {
		t.Fatalf("an invalid file is reported and ignored: %+v %q", b.settings, app.notes)
	}
	w.write(t, config.Template+"\n# juste un commentaire\n")
	w.check()
	if b.settings != nil || len(app.notes) != 1 {
		t.Fatalf("a comment-only edit changes nothing, got %q", app.notes)
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
