package vault

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

// newSnapshotTestVault builds a fresh vault with a skill file and a
// memory file, so packTar and PackSnapshot always have something to
// carry.
func newSnapshotTestVault(t *testing.T) *Vault {
	t.Helper()
	root := filepath.Join(t.TempDir(), "vault")
	v, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(v.SkillsDir(), "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: my-skill\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v.MemoryDir(), "fact.md"), []byte("a fact"), 0o644); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestPackTarIsByteIdenticalAcrossCalls proves the tar layer is
// deterministic: two packs of the same content, taken back to back,
// must match byte for byte before age ever touches them.
func TestPackTarIsByteIdenticalAcrossCalls(t *testing.T) {
	v := newSnapshotTestVault(t)
	first, err := packTar(v)
	if err != nil {
		t.Fatal(err)
	}
	second, err := packTar(v)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("packTar must be byte-identical across calls on unchanged content")
	}
}

// TestPackTarStoresSymlinkWithoutFollowing proves a symlink inside a
// synced path is stored as a symlink entry, never followed into its
// target's content.
func TestPackTarStoresSymlinkWithoutFollowing(t *testing.T) {
	v := newSnapshotTestVault(t)
	link := filepath.Join(v.SkillsDir(), "my-skill", "linked.md")
	if err := os.Symlink("../../memory/fact.md", link); err != nil {
		t.Fatal(err)
	}
	data, err := packTar(v)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(bytes.NewReader(data))
	found := false
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		if hdr.Name == "skills/my-skill/linked.md" {
			found = true
			if hdr.Typeflag != tar.TypeSymlink {
				t.Fatalf("linked.md must be stored as a symlink, got typeflag %v", hdr.Typeflag)
			}
			if hdr.Linkname != "../../memory/fact.md" {
				t.Fatalf("Linkname = %q, want %q", hdr.Linkname, "../../memory/fact.md")
			}
		}
	}
	if !found {
		t.Fatal("packTar must include the symlink entry")
	}
}

// TestPackAndUnpackRoundTrip proves a pack, followed by an unpack,
// reproduces every file's content and mode, and that PackSnapshot's
// headHash names the commit that pinned that content.
func TestPackAndUnpackRoundTrip(t *testing.T) {
	v := newSnapshotTestVault(t)
	scriptPath := filepath.Join(v.SkillsDir(), "my-skill", "run.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(scriptPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Snapshot(v, "add a skill"); err != nil {
		t.Fatal(err)
	}
	wantHash, err := headHash(v)
	if err != nil {
		t.Fatal(err)
	}

	blob, gotHash, err := PackSnapshot(v)
	if err != nil {
		t.Fatal(err)
	}
	if gotHash != wantHash {
		t.Fatalf("headHash = %q, want %q", gotHash, wantHash)
	}

	dir := t.TempDir()
	if err := UnpackSnapshot(v, blob, dir); err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{
		filepath.Join("skills", "my-skill", "SKILL.md"),
		filepath.Join("skills", "my-skill", "run.sh"),
		filepath.Join("memory", "fact.md"),
	} {
		wantPath := filepath.Join(v.Root, rel)
		gotPath := filepath.Join(dir, rel)
		wantData, err := os.ReadFile(wantPath)
		if err != nil {
			t.Fatal(err)
		}
		gotData, err := os.ReadFile(gotPath)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if !bytes.Equal(wantData, gotData) {
			t.Fatalf("%s: content mismatch: got %q want %q", rel, gotData, wantData)
		}
		wantInfo, err := os.Stat(wantPath)
		if err != nil {
			t.Fatal(err)
		}
		gotInfo, err := os.Stat(gotPath)
		if err != nil {
			t.Fatal(err)
		}
		if wantInfo.Mode().Perm() != gotInfo.Mode().Perm() {
			t.Fatalf("%s: mode = %o, want %o", rel, gotInfo.Mode().Perm(), wantInfo.Mode().Perm())
		}
	}
}

// TestPackSnapshotOnNoHistoryVaultGivesFixedError proves PackSnapshot
// surfaces the same fixed error every other history command uses
// when the vault's .git directory is gone.
func TestPackSnapshotOnNoHistoryVaultGivesFixedError(t *testing.T) {
	v := newSnapshotTestVault(t)
	if err := os.RemoveAll(filepath.Join(v.Root, ".git")); err != nil {
		t.Fatal(err)
	}
	_, _, err := PackSnapshot(v)
	if err == nil {
		t.Fatal("PackSnapshot on a vault with no history must fail")
	}
	want := "the vault at " + v.Root + " has no history. Fix: run loadout doctor."
	if err.Error() != want {
		t.Fatalf("bad error: got %q want %q", err.Error(), want)
	}
}

// TestPackSnapshotEncryptsToEveryRosterRecipient proves a two-device
// roster produces a snapshot the second device's own identity can
// decrypt, never touching the first device's key.
func TestPackSnapshotEncryptsToEveryRosterRecipient(t *testing.T) {
	v := newSnapshotTestVault(t)
	ownRecipient, err := DeviceRecipient(v)
	if err != nil {
		t.Fatal(err)
	}
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := AddToRoster(v, "this-device", ownRecipient); err != nil {
		t.Fatal(err)
	}
	if err := AddToRoster(v, "other-device", other.Recipient().String()); err != nil {
		t.Fatal(err)
	}

	blob, _, err := PackSnapshot(v)
	if err != nil {
		t.Fatal(err)
	}

	r, err := age.Decrypt(bytes.NewReader(blob), other)
	if err != nil {
		t.Fatalf("the second device's identity must decrypt the snapshot: %v", err)
	}
	tr := tar.NewReader(r)
	found := false
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		if hdr.Name == "memory/fact.md" {
			found = true
		}
	}
	if !found {
		t.Fatal("the snapshot the second device decrypted must contain memory/fact.md")
	}
}

// TestUnpackSnapshotNotEnrolledGivesFixedError proves a device absent
// from the recipients a snapshot was packed for gets the fixed,
// friendly "cannot decrypt" error, not a raw age failure.
func TestUnpackSnapshotNotEnrolledGivesFixedError(t *testing.T) {
	v := newSnapshotTestVault(t)
	blob, _, err := PackSnapshot(v)
	if err != nil {
		t.Fatal(err)
	}

	other := filepath.Join(t.TempDir(), "other-vault")
	otherVault, err := Init(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := DeviceIdentity(otherVault); err != nil {
		t.Fatal(err)
	}

	err = UnpackSnapshot(otherVault, blob, t.TempDir())
	if err == nil {
		t.Fatal("UnpackSnapshot must fail for a device not among the recipients")
	}
	want := "this device cannot decrypt the snapshot. Fix: approve this device from an enrolled device, then sync again."
	if err.Error() != want {
		t.Fatalf("bad error: %v", err)
	}
}

// encryptForVault wraps plaintext in an age file encrypted to v's own
// device recipient, the same way PackSnapshot would, so the
// traversal tests below can hand UnpackSnapshot a crafted tar without
// going through packTar.
func encryptForVault(t *testing.T, v *Vault, plaintext []byte) []byte {
	t.Helper()
	identity, err := deviceKey(v)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, identity.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(plaintext); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// buildTar writes one tar entry per call to add, then closes the
// archive and returns its bytes.
func buildTar(t *testing.T, add func(tw *tar.Writer)) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	add(tw)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestUnpackSnapshotRefusesRelativeTraversal proves a tar entry named
// with a leading ".." never escapes the unpack directory.
func TestUnpackSnapshotRefusesRelativeTraversal(t *testing.T) {
	v := newSnapshotTestVault(t)
	tarData := buildTar(t, func(tw *tar.Writer) {
		body := []byte("pwned")
		hdr := &tar.Header{Name: "../evil.txt", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
		tw.WriteHeader(hdr)
		tw.Write(body)
	})
	blob := encryptForVault(t, v, tarData)

	dir := t.TempDir()
	err := UnpackSnapshot(v, blob, dir)
	if err == nil {
		t.Fatal("UnpackSnapshot must refuse a \"../\" entry name")
	}
	if !strings.Contains(err.Error(), "../evil.txt") || !strings.Contains(err.Error(), "the snapshot holds an unsafe path") {
		t.Fatalf("bad error: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(dir), "evil.txt")); statErr == nil {
		t.Fatal("UnpackSnapshot must not write outside dir")
	}
}

// TestUnpackSnapshotRefusesAbsolutePath proves a tar entry with an
// absolute name is refused outright.
func TestUnpackSnapshotRefusesAbsolutePath(t *testing.T) {
	v := newSnapshotTestVault(t)
	evilPath := filepath.Join(t.TempDir(), "evil.txt")
	tarData := buildTar(t, func(tw *tar.Writer) {
		body := []byte("pwned")
		hdr := &tar.Header{Name: evilPath, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
		tw.WriteHeader(hdr)
		tw.Write(body)
	})
	blob := encryptForVault(t, v, tarData)

	dir := t.TempDir()
	err := UnpackSnapshot(v, blob, dir)
	if err == nil {
		t.Fatal("UnpackSnapshot must refuse an absolute entry name")
	}
	if !strings.Contains(err.Error(), "the snapshot holds an unsafe path") {
		t.Fatalf("bad error: %v", err)
	}
	if _, statErr := os.Stat(evilPath); statErr == nil {
		t.Fatal("UnpackSnapshot must not write to the absolute path an entry names")
	}
}

// TestUnpackSnapshotRefusesEscapingSymlink proves a symlink entry
// whose target resolves outside dir is refused, and that no symlink
// is created at all.
func TestUnpackSnapshotRefusesEscapingSymlink(t *testing.T) {
	v := newSnapshotTestVault(t)
	tarData := buildTar(t, func(tw *tar.Writer) {
		hdr := &tar.Header{Name: "escape-link", Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: "../outside.txt"}
		tw.WriteHeader(hdr)
	})
	blob := encryptForVault(t, v, tarData)

	dir := t.TempDir()
	err := UnpackSnapshot(v, blob, dir)
	if err == nil {
		t.Fatal("UnpackSnapshot must refuse a symlink whose target escapes dir")
	}
	if !strings.Contains(err.Error(), "escape-link") || !strings.Contains(err.Error(), "the snapshot holds an unsafe path") {
		t.Fatalf("bad error: %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(dir, "escape-link")); statErr == nil {
		t.Fatal("UnpackSnapshot must not create the escaping symlink")
	}
	if _, statErr := os.Lstat(filepath.Join(filepath.Dir(dir), "outside.txt")); statErr == nil {
		t.Fatal("UnpackSnapshot must not write outside dir")
	}
}

// TestReadRosterAbsentFileIsEmpty proves a vault with no devices.toml
// yet reads back as an empty roster, not an error.
func TestReadRosterAbsentFileIsEmpty(t *testing.T) {
	v := newSnapshotTestVault(t)
	roster, err := ReadRoster(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(roster) != 0 {
		t.Fatalf("roster = %v, want empty", roster)
	}
}

// TestAddToRosterIsStableAndSorted proves the roster file AddToRoster
// writes is byte-identical regardless of the order entries were
// added in, since two devices sharing a roster must agree on it.
func TestAddToRosterIsStableAndSorted(t *testing.T) {
	v1 := newSnapshotTestVault(t)
	if err := AddToRoster(v1, "zzz-device", "age1zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzqqqqqqq"); err != nil {
		t.Fatal(err)
	}
	if err := AddToRoster(v1, "aaa-device", "age1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaqqqqqqq"); err != nil {
		t.Fatal(err)
	}

	v2 := newSnapshotTestVault(t)
	if err := AddToRoster(v2, "aaa-device", "age1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaqqqqqqq"); err != nil {
		t.Fatal(err)
	}
	if err := AddToRoster(v2, "zzz-device", "age1zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzqqqqqqq"); err != nil {
		t.Fatal(err)
	}

	data1, err := os.ReadFile(devicesTomlPath(v1))
	if err != nil {
		t.Fatal(err)
	}
	data2, err := os.ReadFile(devicesTomlPath(v2))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data1, data2) {
		t.Fatalf("devices.toml must be byte-identical regardless of add order:\n%s\n---\n%s", data1, data2)
	}

	roster, err := ReadRoster(v1)
	if err != nil {
		t.Fatal(err)
	}
	if len(roster) != 2 {
		t.Fatalf("roster = %v, want 2 entries", roster)
	}
}

// TestReadRosterMalformedFileGivesFixedError proves a devices.toml
// that fails to parse gets the fixed roster-error grammar, naming
// the path and telling the reader how to recover.
func TestReadRosterMalformedFileGivesFixedError(t *testing.T) {
	v := newSnapshotTestVault(t)
	path := devicesTomlPath(v)
	if err := os.WriteFile(path, []byte("this is not [ valid toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadRoster(v)
	if err == nil {
		t.Fatal("ReadRoster must fail on a malformed devices.toml")
	}
	if !strings.Contains(err.Error(), path+": the device roster cannot be read:") {
		t.Fatalf("bad error: %v", err)
	}
	if !strings.Contains(err.Error(), "Fix: repair the file, or remove it to sync with this device only.") {
		t.Fatalf("bad error: %v", err)
	}
}
