// Package mcp implements a Model Context Protocol server for
// loadout. It speaks JSON-RPC 2.0 over stdio, so an agent tool can
// query the vault and, in a later task, use secrets through a
// broker.
//
// The wire format is newline-delimited JSON: one JSON-RPC message per
// line, both ways. MCP also allows Content-Length framing (as used by
// LSP), but newline-delimited JSON needs no extra header parsing and
// every MCP stdio client accepts it, so this server uses it alone.
package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"

	"loadout.dev/loadout/internal/vault"
)

// protocolVersion is the MCP protocol date this server implements.
// serverName and serverVersion identify loadout in the initialize
// handshake.
const (
	protocolVersion = "2024-11-05"
	serverName      = "loadout"
	serverVersion   = "0.6"
)

// Standard JSON-RPC 2.0 error codes. See
// https://www.jsonrpc.org/specification#error_object.
const (
	errParse          = -32700
	errMethodNotFound = -32601
	errInvalidParams  = -32602
)

// request is one JSON-RPC 2.0 request or notification. A request
// carries an id; a notification does not. rawID holds the id exactly
// as it arrived (string, number, or absent) so a response can echo it
// back unchanged.
type request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// response is one JSON-RPC 2.0 response, marshaled with the id already
// resolved. Result and Error are mutually exclusive per the spec, so
// omitempty on Error is enough to drop it from a success response.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ToolResult is what a Tool's Handler returns: the text MCP reports
// back as the tool's output, and whether that output is an error.
type ToolResult struct {
	Text    string
	IsError bool
}

// Tool is one MCP tool. Name and Description and InputSchema are what
// tools/list reports; Handler is what tools/call runs, receiving the
// call's raw "arguments" value and returning the text (or error) to
// send back.
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Handler     func(args json.RawMessage) (ToolResult, error)
}

// Registry holds the tools one MCP server exposes. Tasks 2 and 3
// register the vault's read tools and the secret broker into it
// through registerTools below; this task leaves it empty.
type Registry struct {
	tools []Tool
}

// NewRegistry returns an empty Registry ready for Register calls.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a tool to the registry. Serve exposes it through
// tools/list and tools/call.
func (r *Registry) Register(t Tool) {
	r.tools = append(r.tools, t)
}

// find returns the tool named name, and whether one was registered.
func (r *Registry) find(name string) (Tool, bool) {
	for _, t := range r.tools {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

// registerTools adds every tool this server exposes to reg. Task 1
// leaves it empty; Tasks 2 and 3 extend it with the vault's read
// tools and the secret broker, using v to reach the vault's data.
func registerTools(reg *Registry, v *vault.Vault) {
	_ = v
}

// Serve runs the MCP JSON-RPC loop: it reads newline-delimited JSON
// messages from in, dispatches each to the matching method or tool,
// and writes one JSON-RPC response per line to out. A notification
// (a message with no "id") gets no response, per the JSON-RPC spec.
// Malformed JSON on a line produces a parse-error response and does
// not stop the loop. Serve returns nil when in reaches EOF, and any
// other error reading in otherwise.
func Serve(v *vault.Vault, in io.Reader, out io.Writer) error {
	reg := NewRegistry()
	registerTools(reg, v)

	scanner := bufio.NewScanner(in)
	// A tool's input or output can exceed bufio.Scanner's 64KB
	// default; grow the buffer so a long line is read whole rather
	// than rejected as "token too long".
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	enc := json.NewEncoder(out)

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		handleLine(reg, line, enc)
	}
	return scanner.Err()
}

// handleLine parses one line as a JSON-RPC message and writes its
// response through enc. It writes nothing for a notification (a
// message with no "id").
func handleLine(reg *Registry, line []byte, enc *json.Encoder) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(line, &probe); err != nil {
		writeResponse(enc, json.RawMessage("null"), nil, &rpcError{Code: errParse, Message: "parse error"})
		return
	}
	rawID, isRequest := probe["id"]

	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		// The line is valid JSON but not a valid request shape
		// (for example "method" is not a string). Treat it the
		// same as a parse error: the message is unusable.
		if isRequest {
			writeResponse(enc, rawID, nil, &rpcError{Code: errParse, Message: "parse error"})
		}
		return
	}

	result, rpcErr := dispatch(reg, req)
	if !isRequest {
		// A notification never gets a reply, even one carrying an
		// error, per the JSON-RPC spec.
		return
	}
	writeResponse(enc, rawID, result, rpcErr)
}

// dispatch runs one request's method and returns either its result or
// a JSON-RPC error, never both.
func dispatch(reg *Registry, req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return initializeResult{
			ProtocolVersion: protocolVersion,
			ServerInfo:      serverInfo{Name: serverName, Version: serverVersion},
			Capabilities:    capabilities{Tools: struct{}{}},
		}, nil
	case "tools/list":
		return toolsListResult{Tools: listTools(reg)}, nil
	case "tools/call":
		return dispatchToolCall(reg, req.Params)
	default:
		return nil, &rpcError{Code: errMethodNotFound, Message: "method not found: " + req.Method}
	}
}

// initializeResult is the result of the "initialize" method.
type initializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	ServerInfo      serverInfo   `json:"serverInfo"`
	Capabilities    capabilities `json:"capabilities"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// capabilities is loadout's MCP capability set. It supports tools
// only; struct{} marshals to "{}", which announces the capability
// with no options.
type capabilities struct {
	Tools struct{} `json:"tools"`
}

// toolInfo is one tool's entry in the "tools/list" result.
type toolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type toolsListResult struct {
	Tools []toolInfo `json:"tools"`
}

// listTools builds tools/list's tool array from reg. It always
// returns a non-nil slice, so an empty registry marshals to "[]"
// rather than "null".
func listTools(reg *Registry) []toolInfo {
	tools := make([]toolInfo, 0, len(reg.tools))
	for _, t := range reg.tools {
		schema := t.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{}`)
		}
		tools = append(tools, toolInfo{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	return tools
}

// toolCallParams is "tools/call"'s params shape.
type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// toolContent is one entry of a tool call result's "content" array.
// MCP defines other content types (image, resource, ...); loadout's
// tools return text only.
type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// toolCallResult is the result of a successful "tools/call".
type toolCallResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// dispatchToolCall runs the named tool from params and wraps its
// ToolResult as tools/call's result shape. An unknown tool name, or a
// params value that does not parse, is a JSON-RPC error rather than a
// tool result: the caller asked for something that does not exist,
// not something that ran and failed.
func dispatchToolCall(reg *Registry, params json.RawMessage) (any, *rpcError) {
	var p toolCallParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: errInvalidParams, Message: "invalid params: " + err.Error()}
	}
	tool, ok := reg.find(p.Name)
	if !ok {
		return nil, &rpcError{Code: errMethodNotFound, Message: "tool not found: " + p.Name}
	}
	result, err := tool.Handler(p.Arguments)
	if err != nil {
		result = ToolResult{Text: err.Error(), IsError: true}
	}
	return toolCallResult{
		Content: []toolContent{{Type: "text", Text: result.Text}},
		IsError: result.IsError,
	}, nil
}

// writeResponse writes one JSON-RPC 2.0 response line to enc. result
// and rpcErr are mutually exclusive; passing both is a programming
// error in dispatch, not something a caller does at runtime.
func writeResponse(enc *json.Encoder, id json.RawMessage, result any, rpcErr *rpcError) {
	resp := response{JSONRPC: "2.0", ID: id, Result: result, Error: rpcErr}
	// json.Encoder.Encode never fails to write to an in-memory or
	// pipe writer for the message shapes this server produces; a
	// failure here means out itself is broken, which the caller
	// (Serve's scanner loop on the next read) will surface.
	_ = enc.Encode(resp)
}
