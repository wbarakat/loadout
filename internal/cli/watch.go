package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/remote"
	"loadout.dev/loadout/internal/vault"
)

const watchUsage = "usage: loadout watch [--interval <duration>]"

// defaultWatchInterval is how often a beat runs when --interval is
// not given.
const defaultWatchInterval = 10 * time.Second

// minWatchInterval is the shortest --interval loadout accepts. A
// shorter interval would poll the vault lock and the remote far
// faster than any real edit could arrive, for no gain.
const minWatchInterval = 1 * time.Second

// maxWatchBackoff caps how long a beat waits after a run of remote
// errors, however many failures pile up in a row.
const maxWatchBackoff = 5 * time.Minute

// parseWatchArgs reads "[--interval <duration>]" from args. ok is
// false when args holds anything else, --interval's value is not a
// valid Go duration (e.g. "10s", "1m"), or that duration is under
// minWatchInterval.
func parseWatchArgs(args []string) (interval time.Duration, ok bool) {
	switch len(args) {
	case 0:
		return defaultWatchInterval, true
	case 2:
		if args[0] != "--interval" || args[1] == "" {
			return 0, false
		}
		d, err := time.ParseDuration(args[1])
		if err != nil || d < minWatchInterval {
			return 0, false
		}
		return d, true
	default:
		return 0, false
	}
}

// nextBackoff computes the wait before the next beat after a remote
// error. The first error in a run backs off to base; each further
// consecutive error doubles the previous wait, capped at max. current
// is 0 (or any non-positive value) to mean "no active backoff yet."
func nextBackoff(current, base, max time.Duration) time.Duration {
	if current <= 0 {
		current = base
	} else {
		current *= 2
	}
	if current > max {
		current = max
	}
	return current
}

// cmdWatch runs "loadout watch": it loops forever, running the same
// work as "loadout sync --remote" on every beat, until SIGINT or
// SIGTERM asks it to stop. It fails fast, before the loop starts,
// when the vault has no remote configured: that condition never
// clears on its own, so looping on it forever would only spam a
// backoff error every beat.
func cmdWatch(out, errOut io.Writer, args []string, m mode) int {
	interval, ok := parseWatchArgs(args)
	if !ok {
		fmt.Fprintln(errOut, watchUsage)
		return 2
	}

	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	printWarnings(errOut, v)

	if _, err := remote.Load(v); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	var backoff time.Duration
	for {
		if err := runWatchBeat(v, out, errOut, m); err != nil {
			fmt.Fprintln(errOut, err)
			backoff = nextBackoff(backoff, interval, maxWatchBackoff)
		} else {
			backoff = 0
		}

		wait := interval
		if backoff > 0 {
			wait = backoff
		}
		timer := time.NewTimer(wait)
		select {
		case <-sigCh:
			timer.Stop()
			fmt.Fprintln(out, "watch stopped.")
			return 0
		case <-timer.C:
		}
	}
}

// runWatchBeat runs one beat: the same work as "loadout sync
// --remote" (the local projection, then remote.Sync), reusing that
// code path rather than duplicating it. It skips quietly, returning
// nil, when another loadout command already holds the vault lock. It
// prints one line, only when the beat actually changed something, and
// returns any hard failure from either half for the caller to report
// and back off on.
func runWatchBeat(v *vault.Vault, out, errOut io.Writer, m mode) error {
	release, ok, err := vault.TryLock(v)
	if err != nil {
		return err
	}
	if !ok {
		// Another loadout command holds the lock right now: skip this
		// beat quietly and let the next one try again.
		return nil
	}
	release()

	before, err := vault.HeadHash(v)
	if err != nil {
		return err
	}

	// out is discarded, and modeJSON forces the same suppression on
	// syncLocal's own side, so its per-adapter "synced X (N linked, M
	// pruned)" lines never print here: they would otherwise print every
	// beat, defeating "silent when nothing changed." Blocked skills and
	// per-adapter apply errors still reach errOut, unconditionally on
	// syncLocal's own part, so a real problem is never hidden.
	reports, _, err := syncLocal(v, io.Discard, errOut, false, modeJSON)
	if err != nil {
		return err
	}

	res, err := remote.Sync(v)
	if err != nil {
		return err
	}

	after, err := vault.HeadHash(v)
	if err != nil {
		return err
	}

	printWatchBeatResult(out, m, reports, res, before != after)
	return nil
}

// watchBeatResult is the JSON shape of one changed "loadout watch"
// beat. printWatchBeatResult builds and prints it only when
// something changed; a silent beat prints nothing in either mode.
type watchBeatResult struct {
	AdaptersProjected int    `json:"adapters_projected"`
	Linked            int    `json:"linked"`
	Pruned            int    `json:"pruned"`
	VaultChanged      bool   `json:"vault_changed"`
	Version           string `json:"version,omitempty"`
	Merged            bool   `json:"merged,omitempty"`
	Pushed            bool   `json:"pushed,omitempty"`
}

// printWatchBeatResult renders one beat's outcome to out, in text or
// JSON per m, but only when the beat changed something: an adapter
// linked or pruned a skill, or the vault's own git HEAD moved (a
// local edit picked up, or a remote merge). A beat that did neither
// prints nothing, in either mode.
func printWatchBeatResult(out io.Writer, m mode, reports []adapter.Report, res remote.Result, vaultChanged bool) {
	var linked, pruned, touched int
	for _, r := range reports {
		if r.Linked > 0 || len(r.Pruned) > 0 {
			touched++
		}
		linked += r.Linked
		pruned += len(r.Pruned)
	}
	if touched == 0 && !vaultChanged {
		return
	}

	if m == modeJSON {
		data, err := json.Marshal(watchBeatResult{
			AdaptersProjected: touched,
			Linked:            linked,
			Pruned:            pruned,
			VaultChanged:      vaultChanged,
			Version:           res.Version,
			Merged:            res.Merged,
			Pushed:            res.Pushed,
		})
		if err != nil {
			fmt.Fprintln(out, err)
			return
		}
		out.Write(data)
		fmt.Fprintln(out)
		return
	}

	var parts []string
	if touched > 0 {
		parts = append(parts, fmt.Sprintf("projected %d adapter(s) (%d linked, %d pruned)", touched, linked, pruned))
	}
	if vaultChanged {
		if res.Merged {
			parts = append(parts, fmt.Sprintf("merged the remote's version %s", res.Version))
		} else if res.Pushed {
			parts = append(parts, fmt.Sprintf("synced version %s to the remote", res.Version))
		}
	}
	fmt.Fprintln(out, "watch: "+strings.Join(parts, "; "))
}
