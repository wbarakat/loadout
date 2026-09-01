package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"loadout.dev/loadout/internal/vault"
)

// brokerTimeout caps how long the whole brokered request (connect,
// send, and read the response) may take. A hung server on the other
// end must not hang the MCP session forever.
const brokerTimeout = 30 * time.Second

// maxBrokerResponseBody caps how many bytes of the outbound server's
// response body the broker reads back. A response is never buffered
// past this: the rest is left unread and the connection dropped when
// the tool result is built, bounding memory the same way Serve's own
// maxMessageBytes bounds one JSON-RPC line.
const maxBrokerResponseBody = 4 << 20 // 4 MiB

// secretPlaceholderOpen is the literal prefix of a "{{secret:name}}"
// placeholder. httpRequestTool refuses this substring anywhere in a
// request's URL outright (see httpRequestTool), before it even
// parses the URL: a placeholder is only ever allowed inside a header
// VALUE or the body, never in the URL, so a secret's value can never
// be used to choose or rewrite the host a request is sent to.
const secretPlaceholderOpen = "{{secret:"

// secretRefPattern finds every "{{secret:<name>}}" placeholder in a
// header value or the body. The captured name is validated with
// vault.ValidateSecretName before it is ever used to look up a
// secret, so a malformed reference is refused rather than silently
// ignored or misread.
var secretRefPattern = regexp.MustCompile(`\{\{secret:([^{}]*)\}\}`)

// httpRequestArgs is the "http_request" tool's arguments: a plain
// HTTP request description, where a header value or the body may
// carry one or more "{{secret:<name>}}" placeholders that the broker
// substitutes AFTER checking every referenced secret's allowed_hosts
// against the request's host. The URL itself may never carry a
// placeholder (see httpRequestTool).
type httpRequestArgs struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// brokerResponse is the "http_request" tool's successful result: the
// outbound SERVER's own response, which never carries a secret's
// value — only the agent's own request could have done that, and the
// broker only ever substitutes a secret into the OUTBOUND request,
// never into anything it reports back.
type brokerResponse struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
}

// httpRequestTool is the secret broker: the ONLY place a secret's
// decrypted value is allowed to go (INVARIANT 10, extended). An agent
// never sees a secret's value directly; it references one by name
// with a "{{secret:<name>}}" placeholder in a header value or the
// body, and this tool decrypts and substitutes it into the OUTBOUND
// request only, and only once every referenced secret's allowed_hosts
// permits the request's exact host.
//
// See findSecretRefs, hostAllowed, and the Handler below for the
// refuse-before-decrypt steps this enforces, in order:
//
//  1. The URL must parse, and its scheme must be http or https; the
//     URL text itself must never contain a "{{secret:...}}"
//     placeholder — that is refused outright, before the URL is even
//     parsed, so a secret can never be used to choose or rewrite the
//     request's host.
//  2. Every "{{secret:<name>}}" reference across the header values
//     and the body is found and its name validated. For each
//     referenced secret, its metadata is loaded: an empty
//     allowed_hosts refuses the whole request (fail closed, nothing
//     decrypted); a request host that is not an EXACT, case-
//     insensitive match of one of its allowed_hosts entries also
//     refuses the whole request. This check runs for EVERY referenced
//     secret before ANY of them is decrypted, so one permitted secret
//     can never smuggle another, unpermitted one along for the ride.
//  3. Only once every referenced secret permits this exact host is
//     each one decrypted and substituted into the header values and
//     the body; the plaintext is zeroed immediately after, the same
//     way AddSecret and DecryptSecret's own callers zero theirs.
//  4. The request is sent with a client that refuses to follow a
//     redirect to a different host (see refuseCrossHostRedirect), so
//     a 3xx response can never cause the secret to be re-sent
//     elsewhere.
//  5. One access-log entry per secret used is appended — verb
//     "broker", the secret's name, and the request's HOST (never the
//     full URL, never the value) — before the request is sent, the
//     same "log once the secret has really been used" rule cmdRun
//     follows for "loadout run".
func httpRequestTool(v *vault.Vault) Tool {
	return Tool{
		Name: "http_request",
		Description: "Send an HTTP request, substituting {{secret:<name>}} placeholders in header values or the body with a secret's " +
			"decrypted value. Refuses unless the secret's allowed_hosts permits the request's exact host. Never a placeholder in the url. " +
			"Returns the outbound server's response; never the secret's value.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"method":{"type":"string","description":"HTTP method, for example GET or POST"},` +
			`"url":{"type":"string","description":"the request url; must not contain a {{secret:...}} placeholder"},` +
			`"headers":{"type":"object","additionalProperties":{"type":"string"},"description":"header name to value; a value may contain {{secret:<name>}}"},` +
			`"body":{"type":"string","description":"the request body; may contain {{secret:<name>}}"}` +
			`},"required":["method","url"]}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			return runHTTPRequest(v, args)
		},
	}
}

// runHTTPRequest implements httpRequestTool's Handler. It is a plain
// function, not a closure body, so the refuse-before-decrypt sequence
// documented on httpRequestTool reads top to bottom without the extra
// indentation a Handler literal would add.
func runHTTPRequest(v *vault.Vault, args json.RawMessage) (ToolResult, error) {
	var a httpRequestArgs
	if err := decodeArgs(args, &a); err != nil {
		return ToolResult{Text: "invalid arguments: " + err.Error(), IsError: true}, nil
	}

	method := strings.ToUpper(strings.TrimSpace(a.Method))
	if method == "" {
		return ToolResult{Text: "method: must not be empty.", IsError: true}, nil
	}

	// A placeholder is refused in the URL outright, before the URL is
	// even parsed: allowing one here would let a secret's own value
	// choose or rewrite the host the request goes to.
	if strings.Contains(a.URL, secretPlaceholderOpen) {
		return ToolResult{Text: "url: must not contain a {{secret:...}} placeholder. Fix: put the secret in a header value or the body instead.", IsError: true}, nil
	}
	u, err := url.Parse(a.URL)
	if err != nil || u.Host == "" {
		return ToolResult{Text: "url: not a valid url. Fix: use a url like https://host/path.", IsError: true}, nil
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ToolResult{Text: "url: scheme must be http or https.", IsError: true}, nil
	}
	host := u.Host

	texts := make([]string, 0, len(a.Headers)+1)
	for _, hv := range a.Headers {
		texts = append(texts, hv)
	}
	texts = append(texts, a.Body)
	names, err := findSecretRefs(texts)
	if err != nil {
		return ToolResult{Text: err.Error(), IsError: true}, nil
	}
	if len(names) == 0 {
		return sendBrokeredRequest(method, a.URL, a.Headers, a.Body)
	}

	// Fail closed: every referenced secret must permit this EXACT
	// host before anything at all is decrypted.
	for _, name := range names {
		meta, err := vault.SecretMeta(v, name)
		if err != nil {
			return ToolResult{Text: fmt.Sprintf("secret/%s: %v", name, err), IsError: true}, nil
		}
		if len(meta.AllowedHosts) == 0 {
			return ToolResult{Text: fmt.Sprintf("secret/%s: no allowed_hosts configured. Fix: run loadout secret rotate %s --allowed-hosts <host>.", name, name), IsError: true}, nil
		}
		if !hostAllowed(host, meta.AllowedHosts) {
			return ToolResult{Text: fmt.Sprintf("secret/%s: not permitted to reach host %s.", name, host), IsError: true}, nil
		}
	}

	// Every referenced secret permits this host: decrypt each,
	// substitute into the header values and body, and zero every
	// plaintext right away — the request from here on carries only
	// the substituted strings, the same accepted, documented exposure
	// AddSecret and cmdRun already carry for a decrypted value folded
	// into an immutable Go string.
	plaintexts := make(map[string][]byte, len(names))
	defer zeroPlaintexts(plaintexts)
	for _, name := range names {
		val, err := vault.DecryptSecret(v, name)
		if err != nil {
			return ToolResult{Text: fmt.Sprintf("secret/%s: cannot be used.", name), IsError: true}, nil
		}
		plaintexts[name] = val
	}

	headers := make(map[string]string, len(a.Headers))
	for k, hv := range a.Headers {
		headers[k] = substitutePlaceholders(hv, plaintexts)
	}
	body := substitutePlaceholders(a.Body, plaintexts)
	zeroPlaintexts(plaintexts)

	// One access-log entry per secret used, naming only the HOST —
	// never the full url, never the value — written before the
	// request is sent, mirroring cmdRun's "log once the secret has
	// really been used" rule for "loadout run".
	at := time.Now().UTC().Format(time.RFC3339)
	for _, name := range names {
		if err := vault.AppendAccessLog(v, vault.AccessEntry{At: at, Verb: "broker", Secret: name, Tool: host}); err != nil {
			return ToolResult{Text: "the access log could not be written; the request was not sent.", IsError: true}, nil
		}
	}

	return sendBrokeredRequest(method, a.URL, headers, body)
}

// zeroPlaintexts zeroes every plaintext byte slice in plaintexts.
// Calling it twice (once right after substitution, once again via
// defer on every return path including an error before substitution
// ever runs) is safe: zeroing an already-zeroed or partially filled
// slice is a no-op or harmless.
func zeroPlaintexts(plaintexts map[string][]byte) {
	for _, p := range plaintexts {
		for i := range p {
			p[i] = 0
		}
	}
}

// findSecretRefs scans every text for "{{secret:<name>}}" placeholders
// and returns the unique secret names referenced, in first-appearance
// order. A name that fails vault.ValidateSecretName refuses the whole
// call: err names the malformed reference, never a secret value (the
// reference is text the agent itself wrote).
func findSecretRefs(texts []string) ([]string, error) {
	seen := map[string]bool{}
	var names []string
	for _, text := range texts {
		for _, m := range secretRefPattern.FindAllStringSubmatch(text, -1) {
			name := m[1]
			if err := vault.ValidateSecretName(name); err != nil {
				return nil, fmt.Errorf("the reference {{secret:%s}} is not a valid secret name.", name)
			}
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	return names, nil
}

// hostAllowed reports whether host exactly matches one entry in
// allowed, case-insensitively (DNS host names are case-insensitive)
// but never as a substring or prefix — "api.example.com" must never
// match "api.example.com.evil.com".
func hostAllowed(host string, allowed []string) bool {
	for _, a := range allowed {
		if strings.EqualFold(host, a) {
			return true
		}
	}
	return false
}

// substitutePlaceholders replaces every "{{secret:<name>}}" in text
// whose name is a key of plaintexts with that secret's decrypted
// bytes. A placeholder whose name is not in plaintexts (should not
// happen: findSecretRefs already collected every reference) is left
// untouched rather than silently dropped.
func substitutePlaceholders(text string, plaintexts map[string][]byte) string {
	return secretRefPattern.ReplaceAllStringFunc(text, func(match string) string {
		sub := secretRefPattern.FindStringSubmatch(match)
		if val, ok := plaintexts[sub[1]]; ok {
			return string(val)
		}
		return match
	})
}

// refuseCrossHostRedirect is an http.Client CheckRedirect policy that
// refuses to follow a redirect to a different host than the request
// STARTED at (via[0], the original request) — so a 3xx response
// pointing at another host never causes a secret already folded into
// this request's headers or body to be resent there. Returning
// http.ErrUseLastResponse tells the Client to stop and hand back the
// 3xx response itself, with no error and no further request ever
// built or sent.
func refuseCrossHostRedirect(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	if !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
		return http.ErrUseLastResponse
	}
	if len(via) >= 10 {
		return http.ErrUseLastResponse
	}
	return nil
}

// sendBrokeredRequest builds and sends the OUTBOUND request — with
// every "{{secret:<name>}}" placeholder already substituted by the
// caller — and returns the outbound server's response as the tool
// result. It never sees a secret name or plaintext itself, only the
// final header/body strings, so it cannot leak either even by
// accident.
func sendBrokeredRequest(method, rawURL string, headers map[string]string, body string) (ToolResult, error) {
	req, err := http.NewRequest(method, rawURL, strings.NewReader(body))
	if err != nil {
		return ToolResult{Text: "the request could not be built: " + err.Error(), IsError: true}, nil
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: brokerTimeout, CheckRedirect: refuseCrossHostRedirect}
	resp, err := client.Do(req)
	if err != nil {
		return ToolResult{Text: "the request failed: " + err.Error(), IsError: true}, nil
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxBrokerResponseBody))

	data, err := json.Marshal(brokerResponse{Status: resp.StatusCode, Headers: resp.Header, Body: string(bodyBytes)})
	if err != nil {
		return ToolResult{}, err
	}
	return ToolResult{Text: string(data)}, nil
}
