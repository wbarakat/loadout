package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

// Server wires a Store to HTTP handlers. Every /v1/snapshots body it
// handles is opaque bytes: this file never decodes, decrypts, or
// otherwise reads inside a blob (invariant 8).
type Server struct {
	store  *Store
	token  string
	logger *log.Logger
}

// New builds a Server backed by store, requiring token on every
// route except GET /health. A nil logger writes to os.Stderr.
func New(store *Store, token string, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.New(os.Stderr, "", log.LstdFlags)
	}
	return &Server{store: store, token: token, logger: logger}
}

// Handler returns the server's http.Handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.Handle("POST /v1/devices", s.auth(s.handleUpsertDevice))
	mux.Handle("GET /v1/devices", s.auth(s.handleListDevices))
	mux.Handle("POST /v1/snapshots", s.auth(s.handlePostSnapshot))
	mux.Handle("GET /v1/snapshots/latest", s.auth(s.handleLatest))
	mux.Handle("GET /v1/snapshots/{version}", s.auth(s.handleGetSnapshot))
	return mux
}

// auth requires a valid "Authorization: Bearer <token>" header,
// compared to s.token in constant time so a wrong guess cannot be
// narrowed down by response timing. It never logs the header value.
func (s *Server) auth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		hdr := r.Header.Get("Authorization")
		supplied, hasPrefix := strings.CutPrefix(hdr, prefix)
		if !hasPrefix || subtle.ConstantTimeCompare([]byte(supplied), []byte(s.token)) != 1 {
			writeJSONError(w, http.StatusUnauthorized, "a valid bearer token is required")
			s.log(r, http.StatusUnauthorized, "", 0)
			return
		}
		next(w, r)
	})
}

// log writes one line per request: method, path, status, version,
// and byte count. It never writes a header or a blob's content, so
// the access token and the vault's plaintext never reach the log.
func (s *Server) log(r *http.Request, status int, version string, byteCount int) {
	s.logger.Printf("%s %s %d version=%s bytes=%d", r.Method, r.URL.Path, status, version, byteCount)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	s.log(r, http.StatusOK, "", 0)
}

type deviceRequest struct {
	Name      string `json:"name"`
	Recipient string `json:"recipient"`
}

func (s *Server) handleUpsertDevice(w http.ResponseWriter, r *http.Request) {
	var req deviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.Recipient == "" {
		writeJSONError(w, http.StatusBadRequest, "name and recipient are required")
		s.log(r, http.StatusBadRequest, "", 0)
		return
	}
	dev, err := s.store.UpsertDevice(req.Name, req.Recipient)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "the device cannot be stored")
		s.log(r, http.StatusInternalServerError, "", 0)
		return
	}
	writeJSON(w, http.StatusOK, dev)
	s.log(r, http.StatusOK, "", 0)
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := s.store.ListDevices()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "the device roster cannot be read")
		s.log(r, http.StatusInternalServerError, "", 0)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
	s.log(r, http.StatusOK, "", 0)
}

func (s *Server) handlePostSnapshot(w http.ResponseWriter, r *http.Request) {
	parent := r.Header.Get("X-Loadout-Parent")
	blob, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "the request body cannot be read")
		s.log(r, http.StatusBadRequest, "", 0)
		return
	}
	version, err := s.store.PutSnapshot(parent, blob)
	if err != nil {
		var conflict *ParentConflictError
		if errors.As(err, &conflict) {
			writeJSON(w, http.StatusConflict, map[string]string{"latest": conflict.Latest})
			s.log(r, http.StatusConflict, "", len(blob))
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "the snapshot cannot be stored")
		s.log(r, http.StatusInternalServerError, "", len(blob))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"version": version})
	s.log(r, http.StatusOK, version, len(blob))
}

func (s *Server) handleLatest(w http.ResponseWriter, r *http.Request) {
	info, err := s.store.Latest()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "the index cannot be read")
		s.log(r, http.StatusInternalServerError, "", 0)
		return
	}
	writeJSON(w, http.StatusOK, info)
	s.log(r, http.StatusOK, info.Version, 0)
}

func (s *Server) handleGetSnapshot(w http.ResponseWriter, r *http.Request) {
	version := r.PathValue("version")
	blob, err := s.store.GetSnapshot(version)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSONError(w, http.StatusNotFound, "no such snapshot version")
			s.log(r, http.StatusNotFound, version, 0)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "the snapshot cannot be read")
		s.log(r, http.StatusInternalServerError, version, 0)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(blob)
	s.log(r, http.StatusOK, version, len(blob))
}
