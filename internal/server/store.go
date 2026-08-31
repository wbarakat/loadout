// Package server implements loadoutd, the sync server. It stores
// caller-supplied blobs and never looks inside them: every snapshot
// arrives already encrypted, and the store's job stops at bytes in,
// bytes out. This file holds the on-disk store; api.go wires it to
// HTTP.
package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

// lockPollInterval sets how often lock retries a held lock.
const lockPollInterval = 100 * time.Millisecond

// lockTimeout sets how long lock waits before it gives up.
const lockTimeout = 10 * time.Second

// Store is a file-backed blob store rooted at a data directory. It
// never parses a blob: every snapshot is opaque bytes in, opaque
// bytes out. The index (the latest version and the parent-chain
// metadata) lives in index.json, guarded by an flock so two
// loadoutd processes sharing one data directory never read-modify-
// write it at the same time.
type Store struct {
	Root string
}

// Device is one roster entry: a device's chosen name and its age
// recipient (a public key, safe to hold in plaintext — see
// invariant 8's roster carve-out).
type Device struct {
	Name      string `json:"name"`
	Recipient string `json:"recipient"`
}

// LatestInfo answers GET /v1/snapshots/latest: the store's current
// latest version and the parent it was built on. A store that has
// never received a snapshot reports an empty version.
type LatestInfo struct {
	Version string `json:"version"`
	Parent  string `json:"parent"`
}

// versionMeta is one version's index entry.
type versionMeta struct {
	Parent    string    `json:"parent"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"createdAt"`
}

// indexFile is the on-disk shape of index.json. N is the count of
// versions ever assigned, so the next version number is always N+1
// even though versions are never removed.
type indexFile struct {
	Latest   string                 `json:"latest"`
	N        int                    `json:"n"`
	Versions map[string]versionMeta `json:"versions"`
}

// ParentConflictError is what PutSnapshot returns when the caller's
// parent does not match the store's current latest version: someone
// else stored a snapshot first, and the caller must merge against
// Latest before it retries.
type ParentConflictError struct {
	Latest string
}

func (e *ParentConflictError) Error() string {
	return fmt.Sprintf("stale parent: the store's latest version is %q", e.Latest)
}

// versionPattern is the shape every version PutSnapshot generates
// matches: v<n>-<8 lowercase hex>. GetSnapshot checks an incoming
// version against it before it ever touches the filesystem, so a
// path-traversal attempt (e.g. "../../etc/passwd") can never reach
// os.ReadFile — it just reads back as "no such version".
var versionPattern = regexp.MustCompile(`^v[0-9]+-[0-9a-f]{8}$`)

// Open opens the file store rooted at dataDir, creating dataDir and
// its blobs subdirectory if they do not exist yet. It returns a
// clear error, naming the path, when dataDir is not writable.
func Open(dataDir string) (*Store, error) {
	root, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "blobs"), 0o755); err != nil {
		return nil, fmt.Errorf("%s: the data directory is not writable: %v. Fix: check the directory permissions, or choose another -data path.", root, err)
	}
	return &Store{Root: root}, nil
}

// Token returns the store's bearer token, generating a random
// 32-byte hex token and storing it at <data>/token (mode 0600) on
// the first call for a data directory that holds none yet. created
// reports whether this call just generated the token, so the caller
// (loadoutd's main) can print it exactly once, on first start only.
func (s *Store) Token() (token string, created bool, err error) {
	path := filepath.Join(s.Root, "token")
	data, err := os.ReadFile(path)
	if err == nil {
		return strings.TrimSpace(string(data)), false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf("%s: the access token cannot be read: %v. Fix: check the file permissions.", path, err)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", false, err
	}
	tok := hex.EncodeToString(raw)
	if err := os.WriteFile(path, []byte(tok+"\n"), 0o600); err != nil {
		return "", false, fmt.Errorf("%s: the access token cannot be written: %v. Fix: check the directory permissions.", path, err)
	}
	return tok, true, nil
}

// UpsertDevice adds name and recipient to the roster, or replaces an
// existing entry for name: the call is idempotent, so a client that
// registers twice leaves one entry behind.
func (s *Store) UpsertDevice(name, recipient string) (Device, error) {
	release, err := s.lock()
	if err != nil {
		return Device{}, err
	}
	defer release()

	roster, err := s.readRoster()
	if err != nil {
		return Device{}, err
	}
	roster[name] = recipient
	if err := s.writeRoster(roster); err != nil {
		return Device{}, err
	}
	return Device{Name: name, Recipient: recipient}, nil
}

// ListDevices returns every roster entry, sorted by name.
func (s *Store) ListDevices() ([]Device, error) {
	roster, err := s.readRoster()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(roster))
	for name := range roster {
		names = append(names, name)
	}
	sort.Strings(names)
	devices := make([]Device, 0, len(names))
	for _, name := range names {
		devices = append(devices, Device{Name: name, Recipient: roster[name]})
	}
	return devices, nil
}

// Latest reports the store's current latest version and the parent
// it was built on. A store with no snapshot yet reports an empty
// version and an empty parent.
func (s *Store) Latest() (LatestInfo, error) {
	idx, err := s.readIndex()
	if err != nil {
		return LatestInfo{}, err
	}
	if idx.Latest == "" {
		return LatestInfo{}, nil
	}
	return LatestInfo{Version: idx.Latest, Parent: idx.Versions[idx.Latest].Parent}, nil
}

// PutSnapshot stores blob as a new version built on parent, refusing
// with a *ParentConflictError when parent does not match the store's
// current latest version. The whole check-then-write runs under one
// flock acquisition, so two concurrent callers racing on the same
// stale parent can never both win: the second one re-reads the index
// after the first releases the lock, and finds its parent stale.
//
// blob passes through untouched: PutSnapshot never inspects it,
// decrypts it, or otherwise looks inside — invariant 8.
func (s *Store) PutSnapshot(parent string, blob []byte) (string, error) {
	release, err := s.lock()
	if err != nil {
		return "", err
	}
	defer release()

	idx, err := s.readIndex()
	if err != nil {
		return "", err
	}
	if parent != idx.Latest {
		return "", &ParentConflictError{Latest: idx.Latest}
	}

	suffix, err := randomHex8()
	if err != nil {
		return "", err
	}
	n := idx.N + 1
	version := fmt.Sprintf("v%d-%s", n, suffix)

	if err := writeFileSynced(filepath.Join(s.Root, "blobs", version), blob); err != nil {
		return "", err
	}

	idx.N = n
	idx.Latest = version
	idx.Versions[version] = versionMeta{Parent: parent, Size: int64(len(blob)), CreatedAt: time.Now().UTC()}
	if err := s.writeIndex(idx); err != nil {
		return "", err
	}
	return version, nil
}

// GetSnapshot returns the raw blob bytes stored for version. It
// returns an error satisfying errors.Is(err, os.ErrNotExist) both
// when version was never stored and when version does not match the
// shape PutSnapshot generates — the latter check keeps a path like
// "../../etc/passwd" from ever reaching the filesystem.
func (s *Store) GetSnapshot(version string) ([]byte, error) {
	if !versionPattern.MatchString(version) {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(filepath.Join(s.Root, "blobs", version))
}

// randomHex8 returns 4 random bytes hex-encoded: 8 lowercase hex
// characters, the version suffix PutSnapshot appends to n.
func randomHex8() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// readIndex reads index.json. A store with no index yet (nothing
// stored) reads back as an empty index, not an error.
func (s *Store) readIndex() (indexFile, error) {
	data, err := os.ReadFile(filepath.Join(s.Root, "index.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return indexFile{Versions: map[string]versionMeta{}}, nil
		}
		return indexFile{}, err
	}
	var idx indexFile
	if err := json.Unmarshal(data, &idx); err != nil {
		return indexFile{}, fmt.Errorf("the store index is corrupt: %v", err)
	}
	if idx.Versions == nil {
		idx.Versions = map[string]versionMeta{}
	}
	return idx, nil
}

// writeIndex encodes idx to index.json, fsyncing before it renames
// the temp file into place, so a crash mid-write never leaves a
// half-written index behind.
func (s *Store) writeIndex(idx indexFile) error {
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return writeFileSynced(filepath.Join(s.Root, "index.json"), data)
}

// readRoster reads roster.json: device name to age recipient. A
// store with no roster yet reads back as an empty roster.
func (s *Store) readRoster() (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join(s.Root, "roster.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	var roster map[string]string
	if err := json.Unmarshal(data, &roster); err != nil {
		return nil, fmt.Errorf("the device roster is corrupt: %v", err)
	}
	if roster == nil {
		roster = map[string]string{}
	}
	return roster, nil
}

// writeRoster encodes roster to roster.json, fsyncing before it
// renames the temp file into place.
func (s *Store) writeRoster(roster map[string]string) error {
	data, err := json.MarshalIndent(roster, "", "  ")
	if err != nil {
		return err
	}
	return writeFileSynced(filepath.Join(s.Root, "roster.json"), data)
}

// writeFileSynced writes data to a temp file next to path, fsyncs
// it, then renames it into place. The rename is atomic, so a reader
// never observes a partially written file, and the fsync means a
// crash right after the rename still has the bytes on disk.
func writeFileSynced(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// lock takes an exclusive flock on the store's data directory, so
// two loadoutd processes (or two goroutines) sharing one -data
// directory never read-modify-write index.json or roster.json at
// the same time. It mirrors internal/vault/lock.go's pattern: poll
// every 100ms, give up after 10s.
func (s *Store) lock() (release func(), err error) {
	path := filepath.Join(s.Root, "store.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(lockTimeout)
	for {
		flockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if flockErr == nil {
			return func() {
				syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			}, nil
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("the store at %s is locked by another loadoutd process. Fix: wait for it to finish, or remove store.lock if no loadoutd process runs.", s.Root)
		}
		time.Sleep(lockPollInterval)
	}
}
