package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

// TestDispatchToolCallRunsTheNamedTool proves tools/call finds a
// registered tool by name, hands it the raw arguments, and wraps its
// ToolResult as the MCP content shape. Registry.Register and
// dispatchToolCall are the extension points Tasks 2 and 3 build on,
// so this white-box test exercises them directly: Task 1 registers no
// tool of its own through the public Serve path.
func TestDispatchToolCallRunsTheNamedTool(t *testing.T) {
	reg := NewRegistry()
	var gotArgs json.RawMessage
	reg.Register(Tool{
		Name:        "echo",
		Description: "echoes its arguments back as text",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			gotArgs = args
			return ToolResult{Text: "hello"}, nil
		},
	})

	result, rpcErr := dispatchToolCall(reg, json.RawMessage(`{"name":"echo","arguments":{"who":"world"}}`))
	if rpcErr != nil {
		t.Fatalf("dispatchToolCall returned an error: %+v", rpcErr)
	}
	if string(gotArgs) != `{"who":"world"}` {
		t.Fatalf("handler did not receive the raw arguments, got %s", gotArgs)
	}
	call, ok := result.(toolCallResult)
	if !ok {
		t.Fatalf("want a toolCallResult, got %T", result)
	}
	if call.IsError {
		t.Fatalf("want isError false for a successful call")
	}
	if len(call.Content) != 1 || call.Content[0].Type != "text" || call.Content[0].Text != "hello" {
		t.Fatalf("want one text content \"hello\", got %+v", call.Content)
	}
}

// TestDispatchToolCallWrapsAHandlerError proves a Handler error never
// becomes a JSON-RPC protocol error: it becomes a tool result with
// isError true, so an MCP client sees a failed tool call rather than
// a broken connection.
func TestDispatchToolCallWrapsAHandlerError(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Tool{
		Name: "boom",
		Handler: func(args json.RawMessage) (ToolResult, error) {
			return ToolResult{}, errors.New("it broke")
		},
	})

	result, rpcErr := dispatchToolCall(reg, json.RawMessage(`{"name":"boom","arguments":{}}`))
	if rpcErr != nil {
		t.Fatalf("dispatchToolCall returned a protocol error for a handler error: %+v", rpcErr)
	}
	call := result.(toolCallResult)
	if !call.IsError {
		t.Fatalf("want isError true after a handler error")
	}
	if len(call.Content) != 1 || call.Content[0].Text != "it broke" {
		t.Fatalf("want the error message as the content text, got %+v", call.Content)
	}
}

// TestDispatchToolCallUnknownToolIsAnRPCError proves calling a name no
// tool registered under is a JSON-RPC error, not a tool result: the
// client asked for something that does not exist.
func TestDispatchToolCallUnknownToolIsAnRPCError(t *testing.T) {
	reg := NewRegistry()
	_, rpcErr := dispatchToolCall(reg, json.RawMessage(`{"name":"nope","arguments":{}}`))
	if rpcErr == nil || rpcErr.Code != errMethodNotFound {
		t.Fatalf("want error %d, got %+v", errMethodNotFound, rpcErr)
	}
}

// TestDispatchToolCallInvalidParams proves a "tools/call" whose params
// do not even parse as {name, arguments} is an invalid-params error,
// not a panic or a silent no-op.
func TestDispatchToolCallInvalidParams(t *testing.T) {
	reg := NewRegistry()
	_, rpcErr := dispatchToolCall(reg, json.RawMessage(`"not an object"`))
	if rpcErr == nil || rpcErr.Code != errInvalidParams {
		t.Fatalf("want error %d, got %+v", errInvalidParams, rpcErr)
	}
}

// TestListToolsNeverReturnsNil proves an empty registry's tool list
// marshals to "[]", not "null": Task 1 ships the registry empty, and
// tools/list must still report a valid, empty JSON array.
func TestListToolsNeverReturnsNil(t *testing.T) {
	reg := NewRegistry()
	tools := listTools(reg)
	if tools == nil {
		t.Fatalf("want a non-nil empty slice, got nil")
	}
	data, err := json.Marshal(tools)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != "[]" {
		t.Fatalf("want [], got %s", data)
	}
}

// TestHandleLineWritesNothingForANotification proves handleLine
// writes no output at all when a message carries no "id", even when
// its method is unknown — the general JSON-RPC rule, not just the
// notifications/* naming convention.
func TestHandleLineWritesNothingForANotification(t *testing.T) {
	reg := NewRegistry()
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	if err := handleLine(reg, []byte(`{"jsonrpc":"2.0","method":"whatever/unknown"}`), enc); err != nil {
		t.Fatalf("handleLine: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("want no output for a notification, got %q", out.String())
	}
}
