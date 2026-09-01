package mcp

import (
	"net/http"
	"net/url"
	"testing"
)

// mustParseURL parses raw as a URL, failing the test on error.
func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// redirectRequest builds the minimal *http.Request refuseUnsafeRedirect
// reads: only req.URL matters for this policy.
func redirectRequest(t *testing.T, rawURL string) *http.Request {
	t.Helper()
	return &http.Request{URL: mustParseURL(t, rawURL)}
}

// TestRefuseUnsafeRedirectRefusesSchemeDowngradeSameHost proves the
// gap a same-host CheckRedirect alone would miss: an https origin
// redirecting to http on the EXACT SAME host must be refused, so a
// secret already folded into the original request's headers is never
// carried onto a next request Go would otherwise send in cleartext
// (Go's own default cross-host header stripping does not trigger
// here, since the host is unchanged — only the scheme drops).
func TestRefuseUnsafeRedirectRefusesSchemeDowngradeSameHost(t *testing.T) {
	orig := redirectRequest(t, "https://api.example.com/start")
	next := redirectRequest(t, "http://api.example.com/downgraded")
	if err := refuseUnsafeRedirect(next, []*http.Request{orig}); err != http.ErrUseLastResponse {
		t.Fatalf("refuseUnsafeRedirect = %v, want http.ErrUseLastResponse", err)
	}
}

// TestRefuseUnsafeRedirectAllowsSameHostSameScheme proves the policy
// is not overbroad: a same-host, same-scheme redirect (the ordinary,
// safe case) is still allowed.
func TestRefuseUnsafeRedirectAllowsSameHostSameScheme(t *testing.T) {
	orig := redirectRequest(t, "https://api.example.com/start")
	next := redirectRequest(t, "https://api.example.com/next")
	if err := refuseUnsafeRedirect(next, []*http.Request{orig}); err != nil {
		t.Fatalf("refuseUnsafeRedirect = %v, want nil", err)
	}
}

// TestRefuseUnsafeRedirectAllowsSchemeUpgradeSameHost proves an
// upgrade (http origin redirecting to https on the same host) is not
// mistaken for a downgrade: it is strictly safer, not less secure.
func TestRefuseUnsafeRedirectAllowsSchemeUpgradeSameHost(t *testing.T) {
	orig := redirectRequest(t, "http://api.example.com/start")
	next := redirectRequest(t, "https://api.example.com/next")
	if err := refuseUnsafeRedirect(next, []*http.Request{orig}); err != nil {
		t.Fatalf("refuseUnsafeRedirect = %v, want nil (an upgrade is not a downgrade)", err)
	}
}

// TestRefuseUnsafeRedirectStillRefusesDifferentHost is a regression
// check: the pre-existing cross-host refusal must survive alongside
// the new scheme check.
func TestRefuseUnsafeRedirectStillRefusesDifferentHost(t *testing.T) {
	orig := redirectRequest(t, "https://api.example.com/start")
	next := redirectRequest(t, "https://attacker.example.com/steal")
	if err := refuseUnsafeRedirect(next, []*http.Request{orig}); err != http.ErrUseLastResponse {
		t.Fatalf("refuseUnsafeRedirect = %v, want http.ErrUseLastResponse", err)
	}
}

// TestRefuseUnsafeRedirectAllowsFirstRequest proves via being empty
// (no redirect has happened yet — this is the original request) is
// always allowed.
func TestRefuseUnsafeRedirectAllowsFirstRequest(t *testing.T) {
	req := redirectRequest(t, "https://api.example.com/start")
	if err := refuseUnsafeRedirect(req, nil); err != nil {
		t.Fatalf("refuseUnsafeRedirect = %v, want nil for the original request", err)
	}
}
