package importer

import (
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
// aborts the whole run.
func writeItem(v *vault.Vault, it item) (ItemRef, *Warning, error) {
	if !vault.ValidName(it.name) {
		return ItemRef{}, &Warning{
			Tool:   it.tool,
			Path:   it.name,
			Reason: "not a valid kebab-case name. Fix: rename it, for example my-item.",
		}, nil
	}

	by := "import:" + it.tool
	at := it.modTime
	if at.IsZero() {
		at = time.Now()
	}

	var err error
	if it.kind == "skill" {
		_, err = vault.WriteSkillContent(v, it.name, it.description, it.body, by, at, it.files)
	} else {
		_, err = vault.WriteFactContent(v, it.name, it.description, it.factType, it.body, by, at)
	}
	if err != nil {
		return ItemRef{}, &Warning{Tool: it.tool, Path: it.name, Reason: err.Error()}, nil
	}
	return ItemRef{Kind: it.kind, Name: it.name, Tool: it.tool}, nil, nil
}
