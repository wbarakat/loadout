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

// The response-reflection scrub, the fail-closed empty-allowed-hosts
// refusal, the wrong-host refusal, the exact-match (suffix) refusal,
// the secret-in-url refusal, and the cross-host redirect refusal all
// moved to broker_adversarial_test.go: they are adversarial vectors,
// consolidated there as one named, table-driven regression suite. See
// that file's own doc comment for the full coverage map.

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
