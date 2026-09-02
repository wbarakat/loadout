package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"loadout.dev/loadout/internal/importer"
	"loadout.dev/loadout/internal/remote"
	"loadout.dev/loadout/internal/vault"
)

// initResult is the JSON shape of "loadout init --json". JSON mode
// stays a quick, machine-facing "give me the vault path" contract: it
// creates or keeps the vault (never destroying an existing one) but
// runs none of the wizard's own prompts or steps — those are for a
// human at a terminal. The headless, scriptable path for an agent or
// CI is "loadout init --yes" (see initArgs, headlessInitOptions), a
// separate text-mode path with its own flags, not this JSON mode.
type initResult struct {
	Vault string `json:"vault"`
}

// initOptions captures every decision "loadout init" needs before it
// acts: which tools to enable an adapter for, whether to import, and
// an optional remote to connect. The interactive wizard (this file)
// fills these fields from prompts; the headless "--yes" path
// (headlessInitOptions) fills them from flags instead. runInit treats
// every field as already decided and asks nothing more.
type initOptions struct {
	// Tools names every tool to enable an adapter for. nil means "let
	// runInit default to every tool detectTools finds Present" — a
	// headless caller that got no --tools flag can leave this nil
	// rather than recompute the detected set itself. An empty,
	// non-nil slice means "enable none."
	Tools []string
	// DoImport runs an import's own dry-run-then-real sequence when
	// true; false skips import entirely.
	DoImport bool
	// ProjectMemory sets importer.ImportCtx.ProjectMemory for the
	// import step: false (the default, what the interactive wizard
	// always leaves it at) scopes memory to GLOBAL instruction files
	// only; true also pulls per-project or per-profile memory. Only
	// the headless "--project-memory" flag ever sets this true.
	ProjectMemory bool
	// RemoteURL, when non-empty, connects to a loadoutd remote after
	// import. Empty means local-only: no remote step at all.
	RemoteURL string
	// TokenPath is the path to a file holding the remote's token,
	// read only when RemoteURL is non-empty. The token itself never
	// lives in initOptions, and never appears in output or an error.
	TokenPath string
}

// initNextSteps is the wizard's closing summary line: the two
// commands still left regardless of what ran — a human review, then
// a push. It always prints, even when nothing was imported, so a
// human never has to guess what happens next.
const initNextSteps = "next: loadout review (or the dashboard) to keep the drafts you want. Then run loadout sync --remote to push them."

// initLookPath resolves a binary's PATH presence for the init
// wizard's own tool detection (detectTools). It is a package
// variable, not a bare nil handed to detectTools, so a test can
// substitute a stub: a real contributor's own machine can easily have
// several of loadout's supported tools (claude, codex, pi, hermes,
// ...) genuinely on PATH, and detectTools' PATH-fallback signal would
// otherwise leak that real, machine-specific state into a test's
// detected set. nil (the default) means "use detectTools' own
// exec.LookPath default" — real behavior for a real invocation.
var initLookPath func(string) (string, error)

// initUsage is "loadout init"'s own usage line, printed when its
// flags do not parse — an unknown flag, or a flag missing the value
// it needs.
const initUsage = "usage: loadout init [--yes] [--tools a,b,...] [--no-import] [--remote URL --token-file PATH] [--project-memory]"

// initArgs is the parsed shape of "loadout init"'s headless flags:
// --yes [--tools a,b,...] [--no-import] [--remote URL --token-file
// PATH] [--project-memory]. With no --yes, init ignores every other
// flag here and runs the interactive wizard exactly as before —
// --yes is the switch to the headless, no-prompts path.
type initArgs struct {
	yes           bool
	tools         []string
	noImport      bool
	remoteURL     string
	tokenFile     string
	projectMemory bool
}

// parseInitArgs reads initArgs out of args. ok is false when args does
// not match the expected shape (an unknown flag, a flag with no
// value, or a bare positional argument — init takes none), so the
// caller can print initUsage.
func parseInitArgs(args []string) (initArgs, bool) {
	var a initArgs
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--yes":
			a.yes = true
		case "--no-import":
			a.noImport = true
		case "--project-memory":
			a.projectMemory = true
		case "--tools":
			if i+1 >= len(args) || args[i+1] == "" {
				return initArgs{}, false
			}
			for _, name := range strings.Split(args[i+1], ",") {
				if name = strings.TrimSpace(name); name != "" {
					a.tools = append(a.tools, name)
				}
			}
			if len(a.tools) == 0 {
				return initArgs{}, false
			}
			i++
		case "--remote":
			if i+1 >= len(args) || args[i+1] == "" {
				return initArgs{}, false
			}
			a.remoteURL = args[i+1]
			i++
		case "--token-file":
			if i+1 >= len(args) || args[i+1] == "" {
				return initArgs{}, false
			}
			a.tokenFile = args[i+1]
			i++
		default:
			return initArgs{}, false
		}
	}
	return a, true
}

// headlessInitOptions builds an initOptions from a's flags and
// detected — the same result promptInitOptions builds from a person's
// answers, except every decision comes from a flag instead of a
// prompt. This is what "loadout init --yes" calls instead of
// promptInitOptions, so the headless path runs the exact same runInit
// with no stdin read at all.
//
// a.tools, when set, must name only tools detected reports Present:
// an agent or CI script naming a tool this machine does not actually
// have is almost certainly a mistake, and the error here lists every
// valid name so it is easy to fix. a.remoteURL and a.tokenFile must be
// given together or not at all — connecting a remote with no token
// file (or the reverse) would leave a partial, useless remote
// configuration.
func headlessInitOptions(a initArgs, detected []DetectedTool) (initOptions, error) {
	var present []string
	for _, d := range detected {
		if d.Present {
			present = append(present, d.Name)
		}
	}

	var opts initOptions
	if a.tools != nil {
		byName := make(map[string]bool, len(present))
		for _, name := range present {
			byName[name] = true
		}
		for _, name := range a.tools {
			if byName[name] {
				continue
			}
			if len(present) == 0 {
				return initOptions{}, fmt.Errorf("%s: not a detected tool. Fix: no supported agent tools were detected on this machine.", name)
			}
			return initOptions{}, fmt.Errorf("%s: not a detected tool. Fix: use one of %s.", name, strings.Join(present, ", "))
		}
		opts.Tools = a.tools
	}
	// a.tools == nil leaves opts.Tools nil: runInit defaults to every
	// tool detectTools reports Present, the same default a "yes"
	// answer to the wizard's own "enable adapters?" prompt gives.

	opts.DoImport = !a.noImport
	opts.ProjectMemory = a.projectMemory

	switch {
	case a.remoteURL != "" && a.tokenFile == "":
		return initOptions{}, fmt.Errorf("--remote needs --token-file too. Fix: pass --token-file PATH holding the loadoutd token.")
	case a.remoteURL == "" && a.tokenFile != "":
		return initOptions{}, fmt.Errorf("--token-file needs --remote too. Fix: pass --remote URL to connect a loadoutd remote.")
	}
	opts.RemoteURL = a.remoteURL
	opts.TokenPath = a.tokenFile

	return opts, nil
}

// cmdInit runs "loadout init". JSON mode keeps the original, minimal
// contract (see initResult): create or keep the vault, print its
// path, nothing else — a human prompt has no place in a machine
// output format.
//
// Text mode has two paths. "--yes" is headless: it builds an
// initOptions straight from flags (headlessInitOptions) and calls
// runInit directly, reading no stdin at all — deterministic, for an
// agent or CI to run unattended. With no "--yes", init drives the
// interactive wizard over os.Stdin/out instead: every prompt it asks
// is safe to run with no one actually there to answer, since
// promptYesNo (below) never takes a consequential default action —
// enabling an adapter, importing, connecting a remote — on a question
// stdin had no real answer for at all (a closed pipe, /dev/null, a
// script's own empty stdin); it only ever falls back to a question's
// stated default on an explicit empty line, the "just hit enter" a
// real person at a real prompt can give.
func cmdInit(out, errOut io.Writer, args []string, m mode) int {
	parsed, ok := parseInitArgs(args)
	if !ok {
		fmt.Fprintln(errOut, initUsage)
		return 2
	}

	if m == modeJSON {
		v, _, err := ensureVault(vault.DefaultRoot())
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		printJSON(out, initResult{Vault: v.Root})
		return 0
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(errOut, "could not find the home directory: %v. Fix: set $HOME.\n", err)
		return 1
	}
	detected := detectTools(home, initLookPath)

	if parsed.yes {
		opts, err := headlessInitOptions(parsed, detected)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
		if err := runInit(opts, nil, out); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		return 0
	}

	opts := promptInitOptions(os.Stdin, out, detected)
	if err := runInit(opts, os.Stdin, out); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}

// ensureVault opens the vault at root, creating it first when no
// manifest exists there yet. created reports which happened, so a
// caller can print the right message — this is "loadout init"'s
// re-run safety: an existing vault is always kept, never destroyed.
func ensureVault(root string) (v *vault.Vault, created bool, err error) {
	if _, statErr := os.Stat(filepath.Join(root, "loadout.toml")); statErr == nil {
		v, err = vault.Open(root)
		return v, false, err
	}
	v, err = vault.Init(root)
	return v, true, err
}

// promptInitOptions asks the wizard's questions over in, printing
// each to out, and returns the answers as an initOptions. A question
// answered with a plain "hit enter" (an explicit empty line) takes
// its stated default, exactly as a real person at a real prompt would
// expect; in has no more input to give at all (a closed pipe,
// /dev/null, the empty stdin every non-interactive script and test
// leaves in place) is a different case entirely — see promptYesNo.
func promptInitOptions(in io.Reader, out io.Writer, detected []DetectedTool) initOptions {
	r := bufio.NewReader(in)

	var present []string
	for _, d := range detected {
		if d.Present {
			present = append(present, d.Name)
		}
	}
	if len(present) == 0 {
		fmt.Fprintln(out, "Found: no supported agent tools on this machine.")
	} else {
		fmt.Fprintf(out, "Found: %s\n", strings.Join(present, ", "))
	}

	var opts initOptions
	if len(present) > 0 {
		fmt.Fprint(out, "Enable adapters for these tools? [Y/n] ")
		if promptYesNo(r, true) {
			opts.Tools = present
		} else {
			opts.Tools = []string{}
		}
	} else {
		opts.Tools = []string{}
	}

	fmt.Fprint(out, "Import your existing skills and memory now? [Y/n] ")
	opts.DoImport = promptYesNo(r, true)

	fmt.Fprint(out, "Connect a loadoutd remote? [y/N] ")
	if promptYesNo(r, false) {
		fmt.Fprint(out, "loadoutd URL: ")
		opts.RemoteURL = promptLine(r)
		fmt.Fprint(out, "Path to a file containing the loadoutd token: ")
		opts.TokenPath = promptLine(r)
	}

	return opts
}

// readLine reads one line from r, trimmed of surrounding whitespace.
// ok is false only when r has no more input at all to give (a true
// EOF with nothing read) — the signal every prompt treats as "take
// the default." A final line with content but no trailing newline
// still counts as read: bufio.Reader.ReadString returns io.EOF
// alongside that content, not in place of it.
func readLine(r *bufio.Reader) (line string, ok bool) {
	raw, err := r.ReadString('\n')
	raw = strings.TrimSpace(raw)
	if err != nil && raw == "" {
		return "", false
	}
	return raw, true
}

// promptYesNo reads one line from r and interprets it as a yes/no
// answer. An explicit empty line — a real "hit enter" from someone
// actually being asked — falls back to def, the question's own
// stated default. r having no more input to give at all (a true EOF)
// is a different case: nobody is there to answer, so this falls back
// to false regardless of def, never taking a consequential default
// action — enabling an adapter, importing, connecting a remote — that
// no one actually agreed to. Anything else not starting with "y" or
// "n" (case-insensitively) falls back to def too, so a stray typo
// never gets misread as the opposite answer.
func promptYesNo(r *bufio.Reader, def bool) bool {
	line, ok := readLine(r)
	if !ok {
		return false
	}
	if line == "" {
		return def
	}
	switch strings.ToLower(line)[:1] {
	case "y":
		return true
	case "n":
		return false
	default:
		return def
	}
}

// promptLine reads one line from r, or "" when r has no more input.
func promptLine(r *bufio.Reader) string {
	line, _ := readLine(r)
	return line
}

// runInit performs the actual first-run work "loadout init"'s wizard
// and its headless "--yes" path both drive, once every decision opts
// needs has already been made: create the vault (or keep an existing
// one), enable and configure adapters for opts.Tools, run an import
// when opts.DoImport is set, and connect a remote when opts.RemoteURL
// is set. It prints one human-readable line per step to out.
//
// in is accepted, not read: every decision opts needs is already
// final by the time runInit runs — the interactive wizard above reads
// its own prompts before ever calling runInit, and the headless path
// has none to give (it passes nil). Keeping in in the signature lets
// both entry points share one call shape.
func runInit(opts initOptions, in io.Reader, out io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("could not find the home directory: %v. Fix: set $HOME.", err)
	}
	detected := detectTools(home, initLookPath)

	v, created, err := ensureVault(vault.DefaultRoot())
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintf(out, "created the vault at %s\n", v.Root)
	} else {
		fmt.Fprintf(out, "vault already exists at %s\n", v.Root)
	}

	if err := enableAdapters(v, opts.Tools, detected, out); err != nil {
		return err
	}

	if opts.DoImport {
		if err := runInitImport(v, home, opts.ProjectMemory, out); err != nil {
			return err
		}
	}

	if opts.RemoteURL != "" {
		if err := connectRemote(v, opts.RemoteURL, opts.TokenPath, out); err != nil {
			return err
		}
	}

	fmt.Fprintln(out, initNextSteps)
	return nil
}

// enableAdapters writes loadout.toml enabling an adapter for every
// name in tools, using detected's own SkillsDir/MemoryFile for that
// tool — the same defaults the adapters already use. tools == nil
// defaults to every tool name detected reports Present for; an
// explicit empty slice enables nothing and this is a no-op. A name
// with no matching key in the manifest's Adapters map (droid has no
// projection adapter anywhere in the codebase: it is an import-only
// source, not a tool loadout ever projects skills or memory into) is
// silently skipped: there is no adapter target to configure for it.
func enableAdapters(v *vault.Vault, tools []string, detected []DetectedTool, out io.Writer) error {
	if tools == nil {
		for _, d := range detected {
			if d.Present {
				tools = append(tools, d.Name)
			}
		}
	}

	byName := make(map[string]DetectedTool, len(detected))
	for _, d := range detected {
		byName[d.Name] = d
	}

	var enabled []string
	for _, name := range tools {
		cfg, ok := v.Manifest.Adapters[name]
		if !ok {
			continue
		}
		if d, ok := byName[name]; ok {
			cfg.SkillsDir = d.SkillsDir
			cfg.MemoryFile = d.MemoryFile
		}
		cfg.Enabled = true
		v.Manifest.Adapters[name] = cfg
		enabled = append(enabled, name)
	}

	if len(enabled) == 0 {
		fmt.Fprintln(out, "no adapters enabled.")
		return nil
	}
	sort.Strings(enabled)
	if err := vault.SaveManifest(filepath.Join(v.Root, "loadout.toml"), v.Manifest); err != nil {
		return err
	}
	fmt.Fprintf(out, "enabled adapters: %s\n", strings.Join(enabled, ", "))
	return nil
}

// runInitImport runs the same dry-run-then-real import sequence
// "loadout import" itself runs, over every registered source.
// projectMemory sets importer.ImportCtx.ProjectMemory (false — global
// instruction files only — for the interactive wizard, opts.ProjectMemory
// for the headless "--yes" path; see importArgs.projectMemory's own
// doc comment in import.go for what the flag means). It prints the
// dry-run preview, then the real report, reusing renderImportReport so
// the wizard's import output otherwise matches the plain verb's
// exactly — except the real report's own trailing "loadout review" /
// "loadout sync --remote" next-step line is suppressed
// (showNextSteps: false): runInit already prints that same line,
// once, as its own closing summary (initNextSteps), so a real import
// here must not print it a second time.
func runInitImport(v *vault.Vault, home string, projectMemory bool, out io.Writer) error {
	projectDir, err := os.Getwd()
	if err != nil {
		projectDir = home
	}
	ctx := importer.ImportCtx{
		Home:           home,
		VaultRoot:      v.Root,
		VaultSkillsDir: v.SkillsDir(),
		ProjectDir:     projectDir,
		ProjectMemory:  projectMemory,
	}

	release, err := vault.Lock(v)
	if err != nil {
		return err
	}
	defer release()

	fmt.Fprintln(out, "import preview (dry run):")
	dryResult, err := importer.RunImport(v, importRegistry, ctx, importer.Options{Skills: true, Memory: true, DryRun: true})
	if err != nil {
		return err
	}
	renderImportReport(out, dryResult, true, false)

	realResult, err := importer.RunImport(v, importRegistry, ctx, importer.Options{Skills: true, Memory: true, DryRun: false})
	if err != nil {
		return err
	}
	if err := vault.Snapshot(v, "import"); err != nil {
		return err
	}
	renderImportReport(out, realResult, false, false)
	return nil
}

// connectRemote reads the token from tokenPath and saves rawURL plus
// that token as the vault's remote configuration, reusing the same
// validateRemoteURL check and remote.Save call "loadout remote add"
// itself uses. It never prints the token: only the url and a fixed
// success line ever reach out, and the token bytes read from disk are
// zeroed as soon as they are no longer needed.
func connectRemote(v *vault.Vault, rawURL, tokenPath string, out io.Writer) error {
	if err := validateRemoteURL(rawURL); err != nil {
		return err
	}
	raw, err := os.ReadFile(tokenPath)
	if err != nil {
		return fmt.Errorf("%s: the token file cannot be read: %v. Fix: pass a path to a file holding the loadoutd token.", tokenPath, err)
	}
	defer func() {
		for i := range raw {
			raw[i] = 0
		}
	}()
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return fmt.Errorf("%s: the token file is empty. Fix: put the loadoutd token in that file.", tokenPath)
	}
	if err := remote.Save(v, &remote.Config{URL: rawURL, Token: token}); err != nil {
		return err
	}
	fmt.Fprintf(out, "remote added: %s\n", rawURL)
	return nil
}
