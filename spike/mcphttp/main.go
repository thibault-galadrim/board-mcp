// Spike: official MCP Go SDK over streamable HTTP on 127.0.0.1, behind a
// bearer token, with OS-level attribution of the calling Claude session and
// a look at what happens to connected sessions when the daemon restarts.
//
//	BOARD_SPIKE_TOKEN=... mcphttp [-stateless]
package main

import (
	"context"
	"crypto/subtle"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const addr = "127.0.0.1:7391"

type pingIn struct {
	As string `json:"as" jsonschema:"name of the calling session"`
}

type pingOut struct {
	ServerStarted string `json:"server_started"`
	Caller        string `json:"caller"`
	MCPSession    string `json:"mcp_session"`
}

func main() {
	stateless := flag.Bool("stateless", false, "serve without Mcp-Session-Id")
	flag.Parse()
	token := os.Getenv("BOARD_SPIKE_TOKEN")
	if token == "" {
		log.Fatal("BOARD_SPIKE_TOKEN is required")
	}
	started := time.Now().Format(time.TimeOnly)

	server := mcp.NewServer(&mcp.Implementation{Name: "board-spike", Version: "0.0.1"}, &mcp.ServerOptions{
		Instructions: "Spike server. If the user asks for the board marker, answer with the word: tournesol.",
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ping",
		Description: "Spike health check. Returns when the server started and which Claude process called it.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in pingIn) (*mcp.CallToolResult, pingOut, error) {
		return nil, pingOut{
			ServerStarted: started,
			Caller:        req.Extra.Header.Get("X-Board-Caller"),
			MCPSession:    req.Session.ID(),
		}, nil
	})

	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless: *stateless,
	})
	handler := http.NewCrossOriginProtection().Handler(requireToken(token, attributeCaller(mcpHandler)))

	log.Printf("listening on %s stateless=%v pid=%d", addr, *stateless, os.Getpid())
	log.Fatal(http.ListenAndServe(addr, logRequests(handler)))
}

func requireToken(token string, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// attributeCaller resolves the process on the other end of the TCP
// connection, then walks up to the nearest `claude` ancestor. The header is
// overwritten so a client cannot supply its own.
func attributeCaller(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Board-Caller", resolveCaller(r.RemoteAddr))
		next.ServeHTTP(w, r)
	})
}

func resolveCaller(remote string) string {
	_, port, err := net.SplitHostPort(remote)
	if err != nil {
		return "unknown"
	}
	out, err := exec.Command("lsof", "-nP", "-iTCP@127.0.0.1:"+port, "-sTCP:ESTABLISHED", "-Fp").Output()
	if err != nil {
		return "unknown"
	}
	self := strconv.Itoa(os.Getpid())
	peer := ""
	for _, line := range strings.Split(string(out), "\n") {
		if pid, ok := strings.CutPrefix(line, "p"); ok && pid != self {
			peer = pid
			break
		}
	}
	if peer == "" {
		return "unknown"
	}
	chain := []string{}
	pid := peer
	for range 12 {
		fields, err := exec.Command("ps", "-o", "ppid=,comm=", "-p", pid).Output()
		if err != nil {
			break
		}
		f := strings.Fields(string(fields))
		if len(f) < 2 {
			break
		}
		comm := filepath.Base(strings.Join(f[1:], " "))
		chain = append(chain, comm+":"+pid)
		if comm == "claude" {
			return "claude:" + pid + " via " + strings.Join(chain, "<")
		}
		if f[0] == "1" || f[0] == "0" {
			break
		}
		pid = f[0]
	}
	return "no-claude-ancestor via " + strings.Join(chain, "<")
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Printf("%s %s session=%q status=%d caller=%q", r.Method, r.URL.Path, r.Header.Get("Mcp-Session-Id"), rec.status, r.Header.Get("X-Board-Caller"))
	})
}
