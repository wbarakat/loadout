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

	cfg, err := remote.Load(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}

	// lastHead and lastVersion are the baseline the NEXT beat's
	// announcement decision compares against: not this beat's own
	// before/after (see beatChangeSince's doc comment for why that is
	// wrong), but the state as of the last beat that actually
	// announced something — starting from the state right now, before
	// the first beat ever runs.
	lastHead, err := vault.HeadHash(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	lastVersion := cfg.LastVersion

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	var backoff time.Duration
	for {
		outcome, newHead, newVersion, err := runWatchBeat(v, out, errOut, m, lastHead, lastVersion)
		switch {
		case err != nil:
			fmt.Fprintln(errOut, err)
			backoff = nextBackoff(backoff, interval, maxWatchBackoff)
		case outcome == beatSkipped:
			// Another loadout command holds the lock right now. This is
			// not a success and not a failure: it neither clears a
			// backoff already under way (the remote's own trouble, if
			// any, is still exactly as unresolved as before this beat)
			// nor advances the announcement baseline (nothing new was
			// even attempted).
		default: // beatRan
			backoff = 0
			lastHead = newHead
			lastVersion = newVersion
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

// beatOutcome distinguishes a beat that actually ran (whether or not
// it had anything to announce) from one that skipped because the
// vault was busy: cmdWatch's backoff and its announcement baseline
// both need to tell the two apart.
type beatOutcome int

const (
	beatRan beatOutcome = iota
	beatSkipped
)

// runWatchBeat runs one beat: the same work as "loadout sync
// --remote" (the local projection, then remote.Sync), reusing that
// code path rather than duplicating it. It returns beatSkipped, with
// no error, when another loadout command already holds the vault
// lock. Otherwise it returns beatRan, along with the vault's git
// HeadHash and the remote's last-synced version as they stand right
// now — the caller carries these forward as lastHead/lastVersion for
// the NEXT beat's announcement decision. It prints a line only when
// beatChangeSince says the accumulated state (since the last beat
// that printed) is worth announcing, and returns any hard failure
// from either half for the caller to report and back off on.
func runWatchBeat(v *vault.Vault, out, errOut io.Writer, m mode, lastHead, lastVersion string) (outcome beatOutcome, newHead, newVersion string, err error) {
	release, ok, err := vault.TryLock(v)
	if err != nil {
		return beatRan, "", "", err
	}
	if !ok {
		// Another loadout command holds the lock right now: skip this
		// beat quietly and let the next one try again.
		return beatSkipped, "", "", nil
	}
	release()

	// out is discarded, and modeJSON forces the same suppression on
	// syncLocal's own side, so its per-adapter "synced X (N linked, M
	// pruned)" lines never print here: they would otherwise print every
	// beat, defeating "silent when nothing changed." Blocked skills and
	// per-adapter apply errors still reach errOut, unconditionally on
	// syncLocal's own part, so a real problem is never hidden.
	reports, _, err := syncLocal(v, io.Discard, errOut, false, modeJSON)
	if err != nil {
		return beatRan, "", "", err
	}

	res, err := remote.Sync(v)
	if err != nil {
		return beatRan, "", "", err
	}

	currentHead, err := vault.HeadHash(v)
	if err != nil {
		return beatRan, "", "", err
	}

	var linked, pruned int
	for _, r := range reports {
		linked += r.Linked
		pruned += len(r.Pruned)
	}

	if shouldPrint, line := beatChangeSince(lastHead, currentHead, lastVersion, res.Version, linked, pruned); shouldPrint {
		printWatchBeatLine(out, m, line, currentHead != lastHead, res.Version, linked, pruned)
	}

	return beatRan, currentHead, res.Version, nil
}

// beatChangeSince decides whether a beat is worth announcing, and
// what to say, by comparing this beat's outcome against the state as
// of the last beat that WAS announced (lastHead, lastVersion) —
// never just this one beat's own before/after. A beat that only
// compared its own within-beat before/after would miss a change a
// SEPARATE loadout command (add, edit, review keep/drop, undo)
// committed between two beats: syncLocal and remote.Sync still
// notice that change and push it on the very next beat, but that
// beat's own head never moves during its own execution, since the
// commit already happened before the beat started. Comparing against
// the cross-beat baseline instead catches it: currentHead already
// differs from lastHead the moment such a beat runs, so it prints.
//
// lastVersion is accepted, and currentVersion is reported in the
// message, but neither is ever compared to decide shouldPrint:
// internal/remote's Sync mints a brand-new version string on every
// successful call even when nothing in the vault changed at all
// (push() republishes whenever this device is already caught up —
// Task 5's own, unmodified behavior). Treating a bare version
// difference as "changed" would make every idle beat look like news,
// defeating "silent when nothing changed." currentHead, which only
// moves when git actually finds a real diff to commit, is the
// reliable signal; linked/pruned catch a projection-only change (a
// symlink relinked after something outside the vault deleted it)
// that never touches the vault's own history at all.
func beatChangeSince(lastHead, currentHead, lastVersion, currentVersion string, linked, pruned int) (shouldPrint bool, line string) {
	headChanged := currentHead != lastHead
	touched := linked > 0 || pruned > 0
	if !headChanged && !touched {
		return false, ""
	}
	var parts []string
	if touched {
		parts = append(parts, fmt.Sprintf("projected skills (%d linked, %d pruned)", linked, pruned))
	}
	if headChanged {
		if currentVersion != "" {
			parts = append(parts, fmt.Sprintf("synced version %s", currentVersion))
		} else {
			parts = append(parts, "picked up a local change")
		}
	}
	return true, "watch: " + strings.Join(parts, "; ")
}

// watchBeatResult is the JSON shape of one announced "loadout watch"
// beat. A silent beat prints nothing in either mode.
type watchBeatResult struct {
	Linked       int    `json:"linked"`
	Pruned       int    `json:"pruned"`
	VaultChanged bool   `json:"vault_changed"`
	Version      string `json:"version,omitempty"`
}

// printWatchBeatLine renders an already-decided-worth-announcing beat
// to out, in text (using line, built by beatChangeSince) or JSON
// (built fresh from the same underlying facts) per m.
func printWatchBeatLine(out io.Writer, m mode, line string, vaultChanged bool, version string, linked, pruned int) {
	if m == modeJSON {
		data, err := json.Marshal(watchBeatResult{
			Linked:       linked,
			Pruned:       pruned,
			VaultChanged: vaultChanged,
			Version:      version,
		})
		if err != nil {
			fmt.Fprintln(out, err)
			return
		}
		out.Write(data)
		fmt.Fprintln(out)
		return
	}
	fmt.Fprintln(out, line)
}
