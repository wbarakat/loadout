package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestServer builds an httptest.Server backed by a fresh Store
// over a temp data dir, and returns it alongside the bearer token
// every protected route requires.
func newTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := store.Token()
	if err != nil {
		t.Fatal(err)
	}
	srv := New(store, token, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, token
}

func doReq(t *testing.T, method, url, token string, body io.Reader, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestHealthRequiresNoAuth(t *testing.T) {
	ts, _ := newTestServer(t)
	resp := doReq(t, http.MethodGet, ts.URL+"/health", "", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status=ok, got %+v", body)
	}
}

func TestProtectedRouteRejectsMissingOrWrongToken(t *testing.T) {
	ts, _ := newTestServer(t)

	cases := []struct {
		name  string
		token string
	}{
		{"absent", ""},
		{"wrong", "not-the-real-token"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := doReq(t, http.MethodGet, ts.URL+"/v1/devices", c.token, nil, nil)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", resp.StatusCode)
			}
		})
	}
}

func TestDeviceUpsertIsIdempotentOverHTTP(t *testing.T) {
	ts, token := newTestServer(t)
	body := `{"name":"laptop","recipient":"age1abc"}`

	for i := 0; i < 2; i++ {
		resp := doReq(t, http.MethodPost, ts.URL+"/v1/devices", token, bytes.NewBufferString(body), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("upsert %d: expected 200, got %d", i, resp.StatusCode)
		}
		resp.Body.Close()
	}

	resp := doReq(t, http.MethodGet, ts.URL+"/v1/devices", token, nil, nil)
	defer resp.Body.Close()
	var listed struct {
		Devices []Device `json:"devices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Devices) != 1 {
		t.Fatalf("expected exactly one device after two upserts, got %d", len(listed.Devices))
	}
}

func TestPostSnapshotEmptyParentStoresAndReturnsVersion(t *testing.T) {
	ts, token := newTestServer(t)
	resp := doReq(t, http.MethodPost, ts.URL+"/v1/snapshots", token, bytes.NewBufferString("blob-bytes"), map[string]string{"X-Loadout-Parent": ""})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var out struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !versionPattern.MatchString(out.Version) {
		t.Fatalf("returned version %q does not match the expected shape", out.Version)
	}
}

func TestPostSnapshotStaleParentGives409WithLatest(t *testing.T) {
	ts, token := newTestServer(t)

	resp1 := doReq(t, http.MethodPost, ts.URL+"/v1/snapshots", token, bytes.NewBufferString("first"), map[string]string{"X-Loadout-Parent": ""})
	var first struct {
		Version string `json:"version"`
	}
	json.NewDecoder(resp1.Body).Decode(&first)
	resp1.Body.Close()

	resp2 := doReq(t, http.MethodPost, ts.URL+"/v1/snapshots", token, bytes.NewBufferString("second"), map[string]string{"X-Loadout-Parent": first.Version})
	var second struct {
		Version string `json:"version"`
	}
	json.NewDecoder(resp2.Body).Decode(&second)
	resp2.Body.Close()

	// first.Version is now stale: the server's latest is second.Version.
	resp3 := doReq(t, http.MethodPost, ts.URL+"/v1/snapshots", token, bytes.NewBufferString("third"), map[string]string{"X-Loadout-Parent": first.Version})
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", resp3.StatusCode)
	}
	var conflict struct {
		Latest string `json:"latest"`
	}
	if err := json.NewDecoder(resp3.Body).Decode(&conflict); err != nil {
		t.Fatal(err)
	}
	if conflict.Latest != second.Version {
		t.Fatalf("expected conflict.latest %q, got %q", second.Version, conflict.Latest)
	}
}

func TestGetLatestReflectsLastStore(t *testing.T) {
	ts, token := newTestServer(t)

	resp := doReq(t, http.MethodGet, ts.URL+"/v1/snapshots/latest", token, nil, nil)
	var empty struct {
		Version string `json:"version"`
		Parent  string `json:"parent"`
	}
	json.NewDecoder(resp.Body).Decode(&empty)
	resp.Body.Close()
	if empty.Version != "" {
		t.Fatalf("expected empty version before any snapshot, got %q", empty.Version)
	}

	postResp := doReq(t, http.MethodPost, ts.URL+"/v1/snapshots", token, bytes.NewBufferString("data"), map[string]string{"X-Loadout-Parent": ""})
	var stored struct {
		Version string `json:"version"`
	}
	json.NewDecoder(postResp.Body).Decode(&stored)
	postResp.Body.Close()

	latestResp := doReq(t, http.MethodGet, ts.URL+"/v1/snapshots/latest", token, nil, nil)
	defer latestResp.Body.Close()
	var latest struct {
		Version string `json:"version"`
		Parent  string `json:"parent"`
	}
	json.NewDecoder(latestResp.Body).Decode(&latest)
	if latest.Version != stored.Version {
		t.Fatalf("expected latest.version %q, got %q", stored.Version, latest.Version)
	}
	if latest.Parent != "" {
		t.Fatalf("expected latest.parent empty, got %q", latest.Parent)
	}
}

// TestSnapshotRoundTripByteIdentical proves invariant 8 end to end
// over HTTP: an arbitrary byte blob (including bytes that are not
// valid UTF-8 or a valid age file) comes back exactly as sent. The
// server never has to understand the bytes to serve them.
func TestSnapshotRoundTripByteIdentical(t *testing.T) {
	ts, token := newTestServer(t)
	blob := []byte{0x00, 0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0xFF, 'n', 'o', 't', ' ', 'a', 'g', 'e'}

	postResp := doReq(t, http.MethodPost, ts.URL+"/v1/snapshots", token, bytes.NewReader(blob), map[string]string{"X-Loadout-Parent": ""})
	var stored struct {
		Version string `json:"version"`
	}
	json.NewDecoder(postResp.Body).Decode(&stored)
	postResp.Body.Close()

	getResp := doReq(t, http.MethodGet, ts.URL+"/v1/snapshots/"+stored.Version, token, nil, nil)
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", getResp.StatusCode)
	}
	got, err := io.ReadAll(getResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, blob) {
		t.Fatalf("round trip mismatch: got %v, want %v", got, blob)
	}
}

func TestGetSnapshotAbsentVersionGives404(t *testing.T) {
	ts, token := newTestServer(t)
	resp := doReq(t, http.MethodGet, ts.URL+"/v1/snapshots/v99-deadbeef", token, nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

// TestIndexSurvivesRestartOverHTTP mirrors the store-level restart
// test but through the HTTP surface: a fresh Store and Server over
// the same data dir (simulating loadoutd restarting) still reports
// the prior latest.
func TestIndexSurvivesRestartOverHTTP(t *testing.T) {
	dir := t.TempDir()
	store1, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := store1.Token()
	if err != nil {
		t.Fatal(err)
	}
	srv1 := New(store1, token, log.New(io.Discard, "", 0))
	ts1 := httptest.NewServer(srv1.Handler())

	postResp := doReq(t, http.MethodPost, ts1.URL+"/v1/snapshots", token, bytes.NewBufferString("restart-me"), map[string]string{"X-Loadout-Parent": ""})
	var stored struct {
		Version string `json:"version"`
	}
	json.NewDecoder(postResp.Body).Decode(&stored)
	postResp.Body.Close()
	ts1.Close()

	// "Restart": a brand new Store and Server over the same data dir.
	store2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	token2, created2, err := store2.Token()
	if err != nil {
		t.Fatal(err)
	}
	if created2 {
		t.Fatal("expected the token to survive the restart, not be regenerated")
	}
	srv2 := New(store2, token2, log.New(io.Discard, "", 0))
	ts2 := httptest.NewServer(srv2.Handler())
	defer ts2.Close()

	latestResp := doReq(t, http.MethodGet, ts2.URL+"/v1/snapshots/latest", token2, nil, nil)
	defer latestResp.Body.Close()
	var latest struct {
		Version string `json:"version"`
	}
	json.NewDecoder(latestResp.Body).Decode(&latest)
	if latest.Version != stored.Version {
		t.Fatalf("expected the restarted server to see prior latest %q, got %q", stored.Version, latest.Version)
	}
}

// TestConcurrentPostSnapshotSameParentOverHTTP races two HTTP POSTs
// with the same parent header against a live httptest.Server. The
// store's flock must serialize them: exactly one 200, one 409, and
// the index stays readable and consistent afterward.
func TestConcurrentPostSnapshotSameParentOverHTTP(t *testing.T) {
	ts, token := newTestServer(t)

	baseResp := doReq(t, http.MethodPost, ts.URL+"/v1/snapshots", token, bytes.NewBufferString("base"), map[string]string{"X-Loadout-Parent": ""})
	var base struct {
		Version string `json:"version"`
	}
	json.NewDecoder(baseResp.Body).Decode(&base)
	baseResp.Body.Close()

	var wg sync.WaitGroup
	statuses := make([]int, 2)
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer wg.Done()
			resp := doReq(t, http.MethodPost, ts.URL+"/v1/snapshots", token, bytes.NewBufferString("racer"), map[string]string{"X-Loadout-Parent": base.Version})
			statuses[i] = resp.StatusCode
			resp.Body.Close()
		}(i)
	}
	wg.Wait()

	var ok200, ok409 int
	for _, code := range statuses {
		switch code {
		case http.StatusOK:
			ok200++
		case http.StatusConflict:
			ok409++
		default:
			t.Fatalf("unexpected status code %d", code)
		}
	}
	if ok200 != 1 || ok409 != 1 {
		t.Fatalf("expected exactly one 200 and one 409, got %d 200s and %d 409s", ok200, ok409)
	}

	// The index must still be readable and consistent after the race.
	latestResp := doReq(t, http.MethodGet, ts.URL+"/v1/snapshots/latest", token, nil, nil)
	defer latestResp.Body.Close()
	if latestResp.StatusCode != http.StatusOK {
		t.Fatalf("expected the index to remain readable after the race, got status %d", latestResp.StatusCode)
	}
}

// TestServerNeverLogsTokenOrBlob proves invariant 8's logging half
// with a real logger, not io.Discard: neither the bearer token nor a
// snapshot's content ever reaches a log line, even when a request
// carries the token in its Authorization header or a snapshot body
// holds a memorable marker. It also checks the log does carry the
// method/path/status/version fields, proving the logger actually ran
// rather than the assertions passing on an empty buffer.
func TestServerNeverLogsTokenOrBlob(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := store.Token()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	srv := New(store, token, log.New(&buf, "", 0))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	blobMarker := fmt.Sprintf("SECRET-BLOB-MARKER-%d", time.Now().UnixNano())

	devResp := doReq(t, http.MethodPost, ts.URL+"/v1/devices", token, bytes.NewBufferString(`{"name":"laptop","recipient":"age1abc"}`), nil)
	devResp.Body.Close()

	postResp := doReq(t, http.MethodPost, ts.URL+"/v1/snapshots", token, bytes.NewBufferString(blobMarker), map[string]string{"X-Loadout-Parent": ""})
	var stored struct {
		Version string `json:"version"`
	}
	json.NewDecoder(postResp.Body).Decode(&stored)
	postResp.Body.Close()

	getResp := doReq(t, http.MethodGet, ts.URL+"/v1/snapshots/"+stored.Version, token, nil, nil)
	io.Copy(io.Discard, getResp.Body)
	getResp.Body.Close()

	// A 401 attempt whose Authorization header literally carries the
	// real token, just without the required "Bearer " prefix, so it
	// is refused while still putting the secret in front of the
	// logging code.
	unauthResp := doReq(t, http.MethodGet, ts.URL+"/v1/devices", "", nil, map[string]string{"Authorization": token})
	unauthResp.Body.Close()
	if unauthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a bearer-prefix-less Authorization header, got %d", unauthResp.StatusCode)
	}

	logged := buf.String()
	if strings.Contains(logged, token) {
		t.Fatalf("the access token leaked into the log:\n%s", logged)
	}
	if strings.Contains(logged, blobMarker) {
		t.Fatalf("the blob content leaked into the log:\n%s", logged)
	}
	if strings.Contains(logged, "Authorization") {
		t.Fatalf("the Authorization header name leaked into the log:\n%s", logged)
	}

	for _, want := range []string{"POST /v1/devices", "POST /v1/snapshots", "GET /v1/snapshots/", "version=", "200", "401"} {
		if !strings.Contains(logged, want) {
			t.Fatalf("expected the log to contain %q, proving the logger actually ran:\n%s", want, logged)
		}
	}
}
