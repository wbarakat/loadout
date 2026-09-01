# Install loadoutd on a Raspberry Pi

This guide sets up `loadoutd` on a Raspberry Pi, reachable over
Tailscale. Run these steps yourself, on your own machine and your own
Pi. Nothing in this repository runs them for you.

## 1. Cross-compile loadoutd for the Pi

On your Mac, build a Linux ARM64 binary. Run this from the repo root:

    GOOS=linux GOARCH=arm64 go build -o loadoutd-arm64 ./cmd/loadoutd

This produces one static binary, `loadoutd-arm64`. It needs no other
files on the Pi.

## 2. Copy the binary to the Pi

Copy it over SSH. Replace `pi-host` with your Pi's hostname or IP
address:

    scp loadoutd-arm64 pi-host:/tmp/loadoutd

Then move it into place and make it executable:

    ssh pi-host 'sudo mv /tmp/loadoutd /usr/local/bin/loadoutd && sudo chmod +x /usr/local/bin/loadoutd'

## 3. Create the data directory

`loadoutd` stores its encrypted blobs and its access token under one
data directory. Create it on the Pi, owned by the user that runs the
service:

    ssh pi-host 'sudo mkdir -p /var/lib/loadout && sudo chown pi:pi /var/lib/loadout'

Replace `pi` with the actual user, if different.

## 4. Add a systemd unit

Create `/etc/systemd/system/loadoutd.service` on the Pi, with this
content:

    [Unit]
    Description=loadout sync server
    After=network.target

    [Service]
    Type=simple
    ExecStart=/usr/local/bin/loadoutd -data /var/lib/loadout -addr :7777
    Restart=always
    RestartSec=5
    User=pi

    [Install]
    WantedBy=multi-user.target

Replace `User=pi` with the actual user. `Restart=always` brings
`loadoutd` back up after a crash or a reboot.

Enable and start the service:

    sudo systemctl daemon-reload
    sudo systemctl enable --now loadoutd

## 5. Read the access token

`loadoutd` prints its access token once, on its very first start, to
its own stdout. Read it from the service journal:

    sudo journalctl -u loadoutd -n 50 | grep 'access token'

Copy this token now. It never prints again. Store it somewhere safe;
you need it on every device you enroll.

## 6. Reach the Pi over Tailscale

Join the Pi to your tailnet, if it is not already:

    curl -fsSL https://tailscale.com/install.sh | sh
    sudo tailscale up

Find the Pi's tailnet name:

    tailscale status

Use that name, not the Pi's LAN IP address, from every other device.
A tailnet name keeps working across networks; a LAN IP does not.

## 7. Connect a device to the Pi

On any machine already on your tailnet, point loadout at the Pi:

    loadout remote add http://<pi-tailnet-name>:7777 <token>
    loadout sync --remote

The first device to run this becomes the vault's first trusted
device. Enroll every other device with `loadout join`, as the main
README's "Sync across your machines" section describes.

## Checks

- `sudo systemctl status loadoutd` shows the service as active.
- `curl http://<pi-tailnet-name>:7777/v1/snapshots/latest -H "Authorization: Bearer <token>"` returns a JSON response, not a connection error.
- `loadout status` on a connected device shows a `remote:` line in state "in sync" or "ahead", never "unreachable".
