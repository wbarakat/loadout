package server

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestServerWithCORS builds a test server exactly like
// newTestServer, but with the given CORS origin configured (an
// empty origin keeps CORS off, matching the real default).
func newTestServerWithCORS(t *testing.T, origin string) (*httptest.Server, string) {
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
	srv.SetCORSOrigin(origin)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, token
}

// TestCORSPreflightReturnsHeadersAndNoBody proves the preflight
// contract: when a browser origin is configured, an OPTIONS request
// (which carries no Authorization header) gets 204 and the exact
// CORS headers a browser needs, and never a body — a preflight must
// never be able to leak data.
func TestCORSPreflightReturnsHeadersAndNoBody(t *testing.T) {
	const origin = "https://loadout.example.com"
	ts, _ := newTestServerWithCORS(t, origin)

	resp := doReq(t, http.MethodOptions, ts.URL+"/v1/snapshots/latest", "", nil, map[string]string{"Origin": origin})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("expected Access-Control-Allow-Origin %q, got %q", origin, got)
	}
	if got := resp.Header.Get("Vary"); got != "Origin" {
		t.Fatalf("expected Vary: Origin, got %q", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Methods"); !strings.Contains(got, "POST") {
		t.Fatalf("expected Access-Control-Allow-Methods to contain POST, got %q", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Authorization") || !strings.Contains(got, "X-Loadout-Parent") {
		t.Fatalf("expected Access-Control-Allow-Headers to contain Authorization and X-Loadout-Parent, got %q", got)
	}
	if got := resp.Header.Get("Access-Control-Max-Age"); got != "600" {
		t.Fatalf("expected Access-Control-Max-Age: 600, got %q", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 0 {
		t.Fatalf("expected an empty preflight body, got %q", body)
	}
}

// TestCORSHeaderOnNormalResponse proves the second half of the
// contract: a real, non-preflight response also carries the CORS
// header, so the browser's actual fetch (not just its preflight) is
// allowed to read the response.
func TestCORSHeaderOnNormalResponse(t *testing.T) {
	const origin = "https://loadout.example.com"
	ts, _ := newTestServerWithCORS(t, origin)

	resp := doReq(t, http.MethodGet, ts.URL+"/health", "", nil, map[string]string{"Origin": origin})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("expected Access-Control-Allow-Origin %q, got %q", origin, got)
	}
}

// TestCORSOffByDefaultPreflightPassesThrough proves CORS stays off
// unless a self-host operator opts in: with no origin configured
// (the zero value, exactly what a server built without
// SetCORSOrigin has), an OPTIONS preflight gets no CORS headers at
// all — corsMiddleware is a pure pass-through.
func TestCORSOffByDefaultPreflightPassesThrough(t *testing.T) {
	ts, _ := newTestServer(t) // no SetCORSOrigin call: corsOrigin is "".

	resp := doReq(t, http.MethodOptions, ts.URL+"/v1/snapshots/latest", "", nil, map[string]string{"Origin": "https://loadout.example.com"})
	defer resp.Body.Close()

	for _, h := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Methods", "Access-Control-Allow-Headers", "Access-Control-Max-Age"} {
		if got := resp.Header.Get(h); got != "" {
			t.Fatalf("expected no %s header when CORS is off, got %q", h, got)
		}
	}
}

// TestCORSDoesNotBypassAuthForRealRequests proves the preflight
// exemption is scoped to OPTIONS only: enabling CORS must never
// weaken auth on an actual /v1/* request. A GET with no bearer
// token still gets 401, exactly as it does with CORS off.
func TestCORSDoesNotBypassAuthForRealRequests(t *testing.T) {
	ts, _ := newTestServerWithCORS(t, "https://loadout.example.com")

	resp := doReq(t, http.MethodGet, ts.URL+"/v1/devices", "", nil, map[string]string{"Origin": "https://loadout.example.com"})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a real request with no token, even with CORS enabled, got %d", resp.StatusCode)
	}
}
