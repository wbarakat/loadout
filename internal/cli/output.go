package cli

import (
	"encoding/json"
	"fmt"
	"io"
)

// mode selects how a verb renders its result: text, its current
// output, or JSON.
type mode int

const (
	modeText mode = iota
	modeJSON
)

// extractJSON removes the first "--json" argument found at any
// position in args, and reports whether it found one. Run calls this
// before it dispatches to a verb, so no verb ever sees a "--json"
// token mixed in with its own arguments.
func extractJSON(args []string) ([]string, bool) {
	for i, a := range args {
		if a == "--json" {
			out := make([]string, 0, len(args)-1)
			out = append(out, args[:i]...)
			out = append(out, args[i+1:]...)
			return out, true
		}
	}
	return args, false
}

// printJSON marshals v as indented JSON and writes it to out, with a
// trailing newline. v is always a typed result struct built by the
// caller, so the marshal never fails in practice.
func printJSON(out io.Writer, v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintln(out, err)
		return
	}
	out.Write(data)
	fmt.Fprintln(out)
}
