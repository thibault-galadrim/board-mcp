package identity

/*
#include <libproc.h>
#include <sys/proc_info.h>
#include <arpa/inet.h>
#include <stdlib.h>

// Returns the pid owning the loopback TCP socket bound to lport and connected
// to fport, or -1. Processes we cannot inspect (other users) are skipped.
static int tcp_owner(int lport, int fport) {
	int n = proc_listallpids(NULL, 0);
	if (n <= 0) return -1;
	int cap = n + 256;
	pid_t *pids = malloc(sizeof(pid_t) * cap);
	if (!pids) return -1;
	n = proc_listallpids(pids, (int)(sizeof(pid_t) * cap));
	int found = -1;
	for (int i = 0; i < n && found < 0; i++) {
		pid_t pid = pids[i];
		int size = proc_pidinfo(pid, PROC_PIDLISTFDS, 0, NULL, 0);
		if (size <= 0) continue;
		struct proc_fdinfo *fds = malloc(size);
		if (!fds) continue;
		size = proc_pidinfo(pid, PROC_PIDLISTFDS, 0, fds, size);
		int count = size / (int)PROC_PIDLISTFD_SIZE;
		for (int j = 0; j < count; j++) {
			if (fds[j].proc_fdtype != PROX_FDTYPE_SOCKET) continue;
			struct socket_fdinfo si;
			if (proc_pidfdinfo(pid, fds[j].proc_fd, PROC_PIDFDSOCKETINFO, &si, PROC_PIDFDSOCKETINFO_SIZE) != PROC_PIDFDSOCKETINFO_SIZE) continue;
			if (si.psi.soi_kind != SOCKINFO_TCP) continue;
			struct in_sockinfo *ini = &si.psi.soi_proto.pri_tcp.tcpsi_ini;
			if (ntohs((uint16_t)ini->insi_lport) != lport || ntohs((uint16_t)ini->insi_fport) != fport) continue;
			int loop4 = (ini->insi_vflag & INI_IPV4) && ini->insi_faddr.ina_46.i46a_addr4.s_addr == htonl(INADDR_LOOPBACK);
			int loop6 = (ini->insi_vflag & INI_IPV6) && IN6_IS_ADDR_LOOPBACK(&ini->insi_faddr.ina_6);
			if (!loop4 && !loop6) continue;
			found = pid;
			break;
		}
		free(fds);
	}
	free(pids);
	return found;
}

static int proc_info(int pid, int *ppid, long long *start_sec, int *start_usec, char *path, int path_size) {
	struct proc_bsdinfo info;
	if (proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &info, sizeof(info)) != sizeof(info)) return -1;
	*ppid = (int)info.pbi_ppid;
	*start_sec = (long long)info.pbi_start_tvsec;
	*start_usec = (int)info.pbi_start_tvusec;
	if (proc_pidpath(pid, path, path_size) <= 0) path[0] = 0;
	return 0;
}
*/
import "C"

import (
	"fmt"
	"net"
	"unsafe"
)

const pathMax = 4096

func lookup(pid int) (process, bool) {
	var ppid, usec C.int
	var sec C.longlong
	buf := (*C.char)(C.malloc(pathMax))
	defer C.free(unsafe.Pointer(buf))
	if C.proc_info(C.int(pid), &ppid, &sec, &usec, buf, pathMax) != 0 {
		return process{}, false
	}
	return process{pid: pid, ppid: int(ppid), startSec: int64(sec), startUsec: int32(usec), path: C.GoString(buf)}, true
}

// LibprocResolver resolves identities with the macOS process table.
type LibprocResolver struct {
	ClaudePrefixes []string
}

func (r LibprocResolver) Resolve(local, remote net.Addr) Identity {
	localPort, remotePort, loopback := ports(local, remote)
	if !loopback {
		return Identity{Key: Terminal}
	}
	// The peer's socket is bound to our remote port and connected to our local one.
	peer := int(C.tcp_owner(C.int(remotePort), C.int(localPort)))
	if peer <= 0 {
		return Identity{Key: Terminal}
	}
	return walk(peer, r.ClaudePrefixes, lookup)
}

// Alive reports whether the process behind a Claude identity still runs.
// The start time guards against a recycled pid.
func (LibprocResolver) Alive(key string) bool {
	pid, start, ok := parseKey(key)
	if !ok {
		return false
	}
	p, found := lookup(pid)
	return found && fmt.Sprintf("%d.%06d", p.startSec, p.startUsec) == start
}
