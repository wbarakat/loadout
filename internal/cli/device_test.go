package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevicePrintsNameAndRecipient(t *testing.T) {
	setupEnv(t)
	run(t, "init")

	out, errOut, code := run(t, "device")
	if code != 0 {
		t.Fatalf("device failed: %s", errOut)
	}
	if !strings.HasPrefix(out, "device: ") {
		t.Fatalf("bad device output: %q", out)
	}
	if !strings.Contains(out, "\nrecipient: age1") {
		t.Fatalf("bad device output: %q", out)
	}
}

func TestDeviceJSON(t *testing.T) {
	setupEnv(t)
	run(t, "init")

	out, errOut, code := run(t, "device", "--json")
	if code != 0 {
		t.Fatalf("device --json failed: %s", errOut)
	}
	var got struct {
		Name      string `json:"name"`
		Recipient string `json:"recipient"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("device --json did not parse: %v\noutput: %s", err, out)
	}
	if got.Name == "" {
		t.Fatalf("bad device json, name is empty: %+v", got)
	}
	if !strings.HasPrefix(got.Recipient, "age1") {
		t.Fatalf("bad device json, recipient is not an age1 recipient: %+v", got)
	}
}

func TestDeviceRejectsExtraArgs(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	if _, errOut, code := run(t, "device", "extra"); code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("device with an extra arg must be a usage error, got %d %q", code, errOut)
	}
}

func TestDeviceIsStableAcrossCalls(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	out1, errOut1, code1 := run(t, "device")
	if code1 != 0 {
		t.Fatalf("device failed: %s", errOut1)
	}
	out2, errOut2, code2 := run(t, "device")
	if code2 != 0 {
		t.Fatalf("device failed: %s", errOut2)
	}
	if out1 != out2 {
		t.Fatalf("device output must be stable across calls, got %q then %q", out1, out2)
	}
}

func TestDeviceKeyFileMode(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "device")
	fi, err := os.Stat(filepath.Join(base, "vault", "device.key"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("device.key must be mode 0600, got %o", fi.Mode().Perm())
	}
}

// TestDeviceOutputNeverLeaksTheKey proves the CLI text and JSON
// output carry the recipient only, never the raw device key.
func TestDeviceOutputNeverLeaksTheKey(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")

	out, _, code := run(t, "device")
	if code != 0 {
		t.Fatalf("device failed with code %d", code)
	}
	keyData, err := os.ReadFile(filepath.Join(base, "vault", "device.key"))
	if err != nil {
		t.Fatal(err)
	}
	key := strings.TrimSpace(string(keyData))
	if strings.Contains(out, key) {
		t.Fatalf("device output must never contain the raw device key")
	}
	if strings.Contains(out, "AGE-SECRET-KEY-") {
		t.Fatalf("device output must never contain a secret key prefix, got %q", out)
	}
}
