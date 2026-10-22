package main

import (
	"flag"
	"slices"
	"testing"
)

func TestBoardAddrOnlyAcceptsLoopback(t *testing.T) {
	for addr, ok := range map[string]bool{
		"":                 true,
		"127.0.0.1:7400":   true,
		"[::1]:7400":       true,
		"0.0.0.0:7391":     false,
		"192.168.1.10:80":  false,
		"localhost:7391":   false,
		"example.com:7391": false,
		"nonsense":         false,
	} {
		t.Setenv("BOARD_ADDR", addr)
		if _, err := boardAddr(); (err == nil) != ok {
			t.Errorf("%q: err=%v, want ok=%v", addr, err, ok)
		}
	}
}

func TestFlagsAnywhereAmongArguments(t *testing.T) {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	project := fs.String("p", "", "")
	todo := fs.Bool("t", false, "")
	pos, err := parseArgs(fs, []string{"relancer", "-p", "atlas", "le", "client", "-t"})
	if err != nil {
		t.Fatal(err)
	}
	if *project != "atlas" || !*todo || !slices.Equal(pos, []string{"relancer", "le", "client"}) {
		t.Fatalf("project=%q todo=%v pos=%v", *project, *todo, pos)
	}
}
