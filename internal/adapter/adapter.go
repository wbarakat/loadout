package adapter

import "loadout.dev/loadout/internal/vault"

// Problem is one finding from a check, with a fix the user can run.
type Problem struct {
	Adapter string
	Detail  string
	Fix     string
}

// FixBySync is the Fix text carried by every Problem that a plain
// "loadout sync" resolves on its own. A caller compares a Problem's Fix
// against it to tell pending work apart from a conflict the user has to
// resolve by hand. A fresh install reports only pending work, since
// nothing has been projected yet.
const FixBySync = "run: loadout sync"

// PendingSyncOnly reports whether ps holds at least one problem and
// every one of them is resolved by a plain "loadout sync".
func PendingSyncOnly(ps []Problem) bool {
	if len(ps) == 0 {
		return false
	}
	for _, p := range ps {
		if p.Fix != FixBySync {
			return false
		}
	}
	return true
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
	for _, name := range []string{"claude-code", "pi", "codex", "gemini", "cursor", "hermes", "agents-md"} {
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
		case "cursor":
			out = append(out, Cursor{Cfg: cfg})
		case "hermes":
			out = append(out, Hermes{Cfg: cfg})
		case "agents-md":
			out = append(out, AgentsMD{Cfg: cfg})
		}
	}
	return out
}
