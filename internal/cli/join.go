package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"loadout.dev/loadout/internal/remote"
	"loadout.dev/loadout/internal/vault"
)

const joinUsage = "usage: loadout join <url> <token>"

// joinResult is the JSON shape of "loadout join". It never carries
// the token.
type joinResult struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// cmdJoin enrolls this machine with a remote. It creates the vault
// when none exists yet, writes remote.toml, creates this device's
// identity, and registers the device's name and recipient with the
// remote's bootstrap roster. It never syncs: a freshly enrolled
// device cannot decrypt the vault's content until an already-approved
// device adds it to devices.toml and syncs. The token never appears
// in any output this prints.
func cmdJoin(out, errOut io.Writer, args []string, m mode) int {
	if len(args) != 2 || args[0] == "" || args[1] == "" {
		fmt.Fprintln(errOut, joinUsage)
		return 2
	}
	rawURL, token := args[0], args[1]
	if err := validateRemoteURL(rawURL); err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}

	v, err := openOrInitVault(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	printWarnings(errOut, v)

	if err := remote.Save(v, &remote.Config{URL: rawURL, Token: token}); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}

	name, recipient, err := vault.DeviceIdentity(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}

	client := remote.NewClient(rawURL, token)
	if err := client.RegisterDevice(name, recipient); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}

	if m == modeJSON {
		printJSON(out, joinResult{Name: name, URL: rawURL})
		return 0
	}
	fmt.Fprintf(out, "this device is enrolled as %s and waits for an approval.\n", name)
	fmt.Fprintf(out, "next: on an already-approved device run: loadout devices approve %s.\n", name)
	fmt.Fprintln(out, "then run: loadout sync --remote here.")
	return 0
}

// openOrInitVault opens the vault at root, initializing a fresh one
// when none exists yet. "loadout join" is the one command that may
// run on a brand-new machine with no vault at all.
func openOrInitVault(root string) (*vault.Vault, error) {
	if _, err := os.Stat(filepath.Join(root, "loadout.toml")); err != nil {
		if os.IsNotExist(err) {
			return vault.Init(root)
		}
		return nil, err
	}
	return vault.Open(root)
}
