package remote_test

import (
	"errors"
	"io"
	"log"
	"net/http/httptest"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/remote"
	"loadout.dev/loadout/internal/server"
)

// newTestServer builds an httptest.Server backed by a fresh Store,
// mirroring internal/server's own test helper, so internal/remote's
// tests exercise the client against the real Task 4 API.
func newTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	store, err := server.Open(t.TempDir())
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
	return ts, token
}

func TestClientRegisterDeviceIsIdempotent(t *testing.T) {
	ts, token := newTestServer(t)
	c := remote.NewClient(ts.URL, token)
	for i := 0; i < 2; i++ {
		if err := c.RegisterDevice("laptop", "age1abc"); err != nil {
			t.Fatalf("RegisterDevice %d failed: %v", i, err)
		}
	}
	devices, err := c.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 {
		t.Fatalf("expected exactly one device after two registrations, got %d", len(devices))
	}
}

func TestClientLatestOnEmptyRemoteIsEmpty(t *testing.T) {
	ts, token := newTestServer(t)
	c := remote.NewClient(ts.URL, token)
	version, parent, err := c.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if version != "" || parent != "" {
		t.Fatalf("expected empty version and parent, got %q %q", version, parent)
	}
}

func TestClientPutAndGetSnapshotRoundTrip(t *testing.T) {
	ts, token := newTestServer(t)
	c := remote.NewClient(ts.URL, token)
	version, err := c.PutSnapshot([]byte("blob-bytes"), "")
	if err != nil {
		t.Fatal(err)
	}
	if version == "" {
		t.Fatal("expected a non-empty version")
	}
	got, err := c.GetSnapshot(version)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "blob-bytes" {
		t.Fatalf("GetSnapshot = %q, want %q", got, "blob-bytes")
	}
	gotVersion, gotParent, err := c.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if gotVersion != version || gotParent != "" {
		t.Fatalf("Latest = (%q, %q), want (%q, %q)", gotVersion, gotParent, version, "")
	}
}

// TestClientPutSnapshotStaleParentGivesTypedConflictError proves a
// stale parent surfaces as *remote.ConflictError, not a generic error,
// carrying the remote's actual latest version.
func TestClientPutSnapshotStaleParentGivesTypedConflictError(t *testing.T) {
	ts, token := newTestServer(t)
	c := remote.NewClient(ts.URL, token)
	first, err := c.PutSnapshot([]byte("first"), "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.PutSnapshot([]byte("second"), first)
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.PutSnapshot([]byte("third"), first) // first is now stale
	if err == nil {
		t.Fatal("expected an error for a stale parent")
	}
	var conflict *remote.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected a *remote.ConflictError, got %T: %v", err, err)
	}
	if conflict.Latest != second {
		t.Fatalf("conflict.Latest = %q, want %q", conflict.Latest, second)
	}
}

func TestClientListDevicesEmpty(t *testing.T) {
	ts, token := newTestServer(t)
	c := remote.NewClient(ts.URL, token)
	devices, err := c.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 0 {
		t.Fatalf("expected no devices, got %v", devices)
	}
}

// TestClientUnreachableRemoteGivesGrammarTrueError proves a client
// pointed at a url nothing listens on gets the fixed "not reachable"
// grammar, naming the url and the fix, not a bare net/http error.
func TestClientUnreachableRemoteGivesGrammarTrueError(t *testing.T) {
	c := remote.NewClient("http://127.0.0.1:1", "any-token")
	_, _, err := c.Latest()
	if err == nil {
		t.Fatal("expected an error for an unreachable remote")
	}
	if !strings.Contains(err.Error(), "http://127.0.0.1:1") {
		t.Fatalf("error must name the url, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "is not reachable:") {
		t.Fatalf("error must use the fixed grammar, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "Fix: check the url and that loadoutd runs.") {
		t.Fatalf("error must carry the fix, got %q", err.Error())
	}
}

// TestClientWrongTokenGivesAnError proves an authentication failure
// surfaces as an error, and that the error text never repeats the
// wrong token back.
func TestClientWrongTokenGivesAnError(t *testing.T) {
	ts, _ := newTestServer(t)
	c := remote.NewClient(ts.URL, "not-the-real-token")
	_, _, err := c.Latest()
	if err == nil {
		t.Fatal("expected an error for a wrong token")
	}
	if strings.Contains(err.Error(), "not-the-real-token") {
		t.Fatalf("error must never echo the token, got %q", err.Error())
	}
}
