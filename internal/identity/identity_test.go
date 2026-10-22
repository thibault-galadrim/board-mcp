package identity

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestWalkFindsNearestTrustedAncestor(t *testing.T) {
	table := map[int]process{
		40: {pid: 40, ppid: 30, path: "/usr/bin/curl"},
		30: {pid: 30, ppid: 20, path: "/bin/zsh"},
		20: {pid: 20, ppid: 10, startSec: 1700000000, startUsec: 42, path: "/home/u/.local/share/claude/versions/2.1.308"},
		10: {pid: 10, ppid: 1, path: "/usr/bin/login"},
	}
	lookup := func(pid int) (process, bool) { p, ok := table[pid]; return p, ok }

	got := walk(40, []string{"/home/u/.local/share/claude/versions/"}, lookup)
	if got.Key != "claude:20:1700000000.000042" || got.Peer != 40 {
		t.Fatalf("got %+v", got)
	}
}

func TestWalkIgnoresRenamedBinaries(t *testing.T) {
	table := map[int]process{
		40: {pid: 40, ppid: 1, path: "/tmp/claude"},
	}
	lookup := func(pid int) (process, bool) { p, ok := table[pid]; return p, ok }
	if got := walk(40, []string{"/home/u/.local/share/claude/versions/"}, lookup); !got.Terminal() {
		t.Fatalf("a binary merely named claude must not count, got %+v", got)
	}
}

func TestParseKey(t *testing.T) {
	pid, start, ok := parseKey("claude:123:1700000000.000042")
	if !ok || pid != 123 || start != "1700000000.000042" {
		t.Fatalf("got %d %q %v", pid, start, ok)
	}
	for _, bad := range []string{"terminal", "claude:", "claude:x:1", "claude:-1:1", "other:1:1"} {
		if _, _, ok := parseKey(bad); ok {
			t.Errorf("%q should not parse", bad)
		}
	}
}

// TestLibprocResolvesRealConnection connects to ourselves: the peer is this
// test process. Trusting the test binary's directory turns it into a
// "claude" identity; trusting nothing yields Terminal.
func TestLibprocResolvesRealConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	untrusted := LibprocResolver{ClaudePrefixes: []string{"/nonexistent/"}}.Resolve(server.LocalAddr(), server.RemoteAddr())
	if !untrusted.Terminal() || untrusted.Peer != os.Getpid() {
		t.Fatalf("want terminal with peer %d, got %+v", os.Getpid(), untrusted)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe, _ = filepath.EvalSymlinks(exe)
	r := LibprocResolver{ClaudePrefixes: []string{filepath.Dir(exe) + "/"}}
	got := r.Resolve(server.LocalAddr(), server.RemoteAddr())
	pid, _, ok := parseKey(got.Key)
	if !ok || pid != os.Getpid() {
		t.Fatalf("want claude identity for pid %d, got %+v", os.Getpid(), got)
	}
	if !r.Alive(got.Key) {
		t.Fatal("own process should be alive")
	}
	if r.Alive("claude:" + strconv.Itoa(os.Getpid()) + ":1.000000") {
		t.Fatal("a wrong start time must not be considered alive")
	}
}
