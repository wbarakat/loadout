package server

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestServerPackageNeverImportsAge proves invariant 8 at the
// dependency level: the server package (and everything it pulls in)
// has no path to filippo.io/age, so no code in this package can ever
// call age.Decrypt or age.Encrypt on a stored blob.
func TestServerPackageNeverImportsAge(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", "{{join .Deps \"\\n\"}}", "loadout.dev/loadout/internal/server").CombinedOutput()
	if err != nil {
		t.Fatalf("go list failed: %v\n%s", err, out)
	}
	for _, dep := range strings.Split(string(out), "\n") {
		if dep == "filippo.io/age" {
			t.Fatal("internal/server must never depend on filippo.io/age: invariant 8 requires the server to never decrypt a blob")
		}
	}
}

func TestOpenCreatesBlobsDir(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "blobs")); err != nil {
		t.Fatalf("blobs dir not created: %v", err)
	}
}

func TestOpenRefusesUnwritableDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	roParent := filepath.Join(dir, "ro")
	if err := os.Mkdir(roParent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(roParent, 0o700) })
	_, err := Open(filepath.Join(roParent, "data"))
	if err == nil {
		t.Fatal("expected an error opening a store under an unwritable directory")
	}
}

func TestTokenGeneratedOnceAndPersists(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tok1, created1, err := s.Token()
	if err != nil {
		t.Fatal(err)
	}
	if !created1 {
		t.Fatal("expected created=true on first call")
	}
	if len(tok1) != 64 { // 32 bytes hex-encoded
		t.Fatalf("expected a 64-char hex token, got %d chars", len(tok1))
	}
	tok2, created2, err := s.Token()
	if err != nil {
		t.Fatal(err)
	}
	if created2 {
		t.Fatal("expected created=false on second call")
	}
	if tok2 != tok1 {
		t.Fatalf("token changed across calls: %q != %q", tok1, tok2)
	}

	info, err := os.Stat(filepath.Join(s.Root, "token"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected token file mode 0600, got %v", info.Mode().Perm())
	}

	// A fresh Store instance over the same dir reads back the same token.
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tok3, created3, err := s2.Token()
	if err != nil {
		t.Fatal(err)
	}
	if created3 {
		t.Fatal("expected created=false when a fresh Store re-opens an existing token")
	}
	if tok3 != tok1 {
		t.Fatal("token did not survive a fresh Store instance over the same dir")
	}
}

func TestUpsertDeviceIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.UpsertDevice("laptop", "age1recipientone"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertDevice("laptop", "age1recipienttwo"); err != nil {
		t.Fatal(err)
	}
	devices, err := s.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 {
		t.Fatalf("expected exactly one device after two upserts of the same name, got %d", len(devices))
	}
	if devices[0].Recipient != "age1recipienttwo" {
		t.Fatalf("expected the second upsert's recipient to win, got %q", devices[0].Recipient)
	}
}

func TestListDevicesSortedByName(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.UpsertDevice("zeta", "r-zeta"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertDevice("alpha", "r-alpha"); err != nil {
		t.Fatal(err)
	}
	devices, err := s.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 || devices[0].Name != "alpha" || devices[1].Name != "zeta" {
		t.Fatalf("expected devices sorted by name, got %+v", devices)
	}
}

func TestPutSnapshotWithEmptyParentStores(t *testing.T) {
	s := newTestStore(t)
	version, err := s.PutSnapshot("", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if !versionPattern.MatchString(version) {
		t.Fatalf("version %q does not match v<n>-<8 hex>", version)
	}
	latest, err := s.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if latest.Version != version || latest.Parent != "" {
		t.Fatalf("unexpected latest after first store: %+v", latest)
	}
}

func TestPutSnapshotStaleParentConflicts(t *testing.T) {
	s := newTestStore(t)
	v1, err := s.PutSnapshot("", []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	v2, err := s.PutSnapshot(v1, []byte("two"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.PutSnapshot(v1, []byte("three")) // v1 is now stale; v2 is latest
	var conflict *ParentConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected a *ParentConflictError, got %v", err)
	}
	if conflict.Latest != v2 {
		t.Fatalf("expected conflict.Latest %q, got %q", v2, conflict.Latest)
	}
}

func TestGetSnapshotRoundTripsByteIdentical(t *testing.T) {
	s := newTestStore(t)
	blob := []byte{0x00, 0xFF, 0x10, 0x00, 'h', 'i', 0x00}
	version, err := s.PutSnapshot("", blob)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSnapshot(version)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(blob) {
		t.Fatalf("round trip mismatch: got %v, want %v", got, blob)
	}
}

func TestGetSnapshotAbsentIsNotExist(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetSnapshot("v1-deadbeef")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist for an absent version, got %v", err)
	}
}

func TestGetSnapshotRefusesTraversal(t *testing.T) {
	s := newTestStore(t)
	// Plant a file outside blobs/ that a traversal would read if the
	// version string reached the filesystem unchecked.
	secret := filepath.Join(s.Root, "secret.txt")
	if err := os.WriteFile(secret, []byte("do not leak"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := s.GetSnapshot("../secret.txt")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist for a traversal attempt, got %v", err)
	}
}

func TestIndexSurvivesFreshStoreInstance(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	version, err := s1.PutSnapshot("", []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a restart: a brand new Store value over the same dir.
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := s2.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if latest.Version != version {
		t.Fatalf("expected the fresh store to see prior latest %q, got %q", version, latest.Version)
	}
	blob, err := s2.GetSnapshot(version)
	if err != nil {
		t.Fatal(err)
	}
	if string(blob) != "payload" {
		t.Fatalf("unexpected blob content after restart: %q", blob)
	}
}

// TestConcurrentPutSnapshotSameParentExactlyOneWins races two
// goroutines that both try to build on the same parent. The store's
// flock must serialize them completely: the loser reads the winner's
// new latest, not the shared stale parent, and the index is never
// left corrupt.
func TestConcurrentPutSnapshotSameParentExactlyOneWins(t *testing.T) {
	s := newTestStore(t)
	base, err := s.PutSnapshot("", []byte("base"))
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	results := make([]error, 2)
	versions := make([]string, 2)
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer wg.Done()
			v, err := s.PutSnapshot(base, []byte{byte(i)})
			versions[i] = v
			results[i] = err
		}(i)
	}
	wg.Wait()

	var wins, conflicts int
	var winnerVersion string
	var conflictErr *ParentConflictError
	for i := 0; i < 2; i++ {
		switch {
		case results[i] == nil:
			wins++
			winnerVersion = versions[i]
		case errors.As(results[i], &conflictErr):
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", results[i])
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("expected exactly one win and one conflict, got %d wins, %d conflicts", wins, conflicts)
	}
	if conflictErr.Latest != winnerVersion {
		t.Fatalf("conflict should report the winner %q as latest, got %q", winnerVersion, conflictErr.Latest)
	}

	// The index must be intact: valid JSON, exactly two versions
	// beyond the base (base + winner), and Latest pointing at the
	// winner.
	data, err := os.ReadFile(filepath.Join(s.Root, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var idx indexFile
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatalf("index.json is corrupt: %v", err)
	}
	if idx.Latest != winnerVersion {
		t.Fatalf("index latest = %q, want %q", idx.Latest, winnerVersion)
	}
	if len(idx.Versions) != 2 {
		t.Fatalf("expected 2 versions in the index (base + winner), got %d", len(idx.Versions))
	}

	latest, err := s.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if latest.Version != winnerVersion {
		t.Fatalf("Latest() = %q, want %q", latest.Version, winnerVersion)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}
