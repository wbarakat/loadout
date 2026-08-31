package cli

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"loadout.dev/loadout/internal/remote"
	"loadout.dev/loadout/internal/vault"
)

const remoteUsage = "usage: loadout remote [--json] | loadout remote add <url> <token>"

// remoteAddResult is the JSON shape of "loadout remote add". It never
// carries the token.
type remoteAddResult struct {
	URL string `json:"url"`
}

// remoteShowResult is the JSON shape of "loadout remote". It never
// carries the token.
type remoteShowResult struct {
	URL         string `json:"url"`
	LastVersion string `json:"last_version"`
}

// cmdRemote dispatches "loadout remote" (show) and "loadout remote
// add <url> <token>". Neither path ever prints the token: add prints
// only the url it just wrote, and show reads Config back without
// touching its Token field for output.
func cmdRemote(out, errOut io.Writer, args []string, m mode) int {
	if len(args) == 0 {
		return cmdRemoteShow(out, errOut, m)
	}
	if args[0] != "add" {
		fmt.Fprintln(errOut, remoteUsage)
		return 2
	}
	rest := args[1:]
	if len(rest) != 2 || rest[0] == "" || rest[1] == "" {
		fmt.Fprintln(errOut, remoteUsage)
		return 2
	}
	return cmdRemoteAdd(out, errOut, rest[0], rest[1], m)
}

// validateRemoteURL rejects a url with no scheme or host: a typo
// there would otherwise surface as a confusing network error much
// later, at the first sync.
func validateRemoteURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("%q: not a valid remote url. Fix: use a url like http://host:port.", raw)
	}
	return nil
}

func cmdRemoteAdd(out, errOut io.Writer, rawURL, token string, m mode) int {
	if err := validateRemoteURL(rawURL); err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	printWarnings(errOut, v)
	if err := remote.Save(v, &remote.Config{URL: rawURL, Token: token}); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if m == modeJSON {
		printJSON(out, remoteAddResult{URL: rawURL})
		return 0
	}
	fmt.Fprintf(out, "remote added: %s\n", rawURL)
	return 0
}

func cmdRemoteShow(out, errOut io.Writer, m mode) int {
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	printWarnings(errOut, v)
	cfg, err := remote.Load(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if m == modeJSON {
		printJSON(out, remoteShowResult{URL: cfg.URL, LastVersion: cfg.LastVersion})
		return 0
	}
	last := cfg.LastVersion
	if last == "" {
		last = "(none yet)"
	}
	fmt.Fprintf(out, "remote: %s\nlast synced version: %s\n", cfg.URL, last)
	return 0
}

// remoteStatusLine renders st as the one remote line "loadout status"
// always prints when a remote is configured, whatever its state.
func remoteStatusLine(st remote.Status) string {
	var b strings.Builder
	fmt.Fprintf(&b, "remote: %s — %s", st.URL, st.State)
	if st.Detail != "" {
		fmt.Fprintf(&b, " (%s)", st.Detail)
	}
	return b.String()
}

// doctorRemoteProblem turns a not-in-sync remote status into the one
// remote line "loadout doctor" adds when it finds one: an unreachable
// remote's Detail already carries the client's own ". Fix: ..." text,
// which this splits into doctorProblem's separate Detail and Fix
// fields; a behind or ahead remote gets the same fixed suggestion,
// since either one is resolved the same way.
func doctorRemoteProblem(st remote.Status) doctorProblem {
	if st.State == "unreachable" {
		detail, fix := st.Detail, "check the url and that loadoutd runs."
		if before, after, found := strings.Cut(st.Detail, ". Fix: "); found {
			detail = before
			fix = after
		}
		return doctorProblem{Source: "remote", Detail: detail, Fix: fix}
	}
	return doctorProblem{
		Source: "remote",
		Detail: fmt.Sprintf("the remote at %s is %s: %s", st.URL, st.State, st.Detail),
		Fix:    "run loadout sync --remote",
	}
}
