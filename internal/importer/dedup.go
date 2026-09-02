package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"loadout.dev/loadout/internal/vault"
)

// item is the common shape engine.go dedups and writes, unifying a
// CandidateSkill and a CandidateFact so dedup.go and write.go need
// only one code path. kind is "skill" or "memory", matching
// vault.Item's own kind values.
type item struct {
	kind        string
	name        string
	description string
	body        string
	// factType holds CandidateFact.Type; unused for a skill.
	factType string
	tool     string
	modTime  time.Time
	// files holds CandidateSkill.Files; unused for a fact.
	files map[string][]byte
}

var whitespaceRun = regexp.MustCompile(`\s+`)

// normalizeWhitespace collapses every run of whitespace to one space
// and trims the ends, so two bodies that differ only in formatting —
// trailing spaces, a blank line, CRLF vs LF — hash the same.
func normalizeWhitespace(s string) string {
	return strings.TrimSpace(whitespaceRun.ReplaceAllString(s, " "))
}

// contentHash returns the sha256 of the whitespace-normalized body,
// folded together with a digest of every entry in files — the
// content half of the dedup key (name is the other half). Folding in
// files matters for a skill: two candidates for the same skill can
// carry byte-identical SKILL.md content while differing in their
// support files (a helper.sh present on one side, absent or changed
// on the other) — without files in the hash, dedup would treat them
// as one exact duplicate and silently drop whichever copy it saw
// second, support files and all. files is walked in sorted key order
// so the result is stable regardless of map iteration order; a nil
// or empty files leaves the hash identical to a plain hash of body
// alone, so a fact (which never carries files) hashes exactly as it
// always has.
func contentHash(body string, files map[string][]byte) string {
	h := sha256.New()
	h.Write([]byte(normalizeWhitespace(body)))
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sum := sha256.Sum256(files[k])
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(hex.EncodeToString(sum[:])))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// dedupCandidates drops an exact duplicate — same kind, name, and
// content hash (body plus support files) — across every source,
// keeping the first one seen and recording each drop as a Deduped
// ItemRef. A name collision with a DIFFERENT hash is never dropped:
// every distinct-content item under that name is kept, and every one
// after the first gets a disambiguated name, "<name>-<tool>", then
// "<name>-2", "<name>-3", and so on if that name is itself already
// taken — by another group's own real name, by another candidate's
// own invented name, or by an item v already holds. v may be nil (a
// test with no vault to check names against).
//
// Every group's own base name is reserved up front, in a first pass,
// before any group's divergent members invent a disambiguated name.
// Without this, whichever group happens to be processed first can
// invent a name that is really another group's genuine, real name —
// for example a divergent "shared-skill" group's codex member
// inventing "shared-skill-codex" before the group actually named
// "shared-skill-codex" gets a turn, leaving the real skill bumped to
// "shared-skill-codex-2" instead of the invented one. Reserving every
// group's own name first means an invented name always yields to a
// real one. An item's own first-seen, undisambiguated name is never
// checked against v, only against other groups' names, since
// recognizing "this candidate already exists in the vault under this
// exact name and content" is dedupAgainstVault's job, run right after
// this by the caller — only a name this function itself invents needs
// to dodge the vault too.
func dedupCandidates(items []item, v *vault.Vault) ([]item, []ItemRef, error) {
	vaultNames, err := existingVaultNames(v)
	if err != nil {
		return nil, nil, err
	}

	type group struct{ items []item }
	groups := map[string]*group{}
	var order []string
	for _, it := range items {
		key := it.kind + "\x00" + it.name
		g, ok := groups[key]
		if !ok {
			g = &group{}
			groups[key] = g
			order = append(order, key)
		}
		g.items = append(g.items, it)
	}

	used := map[string]bool{}
	for _, key := range order {
		used[key] = true
	}

	var kept []item
	var deduped []ItemRef

	for _, key := range order {
		g := groups[key]

		// Partition the group by content hash, keeping only the first
		// item seen for each distinct hash; every later item with a
		// hash already seen is an exact duplicate.
		var distinct []item
		seen := map[string]bool{}
		for _, it := range g.items {
			h := contentHash(it.body, it.files)
			if seen[h] {
				deduped = append(deduped, ItemRef{Kind: it.kind, Name: it.name, Tool: it.tool})
				continue
			}
			seen[h] = true
			distinct = append(distinct, it)
		}

		for i, it := range distinct {
			// i == 0 always keeps the group's own, already-reserved
			// base name — every group's key is unique by
			// construction, so this can never collide with another
			// group.
			name := it.name
			if i > 0 {
				name = uniqueName(it.kind, it.name+"-"+it.tool, used, vaultNames)
			}
			it.name = name
			used[it.kind+"\x00"+name] = true
			kept = append(kept, it)
		}
	}
	return kept, deduped, nil
}

// existingVaultNames returns the "kind\x00name" key of every skill
// and fact already in v, or an empty set when v is nil. dedupCandidates
// uses it so a divergent candidate's INVENTED name — "<name>-<tool>",
// "<name>-2", and so on — never lands on a name the vault already
// owns for an unrelated item; picking such a name on purpose would
// only earn that candidate an avoidable "already exists" skip once
// write.go got to it.
func existingVaultNames(v *vault.Vault) (map[string]bool, error) {
	names := map[string]bool{}
	if v == nil {
		return names, nil
	}
	skills, err := vault.ListSkills(v)
	if err != nil {
		return nil, err
	}
	for _, s := range skills {
		names["skill\x00"+s.Name] = true
	}
	facts, err := vault.ListFacts(v)
	if err != nil {
		return nil, err
	}
	for _, f := range facts {
		names["memory\x00"+f.Name] = true
	}
	return names, nil
}

// uniqueName returns base if kind/base is not already in used or
// vaultUsed, else appends "-2", "-3", and so on until it finds a name
// that is free in both. vaultUsed may be nil, in which case only used
// is checked.
func uniqueName(kind, base string, used, vaultUsed map[string]bool) string {
	key := kind + "\x00" + base
	if !used[key] && !vaultUsed[key] {
		return base
	}
	for n := 2; ; n++ {
		candidate := base + "-" + strconv.Itoa(n)
		k := kind + "\x00" + candidate
		if !used[k] && !vaultUsed[k] {
			return candidate
		}
	}
}

// dedupAgainstVault drops every item that already exists in the vault
// under the same kind, name, and content hash, recording each drop as
// a Deduped ItemRef. An item whose name exists in the vault under
// DIFFERENT content is not touched here — it is left for write.go,
// which reports the real name collision as a Skipped warning rather
// than silently overwriting or renaming a human's existing item.
//
// This comparison is body-only (files is always nil on both sides):
// vault.ListSkills does not read a skill's support files back off
// disk, so there is no vault-side files digest to fold in here. A
// name whose support files changed but whose SKILL.md body did not
// therefore still reads as identical at this stage — the same
// scope limit as before this fix wave, not something this pass
// newly introduces.
func dedupAgainstVault(items []item, v *vault.Vault) ([]item, []ItemRef, error) {
	skillHash := map[string]string{}
	skills, err := vault.ListSkills(v)
	if err != nil {
		return nil, nil, err
	}
	for _, s := range skills {
		skillHash[s.Name] = contentHash(s.Body, nil)
	}

	factHash := map[string]string{}
	facts, err := vault.ListFacts(v)
	if err != nil {
		return nil, nil, err
	}
	for _, f := range facts {
		factHash[f.Name] = contentHash(f.Body, nil)
	}

	var kept []item
	var deduped []ItemRef
	for _, it := range items {
		existing := factHash
		if it.kind == "skill" {
			existing = skillHash
		}
		if h, ok := existing[it.name]; ok && h == contentHash(it.body, nil) {
			deduped = append(deduped, ItemRef{Kind: it.kind, Name: it.name, Tool: it.tool})
			continue
		}
		kept = append(kept, it)
	}
	return kept, deduped, nil
}
