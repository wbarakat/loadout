package importer

import (
	"strings"

	"loadout.dev/loadout/internal/vault"
)

// dedupWarnings collapses a Warning that appears more than once — the
// same Tool, Path, and Reason — down to a single entry, keeping the
// first occurrence's place in order. A Source can legitimately emit
// the identical informational warning from two of its own methods
// (Cursor's User-Rules warning, from both Skills and Memory, is the
// motivating case), and RunImport calls both methods in the same run
// whenever a caller asks for both skills and memory. Without this
// pass, that combination would show the warning twice. Two warnings
// that differ in any one field — a different Tool, a different Path,
// or just different wording in Reason — are genuinely distinct
// problems and are never collapsed.
func dedupWarnings(warnings []Warning) []Warning {
	seen := make(map[Warning]bool, len(warnings))
	out := make([]Warning, 0, len(warnings))
	for _, w := range warnings {
		if seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// RunImport pulls skills and memory facts from every source into the
// vault v.
//
// For each source, it first calls Detect and skips a source that
// reports itself absent. It then collects Skills and Memory
// candidates per opt, accumulating each source's own warnings. Once
// every source has run, it collapses identical warnings down to one
// entry each — see dedupWarnings — so a Source that reports the same
// problem from both its Skills and Memory methods is not double
// counted just because a caller asked for both.
//
// It applies the shared reliability rules once, centrally, as
// defense-in-depth even though a well-behaved Source already applies
// them itself:
//   - every memory body is run through StripLoadoutBlock. A damaged
//     block is skipped with a warning; a body left empty after
//     stripping (Loadout's own projection, with nothing native left
//     to recover) is skipped without one.
//   - a skill a Source failed to exclude as vault-owned still cannot
//     survive: its body is byte-identical to the vault's own copy of
//     that skill, so the dedup-against-vault pass below drops it as
//     an exact match. (IsVaultOwnedSkill itself needs a real
//     filesystem path to resolve, which CandidateSkill does not
//     carry — a Source, which does the directory walk, is where that
//     check actually runs.)
//
// It then dedups across every source (dedup.go's dedupCandidates),
// dedups again against the vault's existing items
// (dedupAgainstVault), and either writes what is left (write.go) or,
// under DryRun, records the same set as a preview and writes nothing.
func RunImport(v *vault.Vault, sources []Source, ctx ImportCtx, opt Options) (ImportResult, error) {
	var result ImportResult
	var candidates []item

	for _, src := range sources {
		present, _ := src.Detect(ctx)
		if !present {
			continue
		}
		if opt.Progress != nil {
			opt.Progress(src.Name())
		}

		if opt.Skills {
			skills, warnings, err := src.Skills(ctx)
			result.Warnings = append(result.Warnings, warnings...)
			if err != nil {
				result.Warnings = append(result.Warnings, Warning{Tool: src.Name(), Reason: err.Error()})
			}
			for _, cs := range skills {
				candidates = append(candidates, item{
					kind:        "skill",
					name:        cs.Name,
					description: cs.Description,
					body:        cs.Body,
					tool:        cs.Tool,
					modTime:     cs.ModTime,
					files:       cs.Files,
				})
			}
		}

		if opt.Memory {
			facts, warnings, err := src.Memory(ctx)
			result.Warnings = append(result.Warnings, warnings...)
			if err != nil {
				result.Warnings = append(result.Warnings, Warning{Tool: src.Name(), Reason: err.Error()})
			}
			for _, cf := range facts {
				native, damaged := StripLoadoutBlock(cf.Body)
				if damaged {
					result.Skipped = append(result.Skipped, Warning{
						Tool:   cf.Tool,
						Path:   cf.Name,
						Reason: "the loadout marks in this memory are damaged. Fix: repair or remove the marks at the source.",
					})
					continue
				}
				native = strings.TrimSpace(native)
				if native == "" {
					result.Skipped = append(result.Skipped, Warning{
						Tool:   cf.Tool,
						Path:   cf.Name,
						Reason: "nothing is left after removing loadout's own managed block",
					})
					continue
				}
				candidates = append(candidates, item{
					kind:        "memory",
					name:        cf.Name,
					description: cf.Description,
					body:        native,
					factType:    cf.Type,
					tool:        cf.Tool,
					modTime:     cf.ModTime,
				})
			}
		}
	}

	result.Warnings = dedupWarnings(result.Warnings)

	kept, deduped, err := dedupCandidates(candidates, v)
	if err != nil {
		return result, err
	}
	result.Deduped = append(result.Deduped, deduped...)

	kept, dedupedVault, err := dedupAgainstVault(kept, v)
	if err != nil {
		return result, err
	}
	result.Deduped = append(result.Deduped, dedupedVault...)

	if opt.DryRun {
		// Preview exactly what the real write loop below would do:
		// an item validateItem would refuse — an invalid name, or a
		// name colliding with a different-content vault item — is
		// skipped and warned about here too, never shown as
		// importable.
		for _, it := range kept {
			if warn := validateItem(v, it); warn != nil {
				result.Skipped = append(result.Skipped, *warn)
				continue
			}
			result.Imported = append(result.Imported, ItemRef{Kind: it.kind, Name: it.name, Tool: it.tool})
		}
		return result, nil
	}

	// FIX 3: a single item the vault refuses to write must never abort
	// the run and lose every other item still waiting to write. writeItem
	// already turns most write problems into warn rather than err; the
	// err branch below is the last-resort net for anything that still
	// slips through as a genuine error — it is recorded as a skip+warn,
	// the very same shape warn already gets, and the loop continues.
	for _, it := range kept {
		ref, warn, fileWarnings, err := writeItem(v, it)
		if err != nil {
			result.Skipped = append(result.Skipped, Warning{Tool: it.tool, Path: it.name, Reason: err.Error()})
			continue
		}
		if warn != nil {
			result.Skipped = append(result.Skipped, *warn)
			continue
		}
		result.Warnings = append(result.Warnings, fileWarnings...)
		result.Imported = append(result.Imported, ref)
	}

	return result, nil
}
