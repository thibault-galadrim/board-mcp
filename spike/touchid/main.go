// Spike: can a Go binary ask for Touch ID, both from a terminal and when
// running as a LaunchAgent with no app bundle?
//
//	touchid prompt "reason"      ask directly
//	touchid serve 127.0.0.1:7390 GET /approve?reason=... asks and answers JSON
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: touchid prompt <reason> | touchid serve <addr>")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "prompt":
		ok, err := authenticate(os.Args[2], true, 60*time.Second)
		fmt.Printf("approved=%v err=%v\n", ok, err)
	case "serve":
		http.HandleFunc("/approve", func(w http.ResponseWriter, r *http.Request) {
			reason := r.URL.Query().Get("reason")
			if reason == "" {
				reason = "confirm a board action"
			}
			ok, err := authenticate(reason, true, 60*time.Second)
			errMsg := ""
			if err != nil {
				errMsg = err.Error()
			}
			log.Printf("approve reason=%q approved=%v err=%q", reason, ok, errMsg)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"approved": ok, "error": errMsg})
		})
		log.Printf("listening on %s pid=%d", os.Args[2], os.Getpid())
		log.Fatal(http.ListenAndServe(os.Args[2], nil))
	default:
		os.Exit(2)
	}
}
