package cli

import (
	"fmt"
	"io"

	"loadout.dev/loadout/internal/vault"
)

// deviceResult is the JSON shape of "loadout device".
type deviceResult struct {
	Name      string `json:"name"`
	Recipient string `json:"recipient"`
}

// cmdDevice prints this device's name and its age recipient, creating
// the device identity on first call. It never prints the device key
// itself: only the recipient, which is safe to share.
func cmdDevice(out, errOut io.Writer, m mode) int {
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	printWarnings(errOut, v)
	name, recipient, err := vault.DeviceIdentity(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if m == modeJSON {
		printJSON(out, deviceResult{Name: name, Recipient: recipient})
		return 0
	}
	fmt.Fprintf(out, "device: %s\nrecipient: %s\n", name, recipient)
	return 0
}
