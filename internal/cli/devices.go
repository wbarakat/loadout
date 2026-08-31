package cli

import (
	"fmt"
	"io"
	"sort"

	"loadout.dev/loadout/internal/remote"
	"loadout.dev/loadout/internal/vault"
)

const devicesUsage = "usage: loadout devices [--json] | loadout devices approve <name>"

// cmdDevices dispatches "loadout devices" (the bare list) and
// "loadout devices approve <name>".
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

// deviceEntry is one device's row in "loadout devices": the remote's
// bootstrap roster entry, plus whether this vault's own devices.toml
// approves it.
type deviceEntry struct {
	Name      string `json:"name"`
	Recipient string `json:"recipient"`
	Approved  bool   `json:"approved"`
}

// devicesListResult is the JSON shape of "loadout devices".
type devicesListResult struct {
	Devices []deviceEntry `json:"devices"`
}

// cmdDevicesList merges the remote's bootstrap device roster with
// this vault's own devices.toml: a device is "approved" when its
// recipient is one PackSnapshot currently encrypts to, "waiting"
// otherwise.
//
// A vault whose own devices.toml is still absent or empty has not
// explicitly approved anyone yet, but PackSnapshot already encrypts
// to this device alone in that state (see packRecipients): this
// device is its own bootstrap approval, with no explicit entry
// needed, so it is reported as approved here too.
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
	ownName, _, err := vault.DeviceIdentity(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}

	entries := make([]deviceEntry, 0, len(serverDevices))
	for _, d := range serverDevices {
		_, listed := roster[d.Name]
		approved := listed || (len(roster) == 0 && d.Name == ownName)
		entries = append(entries, deviceEntry{Name: d.Name, Recipient: d.Recipient, Approved: approved})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	if m == modeJSON {
		printJSON(out, devicesListResult{Devices: entries})
		return 0
	}
	for _, e := range entries {
		status := "waiting"
		if e.Approved {
			status = "approved"
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
}

// cmdDevicesApprove approves a waiting device: it fetches the
// device's recipient from the remote's bootstrap roster, adds it to
// this vault's devices.toml, snapshots the change, then syncs with
// the remote so the next snapshot encrypts to the newcomer too.
func cmdDevicesApprove(out, errOut io.Writer, args []string, m mode) int {
	if len(args) != 1 || args[0] == "" {
		fmt.Fprintln(errOut, devicesUsage)
		return 2
	}
	name := args[0]

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

	already, err := approveDevice(v, name, recipient)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if !already {
		if _, err := remote.Sync(v); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
	}

	if m == modeJSON {
		printJSON(out, devicesApproveResult{Name: name, Already: already})
		return 0
	}
	if already {
		fmt.Fprintf(out, "%s is already approved.\n", name)
		return 0
	}
	fmt.Fprintf(out, "approved %s. Run loadout sync --remote on that device now.\n", name)
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

// approveDevice adds name and recipient to the vault's device roster
// and snapshots the change, under the vault lock. When the roster
// already lists name with this exact recipient, it changes nothing
// and reports already as true.
//
// When the roster is still empty — nobody has been explicitly
// approved yet, and PackSnapshot has been encrypting to this device
// alone (see packRecipients) — this also adds the approving device's
// own identity, alongside the newcomer's. Without that, the roster's
// first write would hold only the newcomer, and PackSnapshot would
// stop encrypting to this device on its very next snapshot: the
// device running the approval would lock itself out.
func approveDevice(v *vault.Vault, name, recipient string) (already bool, err error) {
	release, err := vault.Lock(v)
	if err != nil {
		return false, err
	}
	defer release()

	roster, err := vault.ReadRoster(v)
	if err != nil {
		return false, err
	}
	if existing, ok := roster[name]; ok && existing == recipient {
		return true, nil
	}

	if len(roster) == 0 {
		ownName, ownRecipient, err := vault.DeviceIdentity(v)
		if err != nil {
			return false, err
		}
		if err := vault.AddToRoster(v, ownName, ownRecipient); err != nil {
			return false, err
		}
	}
	if err := vault.AddToRoster(v, name, recipient); err != nil {
		return false, err
	}
	if err := vault.Snapshot(v, "approve device "+name); err != nil {
		return false, err
	}
	return false, nil
}
