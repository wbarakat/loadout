package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
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

// maxSnapshotBytes caps how large a single snapshot body may be. A
// snapshot is one full encrypted blob of a whole vault's skills and
// memory, so this is generous, but an unbounded body is still a
// resource-exhaustion risk — fatal on a small self-hosted disk (the
// Pi's SD card, say).
const maxSnapshotBytes = 64 << 20 // 64 MiB

// maxDeviceUpsertBytes caps a device-upsert body: a name and an age
// recipient, both short fixed-shape strings, never need anywhere
// close to this much room.
const maxDeviceUpsertBytes = 64 << 10 // 64 KiB

// ageX25519RecipientPattern matches the shape every age X25519
// recipient string has: the bech32 human-readable part "age1",
// followed by exactly 58 characters from bech32's own 32-character
// data alphabet — the length and alphabet age.GenerateX25519Identity
// always produces (invariant: recipient strings are always exactly
// 62 characters).
//
// This checks shape only, not the bech32 checksum: TestServerPackage-
// NeverImportsAge fixes invariant 8 at the dependency level — the
// server package may never import filippo.io/age, so it can never
// gain a path to age.Decrypt, even by an accidental future edit — so
// this handler cannot call the real age.ParseX25519Recipient the way
// devices approve's own validation does. That real, authoritative
// validation is Layer 1 (internal/cli/devices.go's approveDevice);
// this regexp is Layer 2, a shape-only defense-in-depth check that
// refuses the obviously-garbage recipient an enrollment DoS depends
// on, without weakening invariant 8.
var ageX25519RecipientPattern = regexp.MustCompile(`^age1[qpzry9x8gf2tvdw0s3jn54khce6mua7l]{58}$`)

func (s *Server) handleUpsertDevice(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxDeviceUpsertBytes)
	var req deviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "the request body exceeds the maximum allowed size")
			s.log(r, http.StatusRequestEntityTooLarge, "", 0)
			return
		}
		writeJSONError(w, http.StatusBadRequest, "name and recipient are required")
		s.log(r, http.StatusBadRequest, "", 0)
		return
	}
	if req.Name == "" || req.Recipient == "" {
		writeJSONError(w, http.StatusBadRequest, "name and recipient are required")
		s.log(r, http.StatusBadRequest, "", 0)
		return
	}
	// Reject a recipient that is not shaped like a valid age X25519
	// recipient before it ever reaches the roster: this is the
	// roster's first line of defense against a garbage or malicious
	// registration — approving it later would encrypt every future
	// snapshot, on every device, to a key nobody holds.
	if !ageX25519RecipientPattern.MatchString(req.Recipient) {
		writeJSONError(w, http.StatusBadRequest, "recipient is not a valid age X25519 recipient")
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
	r.Body = http.MaxBytesReader(w, r.Body, maxSnapshotBytes)
	blob, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "the snapshot exceeds the maximum allowed size")
			s.log(r, http.StatusRequestEntityTooLarge, "", 0)
			return
		}
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
