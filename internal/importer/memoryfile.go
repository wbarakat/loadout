package importer

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// readInstructionMemory reads every EXISTING path in paths as one
// instruction-file-shaped memory source — an AGENTS.md, a GEMINI.md,
// or any other plain-markdown file a tool concatenates whole — and
// turns its native content into candidate facts.
//
// For each path: a missing file is not a problem (most installs will
// not have every possible memory file) and is skipped with no
// warning. An existing file over maxMemoryFileSize is skipped with a
// warning rather than read. Loadout's own managed block is stripped
// first (StripLoadoutBlock); a damaged block skips the file with a
// warning, and a body left empty after stripping (Loadout's own
// projection, with nothing native left to recover) is skipped
// without one. What is left is split on its top-level "##" headings
// into one fact per heading, or one fact for the whole file when it
// has no such heading — the same rule claude-code's and codex's own
// memory readers already apply (see the package doc comment on why
// those two keep their own, separately-tested version of this logic
// instead of calling this function).
//
// Every returned fact carries Tool = tool and Type = "user" — a
// caller that reads a project-scoped file, rather than a tool's
// global one, overwrites Type to "project" on the facts it gets
// back. by:import:<tool> and review:draft are applied later, at the
// write path (write.go's writeItem), not here.
func readInstructionMemory(paths []string, tool string) ([]CandidateFact, []Warning) {
	var facts []CandidateFact
	var warnings []Warning

	for _, path := range paths {
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Size() > maxMemoryFileSize {
			warnings = append(warnings, Warning{
				Tool:   tool,
				Path:   path,
				Reason: "this file is larger than 4MiB. Fix: split it into smaller files.",
			})
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			warnings = append(warnings, Warning{Tool: tool, Path: path, Reason: "the file could not be read: " + err.Error()})
			continue
		}

		native, damaged := StripLoadoutBlock(string(raw))
		if damaged {
			warnings = append(warnings, Warning{
				Tool:   tool,
				Path:   path,
				Reason: "the loadout marks in this file are damaged. Fix: repair or remove the marks at the source.",
			})
			continue
		}
		native = strings.TrimSpace(native)
		if native == "" {
			continue
		}

		facts = append(facts, splitInstructionMemory(native, path, tool, info.ModTime())...)
	}

	return facts, warnings
}

// splitInstructionMemory turns one file's already-stripped native
// content into candidate facts: one per top-level "##" heading (name
// = the path's own kebab base, plus a "-<heading>" suffix), or one
// fact for the whole file (name = the path's own kebab base alone)
// when content has no such heading. Basing every name on the path,
// not the heading text alone, is what lets a caller pass more than
// one path in one readInstructionMemory call — a project's AGENTS.md
// chain, from its git root down to the working directory, say —
// without two different files' facts colliding under one name just
// because they share a heading title; a genuine collision is left for
// the shared dedup pass (dedup.go), which disambiguates by content
// hash rather than guessing here.
func splitInstructionMemory(native, path, tool string, modTime time.Time) []CandidateFact {
	base := pathFactBase(path)

	sections, structured := splitTopSections(native)
	var facts []CandidateFact
	for _, sec := range sections {
		body := strings.TrimSpace(sec.body)
		if body == "" {
			continue
		}
		name := base
		description := firstLine(body)
		if structured {
			suffix := "intro"
			if sec.heading != "" {
				suffix = kebabify(sec.heading)
			}
			if suffix != "" {
				name = base + "-" + suffix
			}
			if description == "" {
				description = sec.heading
			}
		}
		if description == "" {
			description = name
		}
		facts = append(facts, CandidateFact{
			Name:        name,
			Description: description,
			Type:        "user",
			Body:        body,
			Tool:        tool,
			ModTime:     modTime,
		})
	}
	return facts
}

// pathFactBase derives a stable, kebab-case base name for one
// memory-file path: its own basename, extension stripped and
// kebabified — "AGENTS.md" becomes "agents", "GEMINI.md" becomes
// "gemini".
func pathFactBase(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return kebabify(base)
}
