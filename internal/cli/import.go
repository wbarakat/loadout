package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"loadout.dev/loadout/internal/importer"
	"loadout.dev/loadout/internal/vault"
)

const importUsage = "usage: loadout import [SOURCE...] [--skills] [--memory] [--project DIR] [--dry-run]"

// importRegistry lists every import Source loadout knows, in the
// order "loadout import" tries them. v1 holds claude-code and codex;
// a later phase adds more sources here, and nothing else in this file
// needs to change.
var importRegistry = []importer.Source{
	importer.ClaudeCode{},
	importer.Codex{},
}

// importSourceNames names every source in importRegistry, in
// registry order — the list an "unknown source" error names.
func importSourceNames() []string {
	names := make([]string, 0, len(importRegistry))
	for _, s := range importRegistry {
		names = append(names, s.Name())
	}
	return names
}

// selectImportSources resolves the SOURCE names typed on the command
// line into the matching importRegistry entries, in the order given.
// An empty names list selects the whole registry: "loadout import"
// with no SOURCE runs every known source and lets RunImport's own
// Detect skip whichever one is not actually installed on this
// machine. An unknown name is an error that names every valid source,
// so a typo is easy to fix.
func selectImportSources(names []string) ([]importer.Source, error) {
	if len(names) == 0 {
		return importRegistry, nil
	}
	byName := make(map[string]importer.Source, len(importRegistry))
	for _, s := range importRegistry {
		byName[s.Name()] = s
	}
	sources := make([]importer.Source, 0, len(names))
	for _, n := range names {
		s, ok := byName[n]
		if !ok {
			return nil, fmt.Errorf("%s: not a known import source. Fix: use one of %s.", n, strings.Join(importSourceNames(), ", "))
		}
		sources = append(sources, s)
	}
	return sources, nil
}

// importArgs is the parsed shape of "loadout import [SOURCE...]
// [--skills] [--memory] [--project DIR] [--dry-run]".
type importArgs struct {
	sources        []string
	skills, memory bool
	dryRun         bool
	project        string
}

// parseImportArgs reads importArgs out of args. ok is false when args
// does not match the expected shape (a flag with no value, or an
// unknown flag), so the caller can print usage. A token that does not
// start with "--" is collected as a SOURCE name, in the order given.
func parseImportArgs(args []string) (importArgs, bool) {
	var a importArgs
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--skills":
			a.skills = true
		case "--memory":
			a.memory = true
		case "--dry-run":
			a.dryRun = true
		case "--project":
			if i+1 >= len(args) || args[i+1] == "" {
				return importArgs{}, false
			}
			a.project = args[i+1]
			i++
		default:
			if strings.HasPrefix(args[i], "--") {
				return importArgs{}, false
			}
			a.sources = append(a.sources, args[i])
		}
	}
	return a, true
}

// cmdImport runs "loadout import": it pulls skills and memory from
// every selected, installed agent tool into the vault as draft items,
// under the vault lock, then snapshots the change. --dry-run runs the
// exact same scan and reports the exact same preview, but writes
// nothing — see RunImport's own DryRun handling.
func cmdImport(out, errOut io.Writer, args []string, m mode) int {
	parsed, ok := parseImportArgs(args)
	if !ok {
		fmt.Fprintln(errOut, importUsage)
		return 2
	}
	sources, err := selectImportSources(parsed.sources)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}

	// Neither --skills nor --memory means both; either one alone
	// narrows to just that kind; both together also means both.
	skills, memory := parsed.skills, parsed.memory
	if !skills && !memory {
		skills, memory = true, true
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(errOut, "could not find the home directory: %v. Fix: set $HOME.\n", err)
		return 1
	}
	projectDir := parsed.project
	if projectDir == "" {
		projectDir, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(errOut, "could not find the current directory: %v. Fix: run loadout import from a real directory, or pass --project DIR.\n", err)
			return 1
		}
	}

	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	printWarnings(errOut, v)

	ctx := importer.ImportCtx{
		Home:           home,
		VaultRoot:      v.Root,
		VaultSkillsDir: v.SkillsDir(),
		ProjectDir:     projectDir,
	}
	opt := importer.Options{Skills: skills, Memory: memory, DryRun: parsed.dryRun}

	release, err := vault.Lock(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	defer release()

	result, err := importer.RunImport(v, sources, ctx, opt)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}

	// import never pushes: it only ever writes drafts into the local
	// vault. A snapshot records the local history entry; reaching a
	// remote is a separate, explicit "loadout sync --remote" the
	// report's own next-step line names.
	if !parsed.dryRun {
		if err := vault.Snapshot(v, "import"); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
	}

	if m == modeJSON {
		printJSON(out, toImportResultJSON(result, parsed.dryRun))
		return 0
	}
	renderImportReport(out, result, parsed.dryRun)
	return 0
}

// importItemRefJSON is one entry in the JSON shape of an import
// result's imported or deduped list.
type importItemRefJSON struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Tool string `json:"tool"`
}

// importWarningJSON is one entry in the JSON shape of an import
// result's skipped or warnings list.
type importWarningJSON struct {
	Tool   string `json:"tool"`
	Path   string `json:"path,omitempty"`
	Reason string `json:"reason"`
}

// importResultJSON is the JSON shape of "loadout import".
type importResultJSON struct {
	DryRun   bool                `json:"dry_run,omitempty"`
	Imported []importItemRefJSON `json:"imported"`
	Deduped  []importItemRefJSON `json:"deduped"`
	Skipped  []importWarningJSON `json:"skipped"`
	Warnings []importWarningJSON `json:"warnings"`
}

// toImportItemRefsJSON turns an importer.ItemRef slice into its JSON
// shape, always as a non-nil slice, so an empty list marshals as "[]"
// rather than "null".
func toImportItemRefsJSON(refs []importer.ItemRef) []importItemRefJSON {
	out := make([]importItemRefJSON, 0, len(refs))
	for _, r := range refs {
		out = append(out, importItemRefJSON{Kind: r.Kind, Name: r.Name, Tool: r.Tool})
	}
	return out
}

// toImportWarningsJSON turns an importer.Warning slice into its JSON
// shape, always as a non-nil slice.
func toImportWarningsJSON(warnings []importer.Warning) []importWarningJSON {
	out := make([]importWarningJSON, 0, len(warnings))
	for _, w := range warnings {
		out = append(out, importWarningJSON{Tool: w.Tool, Path: w.Path, Reason: w.Reason})
	}
	return out
}

// toImportResultJSON builds the JSON shape of an import result.
func toImportResultJSON(result importer.ImportResult, dryRun bool) importResultJSON {
	return importResultJSON{
		DryRun:   dryRun,
		Imported: toImportItemRefsJSON(result.Imported),
		Deduped:  toImportItemRefsJSON(result.Deduped),
		Skipped:  toImportWarningsJSON(result.Skipped),
		Warnings: toImportWarningsJSON(result.Warnings),
	}
}

// importNextSteps is what "loadout import" prints last, on a real
// run: the two steps still left before an imported item takes effect
// anywhere — a human review, then a push.
const importNextSteps = "these items landed as drafts. Review them: loadout review, or the dashboard. Then run loadout sync --remote to push them."

// renderImportReport writes a concise, human report of result to out:
// the items imported (or, under dryRun, previewed), the counts by
// kind and by tool, the deduped count, every skipped item with its
// reason, and every warning a source hit while reading its native
// store. The warnings section always renders, even when, as for
// claude-code and codex today, the list comes back empty — a later
// source's caveat must not need a second code path to appear.
func renderImportReport(out io.Writer, result importer.ImportResult, dryRun bool) {
	verb := "imported"
	if dryRun {
		verb = "would import (dry run — nothing written)"
	}
	if len(result.Imported) == 0 {
		fmt.Fprintf(out, "%s: nothing new to import.\n", verb)
	} else {
		fmt.Fprintf(out, "%s:\n", verb)
		for _, ref := range result.Imported {
			fmt.Fprintf(out, "  %s/%s (%s)\n", ref.Kind, ref.Name, ref.Tool)
		}
		fmt.Fprintln(out, importCountsLine(result.Imported))
	}
	fmt.Fprintln(out, countNoun(len(result.Deduped), "item deduped", "items deduped"))
	if len(result.Skipped) > 0 {
		fmt.Fprintln(out, "skipped:")
		for _, w := range result.Skipped {
			fmt.Fprintln(out, "  "+formatImportWarning(w))
		}
	}
	if len(result.Warnings) > 0 {
		fmt.Fprintln(out, "warnings:")
		for _, w := range result.Warnings {
			fmt.Fprintln(out, "  "+formatImportWarning(w))
		}
	}
	if dryRun {
		fmt.Fprintln(out, "nothing was written. Run loadout import (without --dry-run) to import for real.")
		return
	}
	fmt.Fprintln(out, importNextSteps)
}

// formatImportWarning renders one Warning as "<tool>: <path> —
// <reason>", dropping the path clause when a warning names no path (a
// whole-source error, for example).
func formatImportWarning(w importer.Warning) string {
	if w.Path == "" {
		return fmt.Sprintf("%s: %s", w.Tool, w.Reason)
	}
	return fmt.Sprintf("%s: %s — %s", w.Tool, w.Path, w.Reason)
}

// countNoun renders n with singular for exactly 1, plural otherwise,
// for example countNoun(1, "skill", "skills") == "1 skill".
func countNoun(n int, singular, plural string) string {
	noun := plural
	if n == 1 {
		noun = singular
	}
	return fmt.Sprintf("%d %s", n, noun)
}

// importCountsLine summarizes imported by kind (skill vs memory) and
// by tool, for example "2 skills, 1 memory fact (claude-code: 2,
// codex: 1)".
func importCountsLine(imported []importer.ItemRef) string {
	var skillCount, memoryCount int
	byTool := map[string]int{}
	for _, ref := range imported {
		if ref.Kind == "skill" {
			skillCount++
		} else {
			memoryCount++
		}
		byTool[ref.Tool]++
	}
	line := countNoun(skillCount, "skill", "skills") + ", " + countNoun(memoryCount, "memory fact", "memory facts")
	if len(byTool) == 0 {
		return line
	}
	tools := make([]string, 0, len(byTool))
	for t := range byTool {
		tools = append(tools, t)
	}
	sort.Strings(tools)
	parts := make([]string, 0, len(tools))
	for _, t := range tools {
		parts = append(parts, fmt.Sprintf("%s: %d", t, byTool[t]))
	}
	return line + " (" + strings.Join(parts, ", ") + ")"
}
