// Package identity attributes an incoming local TCP connection to the
// Claude Code session it comes from, using the operating system's process
// table rather than anything the client says.
package identity

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Terminal is the identity of any caller with no Claude Code ancestor: the
// owner's own terminal, or a process detached from its session.
const Terminal = "terminal"

type Identity struct {
	// Key is stable for the life of the Claude process and never reused:
	// "claude:<pid>:<start seconds>.<start micros>", or Terminal.
	Key  string
	Peer int // process on the other end of the connection, for logs
}

func (i Identity) Terminal() bool { return i.Key == Terminal }

// Resolver finds who is behind a connection accepted on local from remote.
type Resolver interface {
	Resolve(local, remote net.Addr) Identity
}

type process struct {
	pid       int
	ppid      int
	startSec  int64
	startUsec int32
	path      string
}

func claudeKey(p process) string {
	return fmt.Sprintf("claude:%d:%d.%06d", p.pid, p.startSec, p.startUsec)
}

func parseKey(key string) (pid int, start string, ok bool) {
	rest, found := strings.CutPrefix(key, "claude:")
	if !found {
		return 0, "", false
	}
	pidStr, start, found := strings.Cut(rest, ":")
	if !found {
		return 0, "", false
	}
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid <= 0 {
		return 0, "", false
	}
	return pid, start, true
}

// walk climbs from pid to the nearest ancestor whose executable lives under
// one of the trusted prefixes. Matching on the executable path, not the
// process name, keeps a renamed program from passing itself off as Claude.
func walk(pid int, prefixes []string, lookup func(int) (process, bool)) Identity {
	id := Identity{Key: Terminal, Peer: pid}
	for range 64 {
		p, ok := lookup(pid)
		if !ok {
			return id
		}
		for _, prefix := range prefixes {
			if prefix != "" && strings.HasPrefix(p.path, prefix) {
				id.Key = claudeKey(p)
				return id
			}
		}
		if p.ppid <= 1 {
			return id
		}
		pid = p.ppid
	}
	return id
}

func ports(local, remote net.Addr) (localPort, remotePort int, loopback bool) {
	l, lok := local.(*net.TCPAddr)
	r, rok := remote.(*net.TCPAddr)
	if !lok || !rok {
		return 0, 0, false
	}
	return l.Port, r.Port, l.IP.IsLoopback() && r.IP.IsLoopback()
}
