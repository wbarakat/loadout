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
// carry. It ends with a Snapshot, so the vault's synced paths start
// clean: PackSnapshot refuses to run over an unsnapshotted change, so
// a test that wants one adds it after this call and snapshots (or
// checks the refusal) itself.
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
	if err := Snapshot(v, "add test content"); err != nil {
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

// TestHeadHashMatchesInternalHelper proves the exported HeadHash
// wrapper (loadout watch's own way to notice a beat changed the
// vault) reports exactly what the package's internal headHash does,
// and that it moves when a new commit lands.
func TestHeadHashMatchesInternalHelper(t *testing.T) {
	v := newSnapshotTestVault(t)
	want, err := headHash(v)
	if err != nil {
		t.Fatal(err)
	}
	got, err := HeadHash(v)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("HeadHash() = %q, want %q", got, want)
	}

	if err := os.WriteFile(filepath.Join(v.MemoryDir(), "new.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Snapshot(v, "add a fact"); err != nil {
		t.Fatal(err)
	}
	after, err := HeadHash(v)
	if err != nil {
		t.Fatal(err)
	}
	if after == got {
		t.Fatal("HeadHash must change after a real commit")
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

// TestPackSnapshotRefusesUnsnapshottedChanges proves PackSnapshot
// never packs the working tree while it disagrees with HEAD: the
// headHash it would report must always name a commit that matches
// the bytes it packs.
func TestPackSnapshotRefusesUnsnapshottedChanges(t *testing.T) {
	v := newSnapshotTestVault(t)
	if err := os.WriteFile(filepath.Join(v.MemoryDir(), "fact.md"), []byte("an edited fact"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := PackSnapshot(v)
	if err == nil {
		t.Fatal("PackSnapshot must refuse while a synced path holds an unsnapshotted change")
	}
	want := "the vault has unsnapshotted changes. Fix: run a loadout command that snapshots, then pack again."
	if err.Error() != want {
		t.Fatalf("bad error: got %q want %q", err.Error(), want)
	}

	if err := Snapshot(v, "edit a fact"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PackSnapshot(v); err != nil {
		t.Fatalf("PackSnapshot after a snapshot must succeed: %v", err)
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
	if err := Snapshot(v, "add the device roster"); err != nil {
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

// TestPackSnapshotRefusesMalformedRecipient proves a devices.toml
// that parses fine as TOML but holds a bad recipient string gets the
// same fixed roster grammar as a parse failure, and does not panic.
func TestPackSnapshotRefusesMalformedRecipient(t *testing.T) {
	v := newSnapshotTestVault(t)
	path := devicesTomlPath(v)
	content := "[devices.bad]\nrecipient = \"not-a-valid-age-recipient\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Snapshot(v, "add a bad recipient"); err != nil {
		t.Fatal(err)
	}

	_, _, err := PackSnapshot(v)
	if err == nil {
		t.Fatal("PackSnapshot must refuse a malformed recipient in devices.toml")
	}
	if !strings.Contains(err.Error(), path+": the device roster cannot be read:") {
		t.Fatalf("bad error: %v", err)
	}
	if !strings.Contains(err.Error(), "Fix: repair the file, or remove it to sync with this device only.") {
		t.Fatalf("bad error: %v", err)
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

// TestUnpackSnapshotRefusesChainedSymlinkEscape reproduces the review
// finding: entry "skills/sub/a" -> "../z" is safe by itself (it
// resolves inside dir, and inside the synced set), and entry
// "skills/sub/a/inner" -> "../../outside" also looks safe by pure
// lexical arithmetic on its own name. But "skills/sub/a" is really a
// symlink to dir/skills/z, so the OS routes the second entry's write
// through it, landing a new symlink at dir/skills/z/inner instead —
// and dir/skills/z/inner's own two ".." hops escape dir for real. The
// intermediate-component check must catch entry 4 before either
// symlink is written for it. (Entries live under skills/ — the
// vault's own synced set — so this fixture also exercises Minor 9's
// synced-set check cleanly on entry 3, which must still be ACCEPTED:
// only the chained escape at entry 4 is the thing under test here.)
func TestUnpackSnapshotRefusesChainedSymlinkEscape(t *testing.T) {
	v := newSnapshotTestVault(t)
	tarData := buildTar(t, func(tw *tar.Writer) {
		tw.WriteHeader(&tar.Header{Name: "skills/z/", Typeflag: tar.TypeDir, Mode: 0o755})
		tw.WriteHeader(&tar.Header{Name: "skills/sub/", Typeflag: tar.TypeDir, Mode: 0o755})
		tw.WriteHeader(&tar.Header{Name: "skills/sub/a", Typeflag: tar.TypeSymlink, Linkname: "../z", Mode: 0o777})
		tw.WriteHeader(&tar.Header{Name: "skills/sub/a/inner", Typeflag: tar.TypeSymlink, Linkname: "../../outside", Mode: 0o777})
	})
	blob := encryptForVault(t, v, tarData)

	dir := t.TempDir()
	err := UnpackSnapshot(v, blob, dir)
	if err == nil {
		t.Fatal("UnpackSnapshot must refuse the chained symlink escape, not return cleanly")
	}
	if !strings.Contains(err.Error(), "skills/sub/a/inner") || !strings.Contains(err.Error(), "the snapshot holds an unsafe path") {
		t.Fatalf("bad error: %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(dir, "skills", "z", "inner")); statErr == nil {
		t.Fatal("UnpackSnapshot must not create anything at the real (symlink-resolved) location")
	}
	if _, statErr := os.Lstat(filepath.Join(dir, "skills", "sub", "a", "inner")); statErr == nil {
		t.Fatal("UnpackSnapshot must not create anything at the lexical location either")
	}
	if _, statErr := os.Lstat(filepath.Join(filepath.Dir(dir), "outside")); statErr == nil {
		t.Fatal("UnpackSnapshot must not write outside dir")
	}
}

// TestUnpackSnapshotRefusesRegularFileThroughSymlinkComponent proves
// the same intermediate-component check refuses a plain regular-file
// entry, not only a symlink entry, when an earlier entry already
// planted a symlink at one of its path components.
func TestUnpackSnapshotRefusesRegularFileThroughSymlinkComponent(t *testing.T) {
	v := newSnapshotTestVault(t)
	tarData := buildTar(t, func(tw *tar.Writer) {
		body := []byte("pwned")
		tw.WriteHeader(&tar.Header{Name: "skills/z/", Typeflag: tar.TypeDir, Mode: 0o755})
		tw.WriteHeader(&tar.Header{Name: "skills/sub/", Typeflag: tar.TypeDir, Mode: 0o755})
		tw.WriteHeader(&tar.Header{Name: "skills/sub/a", Typeflag: tar.TypeSymlink, Linkname: "../z", Mode: 0o777})
		tw.WriteHeader(&tar.Header{Name: "skills/sub/a/inner", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
		tw.Write(body)
	})
	blob := encryptForVault(t, v, tarData)

	dir := t.TempDir()
	err := UnpackSnapshot(v, blob, dir)
	if err == nil {
		t.Fatal("UnpackSnapshot must refuse a regular-file entry routed through a symlink component")
	}
	if !strings.Contains(err.Error(), "skills/sub/a/inner") || !strings.Contains(err.Error(), "the snapshot holds an unsafe path") {
		t.Fatalf("bad error: %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(dir, "skills", "z", "inner")); statErr == nil {
		t.Fatal("UnpackSnapshot must not write the file at the real (symlink-resolved) location")
	}
}

// TestUnpackSnapshotRefusesSymlinkEscapingSyncedSetToDeviceKey is
// Minor 9's key regression test: a symlink whose target resolves
// INSIDE dir (so a plain "does it stay inside dir" check alone would
// accept it) but OUTSIDE the SyncedSet (skills/, memory/,
// devices.toml) must still be refused. Without this, an enrolled but
// malicious device could plant skills/x/link -> ../../device.key in a
// snapshot; once merged into the real working tree, that symlink
// would point straight at this device's real private key file, ready
// to be followed (and its content leaked) by anything that later
// projects skills/ into an adapter's harness.
func TestUnpackSnapshotRefusesSymlinkEscapingSyncedSetToDeviceKey(t *testing.T) {
	v := newSnapshotTestVault(t)
	tarData := buildTar(t, func(tw *tar.Writer) {
		tw.WriteHeader(&tar.Header{Name: "skills/", Typeflag: tar.TypeDir, Mode: 0o755})
		tw.WriteHeader(&tar.Header{Name: "skills/x/", Typeflag: tar.TypeDir, Mode: 0o755})
		tw.WriteHeader(&tar.Header{Name: "skills/x/link", Typeflag: tar.TypeSymlink, Linkname: "../../device.key", Mode: 0o777})
	})
	blob := encryptForVault(t, v, tarData)

	dir := t.TempDir()
	err := UnpackSnapshot(v, blob, dir)
	if err == nil {
		t.Fatal("UnpackSnapshot must refuse a symlink that escapes the synced set even though it resolves inside dir")
	}
	if !strings.Contains(err.Error(), "skills/x/link") || !strings.Contains(err.Error(), "the snapshot holds an unsafe path") {
		t.Fatalf("bad error: %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(dir, "skills", "x", "link")); statErr == nil {
		t.Fatal("UnpackSnapshot must not create the key-escaping symlink")
	}
}

// TestUnpackSnapshotRefusesSymlinkEscapingSyncedSetToManifest is the
// same rule for a shallower escape: a symlink one level inside
// skills/ targeting the device-local manifest one level up.
func TestUnpackSnapshotRefusesSymlinkEscapingSyncedSetToManifest(t *testing.T) {
	v := newSnapshotTestVault(t)
	tarData := buildTar(t, func(tw *tar.Writer) {
		tw.WriteHeader(&tar.Header{Name: "skills/", Typeflag: tar.TypeDir, Mode: 0o755})
		tw.WriteHeader(&tar.Header{Name: "skills/link", Typeflag: tar.TypeSymlink, Linkname: "../loadout.toml", Mode: 0o777})
	})
	blob := encryptForVault(t, v, tarData)

	dir := t.TempDir()
	err := UnpackSnapshot(v, blob, dir)
	if err == nil {
		t.Fatal("UnpackSnapshot must refuse a symlink that escapes the synced set even though it resolves inside dir")
	}
	if !strings.Contains(err.Error(), "skills/link") || !strings.Contains(err.Error(), "the snapshot holds an unsafe path") {
		t.Fatalf("bad error: %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(dir, "skills", "link")); statErr == nil {
		t.Fatal("UnpackSnapshot must not create the manifest-escaping symlink")
	}
}

// TestUnpackSnapshotAllowsSymlinkWithinSyncedSet proves the fix is not
// overbroad: a benign symlink whose target stays fully inside the
// synced set (a skill linking to a sibling skill's shared resource)
// is still allowed.
func TestUnpackSnapshotAllowsSymlinkWithinSyncedSet(t *testing.T) {
	v := newSnapshotTestVault(t)
	tarData := buildTar(t, func(tw *tar.Writer) {
		body := []byte("shared")
		tw.WriteHeader(&tar.Header{Name: "skills/", Typeflag: tar.TypeDir, Mode: 0o755})
		tw.WriteHeader(&tar.Header{Name: "skills/b/", Typeflag: tar.TypeDir, Mode: 0o755})
		tw.WriteHeader(&tar.Header{Name: "skills/b/resource.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
		tw.Write(body)
		tw.WriteHeader(&tar.Header{Name: "skills/a/", Typeflag: tar.TypeDir, Mode: 0o755})
		tw.WriteHeader(&tar.Header{Name: "skills/a/link", Typeflag: tar.TypeSymlink, Linkname: "../b/resource.txt", Mode: 0o777})
	})
	blob := encryptForVault(t, v, tarData)

	dir := t.TempDir()
	if err := UnpackSnapshot(v, blob, dir); err != nil {
		t.Fatalf("a benign symlink within the synced set must still be allowed: %v", err)
	}
	target, err := os.Readlink(filepath.Join(dir, "skills", "a", "link"))
	if err != nil {
		t.Fatal(err)
	}
	if target != "../b/resource.txt" {
		t.Fatalf("bad symlink target: %q", target)
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
