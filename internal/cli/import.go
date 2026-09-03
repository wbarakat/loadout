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

const importUsage = "usage: loadout import [SOURCE...] [--skills] [--memory] [--project DIR] [--project-memory] [--dry-run] [--verbose]\n" +
	"SOURCE is one of: claude-code, codex, cursor, hermes, pi, gemini, droid."

// importRegistry lists every import Source loadout knows, in the
// order "loadout import" tries them and the report prints them. No
// SOURCE on the command line runs every registered source and lets
// RunImport's own Detect skip whichever one is not actually installed.
var importRegistry = []importer.Source{
	importer.ClaudeCode{},
	importer.Codex{},
	importer.Cursor{},
	importer.Hermes{},
	importer.Pi{},
	importer.Gemini{},
	importer.Droid{},
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
// [--skills] [--memory] [--project DIR] [--project-memory]
// [--dry-run]".
type importArgs struct {
	sources        []string
	skills, memory bool
	dryRun         bool
	// verbose prints the full per-item list and every warning verbatim,
	// instead of the default concise summary and grouped warning digest.
	verbose bool
	project string
	// projectMemory sets importer.ImportCtx.ProjectMemory — see FIX 4's
	// doc comment there. The default (false) scopes memory to GLOBAL
	// instruction files only.
	projectMemory bool
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
		case "--project-memory":
			a.projectMemory = true
		case "--dry-run":
			a.dryRun = true
		case "--verbose", "-v":
			a.verbose = true
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
		ProjectMemory:  parsed.projectMemory,
	}
	opt := importer.Options{Skills: skills, Memory: memory, DryRun: parsed.dryRun}

	release, err := vault.Lock(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	defer release()

	// A live spinner on stderr while the scan runs — but never in JSON
	// mode (its output must stay a clean machine stream) and never when
	// stderr is not a terminal (startSpinner returns a no-op there). The
	// spinner names the tool being scanned as RunImport reaches it.
	var sp *spinner
	if m != modeJSON {
		sp = startSpinner(errOut, "importing your skills and memory…")
		opt.Progress = func(tool string) { sp.setLabel("scanning " + tool + "…") }
	}

	result, err := importer.RunImport(v, sources, ctx, opt)
	if err != nil {
		if sp != nil {
			sp.stop()
		}
		fmt.Fprintln(errOut, err)
		return 1
	}

	// import never pushes: it only ever writes drafts into the local
	// vault. A snapshot records the local history entry; reaching a
	// remote is a separate, explicit "loadout sync --remote" the
	// report's own next-step line names.
	if !parsed.dryRun {
		if sp != nil {
			sp.setLabel("saving…")
		}
		if err := vault.Snapshot(v, "import"); err != nil {
			if sp != nil {
				sp.stop()
			}
			fmt.Fprintln(errOut, err)
			return 1
		}
	}
	if sp != nil {
		sp.stop()
	}

	if m == modeJSON {
		printJSON(out, toImportResultJSON(result, parsed.dryRun))
		return 0
	}
	renderImportReport(out, result, parsed.dryRun, true, parsed.verbose)
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

// renderImportReport writes a human report of result to out.
//
// By default it is a concise summary: one headline naming the count of
// drafts by kind and by tool, the deduplicated count, and a grouped
// digest of warnings (one line per category, most frequent first)
// rather than every warning verbatim. The per-item list is shown only
// for a dry run — where the list IS the preview a caller asked for — or
// under verbose. verbose also expands the warning digest back to the
// full, per-item list. This keeps a real import over dozens of tools to
// a few readable lines instead of a wall of every item and every path.
//
// showNextSteps controls only the trailing "loadout review" /
// "loadout sync --remote" next-step line on a real (non-dryRun) run:
// "loadout import" itself passes true, since that line is its own
// report's closing summary. "loadout init"'s wizard passes false for
// the same real result, since runInit already prints that same line
// once as its own closing summary — printing it here too would
// duplicate it. dryRun's own "nothing was written" line is unaffected
// either way.
func renderImportReport(out io.Writer, result importer.ImportResult, dryRun, showNextSteps, verbose bool) {
	// Headline.
	if len(result.Imported) == 0 {
		if dryRun {
			fmt.Fprintln(out, "nothing new to import (dry run — nothing written).")
		} else {
			fmt.Fprintln(out, "nothing new to import.")
		}
	} else {
		verb := "imported"
		suffix := ""
		if dryRun {
			verb = "would import"
			suffix = " (dry run — nothing written)"
		}
		fmt.Fprintf(out, "%s %s%s — %s\n", verb,
			countNoun(len(result.Imported), "draft", "drafts"), suffix,
			importCountsLine(result.Imported))
	}

	// The full per-item list: a dry run is a preview, so it always lists
	// what it would write; a real run lists items only under verbose.
	if len(result.Imported) > 0 && (dryRun || verbose) {
		for _, ref := range result.Imported {
			fmt.Fprintf(out, "  %s/%s (%s)\n", ref.Kind, ref.Name, ref.Tool)
		}
	}

	if n := len(result.Deduped); n > 0 {
		fmt.Fprintln(out, countNoun(n, "duplicate skipped", "duplicates skipped"))
	}

	// Warnings and skips: a grouped digest by default, the full per-item
	// list under verbose. Both a Skipped entry (an item declined at
	// write time) and a Warning (a problem a source hit while reading)
	// are things worth the reader's attention, so the concise digest
	// counts them together.
	if verbose {
		renderWarningsVerbose(out, result.Skipped, result.Warnings)
	} else {
		notes := make([]importer.Warning, 0, len(result.Skipped)+len(result.Warnings))
		notes = append(notes, result.Skipped...)
		notes = append(notes, result.Warnings...)
		renderWarningDigest(out, notes)
	}

	if dryRun {
		fmt.Fprintln(out, "nothing was written. Run loadout import (without --dry-run) to import for real.")
		return
	}
	if showNextSteps {
		fmt.Fprintln(out, importNextSteps)
	}
}

// renderWarningsVerbose prints every skip and every warning verbatim,
// each with its full path and reason — the detail --verbose asks for,
// and the exact shape the report used before the concise default.
func renderWarningsVerbose(out io.Writer, skipped, warnings []importer.Warning) {
	if len(skipped) > 0 {
		fmt.Fprintln(out, "skipped:")
		for _, w := range skipped {
			fmt.Fprintln(out, "  "+formatImportWarning(w))
		}
	}
	if len(warnings) > 0 {
		fmt.Fprintln(out, "warnings:")
		for _, w := range warnings {
			fmt.Fprintln(out, "  "+formatImportWarning(w))
		}
	}
}

// renderWarningDigest collapses a long list of warnings into a short
// digest: one "<count>  <label>" line per category, most frequent
// first, so 60 per-item warnings read as a handful of lines. A reason
// the digest does not recognize keeps its own text as the label, still
// deduped and counted, so nothing important is silently hidden. It
// prints nothing at all when there are no warnings.
func renderWarningDigest(out io.Writer, notes []importer.Warning) {
	if len(notes) == 0 {
		return
	}
	type bucket struct {
		label string
		count int
		order int // first-seen index, for a stable tie-break
	}
	buckets := map[string]*bucket{}
	order := 0
	for _, w := range notes {
		label := warningCategory(w.Reason)
		if label == "" {
			// Unrecognized reason: keep it verbatim so it is never hidden.
			label = w.Reason
		}
		b, ok := buckets[label]
		if !ok {
			b = &bucket{label: label, order: order}
			order++
			buckets[label] = b
		}
		b.count++
	}
	list := make([]*bucket, 0, len(buckets))
	for _, b := range buckets {
		list = append(list, b)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].count != list[j].count {
			return list[i].count > list[j].count
		}
		return list[i].order < list[j].order
	})

	fmt.Fprintf(out, "%s:\n", countNoun(len(notes), "warning", "warnings"))
	for _, b := range list {
		fmt.Fprintf(out, "  %3d  %s\n", b.count, b.label)
	}
	fmt.Fprintln(out, "run loadout import --verbose to see each one.")
}

// warningCategory maps a warning's reason to a short, human category
// label for the digest, or "" when the reason has no known category and
// should keep its own text. It matches on stable fragments of the
// reasons this package's own sources emit, so many per-item warnings
// (one per skill folder, one per oversized file) collapse to one line.
func warningCategory(reason string) string {
	switch {
	case strings.Contains(reason, "is too large"):
		return "skills too large to import"
	case strings.Contains(reason, "per-file limit"):
		return "oversized support files dropped"
	case strings.Contains(reason, "no readable SKILL.md"):
		return "folders with no SKILL.md"
	case strings.Contains(reason, "not a skill folder"):
		return "entries that are not skill folders"
	case strings.Contains(reason, "no valid frontmatter"):
		return "skills with no valid frontmatter"
	case strings.Contains(reason, "points outside the skill folder"):
		return "support files pointing outside the folder"
	case strings.Contains(reason, "dangling"):
		return "dangling skill links"
	case strings.Contains(reason, "per-project memory sources skipped"):
		return "per-project memory skipped — use --project-memory"
	case strings.Contains(reason, "try the import again"):
		return "files the tool was writing — retry later"
	case strings.Contains(reason, "User Rules"):
		return `Cursor "User Rules" — copy from Cursor Settings → Rules`
	default:
		return ""
	}
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
