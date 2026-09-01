package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"filippo.io/age"
	"loadout.dev/loadout/internal/remote"
	"loadout.dev/loadout/internal/vault"
)

const devicesUsage = "usage: loadout devices [--json] | loadout devices approve <name> [--no-secrets] | loadout devices approve <name> --rotate <recipient> [--no-secrets|--full]"

// cmdDevices dispatches "loadout devices" (the bare list) and
// "loadout devices approve <name> [--rotate <recipient>]".
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

// parseApproveArgs reads "<name>", optionally followed by "--rotate
// <recipient>" and/or one of "--no-secrets" or "--full", in any order,
// from args. ok is false when args does not match this shape — in
// particular, "--rotate" with no recipient argument is a usage error,
// not a boolean flag: a rotation must always name the exact key an
// admin has verified out-of-band, never leave it implicit. Likewise,
// "--no-secrets" and "--full" together is a usage error: a device
// cannot ask for both roles at once.
func parseApproveArgs(args []string) (name, rotateRecipient string, rotate, noSecrets, full bool, ok bool) {
	if len(args) == 0 || args[0] == "" || strings.HasPrefix(args[0], "--") {
		return "", "", false, false, false, false
	}
	name = args[0]
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--rotate":
			if rotate || i+1 >= len(rest) || rest[i+1] == "" {
				return "", "", false, false, false, false
			}
			rotateRecipient = rest[i+1]
			rotate = true
			i++
		case "--no-secrets":
			if noSecrets || full {
				return "", "", false, false, false, false
			}
			noSecrets = true
		case "--full":
			if full || noSecrets {
				return "", "", false, false, false, false
			}
			full = true
		default:
			return "", "", false, false, false, false
		}
	}
	return name, rotateRecipient, rotate, noSecrets, full, true
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
	// Role is "full" or "no-secrets" for a name devices.toml lists
	// (Approved or "re-keyed"), or "" for a name that is only
	// waiting: devices.toml, not the remote's bootstrap roster, is
	// what holds a role at all.
	Role string `json:"role"`
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
//     attempt, either way not yet trusted with the new key. The
//     remote's registered recipient shown here is only ever a hint:
//     an admin must verify the real new key out-of-band before
//     rotating to it (see cmdDevicesApprove's --rotate handling).
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
	roster, err := vault.ReadRosterEntries(v)
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
		case inRoster && onServer && stored.Recipient != serverRecipient:
			entry.Recipient = stored.Recipient
			entry.Approved = true
			entry.State = "re-keyed"
			entry.Role = stored.Role
		case inRoster:
			entry.Recipient = stored.Recipient
			entry.Approved = true
			entry.State = "approved"
			entry.Role = stored.Role
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
			status = "re-keyed (verify the new key out-of-band, then run: loadout devices approve " + e.Name + " --rotate <recipient>)"
		}
		line := fmt.Sprintf("%s — %s", e.Name, status)
		if e.Role != "" {
			line += " (" + e.Role + ")"
		}
		fmt.Fprintln(out, line)
	}
	return 0
}

// devicesApproveResult is the JSON shape of "loadout devices approve
// <name>".
type devicesApproveResult struct {
	Name      string `json:"name"`
	Recipient string `json:"recipient"`
	Already   bool   `json:"already_approved"`
	Rotated   bool   `json:"rotated"`
	// Role is the role now on record for name: "full" or
	// "no-secrets".
	Role string `json:"role"`
	// RoleChanged is true when this call changed an existing
	// approval's role (the recipient stayed the same, only the role
	// flipped) rather than adding, matching, or rotating.
	RoleChanged bool `json:"role_changed"`
}

// cmdDevicesApprove approves a waiting device, or — with --rotate
// <recipient> — deliberately replaces an already-approved device's
// key with an admin-supplied, out-of-band-verified one.
func cmdDevicesApprove(out, errOut io.Writer, args []string, m mode) int {
	name, rotateRecipient, rotate, noSecrets, full, ok := parseApproveArgs(args)
	if !ok {
		fmt.Fprintln(errOut, devicesUsage)
		return 2
	}

	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	printWarnings(errOut, v)

	var result approveResult
	if rotate {
		// rolePref is "" (keep the device's existing role) unless the
		// admin's own explicit --no-secrets or --full argument says
		// otherwise: a rotation must never change a role the admin
		// did not ask to change.
		rolePref := ""
		switch {
		case noSecrets:
			rolePref = vault.RoleNoSecrets
		case full:
			rolePref = vault.RoleFull
		}
		// rotateDevice never reads the remote's live roster: see its
		// own doc comment for why that matters.
		result, err = rotateDevice(v, name, rotateRecipient, rolePref)
	} else {
		role := vault.RoleFull
		if noSecrets {
			role = vault.RoleNoSecrets
		}
		client, cerr := remoteClient(v)
		if cerr != nil {
			fmt.Fprintln(errOut, cerr)
			return 1
		}
		serverDevices, cerr := client.ListDevices()
		if cerr != nil {
			fmt.Fprintln(errOut, cerr)
			return 1
		}
		recipient, found := findDeviceRecipient(serverDevices, name)
		if !found {
			fmt.Fprintf(errOut, "%s: no such device on the remote. Fix: run loadout devices to see who is waiting.\n", name)
			return 1
		}
		result, err = approvePlain(v, name, recipient, role)
	}
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}

	if result.kind == approveMismatchBlocked {
		fmt.Fprintf(errOut,
			"%s is already approved with a different key. This is a re-keyed device or an imposter.\nstored:  %s\noffered: %s\nFix: verify the correct recipient out-of-band (run loadout device on that machine), then run loadout devices approve %s --rotate <recipient> with the value you trust — never the remote's own live value.\n",
			name, result.stored, result.recipient, name)
		return 1
	}

	// A secret ReEncryptSecrets could not re-encrypt for the new
	// device is a warning, never a reason to block enrollment: partial
	// success (every OTHER secret and the whole roster change) beats
	// leaving the device unapproved. It names only the secrets, never
	// a value (INVARIANT 10).
	if len(result.skippedSecrets) > 0 {
		fmt.Fprintf(errOut,
			"warning: could not re-encrypt these secrets for the new device (this device cannot read them): %s. Fix: re-run approve from a device that can read them.\n",
			strings.Join(result.skippedSecrets, ", "))
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

	// remote.Sync just pulled and merged the remote's own current
	// devices.toml, under whole-file last-write-wins: a concurrent
	// sync from another device, racing this approval, can silently
	// overwrite this exact roster entry with its own (a real,
	// confirmed merge, reporting no error at all) before this command
	// ever gets to report success. Re-read the roster now, and never
	// celebrate an approval the merge just dropped.
	roster, err := vault.ReadRoster(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if roster[name] != result.recipient {
		fmt.Fprintf(errOut, "the approval of %s was overridden by a concurrent sync. Fix: run loadout devices approve %s again.\n", name, name)
		return 1
	}

	// Extra guarantee for a no-secrets outcome specifically (the
	// Critical finding's own belt-and-suspenders): remote.Sync above
	// may have pulled and merged a concurrent change, and its own
	// merge path already reconciles every secret to the merged roster
	// whenever this device is full (see internal/remote/sync.go's
	// pullMergePush). This call site checks it again anyway, rather
	// than trust that path alone: re-run ReEncryptSecrets against the
	// roster as it now stands, snapshot, and push the result, so the
	// final state on the remote can never retain the just-demoted
	// device as a secret's recipient. This is idempotent — a no-op
	// write in effect when nothing needed fixing — and correct over
	// clever: it costs one more encrypt-and-sync round on a rare admin
	// action, never a hot loop.
	if result.role == vault.RoleNoSecrets {
		self, err := vault.SelfRole(v)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		if self == vault.RoleFull {
			if _, err := vault.ReEncryptSecrets(v); err != nil {
				fmt.Fprintln(errOut, err)
				return 1
			}
			if err := vault.Snapshot(v, "reconcile secrets after "+name+"'s no-secrets approval"); err != nil {
				fmt.Fprintln(errOut, err)
				return 1
			}
			if _, err := remote.Sync(v); err != nil {
				fmt.Fprintln(errOut, err)
				return 1
			}
		}
	}

	if m == modeJSON {
		printJSON(out, devicesApproveResult{
			Name:        name,
			Recipient:   result.recipient,
			Already:     result.kind == approveAlreadyMatches,
			Rotated:     result.kind == approveRotated,
			Role:        result.role,
			RoleChanged: result.kind == approveRoleChanged,
		})
		return 0
	}
	switch result.kind {
	case approveAlreadyMatches:
		fmt.Fprintf(out, "%s is already approved.\n", name)
	case approveRoleChanged:
		fmt.Fprintf(out, "changed %s's role to %s.\n", name, result.role)
	case approveRotated:
		if result.role == vault.RoleNoSecrets {
			fmt.Fprintf(out, "rotated %s to %s (no-secrets).\n", name, result.recipient)
		} else {
			fmt.Fprintf(out, "rotated %s to %s.\n", name, result.recipient)
		}
	default:
		if result.role == vault.RoleNoSecrets {
			fmt.Fprintf(out, "approved %s (%s) as no-secrets. Run loadout sync --remote on that device now.\n", name, result.recipient)
		} else {
			fmt.Fprintf(out, "approved %s (%s). Run loadout sync --remote on that device now.\n", name, result.recipient)
		}
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

// approveKind enumerates what approvePlain or rotateDevice actually
// did to the vault's device roster.
type approveKind int

const (
	// approveAdded is a brand-new roster entry: the first approval of
	// this name.
	approveAdded approveKind = iota
	// approveAlreadyMatches means the roster already held this exact
	// name and recipient: no roster mutation happened.
	approveAlreadyMatches
	// approveMismatchBlocked means the roster already held this name
	// under a different recipient than the one approvePlain fetched
	// from the remote: no roster mutation happened, and the caller
	// must treat this as an error. Only rotateDevice, given an
	// explicit, admin-supplied recipient, may ever replace an
	// existing entry.
	approveMismatchBlocked
	// approveRotated means rotateDevice wrote name's roster entry to
	// an admin-supplied recipient.
	approveRotated
	// approveRoleChanged means the roster already held this exact
	// name and recipient, but under a different role: approvePlain
	// updated the role in place (AddToRoster), re-encrypted every
	// secret to the new roster, and snapshotted — a role flip on
	// re-approve is never silently ignored.
	approveRoleChanged
)

// approveResult is what approvePlain or rotateDevice returns:
// outcome kind, plus the recipient now on record (or, for a blocked
// mismatch, the two recipients in conflict) — always shown in full to
// the caller, since a public key is exactly the value an admin must
// be able to verify out-of-band.
type approveResult struct {
	kind approveKind
	// recipient is the recipient now stored for name (approveAdded,
	// approveAlreadyMatches, approveRotated), or the offered
	// recipient approvePlain refused to silently adopt
	// (approveMismatchBlocked).
	recipient string
	// stored is only set for approveMismatchBlocked: the recipient
	// devices.toml already held for name.
	stored string
	// role is the role now on record for name: vault.RoleFull or
	// vault.RoleNoSecrets. Set for every outcome except
	// approveMismatchBlocked, which changes nothing.
	role string
	// skippedSecrets names every secret ReEncryptSecrets could not
	// re-encrypt for the new roster (this device could not decrypt
	// it), set only for approveAdded and approveRotated — the two
	// outcomes that actually change the roster. Names only, never a
	// value: INVARIANT 10.
	skippedSecrets []string
}

// invalidRecipientErr is the fixed error approvePlain and
// rotateDevice return when the recipient they were given to write is
// not a valid age X25519 recipient. The server itself now refuses to
// store such a value at registration time (see handleUpsertDevice),
// but this check stays here too as a second, independent layer: a
// garbage or malicious value must never enter devices.toml, however
// routine the approval that would otherwise commit it — PackSnapshot
// would then fail on every future snapshot, for every device, until
// someone edits devices.toml by hand.
func invalidRecipientErr(name string) error {
	return fmt.Errorf("%s: the remote gave an invalid recipient key. Fix: that device must run loadout join again; do not approve it until it registers a valid key.", name)
}

// approvePlain adds name, with role (vault.RoleFull or
// vault.RoleNoSecrets), to the vault's device roster using recipient
// fetched from the remote's bootstrap roster — the first-approval
// path, under the vault lock.
//
//   - When the roster does not yet list name, it validates recipient,
//     adds it under role (and — when the roster was still empty — the
//     approving device's own identity too, always as RoleFull, so the
//     roster's first write never drops the one device that is calling
//     it: without this, PackSnapshot would stop encrypting to this
//     device on its very next snapshot, since it already encrypts to
//     this device alone while the roster is empty), snapshots, and
//     reports approveAdded.
//   - When the roster already lists name with this exact recipient
//     AND this exact role, it changes nothing and reports
//     approveAlreadyMatches.
//   - When the roster already lists name with this exact recipient
//     but a DIFFERENT role, it updates the role in place, re-encrypts
//     every secret to the roster as it now stands (so the role change
//     actually takes effect: a device just demoted to no-secrets must
//     drop out of every secret's recipients, and one just promoted to
//     full must join them), snapshots, and reports
//     approveRoleChanged. A role flip on re-approve is never silently
//     ignored.
//   - When the roster already lists name under a different recipient,
//     it changes nothing and reports approveMismatchBlocked: this
//     function must never overwrite an existing entry with a value it
//     only ever read from the remote's own bootstrap roster. That
//     roster is writable by anyone holding the bearer token,
//     including a device whose trust was just revoked — silently
//     trusting it here would let a rotation be reverted just by an
//     evicted device re-registering itself. Only rotateDevice, given
//     an explicit, human-verified recipient, may replace an existing
//     entry.
func approvePlain(v *vault.Vault, name, recipient, role string) (approveResult, error) {
	release, err := vault.Lock(v)
	if err != nil {
		return approveResult{}, err
	}
	defer release()

	roster, err := vault.ReadRosterEntries(v)
	if err != nil {
		return approveResult{}, err
	}

	if existing, ok := roster[name]; ok {
		if existing.Recipient != recipient {
			return approveResult{kind: approveMismatchBlocked, recipient: recipient, stored: existing.Recipient}, nil
		}
		if existing.Role == role {
			// The roster already agrees, but that alone does not prove
			// this name's earlier approval ever finished: AddToRoster
			// persists the roster BEFORE ReEncryptSecrets runs, so a
			// prior call that crashed (or whose ReEncryptSecrets call
			// itself failed) between those two steps leaves exactly
			// this state behind — a roster that already matches, with
			// some secret still unreadable to the newcomer. Re-running
			// ReEncryptSecrets here is how a retry actually completes
			// that interrupted approval; it is safe and idempotent in
			// effect even when nothing was actually interrupted; the
			// only visible cost is a re-encrypted (but equally
			// decryptable) value.age and one more snapshot, and
			// "devices approve" is a rare admin action, not a hot loop.
			skipped, err := vault.ReEncryptSecrets(v)
			if err != nil {
				return approveResult{}, err
			}
			if err := vault.Snapshot(v, "approve device "+name); err != nil {
				return approveResult{}, err
			}
			return approveResult{kind: approveAlreadyMatches, recipient: existing.Recipient, role: existing.Role, skippedSecrets: skipped}, nil
		}
		// A role change on re-approve: the recipient is the same
		// trusted device, only its role flips. Write the new role,
		// then re-encrypt before this snapshot, so the role change
		// lands in secrets, not just in devices.toml.
		if err := vault.AddToRoster(v, name, recipient, role); err != nil {
			return approveResult{}, err
		}
		skipped, err := vault.ReEncryptSecrets(v)
		if err != nil {
			return approveResult{}, err
		}
		if err := vault.Snapshot(v, "change device "+name+" role to "+role); err != nil {
			return approveResult{}, err
		}
		return approveResult{kind: approveRoleChanged, recipient: recipient, role: role, skippedSecrets: skipped}, nil
	}

	if _, err := age.ParseX25519Recipient(recipient); err != nil {
		return approveResult{}, invalidRecipientErr(name)
	}
	if len(roster) == 0 {
		ownName, ownRecipient, err := vault.DeviceIdentity(v)
		if err != nil {
			return approveResult{}, err
		}
		if err := vault.AddToRoster(v, ownName, ownRecipient, vault.RoleFull); err != nil {
			return approveResult{}, err
		}
	}
	if err := vault.AddToRoster(v, name, recipient, role); err != nil {
		return approveResult{}, err
	}
	// Re-encrypt every secret to the roster as it now stands — the new
	// device included, at its own role — BEFORE this snapshot: that is
	// what makes the re-encrypted value.age files part of the same
	// commit the caller's remote.Sync is about to push. A no-secrets
	// device is excluded here exactly as secretRecipients (Task 2)
	// defines: this is the choke point that keeps it that way.
	skipped, err := vault.ReEncryptSecrets(v)
	if err != nil {
		return approveResult{}, err
	}
	if err := vault.Snapshot(v, "approve device "+name); err != nil {
		return approveResult{}, err
	}
	return approveResult{kind: approveAdded, recipient: recipient, role: role, skippedSecrets: skipped}, nil
}

// rotateDevice sets name's roster entry to recipient — a value the
// caller (cmdDevicesApprove, from an admin-typed --rotate argument)
// must already have verified out-of-band, for example by reading it
// directly from that device's own "loadout device" output — under
// the vault lock.
//
// rolePref chooses name's role after the rotation: vault.RoleFull or
// vault.RoleNoSecrets to set it explicitly (from an admin's own
// --full or --no-secrets argument), or "" to keep name's EXISTING
// role unchanged — a plain rotation of a device's key must never
// silently flip whether it can read secrets. A name the roster does
// not list yet (rotating a brand-new name straight to a chosen key,
// bypassing approvePlain's remote lookup) defaults to vault.RoleFull
// when rolePref is "", the same default a fresh approvePlain-driven
// approval uses.
//
// It never consults the remote's live bootstrap roster for the
// recipient, and never calls findDeviceRecipient: that roster is
// writable by anyone holding the bearer token, including a device
// whose trust was just revoked. Sync's own registration guard (see
// remote.deviceEstablished) already stops an evicted device from
// re-asserting its old recipient there once evicted, but rotateDevice
// does not rely on that either — an admin's own explicit argument is
// the only source of truth a rotation ever trusts.
func rotateDevice(v *vault.Vault, name, recipient, rolePref string) (approveResult, error) {
	if _, err := age.ParseX25519Recipient(recipient); err != nil {
		return approveResult{}, invalidRecipientErr(name)
	}

	release, err := vault.Lock(v)
	if err != nil {
		return approveResult{}, err
	}
	defer release()

	roster, err := vault.ReadRosterEntries(v)
	if err != nil {
		return approveResult{}, err
	}
	if len(roster) == 0 {
		ownName, ownRecipient, err := vault.DeviceIdentity(v)
		if err != nil {
			return approveResult{}, err
		}
		if err := vault.AddToRoster(v, ownName, ownRecipient, vault.RoleFull); err != nil {
			return approveResult{}, err
		}
	}

	role := rolePref
	if role == "" {
		if existing, ok := roster[name]; ok {
			role = existing.Role
		} else {
			role = vault.RoleFull
		}
	}

	if err := vault.AddToRoster(v, name, recipient, role); err != nil {
		return approveResult{}, err
	}
	// Same ordering as approvePlain: re-encrypt to the post-rotation
	// roster before this snapshot, so the rotated-in key's ciphertext
	// travels in the same commit the caller's remote.Sync pushes.
	skipped, err := vault.ReEncryptSecrets(v)
	if err != nil {
		return approveResult{}, err
	}
	if err := vault.Snapshot(v, "rotate device "+name+" key"); err != nil {
		return approveResult{}, err
	}
	return approveResult{kind: approveRotated, recipient: recipient, role: role, skippedSecrets: skipped}, nil
}
