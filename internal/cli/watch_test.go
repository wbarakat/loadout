package cli

import (
	"bytes"
	"testing"
	"time"

	"loadout.dev/loadout/internal/vault"
)

// TestParseWatchArgsDefaultsToTenSeconds proves no --interval at all
// falls back to the documented default.
func TestParseWatchArgsDefaultsToTenSeconds(t *testing.T) {
	d, ok := parseWatchArgs(nil)
	if !ok || d != defaultWatchInterval {
		t.Fatalf("parseWatchArgs(nil) = %v, %v; want %v, true", d, ok, defaultWatchInterval)
	}
}

// TestParseWatchArgsAcceptsValidDurations proves every Go duration
// spelling at or above the 1s floor is accepted, "10s" and "1m"
// included, per the task brief.
func TestParseWatchArgsAcceptsValidDurations(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"1s", time.Second},
		{"10s", 10 * time.Second},
		{"1m", time.Minute},
		{"1m30s", 90 * time.Second},
	}
	for _, c := range cases {
		d, ok := parseWatchArgs([]string{"--interval", c.in})
		if !ok || d != c.want {
			t.Fatalf("parseWatchArgs(--interval %s) = %v, %v; want %v, true", c.in, d, ok, c.want)
		}
	}
}

// TestParseWatchArgsRejectsSubSecondDurations proves the brief's <1s
// floor: any duration under one second, however it is spelled, is a
// usage error rather than a silently accepted hot loop.
func TestParseWatchArgsRejectsSubSecondDurations(t *testing.T) {
	for _, in := range []string{"999ms", "1ms", "0s", "500us"} {
		if _, ok := parseWatchArgs([]string{"--interval", in}); ok {
			t.Fatalf("parseWatchArgs(--interval %s) must be rejected, got ok", in)
		}
	}
}

// TestParseWatchArgsRejectsMalformed proves every other malformed
// shape is a usage error: an unknown flag, a missing or empty value,
// a bad duration string, and stray extra arguments.
func TestParseWatchArgsRejectsMalformed(t *testing.T) {
	cases := [][]string{
		{"--bogus", "10s"},
		{"--interval"},
		{"--interval", ""},
		{"--interval", "not-a-duration"},
		{"10s"},
		{"--interval", "10s", "extra"},
	}
	for _, args := range cases {
		if _, ok := parseWatchArgs(args); ok {
			t.Fatalf("parseWatchArgs(%v) must be rejected, got ok", args)
		}
	}
}

// TestNextBackoffSequence proves the documented sequence: the first
// error backs off to the base interval, each further consecutive
// error doubles it, and the wait never exceeds max however many
// errors pile up.
func TestNextBackoffSequence(t *testing.T) {
	base := time.Second
	max := 5 * time.Minute

	got := nextBackoff(0, base, max)
	if got != base {
		t.Fatalf("first backoff = %v, want base %v", got, base)
	}
	got = nextBackoff(got, base, max)
	if got != 2*base {
		t.Fatalf("second backoff = %v, want %v", got, 2*base)
	}
	got = nextBackoff(got, base, max)
	if got != 4*base {
		t.Fatalf("third backoff = %v, want %v", got, 4*base)
	}

	// Keep doubling until it would exceed max: it must clamp there and
	// stay there, never overshoot.
	for i := 0; i < 20; i++ {
		got = nextBackoff(got, base, max)
		if got > max {
			t.Fatalf("backoff must never exceed max, got %v > %v", got, max)
		}
	}
	if got != max {
		t.Fatalf("backoff must reach max after enough consecutive errors, got %v want %v", got, max)
	}

	// A non-positive current (no active backoff, e.g. after a success)
	// always restarts at base, regardless of any stale prior value.
	if got := nextBackoff(0, base, max); got != base {
		t.Fatalf("nextBackoff(0, ...) = %v, want base %v", got, base)
	}
	if got := nextBackoff(-1, base, max); got != base {
		t.Fatalf("nextBackoff(-1, ...) = %v, want base %v", got, base)
	}
}

// TestRunWatchBeatSkipsQuietlyWhenVaultLocked proves the lock-aware
// requirement directly: while another command holds the vault lock, a
// beat returns nil (not an error) and prints nothing to either
// stream, so the caller neither blocks nor treats it as a failure.
func TestRunWatchBeatSkipsQuietlyWhenVaultLocked(t *testing.T) {
	v, err := vault.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	release, err := vault.Lock(v)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	var out, errOut bytes.Buffer
	if err := runWatchBeat(v, &out, &errOut, modeText); err != nil {
		t.Fatalf("a beat that finds the vault locked must not error, got %v", err)
	}
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Fatalf("a skipped beat must print nothing, got out=%q err=%q", out.String(), errOut.String())
	}
}
