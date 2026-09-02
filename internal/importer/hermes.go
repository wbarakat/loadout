package importer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Hermes is the import Source for NousResearch's Hermes agent's own
// on-disk store (source map §4). Three traits make Hermes different
// from every other source in this package:
//
//   - Its top-level skills directory is NOT all user content. It
//     ships a large BUNDLED VENDOR skill library alongside a few of
//     the user's own skills, in the same directory. A manifest file
//     names the vendor set; this source reads it and excludes those
//     names.
//   - It supports named PROFILES — near-complete parallel
//     environments, each with its own skills/, memories/, and
//     SOUL.md. A profile is a per-project/opt-in surface, so this
//     source only scans profiles when ctx.ProjectMemory is set.
//   - Its two memory files, MEMORY.md and USER.md, are AGENT-managed:
//     Hermes itself writes and locks them while it runs. This source
//     checks for that lock before it reads either file.
//
// SECRET SAFETY: this source reads only named SKILL.md files, a
// directory's own .bundled_manifest, and memories/{MEMORY,USER}.md
// (plus the same two under a profile). It never opens config.yaml,
// and it never globs ~/.hermes broadly — every scan below names its
// target directory or file explicitly.
type Hermes struct{}

func (Hermes) Name() string { return "hermes" }

// hermesRoot resolves Hermes's root directory: ctx.Home + "/.hermes".
// Hermes has no documented relocation environment variable (source
// map §7).
func hermesRoot(ctx ImportCtx) string {
	return filepath.Join(ctx.Home, ".hermes")
}

// Detect reports Hermes present when ~/.hermes exists as a
// directory. A hermes binary on PATH is a documented fallback signal
// (source map §7) not implemented here, since directory presence
// already covers every real install this importer needs to detect.
func (Hermes) Detect(ctx ImportCtx) (bool, string) {
	root := hermesRoot(ctx)
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return false, ""
	}
	return true, root
}

// hermesBundledManifest reads <skillsDir>/.bundled_manifest — a flat
// "<skill-name>:<content-hash>" list, one entry per line — and
// returns the set of skill names it names, plus whether the manifest
// file itself is present. A missing manifest returns (nil, false):
// scanHermesSkills degrades to "import every top-level skill" —
// there is nothing to exclude (source map §4's own graceful-
// degradation rule). A manifest that IS present but names zero
// usable skills — malformed content, or no valid "name:hash" line —
// returns (an empty but non-nil set, true): the caller must not treat
// this the same as "no manifest", since that would silently import
// every bundled vendor skill. A line with no ":" is ignored, not an
// error.
func hermesBundledManifest(skillsDir string) (names map[string]bool, present bool) {
	raw, err := os.ReadFile(filepath.Join(skillsDir, ".bundled_manifest"))
	if err != nil {
		return nil, false
	}
	names = map[string]bool{}
	for _, line := range strings.Split(normalizeText(string(raw)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if name = strings.TrimSpace(name); name != "" {
			names[name] = true
		}
	}
	return names, true
}

// scanHermesSkills reads skillsDir, one directory listing, and treats
// each direct subdirectory as one candidate skill folder — the same
// per-entry shape scanAgentsSkills uses (scanSkillEntry), reused here
// so a vault-owned symlink, a dangling link, or a missing SKILL.md
// gets the exact same handling as every other source in this
// package. Before that, it drops three kinds of entry outright,
// never handing them to scanSkillEntry at all:
//
//   - ".bundled_manifest" itself — a file, not a skill folder.
//   - ".archive" — the whole subtree of retired bundled skills
//     (source map §4). It is never descended into.
//   - any entry whose name is a key in this directory's own
//     .bundled_manifest — the bundled vendor library.
//
// A manifest that is PRESENT but names zero usable skills — malformed
// or garbage content — fails CLOSED: this returns no skills at all
// for skillsDir, with a warning, rather than fall back to "no
// manifest" and silently import every bundled vendor skill alongside
// the user's own. Only a genuinely ABSENT manifest imports every
// top-level skill — there is nothing named to exclude.
//
// tool is the CandidateSkill.Tool every returned skill and warning
// carries: "hermes" for the top-level call, "hermes:<profile>" for a
// profile's own skills directory — so a profile skill's provenance
// names the profile it came from. The same fail-closed rule applies
// per profile: a profile with its own present-but-empty manifest
// skips that profile's top-level skills the same way.
func scanHermesSkills(skillsDir, tool, vaultSkillsDir string) ([]CandidateSkill, []Warning) {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil, nil
	}

	bundled, present := hermesBundledManifest(skillsDir)
	if present && len(bundled) == 0 {
		return nil, []Warning{{
			Tool:   tool,
			Path:   filepath.Join(skillsDir, ".bundled_manifest"),
			Reason: "hermes .bundled_manifest is present but unreadable; skipping top-level skills to avoid importing bundled vendor skills. Fix: check the manifest",
		}}
	}

	var skills []CandidateSkill
	var warnings []Warning
	for _, e := range entries {
		name := e.Name()
		if name == ".bundled_manifest" || name == ".archive" || bundled[name] {
			continue
		}
		s, w := scanSkillEntry(filepath.Join(skillsDir, name), tool, vaultSkillsDir)
		warnings = append(warnings, w...)
		if s != nil {
			skills = append(skills, *s)
		}
	}
	return skills, warnings
}

// hermesProfileNames lists the profile names under root's own
// "profiles" directory. Each profile is a near-complete parallel
// Hermes environment with its own skills, memories, and SOUL.md
// (source map §4). A missing "profiles" directory is not a problem:
// most Hermes installs use none. Names come back sorted, so a caller
// gets a stable, repeatable order.
func hermesProfileNames(root string) []string {
	entries, err := os.ReadDir(filepath.Join(root, "profiles"))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// Skills returns the user's own top-level skills at
// ~/.hermes/skills — the bundled vendor library and the .archive
// subtree excluded, per scanHermesSkills — plus, only when
// ctx.ProjectMemory is set, every profile's own
// ~/.hermes/profiles/<name>/skills, namespaced by
// Tool: "hermes:<name>" (name KEBABIFIED — see the doc comment on
// Memory's own use of kebabify(profile) for why). Profiles are a
// per-project/opt-in surface (source map §4): a real install can hold
// meaningful content in a profile and little at the top level, but a
// profile also fragments a single Hermes install into several
// near-complete parallel environments, so this source treats a
// profile scan the same way every other source in this package
// treats per-project content — off by default.
func (Hermes) Skills(ctx ImportCtx) ([]CandidateSkill, []Warning, error) {
	root := hermesRoot(ctx)

	skills, warnings := scanHermesSkills(filepath.Join(root, "skills"), "hermes", ctx.VaultSkillsDir)

	if ctx.ProjectMemory {
		for _, profile := range hermesProfileNames(root) {
			tool := "hermes:" + kebabify(profile)
			pSkills, pWarnings := scanHermesSkills(filepath.Join(root, "profiles", profile, "skills"), tool, ctx.VaultSkillsDir)
			skills = append(skills, pSkills...)
			warnings = append(warnings, pWarnings...)
		}
	}

	return skills, warnings, nil
}

// hermesMemoryFile names one of Hermes's two agent-managed memory
// files and the fact type its chunks import as.
type hermesMemoryFile struct {
	name     string
	factType string
}

// hermesMemoryFiles lists the two files scanHermesMemories reads from
// every memories directory it is given, top-level or per-profile.
// MEMORY.md chunks skew "project"; USER.md chunks skew "user" (source
// map §4's own mapping).
var hermesMemoryFiles = []hermesMemoryFile{
	{"MEMORY.md", "project"},
	{"USER.md", "user"},
}

// scanHermesMemories reads <memoriesDir>/MEMORY.md and
// <memoriesDir>/USER.md — Hermes's own agent-managed memory files
// (source map §4) — splitting each on Hermes's unofficial "§" chunk
// delimiter. tool is the CandidateFact.Tool every fact and warning
// carries: "hermes" for the top-level call, "hermes:<profile>" for a
// profile's own memories directory. namePrefix disambiguates a
// profile's own fact names from the top-level ones, since both share
// the same two file basenames ("" at the top level, "<profile>-" for
// a profile).
//
// SOUL.md is never read here, or anywhere else in this source: this
// function only ever opens a file it names by its own fixed basename,
// MEMORY.md or USER.md. It never lists memoriesDir's own directory
// contents, so a SOUL.md file sitting right next to them is never
// even seen. SOUL.md is Hermes's persona/identity slot, never memory
// (source map §4) — it must never import, under any name.
//
// Before either file is read, its own lock sidecar (<file>.lock) is
// checked first. Hermes itself writes and locks these files while it
// runs (source map §4); the lock's presence means a read right now
// risks a torn read. That ONE file is skipped, with a warning telling
// the user to try again; the OTHER file, with no lock, still reads
// normally — a lock on one file never stops the whole memories scan.
func scanHermesMemories(memoriesDir, tool, namePrefix string) ([]CandidateFact, []Warning) {
	var facts []CandidateFact
	var warnings []Warning

	for _, mf := range hermesMemoryFiles {
		path := filepath.Join(memoriesDir, mf.name)

		if _, err := os.Stat(path + ".lock"); err == nil {
			warnings = append(warnings, Warning{
				Tool:   tool,
				Path:   path,
				Reason: "hermes is writing this file; try the import again.",
			})
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

		text := strings.TrimSpace(normalizeText(string(raw)))
		if text == "" {
			continue
		}

		base := namePrefix + kebabify(strings.TrimSuffix(mf.name, ".md"))
		chunks := splitHermesChunks(text)
		for i, chunk := range chunks {
			name := base
			if len(chunks) > 1 {
				name = fmt.Sprintf("%s-%d", base, i+1)
			}
			facts = append(facts, CandidateFact{
				Name:        name,
				Description: firstLine(chunk),
				Type:        mf.factType,
				Body:        chunk,
				Tool:        tool,
				ModTime:     info.ModTime(),
			})
		}
	}

	return facts, warnings
}

// splitHermesChunks splits text on Hermes's unofficial "§" chunk
// delimiter — observed live on a real install, not documented
// anywhere in Hermes's own docs (source map §4). A file with no "§"
// at all falls back to one chunk: the whole file. An empty chunk,
// from two delimiters in a row or one at either end of the file, is
// dropped.
func splitHermesChunks(text string) []string {
	if !strings.Contains(text, "§") {
		return []string{text}
	}
	var chunks []string
	for _, part := range strings.Split(text, "§") {
		if part = strings.TrimSpace(part); part != "" {
			chunks = append(chunks, part)
		}
	}
	if len(chunks) == 0 {
		return []string{text}
	}
	return chunks
}

// Memory returns candidate facts from ~/.hermes/memories/{MEMORY,USER}.md
// always, plus — only when ctx.ProjectMemory is set — the same two
// files under every profile's own
// ~/.hermes/profiles/<name>/memories, namespaced by
// Tool: "hermes:<name>" and named with a "<name>-" prefix, name
// KEBABIFIED first (lowercased, "_"/space and every other invalid
// character collapsed to "-"). A profile's own directory name is
// whatever the user chose — "Brain_2", say — which is not itself a
// valid vault item name; without kebabifying it first, the item name
// "by: import:hermes:<name>" would carry the invalid form too, and
// every fact under that profile would fail vault.ValidName and get
// skipped as a Warning rather than imported. This is the same
// per-project/opt-in rule Skills applies to a profile's own skills
// (see Skills's own doc comment): a profile is a fragment of one
// Hermes install, not the tool's global memory.
//
// When ctx.ProjectMemory is off and one or more profiles exist
// anyway, this warns how many were skipped — the same
// "N per-project memory sources skipped; pass --project-memory to
// include them" shape Gemini's and Droid's own Memory methods already
// report, so the report reads the same way across every source that
// has an off-by-default, per-project or per-profile scope.
func (Hermes) Memory(ctx ImportCtx) ([]CandidateFact, []Warning, error) {
	root := hermesRoot(ctx)

	facts, warnings := scanHermesMemories(filepath.Join(root, "memories"), "hermes", "")

	if ctx.ProjectMemory {
		for _, profile := range hermesProfileNames(root) {
			kebabProfile := kebabify(profile)
			tool := "hermes:" + kebabProfile
			pFacts, pWarnings := scanHermesMemories(filepath.Join(root, "profiles", profile, "memories"), tool, kebabProfile+"-")
			facts = append(facts, pFacts...)
			warnings = append(warnings, pWarnings...)
		}
	} else if n := len(hermesProfileNames(root)); n > 0 {
		warnings = append(warnings, Warning{
			Tool:   "hermes",
			Reason: fmt.Sprintf("%d per-project memory sources skipped; pass --project-memory to include them.", n),
		})
	}

	return facts, warnings, nil
}
