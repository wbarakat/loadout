package mcp_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/mcp"
	"loadout.dev/loadout/internal/vault"
)

// testVault returns a freshly initialized vault in a temp directory,
// for tests that only need a *vault.Vault to hand to Serve.
func testVault(t *testing.T) *vault.Vault {
	t.Helper()
	v, err := vault.Init(filepath.Join(t.TempDir(), "vault"))
	if err != nil {
		t.Fatalf("vault.Init: %v", err)
	}
	return v
}

// rpcMessage is a loosely typed JSON-RPC 2.0 message, enough to
// assert on any response Serve writes.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// serve runs mcp.Serve on the given input lines (joined with "\n")
// and returns every decoded response line, and Serve's own error.
func serve(t *testing.T, v *vault.Vault, lines ...string) ([]rpcMessage, error) {
	t.Helper()
	in := strings.NewReader(strings.Join(lines, "\n") + "\n")
	var out bytes.Buffer
	err := mcp.Serve(v, in, &out)

	var msgs []rpcMessage
	scanner := bufio.NewScanner(&out)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var m rpcMessage
		if uerr := json.Unmarshal(line, &m); uerr != nil {
			t.Fatalf("output line is not well-formed JSON: %v\nline: %s", uerr, line)
		}
		if m.JSONRPC != "2.0" {
			t.Fatalf("output line missing jsonrpc:2.0: %s", line)
		}
		msgs = append(msgs, m)
	}
	if serr := scanner.Err(); serr != nil {
		t.Fatalf("scanning Serve's output: %v", serr)
	}
	return msgs, err
}

func TestServeEOFReturnsCleanly(t *testing.T) {
	v := testVault(t)
	var out bytes.Buffer
	if err := mcp.Serve(v, strings.NewReader(""), &out); err != nil {
		t.Fatalf("Serve on empty input: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("Serve wrote output for empty input: %q", out.String())
	}
}

func TestServeInitialize(t *testing.T) {
	v := testVault(t)
	msgs, err := serve(t, v, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 response, got %d: %+v", len(msgs), msgs)
	}
	m := msgs[0]
	if m.Error != nil {
		t.Fatalf("initialize returned an error: %+v", m.Error)
	}
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Capabilities struct {
			Tools json.RawMessage `json:"tools"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(m.Result, &result); err != nil {
		t.Fatalf("initialize result did not parse: %v\nresult: %s", err, m.Result)
	}
	if result.ProtocolVersion != "2024-11-05" {
		t.Fatalf("want protocolVersion 2024-11-05, got %q", result.ProtocolVersion)
	}
	if result.ServerInfo.Name != "loadout" {
		t.Fatalf("want serverInfo.name loadout, got %q", result.ServerInfo.Name)
	}
	if result.ServerInfo.Version != "0.6" {
		t.Fatalf("want serverInfo.version 0.6, got %q", result.ServerInfo.Version)
	}
	if result.Capabilities.Tools == nil {
		t.Fatalf("want capabilities.tools present, got none")
	}
	if string(m.ID) != "1" {
		t.Fatalf("want id echoed back as 1, got %s", m.ID)
	}
}

func TestServeToolsListIsEmpty(t *testing.T) {
	v := testVault(t)
	msgs, err := serve(t, v, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 response, got %d", len(msgs))
	}
	var result struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(msgs[0].Result, &result); err != nil {
		t.Fatalf("tools/list result did not parse: %v", err)
	}
	if result.Tools == nil {
		t.Fatalf("want tools:[], got tools:null")
	}
	if len(result.Tools) != 0 {
		t.Fatalf("want an empty tool list, got %d tools", len(result.Tools))
	}
}

func TestServeUnknownMethod(t *testing.T) {
	v := testVault(t)
	msgs, err := serve(t, v, `{"jsonrpc":"2.0","id":3,"method":"bogus/thing"}`)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 response, got %d", len(msgs))
	}
	if msgs[0].Error == nil || msgs[0].Error.Code != -32601 {
		t.Fatalf("want error -32601, got %+v", msgs[0].Error)
	}
}

func TestServeToolsCallUnknownTool(t *testing.T) {
	v := testVault(t)
	msgs, err := serve(t, v, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"no-such-tool","arguments":{}}}`)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 response, got %d", len(msgs))
	}
	if msgs[0].Error == nil || msgs[0].Error.Code != -32601 {
		t.Fatalf("want error -32601 for an unknown tool, got %+v", msgs[0].Error)
	}
}

func TestServeMalformedJSONSurvivesAndAnswersNextRequest(t *testing.T) {
	v := testVault(t)
	msgs, err := serve(t, v,
		`{not valid json`,
		`{"jsonrpc":"2.0","id":5,"method":"initialize"}`,
	)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("want 2 responses (one parse error, one initialize result), got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Error == nil || msgs[0].Error.Code != -32700 {
		t.Fatalf("want the first response to be a -32700 parse error, got %+v", msgs[0])
	}
	if string(msgs[0].ID) != "null" {
		t.Fatalf("want the parse error's id to be null, got %s", msgs[0].ID)
	}
	if msgs[1].Error != nil {
		t.Fatalf("want the second response (initialize) to succeed, got error %+v", msgs[1].Error)
	}
	if string(msgs[1].ID) != "5" {
		t.Fatalf("want the second response's id to be 5, got %s", msgs[1].ID)
	}
}

func TestServeNotificationGetsNoResponse(t *testing.T) {
	v := testVault(t)
	msgs, err := serve(t, v,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":6,"method":"initialize"}`,
	)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 response (the notification gets none), got %d: %+v", len(msgs), msgs)
	}
	if string(msgs[0].ID) != "6" {
		t.Fatalf("want the sole response's id to be 6, got %s", msgs[0].ID)
	}
}

// TestServeOversizedLineSurvives proves a line over Serve's message
// size cap does not end the session: it gets a "message too large"
// error (id null, since a line that big is never parsed for an id),
// and the loop still answers the request on the next line.
func TestServeOversizedLineSurvives(t *testing.T) {
	v := testVault(t)
	oversized := strings.Repeat("a", 8<<20+1) // one byte over the 8 MiB cap
	msgs, err := serve(t, v,
		oversized,
		`{"jsonrpc":"2.0","id":7,"method":"initialize"}`,
	)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("want 2 responses (one too-large error, one initialize result), got %d", len(msgs))
	}
	if msgs[0].Error == nil || msgs[0].Error.Code != -32600 {
		t.Fatalf("want the first response to be a -32600 message-too-large error, got %+v", msgs[0])
	}
	if string(msgs[0].ID) != "null" {
		t.Fatalf("want the too-large error's id to be null, got %s", msgs[0].ID)
	}
	if msgs[1].Error != nil {
		t.Fatalf("want the second response (initialize) to succeed, got error %+v", msgs[1].Error)
	}
	if string(msgs[1].ID) != "7" {
		t.Fatalf("want the second response's id to be 7, got %s", msgs[1].ID)
	}
}

// errWriter is an io.Writer whose every Write fails with err, for
// proving Serve propagates a broken output channel instead of
// swallowing it.
type errWriter struct{ err error }

func (w *errWriter) Write(p []byte) (int, error) { return 0, w.err }

func TestServePropagatesAWriteError(t *testing.T) {
	v := testVault(t)
	wantErr := errors.New("boom: the pipe is closed")
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n")

	err := mcp.Serve(v, in, &errWriter{err: wantErr})
	if !errors.Is(err, wantErr) {
		t.Fatalf("want Serve to return the write error %v, got %v", wantErr, err)
	}
}
