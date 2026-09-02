package importer

import (
	"strings"

	"loadout.dev/loadout/internal/vault"
)

// RunImport pulls skills and memory facts from every source into the
// vault v.
//
// For each source, it first calls Detect and skips a source that
// reports itself absent. It then collects Skills and Memory
// candidates per opt, accumulating each source's own warnings.
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

	for _, it := range kept {
		ref, warn, fileWarnings, err := writeItem(v, it)
		if err != nil {
			return result, err
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
