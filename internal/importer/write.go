package importer

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"loadout.dev/loadout/internal/vault"
)

// writeItem writes it into the vault via the vault's own scaffold
// helpers (vault.WriteSkillContent / vault.WriteFactContent), so the
// file it produces parses identically to what "loadout add" would
// produce. by is always "import:<tool>", which the vault's own
// provenance rule turns into review: draft — nothing an importer
// writes silently counts as already reviewed. at comes from the
// candidate's own ModTime when the source tool recorded one, or now
// otherwise.
//
// A name that fails the vault's kebab-case rule, or a name that
// collides with an existing vault item of different content, comes
// back as a Warning rather than an error, so one bad candidate never
// aborts the whole run. A skill whose Files held a key escaping the
// skill folder still writes — vault.WriteSkillContent already
// dropped that one file — and each dropped key comes back as its own
// entry in fileWarnings, so it is never silently lost.
func writeItem(v *vault.Vault, it item) (ref ItemRef, warn *Warning, fileWarnings []Warning, err error) {
	if w := validateItem(v, it); w != nil {
		return ItemRef{}, w, nil, nil
	}

	by := "import:" + it.tool
	at := it.modTime
	if at.IsZero() {
		at = time.Now()
	}

	if it.kind == "skill" {
		var skipped []string
		_, skipped, err = vault.WriteSkillContent(v, it.name, it.description, it.body, by, at, it.files)
		for _, rel := range skipped {
			fileWarnings = append(fileWarnings, Warning{
				Tool:   it.tool,
				Path:   it.name + "/" + rel,
				Reason: "this support file's own path would write outside the skill folder. Fix: keep support files inside the skill folder.",
			})
		}
	} else {
		_, err = vault.WriteFactContent(v, it.name, it.description, it.factType, it.body, by, at)
	}
	if err != nil {
		return ItemRef{}, &Warning{Tool: it.tool, Path: it.name, Reason: err.Error()}, nil, nil
	}
	return ItemRef{Kind: it.kind, Name: it.name, Tool: it.tool}, nil, fileWarnings, nil
}

// validateItem reports the same problem a real write of it would hit,
// without writing anything: a name that fails the vault's kebab-case
// rule, or a name that already exists in the vault. An exact-content
// match against an existing vault item was already dropped earlier,
// by dedupAgainstVault, as a Deduped item rather than reaching here —
// so any name that still exists at this point is a genuine,
// different-content collision, the same one write.go's real write
// would refuse. RunImport's DryRun preview calls this too, so a dry
// run refuses exactly what a real import would refuse, rather than
// previewing an item the real write would go on to skip.
func validateItem(v *vault.Vault, it item) *Warning {
	if !vault.ValidName(it.name) {
		return &Warning{
			Tool:   it.tool,
			Path:   it.name,
			Reason: "not a valid kebab-case name. Fix: rename it, for example my-item.",
		}
	}
	existsPath := filepath.Join(v.MemoryDir(), it.name+".md")
	label := "fact"
	if it.kind == "skill" {
		existsPath = filepath.Join(v.SkillsDir(), it.name)
		label = "skill"
	}
	if _, err := os.Stat(existsPath); err == nil {
		return &Warning{
			Tool:   it.tool,
			Path:   it.name,
			Reason: fmt.Sprintf("the %s %s already exists. Fix: choose another name, or edit the existing item.", label, it.name),
		}
	}
	return nil
}
