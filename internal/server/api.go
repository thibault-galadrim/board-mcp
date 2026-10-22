package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"board/internal/board"
	"board/internal/identity"
)

const (
	defaultWait = 25 * time.Second
	maxWait     = 55 * time.Second
)

func (s *Server) registerAPI(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/requests", s.apiRequests)
	mux.HandleFunc("GET /api/requests/{id}/wait", s.apiWait)
	mux.HandleFunc("POST /api/requests/{id}/approve", s.ownerOnly(s.apiApprove))
	mux.HandleFunc("POST /api/requests/{id}/refuse", s.ownerOnly(s.apiRefuse))
	mux.HandleFunc("POST /api/unlock", s.ownerOnly(s.apiUnlock))
}

// ownerOnly keeps decisions to callers outside any Claude session. A
// session's own shell is attributed to that session and refused here;
// a detached process would still face the Touch ID prompt.
func (s *Server) ownerOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(identityHeader) != identity.Terminal {
			writeError(w, http.StatusForbidden, "decisions come from the owner's terminal, not from a Claude session")
			return
		}
		next(w, r)
	}
}

func (s *Server) apiRequests(w http.ResponseWriter, r *http.Request) {
	if s.locked.Load() {
		writeError(w, http.StatusLocked, s.lockedError().Error())
		return
	}
	reqs, err := s.svc.PendingRequests(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": reqs})
}

// apiWait long-polls until the request is decided or the timeout passes;
// it answers the request as it stands either way.
func (s *Server) apiWait(w http.ResponseWriter, r *http.Request) {
	id, ok := requestID(w, r)
	if !ok {
		return
	}
	timeout := defaultWait
	if v := r.URL.Query().Get("timeout"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			writeError(w, http.StatusBadRequest, "timeout must be a positive duration")
			return
		}
		timeout = min(d, maxWait)
	}
	ch := s.hub.subscribe(id)
	defer s.hub.unsubscribe(id, ch)
	req, err := s.svc.GetRequest(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if req.State == board.RequestPending {
		select {
		case <-ch:
		case <-time.After(timeout):
		case <-r.Context().Done():
			return
		}
		if req, err = s.svc.GetRequest(r.Context(), id); err != nil {
			writeServiceError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) apiApprove(w http.ResponseWriter, r *http.Request) {
	req, ok := s.pendingRequest(w, r)
	if !ok {
		return
	}
	out, decided := s.confirmRequest(r.Context(), req, false)
	if !decided {
		writeError(w, http.StatusRequestTimeout, "no answer to the prompt: the request stays pending")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) apiRefuse(w http.ResponseWriter, r *http.Request) {
	req, ok := s.pendingRequest(w, r)
	if !ok {
		return
	}
	out, err := s.svc.Refuse(r.Context(), req.ID, r.URL.Query().Get("reason"), "")
	s.hub.notify(req.ID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) apiUnlock(w http.ResponseWriter, r *http.Request) {
	unlocked, err := s.Unlock(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"unlocked": unlocked})
}

func (s *Server) pendingRequest(w http.ResponseWriter, r *http.Request) (board.Request, bool) {
	if s.locked.Load() {
		writeError(w, http.StatusLocked, s.lockedError().Error())
		return board.Request{}, false
	}
	id, ok := requestID(w, r)
	if !ok {
		return board.Request{}, false
	}
	req, err := s.svc.GetRequest(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return board.Request{}, false
	}
	if req.State != board.RequestPending {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "request is " + string(req.State), "request": req})
		return board.Request{}, false
	}
	return req, true
}

func requestID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid request id")
		return 0, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeServiceError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, board.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, board.ErrNotPending), errors.Is(err, board.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, board.ErrInvalid):
		status = http.StatusBadRequest
	}
	writeError(w, status, err.Error())
}
