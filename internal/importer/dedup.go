package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
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
// hex-encoded — the content half of the dedup key (name is the other
// half).
func contentHash(body string) string {
	sum := sha256.Sum256([]byte(normalizeWhitespace(body)))
	return hex.EncodeToString(sum[:])
}

// dedupCandidates drops an exact duplicate — same kind, name, and
// content hash — across every source, keeping the first one seen and
// recording each drop as a Deduped ItemRef. A name collision with a
// DIFFERENT hash is never dropped: every distinct-content item under
// that name is kept, and every one after the first gets a
// disambiguated name, "<name>-<tool>", then "<name>-2", "<name>-3",
// and so on if that name is itself already taken.
func dedupCandidates(items []item) ([]item, []ItemRef) {
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

	var kept []item
	var deduped []ItemRef
	used := map[string]bool{}

	for _, key := range order {
		g := groups[key]

		// Partition the group by content hash, keeping only the first
		// item seen for each distinct hash; every later item with a
		// hash already seen is an exact duplicate.
		var distinct []item
		seen := map[string]bool{}
		for _, it := range g.items {
			h := contentHash(it.body)
			if seen[h] {
				deduped = append(deduped, ItemRef{Kind: it.kind, Name: it.name, Tool: it.tool})
				continue
			}
			seen[h] = true
			distinct = append(distinct, it)
		}

		for i, it := range distinct {
			name := it.name
			if i > 0 {
				name = uniqueName(it.kind, it.name+"-"+it.tool, used)
			} else {
				name = uniqueName(it.kind, it.name, used)
			}
			it.name = name
			used[it.kind+"\x00"+name] = true
			kept = append(kept, it)
		}
	}
	return kept, deduped
}

// uniqueName returns base if kind/base is not already in used, else
// appends "-2", "-3", and so on until it finds a name that is free.
func uniqueName(kind, base string, used map[string]bool) string {
	if !used[kind+"\x00"+base] {
		return base
	}
	for n := 2; ; n++ {
		candidate := base + "-" + strconv.Itoa(n)
		if !used[kind+"\x00"+candidate] {
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
func dedupAgainstVault(items []item, v *vault.Vault) ([]item, []ItemRef, error) {
	skillHash := map[string]string{}
	skills, err := vault.ListSkills(v)
	if err != nil {
		return nil, nil, err
	}
	for _, s := range skills {
		skillHash[s.Name] = contentHash(s.Body)
	}

	factHash := map[string]string{}
	facts, err := vault.ListFacts(v)
	if err != nil {
		return nil, nil, err
	}
	for _, f := range facts {
		factHash[f.Name] = contentHash(f.Body)
	}

	var kept []item
	var deduped []ItemRef
	for _, it := range items {
		existing := factHash
		if it.kind == "skill" {
			existing = skillHash
		}
		if h, ok := existing[it.name]; ok && h == contentHash(it.body) {
			deduped = append(deduped, ItemRef{Kind: it.kind, Name: it.name, Tool: it.tool})
			continue
		}
		kept = append(kept, it)
	}
	return kept, deduped, nil
}
