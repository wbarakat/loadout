package adapter

import "loadout.dev/loadout/internal/vault"

// Problem is one finding from a check, with a fix the user can run.
type Problem struct {
	Adapter string
	Detail  string
	Fix     string
}

// Adapter projects the vault into one tool.
type Adapter interface {
	Name() string
	Apply(v *vault.Vault, dry bool) (Report, error)
	Check(v *vault.Vault) []Problem
}

// Enabled returns the enabled adapters from the manifest, in a
// stable order.
func Enabled(v *vault.Vault) []Adapter {
	var out []Adapter
	for _, name := range []string{"claude-code", "pi", "codex", "gemini", "agents-md"} {
		cfg, ok := v.Manifest.Adapters[name]
		if !ok || !cfg.Enabled {
			continue
		}
		switch name {
		case "claude-code":
			out = append(out, ClaudeCode{Cfg: cfg})
		case "pi":
			out = append(out, Pi{Cfg: cfg})
		case "codex":
			out = append(out, Codex{Cfg: cfg})
		case "gemini":
			out = append(out, Gemini{Cfg: cfg})
		case "agents-md":
			out = append(out, AgentsMD{Cfg: cfg})
		}
	}
	return out
}
