package cli

import (
	"fmt"
	"io"
	"sort"

	"filippo.io/age"
	"loadout.dev/loadout/internal/remote"
	"loadout.dev/loadout/internal/vault"
)

const devicesUsage = "usage: loadout devices [--json] | loadout devices approve <name> [--rotate]"

// cmdDevices dispatches "loadout devices" (the bare list) and
// "loadout devices approve <name> [--rotate]".
func cmdDevices(out, errOut io.Writer, args []string, m mode) int {
	if len(args) == 0 {
		return cmdDevicesList(out, errOut, m)
	}
	if args[0] != "approve" {
		fmt.Fprintln(errOut, devicesUsage)
		return 2
	}
	return cmdDevicesApprove(out, errOut, args[1:], m)
}

// extractRotateFlag removes the first "--rotate" argument found at
// any position in args, and reports whether it found one, the same
// way extractDryRun and extractRemoteFlag strip their own flags in
// sync.go.
func extractRotateFlag(args []string) ([]string, bool) {
	for i, a := range args {
		if a == "--rotate" {
			out := make([]string, 0, len(args)-1)
			out = append(out, args[:i]...)
			out = append(out, args[i+1:]...)
			return out, true
		}
	}
	return args, false
}

// remoteClient loads the vault's remote configuration and builds a
// client for it. It never touches the token itself: every caller only
// ever forwards it into the client, never into output.
func remoteClient(v *vault.Vault) (*remote.Client, error) {
	cfg, err := remote.Load(v)
	if err != nil {
		return nil, err
	}
	return remote.NewClient(cfg.URL, cfg.Token), nil
}

// deviceEntry is one device's row in "loadout devices".
type deviceEntry struct {
	Name string `json:"name"`
	// Recipient is the recipient devices.toml actually stores for an
	// approved or re-keyed device (the one PackSnapshot really
	// encrypts to), or the remote's registered recipient for a device
	// that is only waiting.
	Recipient string `json:"recipient"`
	// Approved is true for both "approved" and "re-keyed" states:
	// both mean the name is in devices.toml, the real decrypt
	// allowlist. State distinguishes a re-keyed device from a clean
	// approval.
	Approved bool `json:"approved"`
	// State is "approved", "waiting", or "re-keyed".
	State string `json:"state"`
}

// devicesListResult is the JSON shape of "loadout devices".
type devicesListResult struct {
	Devices []deviceEntry `json:"devices"`
}

// cmdDevicesList merges the remote's bootstrap device roster with
// this vault's own devices.toml — the real decrypt allowlist — and
// reports each name's state:
//
//   - "approved": the name is in devices.toml. This includes a name
//     devices.toml lists that the remote roster does not (or does,
//     with a matching recipient).
//   - "waiting": the name is only on the remote roster, never
//     approved here.
//   - "re-keyed": the name is in devices.toml, but the remote's
//     currently registered recipient for it differs from the one
//     devices.toml stores — a re-keyed device or an impersonation
//     attempt, either way not yet trusted with the new key.
func cmdDevicesList(out, errOut io.Writer, m mode) int {
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	printWarnings(errOut, v)

	client, err := remoteClient(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	serverDevices, err := client.ListDevices()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	roster, err := vault.ReadRoster(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}

	serverRecipients := make(map[string]string, len(serverDevices))
	for _, d := range serverDevices {
		serverRecipients[d.Name] = d.Recipient
	}
	names := make(map[string]bool, len(roster)+len(serverDevices))
	for name := range roster {
		names[name] = true
	}
	for name := range serverRecipients {
		names[name] = true
	}

	entries := make([]deviceEntry, 0, len(names))
	for name := range names {
		stored, inRoster := roster[name]
		serverRecipient, onServer := serverRecipients[name]
		entry := deviceEntry{Name: name}
		switch {
		case inRoster && onServer && stored != serverRecipient:
			entry.Recipient = stored
			entry.Approved = true
			entry.State = "re-keyed"
		case inRoster:
			entry.Recipient = stored
			entry.Approved = true
			entry.State = "approved"
		default:
			entry.Recipient = serverRecipient
			entry.Approved = false
			entry.State = "waiting"
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	if m == modeJSON {
		printJSON(out, devicesListResult{Devices: entries})
		return 0
	}
	for _, e := range entries {
		status := e.State
		if e.State == "re-keyed" {
			status = fmt.Sprintf("re-keyed (run: loadout devices approve %s --rotate)", e.Name)
		}
		fmt.Fprintf(out, "%s — %s\n", e.Name, status)
	}
	return 0
}

// devicesApproveResult is the JSON shape of "loadout devices approve
// <name>".
type devicesApproveResult struct {
	Name    string `json:"name"`
	Already bool   `json:"already_approved"`
	Rotated bool   `json:"rotated"`
}

// cmdDevicesApprove approves a waiting device, or — with --rotate —
// deliberately replaces an already-approved device's key with the
// remote's currently registered one.
func cmdDevicesApprove(out, errOut io.Writer, args []string, m mode) int {
	rest, rotate := extractRotateFlag(args)
	if len(rest) != 1 || rest[0] == "" {
		fmt.Fprintln(errOut, devicesUsage)
		return 2
	}
	name := rest[0]

	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	printWarnings(errOut, v)

	client, err := remoteClient(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	serverDevices, err := client.ListDevices()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	recipient, ok := findDeviceRecipient(serverDevices, name)
	if !ok {
		fmt.Fprintf(errOut, "%s: no such device on the remote. Fix: run loadout devices to see who is waiting.\n", name)
		return 1
	}

	outcome, err := approveDevice(v, name, recipient, rotate)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if outcome == approveMismatchBlocked {
		fmt.Fprintf(errOut, "%s is already approved with a different key. This is a re-keyed device or an imposter. Fix: run loadout devices approve %s --rotate only if you trust the new key.\n", name, name)
		return 1
	}

	// Every other outcome — a fresh approval, a deliberate rotation,
	// or an idempotent same-recipient re-approval — syncs now: even
	// the idempotent path must retry a push that failed after an
	// earlier approval, so the newcomer is never left stranded by a
	// silent no-op.
	if _, err := remote.Sync(v); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}

	if m == modeJSON {
		printJSON(out, devicesApproveResult{
			Name:    name,
			Already: outcome == approveAlreadyMatches,
			Rotated: outcome == approveRotated,
		})
		return 0
	}
	switch outcome {
	case approveAlreadyMatches:
		fmt.Fprintf(out, "%s is already approved.\n", name)
	case approveRotated:
		fmt.Fprintf(out, "rotated %s to a new key.\n", name)
	default:
		fmt.Fprintf(out, "approved %s. Run loadout sync --remote on that device now.\n", name)
	}
	return 0
}

// findDeviceRecipient finds name's recipient among devices, the
// remote's bootstrap roster.
func findDeviceRecipient(devices []remote.Device, name string) (recipient string, ok bool) {
	for _, d := range devices {
		if d.Name == name {
			return d.Recipient, true
		}
	}
	return "", false
}

// approveOutcome enumerates what approveDevice actually did to the
// vault's device roster.
type approveOutcome int

const (
	// approveAdded is a brand-new roster entry: the first approval of
	// this name.
	approveAdded approveOutcome = iota
	// approveAlreadyMatches means the roster already held this exact
	// name and recipient: no roster mutation happened.
	approveAlreadyMatches
	// approveMismatchBlocked means the roster already held this name
	// under a different recipient, and rotate was not requested: no
	// roster mutation happened, and the caller must treat this as an
	// error.
	approveMismatchBlocked
	// approveRotated means the roster already held this name under a
	// different recipient, and rotate was requested: the roster now
	// holds the new recipient.
	approveRotated
)

// invalidRecipientErr is the fixed error approveDevice returns when
// the remote's roster holds a recipient string that is not a valid
// age X25519 recipient. The server itself now refuses to store such a
// value (see handleUpsertDevice), but this check stays here too as a
// second, independent layer: a garbage or malicious registration must
// never enter devices.toml, however routine the approval that would
// otherwise commit it — PackSnapshot would then fail on every future
// snapshot, for every device, until someone edits devices.toml by
// hand.
func invalidRecipientErr(name string) error {
	return fmt.Errorf("%s: the remote gave an invalid recipient key. Fix: that device must run loadout join again; do not approve it until it registers a valid key.", name)
}

// approveDevice reconciles name and recipient into the vault's device
// roster, under the vault lock.
//
//   - When the roster does not yet list name, it validates recipient,
//     adds it (and — when the roster was still empty — the approving
//     device's own identity too, so the roster's first write never
//     drops the one device that is calling it: without this,
//     PackSnapshot would stop encrypting to this device on its very
//     next snapshot, since it already encrypts to this device alone
//     while the roster is empty), snapshots, and reports
//     approveAdded.
//   - When the roster already lists name with this exact recipient,
//     it changes nothing and reports approveAlreadyMatches.
//   - When the roster already lists name under a different recipient
//     and rotate is false, it changes nothing and reports
//     approveMismatchBlocked: a silent overwrite here would be a
//     spoofing vector, since a re-keyed device and an imposter
//     registering under a stolen name look identical to the remote's
//     roster.
//   - When the roster already lists name under a different recipient
//     and rotate is true, it validates the new recipient, overwrites
//     the roster entry, snapshots, and reports approveRotated.
func approveDevice(v *vault.Vault, name, recipient string, rotate bool) (approveOutcome, error) {
	release, err := vault.Lock(v)
	if err != nil {
		return 0, err
	}
	defer release()

	roster, err := vault.ReadRoster(v)
	if err != nil {
		return 0, err
	}

	if existing, ok := roster[name]; ok {
		if existing == recipient {
			return approveAlreadyMatches, nil
		}
		if !rotate {
			return approveMismatchBlocked, nil
		}
		if _, err := age.ParseX25519Recipient(recipient); err != nil {
			return 0, invalidRecipientErr(name)
		}
		if err := vault.AddToRoster(v, name, recipient); err != nil {
			return 0, err
		}
		if err := vault.Snapshot(v, "rotate device "+name+" key"); err != nil {
			return 0, err
		}
		return approveRotated, nil
	}

	if _, err := age.ParseX25519Recipient(recipient); err != nil {
		return 0, invalidRecipientErr(name)
	}
	if len(roster) == 0 {
		ownName, ownRecipient, err := vault.DeviceIdentity(v)
		if err != nil {
			return 0, err
		}
		if err := vault.AddToRoster(v, ownName, ownRecipient); err != nil {
			return 0, err
		}
	}
	if err := vault.AddToRoster(v, name, recipient); err != nil {
		return 0, err
	}
	if err := vault.Snapshot(v, "approve device "+name); err != nil {
		return 0, err
	}
	return approveAdded, nil
}
