// Package mcp implements a Model Context Protocol server for
// loadout. It speaks JSON-RPC 2.0 over stdio, so an agent tool can
// query the vault and use secrets through a broker (http_request)
// without ever seeing a secret's value.
//
// The wire format is newline-delimited JSON: one JSON-RPC message per
// line, both ways. MCP also allows Content-Length framing (as used by
// LSP), but newline-delimited JSON needs no extra header parsing and
// every MCP stdio client accepts it, so this server uses it alone.
//
// A single line is capped at maxMessageBytes. A longer line is not a
// fatal error: Serve discards it and answers with a "message too
// large" error, then keeps serving the messages that follow.
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
// https://www.jsonrpc.org/specification#error_object. errMessageTooLarge
// reuses "Invalid Request": JSON-RPC defines no code of its own for an
// oversized message.
const (
	errParse          = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
)

const errMessageTooLarge = errInvalidRequest

// maxMessageBytes caps a single JSON-RPC message (one line). A line
// longer than this is never buffered in full: readMessage discards it
// as it drains to the next newline, so one oversized message costs
// bounded memory rather than growing without limit.
const maxMessageBytes = 8 << 20 // 8 MiB

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

// Registry holds the tools one MCP server exposes. registerTools
// registers the vault's read tools (Task 2) and the secret broker
// (Task 3).
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

// registerTools adds every tool this server exposes to reg: the five
// read tools (context, recall, show, list, list_secrets) from
// registerReadTools, and the secret broker (http_request) — using v
// to reach the vault's data.
func registerTools(reg *Registry, v *vault.Vault) {
	registerReadTools(reg, v)
	reg.Register(httpRequestTool(v))
}

// Serve runs the MCP JSON-RPC loop: it reads newline-delimited JSON
// messages from in, dispatches each to the matching method or tool,
// and writes one JSON-RPC response per line to out. A notification
// (a message with no "id") gets no response, per the JSON-RPC spec.
// Malformed JSON on a line, or a line over maxMessageBytes, produces
// an error response and does not stop the loop. Serve returns nil
// when in reaches EOF; it returns an error when in fails to read, or
// when out fails to take a response — a broken output channel means
// the session cannot continue, so Serve ends it rather than silently
// dropping replies until EOF.
func Serve(v *vault.Vault, in io.Reader, out io.Writer) error {
	reg := NewRegistry()
	registerTools(reg, v)

	reader := bufio.NewReaderSize(in, 64*1024)
	enc := json.NewEncoder(out)

	for {
		line, tooLong, readErr := readMessage(reader, maxMessageBytes)
		switch {
		case tooLong:
			if werr := writeResponse(enc, json.RawMessage("null"), nil, &rpcError{Code: errMessageTooLarge, Message: "message too large"}); werr != nil {
				return werr
			}
		default:
			if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
				if werr := handleLine(reg, trimmed, enc); werr != nil {
					return werr
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return nil
			}
			return readErr
		}
	}
}

// readMessage reads one newline-terminated message from r, up to max
// bytes. A message no longer than max is returned in full (with its
// trailing line ending stripped) with tooLong false. A message longer
// than max is never held in memory past max bytes: readMessage keeps
// reading and discarding until it finds the next newline (or a read
// error), returns tooLong true, and line is nil. readErr carries any
// error reading r, most commonly io.EOF on the final message; a
// message was still read (or found too long) when readErr is non-nil,
// so the caller must handle line/tooLong before checking readErr.
func readMessage(r *bufio.Reader, max int) (line []byte, tooLong bool, readErr error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if !tooLong {
			if len(buf)+len(chunk) > max {
				tooLong = true
				buf = nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		switch err {
		case nil:
			return bytes.TrimRight(buf, "\r\n"), tooLong, nil
		case bufio.ErrBufferFull:
			continue
		default:
			return buf, tooLong, err
		}
	}
}

// handleLine parses one line as a JSON-RPC message and writes its
// response through enc, returning any error writing it. It writes
// nothing for a notification (a message with no "id").
func handleLine(reg *Registry, line []byte, enc *json.Encoder) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(line, &probe); err != nil {
		return writeResponse(enc, json.RawMessage("null"), nil, &rpcError{Code: errParse, Message: "parse error"})
	}
	rawID, isRequest := probe["id"]

	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		// The line is valid JSON but not a valid request shape
		// (for example "method" is not a string). Treat it the
		// same as a parse error: the message is unusable.
		if isRequest {
			return writeResponse(enc, rawID, nil, &rpcError{Code: errParse, Message: "parse error"})
		}
		return nil
	}

	result, rpcErr := dispatch(reg, req)
	if !isRequest {
		// A notification never gets a reply, even one carrying an
		// error, per the JSON-RPC spec.
		return nil
	}
	return writeResponse(enc, rawID, result, rpcErr)
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

// writeResponse writes one JSON-RPC 2.0 response line to enc and
// returns any error doing so. result and rpcErr are mutually
// exclusive; passing both is a programming error in dispatch, not
// something a caller does at runtime.
//
// enc.Encode's error is never swallowed: enc wraps Serve's out, not
// its in, so a write failure here would otherwise go unnoticed until
// in reaches EOF and Serve returns nil as if every reply had gone
// out. Serve instead returns this error directly, ending the session:
// a broken output channel cannot be recovered from mid-loop.
func writeResponse(enc *json.Encoder, id json.RawMessage, result any, rpcErr *rpcError) error {
	resp := response{JSONRPC: "2.0", ID: id, Result: result, Error: rpcErr}
	return enc.Encode(resp)
}
