package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"loadout.dev/loadout/internal/vault"
)

// secretShowRefusal is what the "show" tool returns for a secret/*
// address. INVARIANT 10 holds here too: a secret's value never
// appears over MCP, so "show" refuses the address before it ever
// looks at the secret's files. Use "list_secrets" for metadata.
const secretShowRefusal = "Secrets are not shown over MCP. Use list_secrets for metadata."

// registerReadTools adds the five read-only tools to reg: context,
// recall, show, list, and list_secrets. registerTools calls this.
func registerReadTools(reg *Registry, v *vault.Vault) {
	reg.Register(contextTool(v))
	reg.Register(recallTool(v))
	reg.Register(showTool(v))
	reg.Register(listTool(v))
	reg.Register(listSecretsTool(v))
}

// decodeArgs unmarshals a tool call's raw arguments into dst. Empty
// arguments (an absent "arguments" field) decode as an empty JSON
// object, so a tool call with no arguments at all does not fail on
// the JSON decode step itself — a tool that requires a field still
// reports its own missing-field error afterward.
func decodeArgs(args json.RawMessage, dst any) error {
	if len(bytes.TrimSpace(args)) == 0 {
		args = json.RawMessage(`{}`)
	}
	return json.Unmarshal(args, dst)
}

// renderItems writes one line per item: "<kind>/<name> — <hook>",
// the same line shape "loadout list" and "loadout recall" print.
func renderItems(items []vault.Item) string {
	var b strings.Builder
	for _, it := range items {
		fmt.Fprintf(&b, "%s — %s\n", it.Address(), vault.HookOrDefault(it.Hook))
	}
	return b.String()
}

// contextTool reads the vault's compact situational picture: counts,
// every memory hook, every skill hook, and recent history. It takes
// no arguments.
func contextTool(v *vault.Vault) Tool {
	return Tool{
		Name:        "context",
		Description: "Read the vault's compact situational picture: counts, memory hooks, skill hooks, and recent history.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(_ json.RawMessage) (ToolResult, error) {
			text, err := vault.RenderContext(v)
			if err != nil {
				return ToolResult{}, err
			}
			return ToolResult{Text: text}, nil
		},
	}
}

// recallArgs is the "recall" tool's arguments: one string of space-
// separated search terms.
type recallArgs struct {
	Terms string `json:"terms"`
}

// recallTool searches every skill and memory fact for terms that all
// match, case-insensitively, and lists the matches as addresses with
// hooks.
func recallTool(v *vault.Vault) Tool {
	return Tool{
		Name:        "recall",
		Description: "Search skills and memory facts for terms and list the matches as addresses with hooks.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"terms":{"type":"string","description":"space-separated search terms; an item must match every term"}},"required":["terms"]}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			var a recallArgs
			if err := decodeArgs(args, &a); err != nil {
				return ToolResult{Text: "invalid arguments: " + err.Error(), IsError: true}, nil
			}
			terms := strings.Fields(a.Terms)
			if len(terms) == 0 {
				return ToolResult{Text: "terms: must not be empty. Fix: pass one or more search words.", IsError: true}, nil
			}
			items, err := vault.Recall(v, terms)
			if err != nil {
				return ToolResult{}, err
			}
			if len(items) == 0 {
				return ToolResult{Text: "no items match. Fix: call list to see every item."}, nil
			}
			return ToolResult{Text: renderItems(items)}, nil
		},
	}
}

// showArgs is the "show" tool's arguments: one item's address.
type showArgs struct {
	Address string `json:"address"`
}

// showTool reads one skill or memory item's full body by its
// address. A secret/* address is refused: a secret value never
// appears over MCP (INVARIANT 10).
func showTool(v *vault.Vault) Tool {
	return Tool{
		Name:        "show",
		Description: "Read one skill or memory item's full body by its address (kind/name). Refuses a secret address.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"address":{"type":"string","description":"an address like skill/deploy-checks or memory/my-stack"}},"required":["address"]}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			var a showArgs
			if err := decodeArgs(args, &a); err != nil {
				return ToolResult{Text: "invalid arguments: " + err.Error(), IsError: true}, nil
			}
			if strings.HasPrefix(a.Address, "secret/") {
				return ToolResult{Text: secretShowRefusal, IsError: true}, nil
			}
			kind, name, err := vault.ParseAddress(a.Address)
			if err != nil {
				return ToolResult{Text: err.Error(), IsError: true}, nil
			}
			path, err := vault.ItemPath(v, kind, name)
			if err != nil {
				return ToolResult{Text: err.Error(), IsError: true}, nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return ToolResult{}, err
			}
			return ToolResult{Text: string(data)}, nil
		},
	}
}

// listTool lists every skill and memory fact as an address with its
// hook. Secrets are excluded, by design, to keep this read surface
// clean; "list_secrets" reports secret metadata on its own.
func listTool(v *vault.Vault) Tool {
	return Tool{
		Name:        "list",
		Description: "List every skill and memory fact as an address with its hook. Secrets are excluded; use list_secrets for those.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(_ json.RawMessage) (ToolResult, error) {
			items, err := vault.AllItems(v)
			if err != nil {
				return ToolResult{}, err
			}
			vault.SortItems(items)
			if len(items) == 0 {
				return ToolResult{Text: "the vault holds no items yet."}, nil
			}
			return ToolResult{Text: renderItems(items)}, nil
		},
	}
}

// secretMetadata is one secret's entry in the "list_secrets" tool's
// JSON result. It carries exactly the fields INVARIANT 10 allows a
// caller to see: never a value, and never whether the secret can be
// decrypted. AllowedHosts tells an agent which host it may reach
// through the http_request broker without needing to guess or probe.
type secretMetadata struct {
	Name         string   `json:"name"`
	Service      string   `json:"service"`
	Hook         string   `json:"hook"`
	RotateAfter  string   `json:"rotate_after"`
	AllowedHosts []string `json:"allowed_hosts"`
}

// listSecretsTool lists every secret's metadata: name, service, hook,
// rotation reminder, and allowed hosts. It never decrypts a value,
// and a Secret never carries one to leak in the first place
// (INVARIANT 10).
func listSecretsTool(v *vault.Vault) Tool {
	return Tool{
		Name:        "list_secrets",
		Description: "List every secret's metadata: name, service, hook, rotation reminder, and allowed hosts. Never a value; never a decrypt.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(_ json.RawMessage) (ToolResult, error) {
			secrets, err := vault.ListSecrets(v)
			if err != nil {
				return ToolResult{}, err
			}
			items := make([]secretMetadata, 0, len(secrets))
			for _, s := range secrets {
				items = append(items, secretMetadata{
					Name:         s.Name,
					Service:      s.Service,
					Hook:         s.Hook,
					RotateAfter:  s.RotateAfter,
					AllowedHosts: s.AllowedHosts,
				})
			}
			data, err := json.Marshal(items)
			if err != nil {
				return ToolResult{}, err
			}
			return ToolResult{Text: string(data)}, nil
		},
	}
}
