package adapter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"loadout.dev/loadout/internal/vault"
)

// memoryMode picks how a fileAdapter projects memory into its tool.
type memoryMode int

const (
	// memoryNone skips memory. Use this for a skills-only tool.
	memoryNone memoryMode = iota
	// memoryBlock puts the full rendered memory in the managed block.
	memoryBlock
	// memoryImport puts one import line in the managed block, and
	// writes the full rendered memory to render/memory.md.
	memoryImport
)

// fileAdapter projects the vault into one file-based tool: skills as
// symlinks, memory per mode. ClaudeCode and Pi are thin wrappers
// around this kit; a future file-based adapter is a third wrapper
// with its own mode and no new logic.
type fileAdapter struct {
	name string
	cfg  vault.AdapterConfig
	mode memoryMode
}

// newFileAdapter builds a fileAdapter for one tool.
func newFileAdapter(name string, cfg vault.AdapterConfig, mode memoryMode) fileAdapter {
	return fileAdapter{name: name, cfg: cfg, mode: mode}
}

func (a fileAdapter) Name() string { return a.name }

// importLine names the render/memory.md file as an import target, in
// the form the memoryImport mode writes into the managed block.
func (a fileAdapter) importLine(v *vault.Vault) string {
	return "@" + filepath.Join(v.RenderDir(), "memory.md")
}

// memoryFileMissingDetail and memoryFileMissingFix hold the config
// error text for a memory mode with no memory_file set. Apply joins
// them into one error; Check keeps them as separate Problem fields.
func (a fileAdapter) memoryFileMissingDetail() string {
	return fmt.Sprintf("the adapter %s has no memory_file in the manifest", a.name)
}

func (a fileAdapter) memoryFileMissingFix() string {
	return fmt.Sprintf("set adapters.%s.memory_file, or disable the adapter.", a.name)
}

func (a fileAdapter) memoryFileConfigError() error {
	return fmt.Errorf("%s. Fix: %s", a.memoryFileMissingDetail(), a.memoryFileMissingFix())
}

// Apply projects the vault into the tool. It scans for stray loadout
// marks, then projects memory (per mode), then links skills.
//
// A mode other than memoryNone needs a memory_file in the manifest;
// an empty one is a config error, not a write attempt. An empty
// SkillsDir skips the skills projection entirely — a future
// instructions-only adapter can set no SkillsDir at all.
func (a fileAdapter) Apply(v *vault.Vault, dry bool) (Report, error) {
	report := newReport(a.name, dry)
	facts, err := vault.ListFacts(v)
	if err != nil {
		return report, err
	}
	skills, err := vault.ListSkills(v)
	if err != nil {
		return report, err
	}

	if a.mode != memoryNone {
		if err := scanForMarks(facts, skills); err != nil {
			return report, err
		}
		memoryFile := vault.ExpandPath(a.cfg.MemoryFile)
		if memoryFile == "" {
			return report, a.memoryFileConfigError()
		}
		switch a.mode {
		case memoryBlock:
			content := renderProjection(facts)
			if dry {
				if err := checkManagedBlockDamage(memoryFile); err != nil {
					return report, err
				}
				report.Applied = append(report.Applied, managedBlockDryMsg(memoryFile, strings.TrimSpace(content)))
			} else {
				if err := WriteManagedBlock(memoryFile, content); err != nil {
					return report, err
				}
				report.Applied = append(report.Applied, "memory: block written")
			}
		case memoryImport:
			renderPath := filepath.Join(v.RenderDir(), "memory.md")
			importLine := a.importLine(v)
			if dry {
				if err := checkManagedBlockDamage(memoryFile); err != nil {
					return report, err
				}
				msg := managedBlockDryMsg(memoryFile, importLine)
				// The import line can still match while the file it
				// points to has drifted: a fact edited straight in the
				// vault, with no sync run since. Check the render too,
				// so a dry run and doctor's Check always agree on
				// whether the block is stale.
				if msg == "memory: up to date" {
					data, err := os.ReadFile(renderPath)
					if err != nil || string(data) != renderProjection(facts) {
						msg = "memory: block would change"
					}
				}
				report.Applied = append(report.Applied, msg)
			} else {
				if err := writeFileAtomic(renderPath, []byte(renderProjection(facts))); err != nil {
					return report, err
				}
				if err := WriteManagedBlock(memoryFile, importLine); err != nil {
					return report, err
				}
				report.Applied = append(report.Applied, "memory: block written")
			}
		}
	}

	if a.cfg.SkillsDir == "" {
		return report, nil
	}
	skillsDir := vault.ExpandPath(a.cfg.SkillsDir)
	applied, pruned, blocked, err := LinkSkills(skills, v.SkillsDir(), skillsDir, dry)
	if err != nil {
		return report, err
	}
	report.Linked = len(applied)
	report.Applied = append(report.Applied, applied...)
	report.Pruned = orEmpty(pruned)
	report.Blocked = orEmpty(blocked)
	return report, nil
}

// Check reports every way the tool has drifted from the vault. It
// checks the managed block for damage first, then compares memory per
// mode (memoryImport also compares the render file), then checks the
// skill links and scans for orphans.
//
// A mode other than memoryNone needs a memory_file in the manifest;
// an empty one is the same config error Apply reports. memoryNone
// ignores its memory_file, if any, but reports it as a Problem: the
// setting looks live but Apply never touches it. An empty SkillsDir
// skips the skills check entirely.
func (a fileAdapter) Check(v *vault.Vault) []Problem {
	var ps []Problem

	if a.mode == memoryNone {
		if a.cfg.MemoryFile != "" {
			ps = append(ps, Problem{
				a.name,
				fmt.Sprintf("the adapter %s takes no memory_file; loadout ignores it.", a.name),
				fmt.Sprintf("remove adapters.%s.memory_file, or use the agents-md adapter for extra instruction files.", a.name),
			})
		}
	} else {
		memoryFile := vault.ExpandPath(a.cfg.MemoryFile)
		if memoryFile == "" {
			return append(ps, Problem{a.name, a.memoryFileMissingDetail(), a.memoryFileMissingFix()})
		}
		if err := checkManagedBlockDamage(memoryFile); err != nil {
			// Damaged marks need a repair, not a sync: a sync would
			// refuse to touch the file too, so telling the user to
			// sync it is a dead end.
			ps = append(ps, Problem{a.name, fmt.Sprintf("the loadout marks in %s are damaged", memoryFile), fmt.Sprintf("repair or remove the marks in %s.", memoryFile)})
			return ps
		}
		facts, err := vault.ListFacts(v)
		if err != nil {
			return append(ps, Problem{a.name, err.Error(), "repair the vault memory directory"})
		}
		switch a.mode {
		case memoryBlock:
			got, ok := ReadManagedBlock(memoryFile)
			if !ok || got != strings.TrimSpace(renderProjection(facts)) {
				ps = append(ps, Problem{a.name, "the memory block is missing or stale", "run: loadout sync"})
			}
		case memoryImport:
			got, ok := ReadManagedBlock(memoryFile)
			if !ok || got != a.importLine(v) {
				ps = append(ps, Problem{a.name, "the memory import block is missing or stale", "run: loadout sync"})
			}
			renderPath := filepath.Join(v.RenderDir(), "memory.md")
			data, err := os.ReadFile(renderPath)
			if err != nil || string(data) != renderProjection(facts) {
				ps = append(ps, Problem{a.name, "the rendered memory is missing or stale", "run: loadout sync"})
			}
		}
	}

	if a.cfg.SkillsDir == "" {
		return ps
	}
	skills, err := vault.ListSkills(v)
	if err != nil {
		return append(ps, Problem{a.name, err.Error(), "repair the vault skills directory"})
	}
	skillsDir := vault.ExpandPath(a.cfg.SkillsDir)
	ps = append(ps, checkLinks(a.name, skills, v.SkillsDir(), skillsDir)...)
	for _, p := range orphanLinks(skills, v.SkillsDir(), skillsDir) {
		p.Adapter = a.name
		ps = append(ps, p)
	}
	return ps
}
