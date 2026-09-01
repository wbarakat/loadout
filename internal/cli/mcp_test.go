package cli_test

import (
	"io"
	"os"
	"strings"
	"testing"
)

// runMCP runs "loadout mcp" (plus any extra args) with os.Stdin and
// os.Stdout replaced by pipes: requestLines is written to stdin and
// closed, and the captured stdout, the CLI's own stderr, and the exit
// code are returned. cmdMCP writes its JSON-RPC replies straight to
// os.Stdout, not to the buffer run() passes as "out", so a test that
// wants to see them must swap os.Stdout itself.
func runMCP(t *testing.T, requestLines string, extraArgs ...string) (stdout, stderr string, code int) {
	t.Helper()

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdinR.Close()
	origStdin := os.Stdin
	os.Stdin = stdinR
	defer func() { os.Stdin = origStdin }()

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdoutR.Close()
	origStdout := os.Stdout
	os.Stdout = stdoutW
	defer func() { os.Stdout = origStdout }()

	go func() {
		io.WriteString(stdinW, requestLines)
		stdinW.Close()
	}()

	args := append([]string{"mcp"}, extraArgs...)
	_, errOut, c := run(t, args...)

	stdoutW.Close()
	os.Stdout = origStdout

	out, err := io.ReadAll(stdoutR)
	if err != nil {
		t.Fatal(err)
	}
	return string(out), errOut, c
}

func TestMCPRejectsExtraArgs(t *testing.T) {
	setupEnv(t)
	run(t, "init")

	if _, errOut, code := run(t, "mcp", "extra"); code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("mcp with an extra arg must be a usage error, got %d %q", code, errOut)
	}
}

// TestMCPServesJSONRPCOnRealStdio proves cmdMCP wires mcp.Serve to the
// real os.Stdin/os.Stdout, and that nothing but the JSON-RPC reply
// reaches stdout.
func TestMCPServesJSONRPCOnRealStdio(t *testing.T) {
	setupEnv(t)
	run(t, "init")

	out, errOut, code := runMCP(t, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`+"\n")
	if code != 0 {
		t.Fatalf("mcp failed: %s", errOut)
	}
	if errOut != "" {
		t.Fatalf("mcp wrote to stderr unexpectedly: %q", errOut)
	}
	if !strings.Contains(out, `"protocolVersion":"2024-11-05"`) {
		t.Fatalf("mcp did not answer initialize on stdout, got %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("mcp's reply must end with a newline, got %q", out)
	}
}

// TestMCPIgnoresJSONFlag proves "--json" (already stripped by Run
// before dispatch) changes nothing about mcp's behavior: the whole
// reply is JSON-RPC regardless.
func TestMCPIgnoresJSONFlag(t *testing.T) {
	setupEnv(t)
	run(t, "init")

	out, errOut, code := runMCP(t, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`+"\n", "--json")
	if code != 0 {
		t.Fatalf("mcp --json failed: %s", errOut)
	}
	if !strings.Contains(out, `"protocolVersion":"2024-11-05"`) {
		t.Fatalf("mcp --json did not answer initialize on stdout, got %q", out)
	}
}

func TestMCPRequiresAVault(t *testing.T) {
	setupEnv(t)
	// No "init": no vault exists at LOADOUT_HOME.
	out, errOut, code := runMCP(t, "")
	if code == 0 {
		t.Fatalf("mcp must fail without a vault, got code 0")
	}
	if out != "" {
		t.Fatalf("mcp must write nothing to stdout on failure, got %q", out)
	}
	if errOut == "" {
		t.Fatalf("mcp must explain the failure on stderr")
	}
}
