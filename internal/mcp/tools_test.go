package mcp_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/mcp"
	"loadout.dev/loadout/internal/vault"
)

// dummySecretValue is the only secret value any test in this file
// ever uses. It is not a real credential.
const dummySecretValue = "test-secret-value-xyz789"

// fixtureVault returns a freshly initialized vault, in a temp
// directory, holding one skill, one fact, and one dummy secret — the
// fixture every read-tool test in this file drives against.
func fixtureVault(t *testing.T) *vault.Vault {
	t.Helper()
	v, err := vault.Init(filepath.Join(t.TempDir(), "vault"))
	if err != nil {
		t.Fatalf("vault.Init: %v", err)
	}

	skillDir := filepath.Join(v.SkillsDir(), "deploy-checks")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillContent := "---\nname: deploy-checks\ndescription: run checks before a deploy\n---\n\nDo the thing.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0o644); err != nil {
		t.Fatal(err)
	}

	factContent := "---\nname: my-stack\ndescription: the stack I use\n---\n\nI use Go and Postgres.\n"
	if err := os.WriteFile(filepath.Join(v.MemoryDir(), "my-stack.md"), []byte(factContent), 0o644); err != nil {
		t.Fatal(err)
	}

	value := []byte(dummySecretValue)
	if err := vault.AddSecret(v, "openai-key", "openai", "deploy hook", "720h", "human", nil, value); err != nil {
		t.Fatalf("vault.AddSecret: %v", err)
	}
	return v
}

// callTool runs one "tools/call" for name/argsJSON against v through
// Serve, and returns the decoded tool result content.
func callTool(t *testing.T, v *vault.Vault, name, argsJSON string) (text string, isError bool) {
	t.Helper()
	line := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + argsJSON + `}}`
	msgs, err := serve(t, v, line)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 response, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Error != nil {
		t.Fatalf("%s: got a protocol error, want a tool result: %+v", name, msgs[0].Error)
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(msgs[0].Result, &result); err != nil {
		t.Fatalf("%s: result did not parse: %v\nraw: %s", name, err, msgs[0].Result)
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("%s: want exactly one text content entry, got %+v", name, result.Content)
	}
	return result.Content[0].Text, result.IsError
}

func TestContextToolReturnsSituationalPicture(t *testing.T) {
	v := fixtureVault(t)
	text, isError := callTool(t, v, "context", "{}")
	if isError {
		t.Fatalf("context returned isError true: %s", text)
	}
	for _, want := range []string{"1 skills, 1 facts", "deploy-checks", "run checks before a deploy", "my-stack", "the stack I use"} {
		if !strings.Contains(text, want) {
			t.Fatalf("context result missing %q, got:\n%s", want, text)
		}
	}
	if strings.Contains(text, dummySecretValue) {
		t.Fatalf("context result leaked the secret value:\n%s", text)
	}
}

func TestRecallToolFindsByTerm(t *testing.T) {
	v := fixtureVault(t)
	text, isError := callTool(t, v, "recall", `{"terms":"postgres"}`)
	if isError {
		t.Fatalf("recall returned isError true: %s", text)
	}
	if strings.TrimRight(text, "\n") != "memory/my-stack — the stack I use" {
		t.Fatalf("bad recall result: %q", text)
	}
}

func TestRecallToolNoMatch(t *testing.T) {
	v := fixtureVault(t)
	text, isError := callTool(t, v, "recall", `{"terms":"bogusterm"}`)
	if isError {
		t.Fatalf("recall returned isError true for a clean no-match: %s", text)
	}
	if !strings.Contains(text, "no items match") {
		t.Fatalf("want a no-match message, got %q", text)
	}
}

func TestRecallToolEmptyTermsIsError(t *testing.T) {
	v := fixtureVault(t)
	text, isError := callTool(t, v, "recall", `{"terms":""}`)
	if !isError {
		t.Fatalf("want isError true for empty terms, got text %q", text)
	}
}

func TestListToolListsSkillAndMemoryNotSecrets(t *testing.T) {
	v := fixtureVault(t)
	text, isError := callTool(t, v, "list", "{}")
	if isError {
		t.Fatalf("list returned isError true: %s", text)
	}
	if !strings.Contains(text, "skill/deploy-checks") || !strings.Contains(text, "memory/my-stack") {
		t.Fatalf("list result missing an expected address, got:\n%s", text)
	}
	if strings.Contains(text, "secret/") {
		t.Fatalf("list must exclude secrets, got:\n%s", text)
	}
	if strings.Contains(text, dummySecretValue) {
		t.Fatalf("list result leaked the secret value:\n%s", text)
	}
}

func TestShowToolReadsItemBody(t *testing.T) {
	v := fixtureVault(t)
	text, isError := callTool(t, v, "show", `{"address":"memory/my-stack"}`)
	if isError {
		t.Fatalf("show returned isError true: %s", text)
	}
	if !strings.Contains(text, "I use Go and Postgres.") {
		t.Fatalf("show result missing the fact body, got:\n%s", text)
	}
}

func TestShowToolRefusesSecretAddress(t *testing.T) {
	v := fixtureVault(t)
	text, isError := callTool(t, v, "show", `{"address":"secret/openai-key"}`)
	if !isError {
		t.Fatalf("show of a secret address must return isError true, got text %q", text)
	}
	if strings.Contains(text, dummySecretValue) {
		t.Fatalf("show's refusal leaked the secret value: %q", text)
	}
	if !strings.Contains(text, "not shown over MCP") {
		t.Fatalf("want a refusal message naming MCP, got %q", text)
	}
}

func TestShowToolMissingItemIsError(t *testing.T) {
	v := fixtureVault(t)
	text, isError := callTool(t, v, "show", `{"address":"memory/does-not-exist"}`)
	if !isError {
		t.Fatalf("show of a missing item must return isError true, got text %q", text)
	}
}

func TestShowToolBadAddressIsError(t *testing.T) {
	v := fixtureVault(t)
	text, isError := callTool(t, v, "show", `{"address":"not-an-address"}`)
	if !isError {
		t.Fatalf("show of a malformed address must return isError true, got text %q", text)
	}
}

func TestListSecretsToolReturnsMetadataNoValue(t *testing.T) {
	v := fixtureVault(t)
	text, isError := callTool(t, v, "list_secrets", "{}")
	if isError {
		t.Fatalf("list_secrets returned isError true: %s", text)
	}
	if strings.Contains(text, dummySecretValue) {
		t.Fatalf("list_secrets result leaked the secret value: %s", text)
	}
	if strings.Contains(text, `"value"`) {
		t.Fatalf("list_secrets result carries a value field: %s", text)
	}

	var secrets []struct {
		Name        string `json:"name"`
		Service     string `json:"service"`
		Hook        string `json:"hook"`
		RotateAfter string `json:"rotate_after"`
	}
	if err := json.Unmarshal([]byte(text), &secrets); err != nil {
		t.Fatalf("list_secrets text did not parse as JSON: %v\ntext: %s", err, text)
	}
	if len(secrets) != 1 {
		t.Fatalf("want 1 secret, got %d: %+v", len(secrets), secrets)
	}
	got := secrets[0]
	if got.Name != "openai-key" || got.Service != "openai" || got.Hook != "deploy hook" || got.RotateAfter != "720h" {
		t.Fatalf("bad secret metadata: %+v", got)
	}
}

// TestDummySecretValueNeverAppearsInAnyToolResult drives all five
// read tools in one Serve session and proves the dummy secret's
// VALUE appears in none of the raw JSON-RPC output — not folded into
// content text, not anywhere else on the wire.
func TestDummySecretValueNeverAppearsInAnyToolResult(t *testing.T) {
	v := fixtureVault(t)
	lines := []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"context","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recall","arguments":{"terms":"postgres deploy openai"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"show","arguments":{"address":"memory/my-stack"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"show","arguments":{"address":"secret/openai-key"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"list","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"list_secrets","arguments":{}}}`,
	}
	var out bytes.Buffer
	in := strings.NewReader(strings.Join(lines, "\n") + "\n")
	if err := mcp.Serve(v, in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if bytes.Contains(out.Bytes(), []byte(dummySecretValue)) {
		t.Fatalf("the dummy secret value leaked into a tool result:\n%s", out.String())
	}
}
