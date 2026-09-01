#!/usr/bin/env bash
# scripts/interop-test.sh
#
# The Phase 8b acceptance gate: proves the TS vault-client library
# interoperates with the real Go loadoutd/CLI on the wire (age blob, ustar
# tar, push protocol), and that the no-secrets guarantee holds across the
# language boundary.
#
# Run this from the repo root:
#   bash scripts/interop-test.sh
#
# It runs three steps, in order, each in its own process:
#   1. Go writes fixtures for TS to read (a temp vault, a three-device
#      roster, one secret encrypted to the full devices only).
#   2. TS reads those fixtures, proves the no-secrets guarantee on its own
#      side, edits one memory item, and writes a snapshot Go must accept.
#   3. Go reads that TS-produced snapshot, proves the edit landed, every
#      secret byte survived unchanged, and the no-secrets guarantee still
#      holds after the round trip.
#
# SANDBOX: every step here touches only web/lib/vault/testdata/ (gitignored)
# and Go/npm's own temp directories. Nothing here ever reads or writes a
# real ~/.loadout.
#
# Exits non-zero on the first failing step.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

testdata_dir="web/lib/vault/testdata"

banner() {
  echo ""
  echo "==> $1"
}

fail() {
  echo ""
  echo "FAIL: $1"
  exit 1
}

# A clean slate: every run of the gate proves the interop for real, not
# against ciphertext a previous run happened to leave behind. This
# directory is gitignored and holds only generated fixtures.
banner "Clearing $testdata_dir for a fresh run"
rm -rf "$testdata_dir"

banner "Step 1/3: Go writes fixtures for TS (TestWriteFixturesForTS)"
if ! go test ./internal/interop -run TestWriteFixturesForTS -count=1 -v; then
  fail "step 1 (Go writes fixtures) failed"
fi

banner "Step 2/3: TS reads the Go fixtures and writes a snapshot back (npm test -- interop)"
if ! npm --prefix web test -- interop; then
  fail "step 2 (TS reads Go fixtures, writes ts-snapshot.age) failed"
fi

banner "Step 3/3: Go reads the TS-produced snapshot (TestGoReadsTSSnapshot)"
if ! go test ./internal/interop -run TestGoReadsTSSnapshot -count=1 -v; then
  fail "step 3 (Go reads the TS snapshot) failed"
fi

echo ""
echo "============================================================"
echo "PASS: Go <-> TS snapshot interop gate is green."
echo "  - Go wrote a snapshot + secret TS could read and decrypt."
echo "  - TS wrote a snapshot Go's real UnpackSnapshot/DecryptSecret accept."
echo "  - The no-secrets guarantee held on both sides, before and after"
echo "    the round trip through the TS tar writer."
echo "============================================================"
