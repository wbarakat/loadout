package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/remote"
	"loadout.dev/loadout/internal/vault"
)

// doctorProblem is one entry in the JSON shape of "loadout doctor".
// Source names where the problem comes from: "vault" for a vault-
// level check, or the adapter's name for an adapter check.
type doctorProblem struct {
	Source string `json:"source"`
	Detail string `json:"detail"`
	Fix    string `json:"fix"`
}

// doctorResult is the JSON shape of "loadout doctor".
type doctorResult struct {
	Problems []doctorProblem `json:"problems"`
	Count    int             `json:"count"`
}

// checkSecretReadability probes every secret with this device's own
// key and reports one problem for each it cannot decrypt: a durable,
// surfaced signal for the case ReEncryptSecrets' own skip list
// otherwise only warns about once, at approve time.
//
// This is a READABILITY PROBE, not a use: it calls vault.DecryptSecret
// directly and checks only the error, never AppendAccessLog — doctor
// reading every secret on every run must never look like this device
// actually using them (that would flood the access log with entries
// no human or tool ever asked for). Every plaintext DecryptSecret
// hands back is zeroed immediately: doctor never needs the value,
// only whether the decrypt succeeded.
//
// A NO-SECRETS device skips this probe entirely (Important finding,
// 2026-09-01 whole-branch fix wave): such a device is EXPECTED to be
// unable to read any secret — that is what the role means, not a
// problem. Probing it anyway used to report every secret as "cannot
// read it", with a fix that said to run "loadout devices approve
// <name>" — which, if actually followed, PROMOTES the device back to
// full, the exact opposite of what a no-secrets device wants. So this
// checks this device's own role first, and reports nothing about
// secret readability at all when it is no-secrets.
func checkSecretReadability(v *vault.Vault, secrets []vault.Secret) ([]doctorProblem, error) {
	if len(secrets) == 0 {
		return nil, nil
	}
	role, err := vault.SelfRole(v)
	if err != nil {
		return nil, err
	}
	if role == vault.RoleNoSecrets {
		return nil, nil
	}
	deviceName, _, err := vault.DeviceIdentity(v)
	if err != nil {
		return nil, err
	}
	var problems []doctorProblem
	for _, s := range secrets {
		value, err := vault.DecryptSecret(v, s.Name)
		if err != nil {
			problems = append(problems, doctorProblem{
				Source: "secret/" + s.Name,
				Detail: "this device cannot read it",
				Fix:    "run loadout devices approve " + deviceName + " from a device that can read it, then sync.",
			})
			continue
		}
		for i := range value {
			value[i] = 0
		}
	}
	return problems, nil
}

// checkUnrecognizedRoles flags any devices.toml entry whose RAW,
// on-disk role is neither absent nor a recognized role ("full" or
// "no-secrets") — a typo, or a hand-edited manifest. normalizeRole
// already fails closed on such a value (vault.ReadRosterEntries reads
// it as RoleNoSecrets, so a typo can never leak a secret to a device
// nobody meant to grant it to), but that silent fallback would
// otherwise hide the mistake forever: the operator who typed "admin"
// meaning "full" never finds out their device is actually no-secrets.
func checkUnrecognizedRoles(v *vault.Vault) ([]doctorProblem, error) {
	entries, err := vault.ReadRosterEntries(v)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	var problems []doctorProblem
	for _, name := range names {
		raw := entries[name].RawRole
		if !vault.UnrecognizedRole(raw) {
			continue
		}
		problems = append(problems, doctorProblem{
			Source: "vault",
			Detail: fmt.Sprintf("device %s has an unknown role %s in the manifest; loadout treats it as no-secrets", name, raw),
			Fix:    "set the role to full or no-secrets.",
		})
	}
	return problems, nil
}

// checkSecretRotation flags every secret whose rotate_after duration
// has elapsed since it was added: a durable rotation reminder, so a
// key nobody is watching does not sit unrotated forever. It is a
// metadata-only check — it reads only Secret's own plaintext fields
// through vault.SecretDue, and never calls DecryptSecret — so it never
// touches a value and never needs the readability probe's own
// device-cannot-read handling.
func checkSecretRotation(secrets []vault.Secret) []doctorProblem {
	var problems []doctorProblem
	now := time.Now().UTC()
	for _, s := range secrets {
		if !vault.SecretDue(s, now) {
			continue
		}
		problems = append(problems, doctorProblem{
			Source: "secret/" + s.Name,
			Detail: fmt.Sprintf("is due for rotation (added %s, rotate after %s)", s.At, s.RotateAfter),
			Fix:    fmt.Sprintf("rotate the key at %s, then run loadout secret rotate %s to replace it.", s.Service, s.Name),
		})
	}
	return problems
}

func cmdDoctor(out, errOut io.Writer, m mode) int {
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	printWarnings(errOut, v)
	problems := []doctorProblem{}
	if _, err := os.Stat(filepath.Join(v.Root, ".git")); err != nil {
		problems = append(problems, doctorProblem{
			Source: "vault",
			Detail: "the vault history is missing",
			Fix:    "restore the .git directory from a backup, or re-create the vault.",
		})
	}
	repos, err := vault.EmbeddedSkillRepos(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	for _, d := range repos {
		problems = append(problems, doctorProblem{
			Source: "vault",
			Detail: fmt.Sprintf("the skill folder %s is a git repository", d),
			Fix:    "remove its .git directory; the vault keeps history for you.",
		})
	}
	bad, err := vault.InvalidSkillDirs(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	for _, d := range bad {
		problems = append(problems, doctorProblem{
			Source: "vault",
			Detail: fmt.Sprintf("the skill directory %s has no SKILL.md file", d),
			Fix:    "add a SKILL.md file, or remove the directory",
		})
	}
	secrets, err := vault.ListSecrets(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	secretProblems, err := checkSecretReadability(v, secrets)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	problems = append(problems, secretProblems...)
	problems = append(problems, checkSecretRotation(secrets)...)
	roleProblems, err := checkUnrecognizedRoles(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	problems = append(problems, roleProblems...)
	for _, a := range adapter.Enabled(v) {
		for _, p := range a.Check(v) {
			problems = append(problems, doctorProblem{Source: p.Adapter, Detail: p.Detail, Fix: p.Fix})
		}
	}
	remoteStatus, hasRemote, err := remote.LoadStatus(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if hasRemote && remoteStatus.State != "in sync" {
		problems = append(problems, doctorRemoteProblem(remoteStatus))
	}
	count := len(problems)
	if m == modeJSON {
		printJSON(out, doctorResult{Problems: problems, Count: count})
		if count == 0 {
			return 0
		}
		return 1
	}
	for _, p := range problems {
		fmt.Fprintf(out, "%s: %s\n  fix: %s\n", p.Source, p.Detail, p.Fix)
	}
	if count == 0 {
		fmt.Fprintln(out, "all good")
		return 0
	}
	if count == 1 {
		fmt.Fprintln(out, "1 problem")
	} else {
		fmt.Fprintf(out, "%d problems\n", count)
	}
	return 1
}
