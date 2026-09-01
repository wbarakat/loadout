package remote_test

// Regression suite for the 2026-08-31 whole-branch fix wave (senior
// review of the Phase 4 cloud-sync branch). Each test here proves one
// numbered finding from that review, in internal/remote/sync.go:
//
//   - CRITICAL 1 (TestSyncIdleRepublishSkipsWhenNothingChanged,
//     TestLoadStatusIgnoresNonSyncedSetChanges): Sync's caught-up
//     branch pushed unconditionally on every call, minting a new
//     full-blob version even when nothing changed — under "loadout
//     watch" this is unbounded server growth for zero real content
//     change, fatal on a small disk. Fixed by comparing the synced
//     tree at HEAD against the tree at the last confirmed sync base
//     before ever packing or pushing, and skipping the push (and
//     LoadStatus's "ahead" report) when nothing in the SyncedSet
//     actually changed.
//   - MINOR 7 (TestSyncRecoversFromServerResetByReseeding): a push
//     conflict whose reported latest is "" (the remote's store was
//     reset or emptied) used to fall into pullMergePush, which tried
//     to GET version "" and got a raw, un-fixed 404. Fixed by
//     treating an empty conflict latest as "re-seed the remote from
//     this device's own content" rather than pulling nothing.
//
// These tests must never be deleted or weakened: they are the proof
// the fixes hold.

import (
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"loadout.dev/loadout/internal/remote"
	"loadout.dev/loadout/internal/server"
	"loadout.dev/loadout/internal/vault"
)

// TestSyncIdleRepublishSkipsWhenNothingChanged is CRITICAL 1's core
// regression test: two syncs with no local edit between them must
// mint only ONE version total — the second sync is a true no-op,
// reporting the same version and Pushed=false — while a real edit
// between two syncs must still mint a second, distinct version.
func TestSyncIdleRepublishSkipsWhenNothingChanged(t *testing.T) {
	ts, token := newTestServer(t)
	a := newSyncTestVault(t, "device-a", ts.URL, token)

	writeFact(t, a, "stack", "hello\n")
	res1, err := remote.Sync(a)
	if err != nil {
		t.Fatalf("first sync failed: %v", err)
	}
	if !res1.Pushed || res1.Version == "" {
		t.Fatalf("the first sync must push a version, got %+v", res1)
	}

	// Idle: no local change since res1. This must be a true no-op.
	res2, err := remote.Sync(a)
	if err != nil {
		t.Fatalf("second (idle) sync failed: %v", err)
	}
	if res2.Pushed {
		t.Fatalf("an idle sync with no local change must not push a new version, got %+v", res2)
	}
	if res2.Version != res1.Version {
		t.Fatalf("an idle sync must report the same version, got %q want %q", res2.Version, res1.Version)
	}

	// A further idle sync is still a no-op: the churn is not just
	// suppressed once.
	res3, err := remote.Sync(a)
	if err != nil {
		t.Fatalf("third (idle) sync failed: %v", err)
	}
	if res3.Pushed || res3.Version != res1.Version {
		t.Fatalf("a further idle sync must still be a no-op, got %+v", res3)
	}

	// A real edit between syncs must mint a genuinely new version.
	writeFact(t, a, "stack", "a real edit\n")
	res4, err := remote.Sync(a)
	if err != nil {
		t.Fatalf("sync after a real edit failed: %v", err)
	}
	if !res4.Pushed {
		t.Fatalf("a real edit between syncs must mint a new version, got %+v", res4)
	}
	if res4.Version == res1.Version {
		t.Fatal("a real edit must mint a version distinct from the idle one")
	}
}

// TestLoadStatusIgnoresNonSyncedSetChanges proves LoadStatus's "ahead"
// heuristic was aligned to the same synced-set tree-compare CRITICAL
// 1's push-skip uses: a git commit that touches only a path OUTSIDE
// the SyncedSet (skills/, memory/, devices.toml) must never report
// "ahead", since a sync would never push it anyway. A real edit
// inside the synced set must still report "ahead".
func TestLoadStatusIgnoresNonSyncedSetChanges(t *testing.T) {
	ts, token := newTestServer(t)
	a := newSyncTestVault(t, "device-a", ts.URL, token)

	writeFact(t, a, "stack", "hello\n")
	if _, err := remote.Sync(a); err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}

	st, ok, err := remote.LoadStatus(a)
	if err != nil || !ok {
		t.Fatalf("LoadStatus failed: ok=%v err=%v", ok, err)
	}
	if st.State != "in sync" {
		t.Fatalf("a freshly synced device must report in sync, got %+v", st)
	}

	// Touch and commit a tracked file OUTSIDE the synced set: the
	// vault's own git HEAD moves, but nothing a sync would ever push
	// actually changed.
	if err := os.WriteFile(filepath.Join(a.Root, "notes.txt"), []byte("a device-local note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(a, "add a local note"); err != nil {
		t.Fatal(err)
	}

	st, ok, err = remote.LoadStatus(a)
	if err != nil || !ok {
		t.Fatalf("LoadStatus failed: ok=%v err=%v", ok, err)
	}
	if st.State != "in sync" {
		t.Fatalf("a change outside the synced set must never report ahead, got %+v", st)
	}

	// A real synced-set edit, by contrast, must still report ahead.
	writeFact(t, a, "stack", "a real edit\n")
	st, ok, err = remote.LoadStatus(a)
	if err != nil || !ok {
		t.Fatalf("LoadStatus failed: ok=%v err=%v", ok, err)
	}
	if st.State != "ahead" {
		t.Fatalf("a real synced-set edit must still report ahead, got %+v", st)
	}
}

// TestSyncRecoversFromServerResetByReseeding is MINOR 7's regression
// test: the remote's store is reset (its index lost) while this
// device still believes it holds a valid last version. The next sync
// must recover by re-seeding the remote from this device's own
// content, never dead-ending on a GetSnapshot("") 404.
func TestSyncRecoversFromServerResetByReseeding(t *testing.T) {
	dataDir := t.TempDir()
	store, err := server.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := store.Token()
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(store, token, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	a := newSyncTestVault(t, "device-a", ts.URL, token)
	writeFact(t, a, "x", "hello\n")
	res1, err := remote.Sync(a)
	if err != nil {
		t.Fatalf("first sync failed: %v", err)
	}
	if !res1.Pushed {
		t.Fatalf("the first sync must push, got %+v", res1)
	}

	// Simulate the remote being reset or emptied: its index is gone,
	// so Latest() now reports no version at all, even though this
	// device still believes it holds one.
	if err := os.Remove(filepath.Join(dataDir, "index.json")); err != nil {
		t.Fatal(err)
	}

	writeFact(t, a, "y", "world\n")
	res2, err := remote.Sync(a)
	if err != nil {
		t.Fatalf("sync must recover by re-seeding the reset remote, not dead-end, got err: %v", err)
	}
	if !res2.Pushed {
		t.Fatalf("recovering from a reset remote must be a push, got %+v", res2)
	}

	client := remote.NewClient(ts.URL, token)
	version, _, err := client.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if version != res2.Version {
		t.Fatalf("the remote's latest must match what Sync just re-seeded, got %q want %q", version, res2.Version)
	}

	// The re-seeded snapshot must carry this device's real content,
	// not an empty blob.
	blob, err := client.GetSnapshot(res2.Version)
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	if err := vault.UnpackSnapshot(a, blob, tmp); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(tmp, "memory", "y.md"))
	if err != nil || string(got) != "world\n" {
		t.Fatalf("the re-seeded snapshot must carry this device's real content, got %q err=%v", got, err)
	}
}
