package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"time"

	"board/internal/board"
)

type api struct {
	base string
	http *http.Client
}

func newAPI() (*api, error) {
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
	return &api{
		base: "http://" + addr,
		http: &http.Client{Transport: bearer{token: token, base: http.DefaultTransport}, Timeout: 2 * time.Minute},
	}, nil
}

// do calls the daemon and decodes its JSON answer into out. Non-2xx
// statuses come back with the daemon's message as error.
func (a *api) do(method, path string, out any) (int, error) {
	req, err := http.NewRequest(method, a.base+path, nil)
	if err != nil {
		return 0, err
	}
	res, err := a.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("cannot reach the daemon: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(res.Body).Decode(&e)
		if e.Error == "" {
			e.Error = res.Status
		}
		return res.StatusCode, errors.New(e.Error)
	}
	return res.StatusCode, json.NewDecoder(res.Body).Decode(out)
}

func runApprovals(cmd string, args []string) (bool, error) {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	switch cmd {
	case "requests":
		a, err := newAPI()
		if err != nil {
			return true, err
		}
		var out struct {
			Requests []board.Request `json:"requests"`
		}
		if _, err := a.do(http.MethodGet, "/api/requests", &out); err != nil {
			return true, err
		}
		if len(out.Requests) == 0 {
			fmt.Println("aucune demande en attente")
		}
		for _, r := range out.Requests {
			fmt.Printf("#%-4d %s  %-14s %s\n", r.ID, localTime(r.CreatedAt), r.Actor, r.Summary)
		}
		return true, nil

	case "ok":
		id, err := ticketID(fs, args)
		if err != nil {
			return true, err
		}
		a, err := newAPI()
		if err != nil {
			return true, err
		}
		var out board.Outcome
		status, err := a.do(http.MethodPost, "/api/requests/"+strconv.FormatInt(id, 10)+"/approve", &out)
		if status == http.StatusRequestTimeout {
			return true, exitError{code: 4, msg: fmt.Sprintf("pas de réponse à l'invite : la demande #%d reste en attente", id)}
		}
		if err != nil {
			return true, err
		}
		printOutcome(out)
		return true, nil

	case "refuse":
		reason := fs.String("r", "", "motif")
		id, err := ticketID(fs, args)
		if err != nil {
			return true, err
		}
		a, err := newAPI()
		if err != nil {
			return true, err
		}
		var out board.Outcome
		if _, err := a.do(http.MethodPost, "/api/requests/"+strconv.FormatInt(id, 10)+"/refuse?reason="+url.QueryEscape(*reason), &out); err != nil {
			return true, err
		}
		fmt.Printf("demande #%d refusée\n", id)
		return true, nil

	case "wait":
		timeout := fs.Duration("timeout", 12*time.Hour, "attente maximale")
		id, err := ticketID(fs, args)
		if err != nil {
			return true, err
		}
		return true, waitFor(id, *timeout)

	case "open":
		a, err := newAPI()
		if err != nil {
			return true, err
		}
		var out struct {
			URL string `json:"url"`
		}
		if _, err := a.do(http.MethodPost, "/api/login-link", &out); err != nil {
			return true, err
		}
		if err := exec.Command("/usr/bin/open", out.URL).Run(); err != nil {
			return true, fmt.Errorf("open the page yourself within a minute: %s (%w)", out.URL, err)
		}
		return true, nil

	case "unlock":
		a, err := newAPI()
		if err != nil {
			return true, err
		}
		var out struct {
			Unlocked bool `json:"unlocked"`
		}
		if _, err := a.do(http.MethodPost, "/api/unlock", &out); err != nil {
			return true, err
		}
		if !out.Unlocked {
			return true, exitError{code: 2, msg: "le board reste verrouillé"}
		}
		fmt.Println("board déverrouillé")
		return true, nil
	}
	return false, nil
}

// waitFor blocks until the owner decides. It outlives daemon restarts: a
// session waiting in the background must not die because the daemon did.
// Exit codes: 0 approved, 2 refused, 3 failed, 4 still pending.
func waitFor(id int64, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	path := fmt.Sprintf("/api/requests/%d/wait?timeout=50s", id)
	for time.Now().Before(deadline) {
		a, err := newAPI()
		if err != nil {
			return err
		}
		var r board.Request
		status, err := a.do(http.MethodGet, path, &r)
		switch {
		case status == http.StatusNotFound:
			return err
		case err != nil:
			time.Sleep(5 * time.Second)
			continue
		}
		switch r.State {
		case board.RequestApproved:
			return exitError{code: 0, msg: fmt.Sprintf("demande #%d approuvée : %s", id, r.Summary)}
		case board.RequestRefused:
			return exitError{code: 2, msg: fmt.Sprintf("demande #%d refusée : %s %s", id, r.Summary, r.Outcome)}
		case board.RequestFailed:
			return exitError{code: 3, msg: fmt.Sprintf("demande #%d approuvée mais impossible à appliquer : %s", id, r.Outcome)}
		}
	}
	return exitError{code: 4, msg: fmt.Sprintf("demande #%d toujours en attente", id)}
}
