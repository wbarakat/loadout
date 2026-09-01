package cli_test

// This is Task 4's mandated SUBPROCESS security smoke: it never calls
// mcp.Serve or cli.Run in-process. It builds the real `loadout` binary
// to a temp path, spawns `loadout mcp` as a genuine child process
// (os/exec, its stdin and stdout as pipes), and drives a real MCP
// session over that pipe exactly the way an agent tool would — the
// same "read the vault, use a secret through the broker" invariant
// Task 3's own tests already cover in-process, proven once more at the
// process boundary a real agent tool actually sits behind.
//
// It runs entirely inside temp, per-test HOME and LOADOUT_HOME
// sandboxes (setupEnv), against local httptest servers only. It never
// touches the real user's home, and never a real credential.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// buildLoadoutBinary compiles the real cmd/loadout binary to a fresh
// path under t.TempDir() and returns that path. Building once, by
// import path rather than a relative "./..." path, works no matter
// which directory `go test` happens to run from, since Go resolves an
// import path through the module graph.
func buildLoadoutBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "loadout-mcp-smoke")
	cmd := exec.Command("go", "build", "-o", bin, "loadout.dev/loadout/cmd/loadout")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("building loadout: %v\n%s", err, stderr.String())
	}
	return bin
}

// runLoadoutSubprocess runs the built loadout binary once, to
// completion, with stdin carrying stdinText (piped, then closed — the
// same way "secret add" reads a value) and env as its whole
// environment. It is used for the setup steps (init, secret add) that
// do not need a live pipe: only "mcp" itself is driven as a long-lived
// subprocess, below.
func runLoadoutSubprocess(t *testing.T, bin string, env []string, stdinText string, args ...string) (stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	if stdinText != "" {
		cmd.Stdin = strings.NewReader(stdinText)
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("loadout %s failed: %v\nstdout: %s\nstderr: %s", strings.Join(args, " "), err, outBuf.String(), errBuf.String())
	}
	return outBuf.String(), errBuf.String()
}

// syncBuf is a mutex-guarded byte buffer: cmd.Stderr writes to it from
// the child's own goroutine (via the os/exec plumbing) while the test
// reads it, so plain bytes.Buffer would race.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// rpcLine is one JSON-RPC 2.0 response line, loosely typed, enough to
// dispatch on error vs result.
type rpcLine struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// toolCallResult is "tools/call"'s result shape, decoded from
// rpcLine.Result.
type toolCallResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// mcpProc drives one `loadout mcp` child process over its real stdin
// and stdout pipes: call sends one JSON-RPC request and blocks for its
// matching response. Serve answers one request at a time in the order
// it receives them, so reading responses off a channel fed by a single
// background scanner goroutine keeps them correctly paired with no
// extra bookkeeping.
type mcpProc struct {
	t      *testing.T
	cmd    *exec.Cmd
	stdin  *os.File
	lines  chan string
	stderr *syncBuf
	nextID int

	mu         sync.Mutex
	transcript []string // every raw line this process ever wrote to stdout

	stopOnce sync.Once
	waitErr  error
}

// startMCPSubprocess starts `bin mcp` as a real child process with env
// as its whole environment (the caller's temp HOME/LOADOUT_HOME
// overrides, captured via os.Environ() after t.Setenv — see
// TestMCPSubprocessSecuritySmoke). A background goroutine continuously
// scans the child's stdout, both feeding the response-matching channel
// and recording every line into the transcript the test inspects
// afterward.
func startMCPSubprocess(t *testing.T, bin string, env []string) *mcpProc {
	t.Helper()
	cmd := exec.Command(bin, "mcp")
	cmd.Env = env

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdin = stdinR
	cmd.Stdout = stdoutW
	stderrBuf := &syncBuf{}
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		t.Fatalf("starting loadout mcp: %v", err)
	}
	stdinR.Close()
	stdoutW.Close()

	p := &mcpProc{t: t, cmd: cmd, stdin: stdinW, lines: make(chan string, 64), stderr: stderrBuf}
	go func() {
		scanner := bufio.NewScanner(stdoutR)
		scanner.Buffer(make([]byte, 64*1024), 8<<20)
		for scanner.Scan() {
			line := scanner.Text()
			p.mu.Lock()
			p.transcript = append(p.transcript, line)
			p.mu.Unlock()
			p.lines <- line
		}
		close(p.lines)
	}()

	t.Cleanup(func() { p.stop() })
	return p
}

// stop closes the subprocess's stdin — the same clean shutdown signal
// a real MCP client gives (Serve reads EOF, returns nil, the process
// exits 0) — then waits for it to exit, killing it only if it takes
// longer than 5 seconds. Calling stop more than once is safe: the
// second call is a no-op, so the test body can call it explicitly and
// still let t.Cleanup call it again as a safety net.
func (p *mcpProc) stop() error {
	p.stopOnce.Do(func() {
		p.stdin.Close()
		done := make(chan error, 1)
		go func() { done <- p.cmd.Wait() }()
		select {
		case p.waitErr = <-done:
		case <-time.After(5 * time.Second):
			p.cmd.Process.Kill()
			p.waitErr = <-done
		}
	})
	return p.waitErr
}

// call sends one JSON-RPC request (method plus params, params may be
// nil) and returns its decoded response, failing the test if the
// subprocess answers with nothing within 10 seconds, or closes its
// stdout first (which its stderr, included in the failure, explains).
func (p *mcpProc) call(method string, params any) rpcLine {
	p.t.Helper()
	p.nextID++
	req := map[string]any{"jsonrpc": "2.0", "id": p.nextID, "method": method}
	if params != nil {
		req["params"] = params
	}
	data, err := json.Marshal(req)
	if err != nil {
		p.t.Fatal(err)
	}
	if _, err := p.stdin.Write(append(data, '\n')); err != nil {
		p.t.Fatalf("writing %s to the loadout mcp subprocess: %v", method, err)
	}
	select {
	case line, ok := <-p.lines:
		if !ok {
			p.t.Fatalf("the loadout mcp subprocess closed stdout before answering %s; stderr:\n%s", method, p.stderr.String())
		}
		var resp rpcLine
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			p.t.Fatalf("the response to %s did not parse as JSON-RPC: %v\nline: %s", method, err, line)
		}
		return resp
	case <-time.After(10 * time.Second):
		p.t.Fatalf("timed out waiting for a response to %s; stderr so far:\n%s", method, p.stderr.String())
	}
	return rpcLine{}
}

// callTool runs one "tools/call" and returns its decoded result,
// failing the test on a JSON-RPC-level error (an unknown tool or bad
// params) rather than a tool-level refusal — the caller checks IsError
// for that.
func (p *mcpProc) callTool(name string, args any) toolCallResult {
	p.t.Helper()
	resp := p.call("tools/call", map[string]any{"name": name, "arguments": args})
	if resp.Error != nil {
		p.t.Fatalf("tools/call %s returned a protocol error: %+v", name, resp.Error)
	}
	var result toolCallResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		p.t.Fatalf("tools/call %s result did not parse: %v\nraw: %s", name, err, resp.Result)
	}
	return result
}

// text returns a tool call's sole text content, failing the test if
// the result does not carry exactly one text entry — every loadout
// tool returns exactly one, so any other shape is itself a bug worth
// catching here rather than in a nil-slice panic downstream.
func (r toolCallResult) text(t *testing.T) string {
	t.Helper()
	if len(r.Content) != 1 || r.Content[0].Type != "text" {
		t.Fatalf("want exactly one text content entry, got %+v", r.Content)
	}
	return r.Content[0].Text
}

// smokeHostOf returns rawURL's host (with port), the shape an
// allowed_hosts entry and the broker's own host check both use.
func smokeHostOf(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

// TestMCPSubprocessSecuritySmoke is Task 4's mandated subprocess
// security smoke. Full narrative:
//
//  1. Build the real loadout binary.
//  2. Point HOME and LOADOUT_HOME at temp directories (setupEnv) —
//     the real user's home is never referenced by any step below.
//  3. "loadout init" (subprocess) creates the vault.
//  4. An httptest "API" (the allowed host) and a second httptest
//     server (forbidden — must get 0 hits) start locally; no other
//     network is ever touched.
//  5. "loadout secret add" (subprocess) adds a dummy secret, allowed
//     only to reach the API's host, value piped on stdin.
//  6. "loadout mcp" starts as a genuine child process, its stdin and
//     stdout wired to real OS pipes.
//  7. A real MCP session drives over those pipes: initialize,
//     tools/list (asserts the 6 tools), tools/call context,
//     tools/call list_secrets, tools/call http_request to the allowed
//     API (the API receives the dummy; the tool result does not), and
//     tools/call http_request to the forbidden host (refused, 0 hits).
//  8. The subprocess is stopped cleanly (stdin closed, EOF, clean
//     exit) and its whole stdout transcript and stderr are checked:
//     the dummy appears in NEITHER.
func TestMCPSubprocessSecuritySmoke(t *testing.T) {
	const dummy = "MCP-SMOKE-DUMMY-do-not-leak-9e21ac74"

	realHome := os.Getenv("HOME") // captured BEFORE setupEnv overrides it, purely to name it in the final log line below — never read or written again after this point.

	bin := buildLoadoutBinary(t)
	t.Logf("[0] built loadout at %s", bin)

	base := setupEnv(t) // t.Setenv-scoped HOME/LOADOUT_HOME under t.TempDir(); reverts automatically, real home never touched.
	env := os.Environ() // captures the now-overridden HOME/LOADOUT_HOME, handed to every subprocess below explicitly.
	homeDir := filepath.Join(base, "home")
	vaultDir := filepath.Join(base, "vault")
	t.Logf("[1] temp HOME=%s LOADOUT_HOME=%s (real home never referenced by this test)", homeDir, vaultDir)

	initOut, _ := runLoadoutSubprocess(t, bin, env, "", "init")
	t.Logf("[2] loadout init (subprocess): %s", strings.TrimSpace(initOut))

	var apiHits int32
	var apiGotAuth string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&apiHits, 1)
		apiGotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer api.Close()
	apiHost := smokeHostOf(t, api.URL)
	t.Logf("[3] started the local httptest API (the allowed host) at %s", api.URL)

	var forbiddenHits int32
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&forbiddenHits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer forbidden.Close()
	t.Logf("[3] started a second local httptest server (forbidden — must get 0 hits) at %s", forbidden.URL)

	runLoadoutSubprocess(t, bin, env, dummy,
		"secret", "add", "smoke-mcp-key", "--service", "smoke-svc", "--allowed-hosts", apiHost)
	t.Logf("[4] loadout secret add smoke-mcp-key --service smoke-svc --allowed-hosts %s (value piped on stdin) (subprocess): ok", apiHost)

	p := startMCPSubprocess(t, bin, env)
	t.Logf("[5] loadout mcp started as a real child process (pid %d), stdin/stdout wired to OS pipes", p.cmd.Process.Pid)

	// --- initialize ---------------------------------------------------
	initResp := p.call("initialize", map[string]any{})
	if initResp.Error != nil {
		t.Fatalf("initialize returned an error: %+v", initResp.Error)
	}
	var initResult struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(initResp.Result, &initResult); err != nil {
		t.Fatalf("initialize result did not parse: %v", err)
	}
	if initResult.ServerInfo.Name != "loadout" {
		t.Fatalf("want serverInfo.name loadout, got %q", initResult.ServerInfo.Name)
	}
	t.Logf("[6] initialize -> protocolVersion=%s serverInfo.name=%s", initResult.ProtocolVersion, initResult.ServerInfo.Name)

	// --- tools/list -----------------------------------------------------
	listResp := p.call("tools/list", nil)
	if listResp.Error != nil {
		t.Fatalf("tools/list returned an error: %+v", listResp.Error)
	}
	var toolsList struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(listResp.Result, &toolsList); err != nil {
		t.Fatalf("tools/list result did not parse: %v", err)
	}
	wantTools := []string{"context", "recall", "show", "list", "list_secrets", "http_request"}
	if len(toolsList.Tools) != len(wantTools) {
		t.Fatalf("want %d tools, got %d: %+v", len(wantTools), len(toolsList.Tools), toolsList.Tools)
	}
	gotTools := map[string]bool{}
	for _, tool := range toolsList.Tools {
		gotTools[tool.Name] = true
	}
	for _, name := range wantTools {
		if !gotTools[name] {
			t.Fatalf("tools/list missing %q, got %+v", name, toolsList.Tools)
		}
	}
	t.Logf("[7] tools/list -> all 6 tools advertised: %v", wantTools)

	// --- tools/call context ---------------------------------------------
	ctxResult := p.callTool("context", map[string]any{})
	ctxText := ctxResult.text(t)
	if ctxResult.IsError {
		t.Fatalf("tools/call context returned isError true: %s", ctxText)
	}
	if strings.Contains(ctxText, dummy) {
		t.Fatalf("tools/call context leaked the dummy: %s", ctxText)
	}
	t.Logf("[8] tools/call context -> ok, %d bytes, no dummy", len(ctxText))

	// --- tools/call list_secrets -----------------------------------------
	listSecretsResult := p.callTool("list_secrets", map[string]any{})
	lsText := listSecretsResult.text(t)
	if listSecretsResult.IsError {
		t.Fatalf("tools/call list_secrets returned isError true: %s", lsText)
	}
	if strings.Contains(lsText, dummy) {
		t.Fatalf("tools/call list_secrets leaked the dummy: %s", lsText)
	}
	if strings.Contains(lsText, `"value"`) {
		t.Fatalf("tools/call list_secrets carries a value field: %s", lsText)
	}
	var secrets []struct {
		Name         string   `json:"name"`
		AllowedHosts []string `json:"allowed_hosts"`
	}
	if err := json.Unmarshal([]byte(lsText), &secrets); err != nil {
		t.Fatalf("list_secrets text did not parse as JSON: %v\ntext: %s", err, lsText)
	}
	if len(secrets) != 1 || secrets[0].Name != "smoke-mcp-key" {
		t.Fatalf("want exactly the smoke-mcp-key secret's metadata, got %+v", secrets)
	}
	if len(secrets[0].AllowedHosts) != 1 || secrets[0].AllowedHosts[0] != apiHost {
		t.Fatalf("want allowed_hosts = [%q], got %v", apiHost, secrets[0].AllowedHosts)
	}
	t.Logf("[9] tools/call list_secrets -> metadata only, name=%s allowed_hosts=%v, no value field, no dummy", secrets[0].Name, secrets[0].AllowedHosts)

	// --- tools/call http_request: the allowed host ------------------------
	allowedArgs := map[string]any{
		"method":  "GET",
		"url":     api.URL + "/v1/thing",
		"headers": map[string]string{"Authorization": "Bearer {{secret:smoke-mcp-key}}"},
	}
	allowedResult := p.callTool("http_request", allowedArgs)
	allowedText := allowedResult.text(t)
	if allowedResult.IsError {
		t.Fatalf("tools/call http_request to the allowed host returned isError true: %s", allowedText)
	}
	if atomic.LoadInt32(&apiHits) != 1 {
		t.Fatalf("want the allowed API to receive exactly 1 request, got %d", atomic.LoadInt32(&apiHits))
	}
	if apiGotAuth != "Bearer "+dummy {
		t.Fatalf("the allowed API must receive the substituted dummy, got Authorization = %q", apiGotAuth)
	}
	if strings.Contains(allowedText, dummy) {
		t.Fatalf("tools/call http_request (allowed) leaked the dummy in its own result: %s", allowedText)
	}
	t.Logf("[10] tools/call http_request -> allowed host %s: the API received the real dummy in its Authorization header; the tool result carries no dummy", apiHost)

	// --- tools/call http_request: the forbidden host -----------------------
	forbiddenArgs := map[string]any{
		"method":  "GET",
		"url":     forbidden.URL + "/steal",
		"headers": map[string]string{"Authorization": "Bearer {{secret:smoke-mcp-key}}"},
	}
	forbiddenResult := p.callTool("http_request", forbiddenArgs)
	forbiddenText := forbiddenResult.text(t)
	if !forbiddenResult.IsError {
		t.Fatalf("tools/call http_request to the forbidden host must refuse, got isError false: %s", forbiddenText)
	}
	if atomic.LoadInt32(&forbiddenHits) != 0 {
		t.Fatalf("the forbidden host must get 0 hits, got %d", atomic.LoadInt32(&forbiddenHits))
	}
	if strings.Contains(forbiddenText, dummy) {
		t.Fatalf("tools/call http_request (forbidden) leaked the dummy in its refusal: %s", forbiddenText)
	}
	t.Logf("[11] tools/call http_request -> forbidden host %s: refused (%q), 0 hits, no dummy in the refusal", smokeHostOf(t, forbidden.URL), forbiddenText)

	// --- clean shutdown ------------------------------------------------
	if err := p.stop(); err != nil {
		t.Fatalf("loadout mcp did not exit cleanly on stdin close: %v; stderr:\n%s", err, p.stderr.String())
	}
	t.Logf("[12] closed stdin; loadout mcp exited cleanly on EOF")

	// --- the whole-transcript and stderr leak check -----------------------
	p.mu.Lock()
	fullTranscript := strings.Join(p.transcript, "\n")
	p.mu.Unlock()
	stderrText := p.stderr.String()
	if strings.Contains(fullTranscript, dummy) {
		t.Fatalf("the dummy leaked into the raw JSON-RPC stdout stream:\n%s", fullTranscript)
	}
	if strings.Contains(stderrText, dummy) {
		t.Fatalf("the dummy leaked into loadout mcp's stderr:\n%s", stderrText)
	}
	t.Logf("[13] the dummy value appears in NEITHER the JSON-RPC stream (%d bytes, %d lines) NOR stderr (%d bytes) — only inside the outbound request the httptest API captured",
		len(fullTranscript), len(p.transcript), len(stderrText))
	t.Logf("[done] the real HOME (%s) was never referenced by any loadout command above: HOME and LOADOUT_HOME were overridden to temp paths (%s, %s) before the first loadout subprocess ever ran, and every subprocess (init, secret add, mcp) inherited that same override explicitly via its own env.",
		realHome, homeDir, vaultDir)
}
