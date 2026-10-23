package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"board/internal/board"
	"board/internal/touchid"
)

// slowApprover approves after a short pause, like a finger on the sensor.
type slowApprover struct{}

func (slowApprover) Confirm(ctx context.Context, _ string) (touchid.Decision, error) {
	select {
	case <-time.After(time.Second):
		return touchid.Approved, nil
	case <-ctx.Done():
		return touchid.Undecided, nil
	}
}

func (slowApprover) Proof() string { return "touchid" }

// TestPreview serves a seeded board for looking at the page in a browser.
// It only runs with BOARD_PREVIEW=1 and prints the sign-in link.
func TestPreview(t *testing.T) {
	if os.Getenv("BOARD_PREVIEW") != "1" {
		t.Skip("set BOARD_PREVIEW=1 to serve the preview")
	}
	ctx := context.Background()
	db, err := board.OpenDB(ctx, filepath.Join(t.TempDir(), "board.db"))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	shift := -3 * time.Hour
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return time.Now().Add(shift) }
	settings := board.DefaultSettings()
	settings.ProjectModes["perso"] = "auto"
	svc, err := board.NewService(db, settings, alive{}, now)
	if err != nil {
		t.Fatal(err)
	}

	owner := board.Caller{Identity: "terminal", Name: board.TerminalName, Terminal: true, Vouched: true}
	approve := func(out board.Outcome, err error) board.Outcome {
		if err != nil {
			t.Fatal(err)
		}
		if out.Status == board.StatusPendingApproval {
			if out, err = svc.Approve(ctx, out.Request.ID, "touchid"); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	approve(svc.CreateProject(ctx, owner, "atlas", "Atlas"))
	approve(svc.CreateProject(ctx, owner, "perso", "Galadrim perso"))
	approve(svc.CreateProject(ctx, owner, "orion", "Migration Orion"))
	coord := board.Caller{Identity: "claude:1", Name: "coord-atlas"}
	w1 := board.Caller{Identity: "claude:2", Name: "worker-planning"}
	w2 := board.Caller{Identity: "claude:3", Name: "fix-login"}
	todo := func(project, title string, prio int, due string) int64 {
		out := approve(svc.Add(ctx, owner, board.AddInput{Project: project, Title: title, Priority: &prio, Due: due, Todo: true}))
		return out.Ticket.ID
	}
	take := func(c board.Caller, id int64) { approve(svc.Claim(ctx, c, id, "")) }

	silent := todo("atlas", "Recalculer les tournées après la mise à jour OSRM", 1, "")
	take(w1, silent)
	mu.Lock()
	shift = 0
	mu.Unlock()

	today := time.Now()
	day := func(d int) string { return today.AddDate(0, 0, d).Format(time.DateOnly) }
	grafana := todo("perso", "Grafana : un seul conteneur MCP partagé au lieu d'un par session", 1, "")
	export := todo("atlas", "Export Excel des interventions pour la recette", 2, day(3))
	todo("atlas", "Page des compteurs P1 côté web", 2, "")
	todo("orion", "Comparer les montants du lot 2 avec l'ancien système", 0, day(-1))
	blocked := todo("atlas", "Brancher le nouveau calendrier des techniciens", 1, day(1))
	take(coord, blocked)
	approve(svc.Note(ctx, coord, blocked, "Le client a deux référentiels de secteurs : lequel fait foi ?", board.StateBlocked, nil))
	review := todo("atlas", "Photo obligatoire du compteur avant envoi de l'index", 1, "")
	take(w2, review)
	approve(svc.Note(ctx, w2, review, "Prêt : test e2e mobile vert, migration 0091.", board.StateToValidate, []string{"https://github.com/example/atlas/pull/1042"}))
	progress := todo("orion", "Réécrire le garde-fou de transpilation", 2, "")
	take(board.Caller{Identity: "claude:4", Name: "orion-transpiler"}, progress)
	svc.Add(ctx, w1, board.AddInput{Project: "atlas", Title: "Test planning instable sur le fuseau horaire (vu en passant)"})
	svc.Add(ctx, w2, board.AddInput{Project: "atlas", Title: "Dépendance vulnérable dans le build mobile"})
	svc.Add(ctx, owner, board.AddInput{Kind: board.KindReminder, Title: "Relancer le client sur les référentiels", Due: day(0)})
	svc.Add(ctx, owner, board.AddInput{Kind: board.KindReminder, Project: "atlas", Title: "Préparer la démo de vendredi", Due: day(1)})
	done := todo("perso", "Ranger les worktrees inutilisés", 3, "")
	take(board.Caller{Identity: "claude:5", Name: "menage"}, done)
	approve(svc.Note(ctx, board.Caller{Identity: "claude:5", Name: "menage"}, done, "", board.StateDone, nil))
	if _, err := svc.Claim(ctx, board.Caller{Identity: "claude:6", Name: "infra-grafana"}, grafana, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Claim(ctx, w2, export, ""); err != nil {
		t.Fatal(err)
	}

	opts := DefaultOptions()
	opts.UnlockAtStart = false
	opts.Prompt = PromptNever
	srv := New(svc, &portIdentities{ports: map[int]string{}}, slowApprover{}, &fakeNotifier{}, "preview-token", opts, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ln, err := net.Listen("tcp", "127.0.0.1:"+envOr("BOARD_PREVIEW_PORT", "7393"))
	if err != nil {
		t.Fatal(err)
	}
	go srv.HTTPServer(ln).Serve(ln)
	fmt.Printf("PREVIEW http://%s/login?t=%s\n", ln.Addr(), srv.web.newLink(time.Now().Add(time.Hour)))
	minutes, _ := strconv.Atoi(envOr("BOARD_PREVIEW_MINUTES", "20"))
	time.Sleep(time.Duration(minutes) * time.Minute)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
