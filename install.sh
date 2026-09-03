#!/bin/sh
# Install the latest Loadout release binaries (loadout and loadoutd).
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/wbarakat/loadout/main/install.sh | sh
#
# Override the install directory with LOADOUT_BIN, for example:
#   curl -fsSL .../install.sh | LOADOUT_BIN="$HOME/bin" sh
set -eu

REPO="wbarakat/loadout"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"

case "$arch" in
  x86_64 | amd64) arch="amd64" ;;
  arm64 | aarch64) arch="arm64" ;;
  *)
    echo "loadout: unsupported architecture: $arch" >&2
    echo "Build from source instead: https://github.com/${REPO}" >&2
    exit 1
    ;;
esac

case "$os" in
  darwin | linux) ;;
  *)
    echo "loadout: unsupported OS: $os" >&2
    echo "Build from source instead: https://github.com/${REPO}" >&2
    exit 1
    ;;
esac

asset="loadout_${os}_${arch}.tar.gz"
url="https://github.com/${REPO}/releases/latest/download/${asset}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading ${asset} ..."
if ! curl -fsSL "$url" -o "$tmp/$asset"; then
  echo "loadout: could not download $url" >&2
  echo "Check that a release exists at https://github.com/${REPO}/releases" >&2
  exit 1
fi

tar -xzf "$tmp/$asset" -C "$tmp"

# Pick an install directory: LOADOUT_BIN, then /usr/local/bin if
# writable, then ~/.local/bin.
dir="${LOADOUT_BIN:-}"
if [ -z "$dir" ]; then
  if [ -w /usr/local/bin ]; then
    dir="/usr/local/bin"
  else
    dir="$HOME/.local/bin"
  fi
fi
mkdir -p "$dir"

install -m 0755 "$tmp/loadout" "$dir/loadout"
if [ -f "$tmp/loadoutd" ]; then
  install -m 0755 "$tmp/loadoutd" "$dir/loadoutd"
fi

echo "Installed loadout to $dir"
case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "Add $dir to your PATH, then run: loadout init" ;;
esac
echo "Next: loadout init"
