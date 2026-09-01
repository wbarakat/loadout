package mcp_test

// broker_adversarial_test.go is the CONSOLIDATED, PERMANENT adversarial
// suite for the MCP secret broker (internal/mcp/broker.go): every
// exfiltration vector this branch's review tried against http_request,
// collected here as named, table-driven regression tests so none of
// them can silently regress. It reuses broker_test.go's fixtures
// (newBrokerVault, hostOf, brokerArgs, toJSON, accessLogText) and
// tools_test.go's callTool, all in this same package.
//
// Every refused case below asserts the same three things: the
// forbidden/attacker server gets 0 hits, the dummy value never appears
// in the tool result or its error text, and the access log has no
// "broker" entry. The one deliberate exception is the case-insensitive
// host match: it must be ALLOWED, and its log entry must name only the
// host, never the value.
//
// Two vectors already have a permanent, named test elsewhere and are
// not duplicated here:
//   - scheme-downgrade same-host redirect: broker_internal_test.go's
//     TestRefuseUnsafeRedirectRefusesSchemeDowngradeSameHost. It is a
//     white-box test of refuseUnsafeRedirect itself; no true
//     end-to-end equivalent is possible here, since that would need a
//     TLS certificate the broker's plain http.Client would trust, and
//     the broker builds its own client with no hook for a test to
//     inject one.
//   - the headline substitution mechanism and the access-log line
//     shape: broker_test.go's TestHTTPRequestSubstitutesSecretIntoAllowedHost
//     and TestHTTPRequestAccessLogRecordsHostNotValue. Those are
//     functional proofs, not adversarial ones.

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// newHitCountingServer returns an httptest.Server that counts every
// request it receives. Every "the forbidden server got 0 hits"
// assertion below reads this counter — proof the broker never even
// dialed it, not just that it reported a refusal.
func newHitCountingServer(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// TestHTTPRequestAdversarialHostVectorsRefused is the core adversarial
// table: every way an attacker might try to make the request's real
// host look like, or hide behind, an allow-listed one. forbidden is a
// REAL local server: for the vectors whose malicious host can be made
// to literally resolve to it (userinfo, path smuggle, the two port
// mismatches, the trailing dot), a regression that ever allowed the
// request through would show up as a real hit here, not just a
// changed error string. The suffix and prefix vectors use a purely
// fictitious allowed host instead: the attack there is a malformed
// domain shape, not a resolvable target, and the check refuses it from
// the parsed host string alone, before any connection is even
// attempted — the same reasoning the pre-existing exact-match test in
// this package already relied on.
func TestHTTPRequestAdversarialHostVectorsRefused(t *testing.T) {
	forbidden, hits := newHitCountingServer(t)
	forbiddenHost := hostOf(t, forbidden.URL)
	bareHost, port, err := net.SplitHostPort(forbiddenHost)
	if err != nil {
		t.Fatalf("splitting %q: %v", forbiddenHost, err)
	}

	// fakeAllowed is a plausible allow-list entry that this suite never
	// dials: every case that uses it is refused before any request is
	// ever sent, so it never needs to resolve to anything real.
	const fakeAllowed = "api.example.com"

	cases := []struct {
		name         string
		allowedHosts []string
		url          string
	}{
		{
			name:         "userinfo host spoof (allowed host before the @, real host after)",
			allowedHosts: []string{fakeAllowed},
			url:          "http://" + fakeAllowed + "@" + forbiddenHost + "/steal",
		},
		{
			name:         "path smuggle (allowed host in the path, real host elsewhere)",
			allowedHosts: []string{fakeAllowed},
			url:          "http://" + forbiddenHost + "/" + fakeAllowed,
		},
		{
			name:         "port added (allow-list has no port, request has one)",
			allowedHosts: []string{bareHost},
			url:          "http://" + forbiddenHost + "/x",
		},
		{
			name:         "port stripped (allow-list has a port, request has none)",
			allowedHosts: []string{forbiddenHost},
			url:          "http://" + bareHost + "/x",
		},
		{
			name:         "trailing dot on the request host",
			allowedHosts: []string{forbiddenHost},
			url:          "http://" + bareHost + ".:" + port + "/x",
		},
		{
			name:         "suffix: the allowed host is a PREFIX of the request host",
			allowedHosts: []string{fakeAllowed},
			url:          "http://" + fakeAllowed + ".evil.com/steal",
		},
		{
			name:         "prefix: the allowed host is a SUFFIX of the request host",
			allowedHosts: []string{fakeAllowed},
			url:          "http://evil-" + fakeAllowed + "/steal",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			atomic.StoreInt32(hits, 0)
			v := newBrokerVault(t, c.allowedHosts)
			args := toJSON(t, brokerArgs{
				Method:  "GET",
				URL:     c.url,
				Headers: map[string]string{"Authorization": "Bearer {{secret:api-key}}"},
			})
			text, isError := callTool(t, v, "http_request", args)
			if !isError {
				t.Fatalf("want isError true, got text %q", text)
			}
			if atomic.LoadInt32(hits) != 0 {
				t.Fatalf("the forbidden server must get 0 hits, got %d", atomic.LoadInt32(hits))
			}
			if strings.Contains(text, brokerDummyValue) {
				t.Fatalf("the refusal leaked the dummy value: %q", text)
			}
			if strings.Contains(accessLogText(t, v), "broker") {
				t.Fatal("a refused call must never write an access-log entry")
			}
		})
	}
}

// TestHTTPRequestCaseInsensitiveHostAllowed proves the check is not
// overbroad: DNS host names are case-insensitive, so an allow-list
// entry that differs from the request's host only in case must still
// be ALLOWED, not refused. This runs against a real local server: the
// request must genuinely reach it, with the value substituted.
func TestHTTPRequestCaseInsensitiveHostAllowed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()
	_, port, err := net.SplitHostPort(hostOf(t, server.URL))
	if err != nil {
		t.Fatal(err)
	}

	v := newBrokerVault(t, []string{"LOCALHOST:" + port})
	args := toJSON(t, brokerArgs{
		Method:  "GET",
		URL:     "http://localhost:" + port + "/path",
		Headers: map[string]string{"Authorization": "Bearer {{secret:api-key}}"},
	})
	text, isError := callTool(t, v, "http_request", args)
	if isError {
		t.Fatalf("a case-only difference in the host must still be allowed, got isError true: %s", text)
	}
	if strings.Contains(text, brokerDummyValue) {
		t.Fatalf("the tool result leaked the dummy value: %s", text)
	}
	logText := accessLogText(t, v)
	if !strings.Contains(logText, `"secret":"api-key"`) {
		t.Fatalf("the allowed call must still write a broker access-log entry: %s", logText)
	}
	if strings.Contains(logText, brokerDummyValue) {
		t.Fatalf("the access log leaked the dummy value: %s", logText)
	}
}

// TestHTTPRequestPlaceholderAnywhereInURLRefused proves a
// "{{secret:...}}" reference is refused wherever it sits in the url —
// the host, the port, the path, the query, or the userinfo — never
// only the host case already named elsewhere. No server is needed for
// any of these: httpRequestTool's Contains check runs on the raw url
// STRING before url.Parse is even called, so no case here is ever
// dialed, valid syntax or not.
func TestHTTPRequestPlaceholderAnywhereInURLRefused(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{name: "in the host", url: "http://{{secret:api-key}}.example.com/"},
		{name: "in the port", url: "http://example.com:{{secret:api-key}}/path"},
		{name: "in the path", url: "http://example.com/{{secret:api-key}}"},
		{name: "in the query", url: "http://example.com/path?token={{secret:api-key}}"},
		{name: "in the userinfo", url: "http://{{secret:api-key}}@example.com/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := newBrokerVault(t, []string{"example.com"})
			args := toJSON(t, brokerArgs{Method: "GET", URL: c.url})
			text, isError := callTool(t, v, "http_request", args)
			if !isError {
				t.Fatalf("want isError true for a placeholder in the url, got text %q", text)
			}
			if !strings.Contains(text, "must not contain") {
				t.Fatalf("bad refusal message: %q", text)
			}
			if strings.Contains(text, brokerDummyValue) {
				t.Fatalf("the refusal leaked the dummy value: %q", text)
			}
			if strings.Contains(accessLogText(t, v), "broker") {
				t.Fatal("a refused-before-decrypt call must never write an access-log entry")
			}
		})
	}
}

// TestHTTPRequestCrossHostRedirectRefusedForEveryStatus proves the
// cross-host redirect refusal holds for every 3xx status a real server
// might use, not just 302: the allowed host receives the secret, but
// the redirect target (a different host) is never sent a request,
// whichever of 301/302/303/307/308 the allowed host answers with.
func TestHTTPRequestCrossHostRedirectRefusedForEveryStatus(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(fmt.Sprintf("%d", status), func(t *testing.T) {
			var targetHits int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&targetHits, 1)
			}))
			defer target.Close()

			var redirectorAuth string
			redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				redirectorAuth = r.Header.Get("Authorization")
				http.Redirect(w, r, target.URL+"/steal", status)
			}))
			defer redirector.Close()

			v := newBrokerVault(t, []string{hostOf(t, redirector.URL)})
			args := toJSON(t, brokerArgs{
				Method:  "GET",
				URL:     redirector.URL,
				Headers: map[string]string{"Authorization": "Bearer {{secret:api-key}}"},
			})
			text, isError := callTool(t, v, "http_request", args)
			if isError {
				t.Fatalf("http_request returned isError true: %s", text)
			}
			if redirectorAuth != "Bearer "+brokerDummyValue {
				t.Fatalf("the allowed host must receive the substituted value, got Authorization = %q", redirectorAuth)
			}
			if atomic.LoadInt32(&targetHits) != 0 {
				t.Fatalf("status %d: the redirect target (a different host) must never receive a request", status)
			}
			if !strings.Contains(text, fmt.Sprintf(`"status":%d`, status)) {
				t.Fatalf("the tool result must report the %d status rather than follow it, got: %s", status, text)
			}
			if strings.Contains(text, brokerDummyValue) {
				t.Fatalf("the tool result leaked the dummy value: %s", text)
			}
		})
	}
}

// TestHTTPRequestEmptyAllowedHostsNeverDecrypts proves the fail-closed
// default runs all the way to never decrypting: this secret's
// value.age is deliberately corrupted first, so IF the broker ever
// tried to decrypt it, that attempt would fail with ITS OWN distinct
// message ("cannot be used"). The refusal actually returned must be
// the allow-list message instead — proof the decrypt step was never
// reached at all, not just that its result was discarded.
func TestHTTPRequestEmptyAllowedHostsNeverDecrypts(t *testing.T) {
	forbidden, hits := newHitCountingServer(t)

	v := newBrokerVault(t, nil) // no allowed_hosts: fail closed.
	valuePath := filepath.Join(v.SecretsDir(), "api-key", "value.age")
	if err := os.WriteFile(valuePath, []byte("not a valid age file"), 0o600); err != nil {
		t.Fatal(err)
	}

	args := toJSON(t, brokerArgs{
		Method:  "GET",
		URL:     forbidden.URL,
		Headers: map[string]string{"Authorization": "Bearer {{secret:api-key}}"},
	})
	text, isError := callTool(t, v, "http_request", args)
	if !isError {
		t.Fatalf("want isError true for an empty allowed_hosts, got text %q", text)
	}
	if !strings.Contains(text, "no allowed_hosts configured") {
		t.Fatalf("want the allow-list refusal message, got %q", text)
	}
	if strings.Contains(text, "cannot be used") || strings.Contains(text, "cannot be read") {
		t.Fatalf("the refusal must be the allow-list message, never a decrypt error — decrypt must never run: %q", text)
	}
	if atomic.LoadInt32(hits) != 0 {
		t.Fatal("the outbound server must never receive a request when allowed_hosts is empty")
	}
	if strings.Contains(text, brokerDummyValue) {
		t.Fatalf("the refusal leaked the dummy value: %q", text)
	}
	if strings.Contains(accessLogText(t, v), "broker") {
		t.Fatal("a refused-before-decrypt call must never write an access-log entry")
	}
}

// TestHTTPRequestTraversalSecretNameRefused proves a secret reference
// shaped like a path-traversal attempt is refused as a malformed name
// — the same ValidateSecretName check that closed the path-traversal
// hole in the vault's own secret storage (see vault.ValidateSecretName)
// — rather than ever being looked up or decrypted.
func TestHTTPRequestTraversalSecretNameRefused(t *testing.T) {
	forbidden, hits := newHitCountingServer(t)
	v := newBrokerVault(t, []string{hostOf(t, forbidden.URL)})
	args := toJSON(t, brokerArgs{
		Method:  "GET",
		URL:     forbidden.URL,
		Headers: map[string]string{"Authorization": "Bearer {{secret:../../../etc/passwd}}"},
	})
	text, isError := callTool(t, v, "http_request", args)
	if !isError {
		t.Fatalf("want isError true for a path-traversal secret name, got text %q", text)
	}
	if !strings.Contains(text, "not a valid secret name") {
		t.Fatalf("bad refusal message: %q", text)
	}
	if strings.Contains(text, brokerDummyValue) {
		t.Fatalf("the refusal leaked the dummy value: %q", text)
	}
	if atomic.LoadInt32(hits) != 0 {
		t.Fatal("a traversal secret name must never reach the outbound server")
	}
	if strings.Contains(accessLogText(t, v), "broker") {
		t.Fatal("a refused-before-decrypt call must never write an access-log entry")
	}
}

// TestHTTPRequestUnknownOrEmptySecretNameRefused proves two distinct
// refusals: an empty "{{secret:}}" reference (a malformed name) and a
// syntactically valid but nonexistent secret name each refuse with
// their own distinct message, and neither ever reaches the outbound
// server.
func TestHTTPRequestUnknownOrEmptySecretNameRefused(t *testing.T) {
	cases := []struct {
		name      string
		reference string
		wantMsg   string
	}{
		{name: "empty name", reference: "{{secret:}}", wantMsg: "not a valid secret name"},
		{name: "unknown name", reference: "{{secret:does-not-exist}}", wantMsg: "no such secret"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			forbidden, hits := newHitCountingServer(t)
			v := newBrokerVault(t, []string{hostOf(t, forbidden.URL)})
			args := toJSON(t, brokerArgs{
				Method:  "GET",
				URL:     forbidden.URL,
				Headers: map[string]string{"Authorization": "Bearer " + c.reference},
			})
			text, isError := callTool(t, v, "http_request", args)
			if !isError {
				t.Fatalf("want isError true, got text %q", text)
			}
			if !strings.Contains(text, c.wantMsg) {
				t.Fatalf("want message containing %q, got %q", c.wantMsg, text)
			}
			if strings.Contains(text, brokerDummyValue) {
				t.Fatalf("the refusal leaked the dummy value: %q", text)
			}
			if atomic.LoadInt32(hits) != 0 {
				t.Fatal("must never reach the outbound server")
			}
			if strings.Contains(accessLogText(t, v), "broker") {
				t.Fatal("a refused-before-decrypt call must never write an access-log entry")
			}
		})
	}
}

// TestHTTPRequestScrubsSecretReflectedInResponse proves the second
// half of INVARIANT 10's extension: an allow-listed host is trusted
// with the secret, but if it reflects the value back — an echoed
// header, an error body quoting it — the tool result must never hand
// that value to the agent. Every exact occurrence is replaced with
// "[redacted-by-loadout]" instead. Moved here from broker_test.go: it
// is an adversarial vector (a hostile-or-buggy allowed host trying to
// leak the secret back through the response), not a functional one.
func TestHTTPRequestScrubsSecretReflectedInResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Echoed-Auth", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"error":"bad credential: ` + r.Header.Get("Authorization") + `"}`))
	}))
	defer server.Close()

	v := newBrokerVault(t, []string{hostOf(t, server.URL)})
	args := toJSON(t, brokerArgs{
		Method:  "GET",
		URL:     server.URL,
		Headers: map[string]string{"Authorization": "Bearer {{secret:api-key}}"},
	})
	text, isError := callTool(t, v, "http_request", args)
	if isError {
		t.Fatalf("http_request returned isError true: %s", text)
	}
	if strings.Contains(text, brokerDummyValue) {
		t.Fatalf("the tool result must never hand the reflected dummy value back to the agent: %s", text)
	}
	if !strings.Contains(text, "[redacted-by-loadout]") {
		t.Fatalf("the tool result must show the redaction placeholder in place of the reflected value, got: %s", text)
	}
	// Both the reflected header AND the reflected body occurrence must
	// be scrubbed — not just one of the two.
	if strings.Count(text, "[redacted-by-loadout]") < 2 {
		t.Fatalf("want the placeholder in both the echoed header and the echoed body, got: %s", text)
	}
}
