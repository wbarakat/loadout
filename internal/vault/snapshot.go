package vault

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/BurntSushi/toml"
)

// devicesTomlPath returns the path to the vault's device roster.
func devicesTomlPath(v *Vault) string { return filepath.Join(v.Root, "devices.toml") }

// rosterDevice is one entry of the on-disk device roster.
type rosterDevice struct {
	Recipient string `toml:"recipient"`
}

// rosterFile is the on-disk shape of devices.toml.
type rosterFile struct {
	Devices map[string]rosterDevice `toml:"devices"`
}

// rosterErr wraps cause in the fixed grammar every devices.toml
// failure uses. It never repeats file content, only the path and the
// underlying cause.
func rosterErr(path string, cause error) error {
	return fmt.Errorf("%s: the device roster cannot be read: %v. Fix: repair the file, or remove it to sync with this device only.", path, cause)
}

// ReadRoster reads the vault's device roster: device name to age
// recipient. A vault with no devices.toml yet has an empty roster,
// not an error — every vault starts out synced to just this device.
func ReadRoster(v *Vault) (map[string]string, error) {
	rosterPath := devicesTomlPath(v)
	var rf rosterFile
	if _, err := toml.DecodeFile(rosterPath, &rf); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, rosterErr(rosterPath, err)
	}
	roster := make(map[string]string, len(rf.Devices))
	for name, d := range rf.Devices {
		roster[name] = d.Recipient
	}
	return roster, nil
}

// AddToRoster adds name and recipient to the vault's device roster,
// writing the file atomically with sorted, stable output. It does
// not snapshot the vault: callers that want the change in history
// call Snapshot themselves.
func AddToRoster(v *Vault, name, recipient string) error {
	roster, err := ReadRoster(v)
	if err != nil {
		return err
	}
	roster[name] = recipient
	return writeRoster(v, roster)
}

// writeRoster encodes roster to devices.toml. It writes a temp file
// first and renames it into place, so a crash mid-write never leaves
// a half-written roster behind. github.com/BurntSushi/toml sorts map
// keys before it encodes them, so the output is stable across calls
// that add the same devices.
func writeRoster(v *Vault, roster map[string]string) error {
	rf := rosterFile{Devices: make(map[string]rosterDevice, len(roster))}
	for name, recipient := range roster {
		rf.Devices[name] = rosterDevice{Recipient: recipient}
	}
	rosterPath := devicesTomlPath(v)
	tmp := rosterPath + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := toml.NewEncoder(f).Encode(rf); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, rosterPath)
}

// headHash returns the git HEAD commit hash: the vault content a
// pack pins itself to. A vault with no history surfaces the same
// fixed error every other history command uses.
func headHash(v *Vault) (string, error) {
	out, err := git(v, "rev-parse", "HEAD")
	if err != nil {
		return "", noHistoryErr(v, err)
	}
	return strings.TrimSpace(out), nil
}

// HeadHash returns the vault's current git HEAD commit hash: the
// same identifier PackSnapshot pins a snapshot to. loadout watch
// calls this once per beat and carries the result forward as the next
// beat's baseline (not a within-beat before/after), so a beat can
// tell whether the vault's tracked content changed since the last
// beat it announced, and stay silent when it did not.
func HeadHash(v *Vault) (string, error) {
	return headHash(v)
}

// PackSnapshot builds an encrypted snapshot of the vault's synced
// paths (SyncedSet) and reports the git HEAD hash that snapshot's
// content is pinned to. It encrypts to every recipient listed in
// devices.toml, or to this device alone when that file is absent or
// lists no one.
//
// It refuses to pack while any synced path holds a change HEAD does
// not know about yet: the tar it builds reads the working tree, not
// the commit, so an unsnapshotted edit would make headHashOut name a
// commit that does not match the bytes the caller is about to send.
func PackSnapshot(v *Vault) (blob []byte, headHashOut string, err error) {
	headHashOut, err = headHash(v)
	if err != nil {
		return nil, "", err
	}
	statusArgs := append([]string{"status", "--porcelain", "--"}, SyncedSet()...)
	status, err := git(v, statusArgs...)
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(status) != "" {
		return nil, "", errors.New("the vault has unsnapshotted changes. Fix: run a loadout command that snapshots, then pack again.")
	}
	tarBytes, err := packTar(v)
	if err != nil {
		return nil, "", err
	}
	recipients, err := packRecipients(v)
	if err != nil {
		return nil, "", err
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, recipients...)
	if err != nil {
		return nil, "", fmt.Errorf("the snapshot cannot be encrypted: %v", err)
	}
	if _, err := w.Write(tarBytes); err != nil {
		return nil, "", fmt.Errorf("the snapshot cannot be encrypted: %v", err)
	}
	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("the snapshot cannot be encrypted: %v", err)
	}
	return buf.Bytes(), headHashOut, nil
}

// packRecipients lists the age recipients a pack must encrypt to:
// every recipient in devices.toml, sorted by device name, or this
// device alone when the roster is empty.
func packRecipients(v *Vault) ([]age.Recipient, error) {
	roster, err := ReadRoster(v)
	if err != nil {
		return nil, err
	}
	if len(roster) == 0 {
		identity, err := deviceKey(v)
		if err != nil {
			return nil, err
		}
		return []age.Recipient{identity.Recipient()}, nil
	}
	names := make([]string, 0, len(roster))
	for name := range roster {
		names = append(names, name)
	}
	sort.Strings(names)
	recipients := make([]age.Recipient, 0, len(names))
	for _, name := range names {
		r, err := age.ParseX25519Recipient(roster[name])
		if err != nil {
			return nil, rosterErr(devicesTomlPath(v), fmt.Errorf("device %q holds an invalid recipient", name))
		}
		recipients = append(recipients, r)
	}
	return recipients, nil
}

// packTar tars the vault's SyncedSet paths that exist, in a
// deterministic byte order: every path sorted, timestamps zeroed to
// the Unix epoch, and no per-machine user or group names. Two packs
// of the same vault content produce byte-identical tar bytes; the
// encrypted blob PackSnapshot builds from that tar is not
// byte-identical across calls, since age randomizes the encryption,
// but that randomization never touches the plaintext layer this
// function builds.
//
// A symlink inside a synced path (a skill linking to shared
// resources, say) is stored as a symlink, never followed: pack never
// reads through it, so it cannot walk outside the vault.
func packTar(v *Vault) ([]byte, error) {
	var relPaths []string
	for _, rel := range SyncedSet() {
		full := filepath.Join(v.Root, rel)
		if _, err := os.Lstat(full); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		err := filepath.WalkDir(full, func(walked string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			relPath, err := filepath.Rel(v.Root, walked)
			if err != nil {
				return err
			}
			relPaths = append(relPaths, relPath)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(relPaths)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, relPath := range relPaths {
		if err := addTarEntry(tw, v.Root, relPath); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// tarEpoch is the fixed modification time every tar entry gets, so a
// file's real mtime never makes two packs of the same content differ.
var tarEpoch = time.Unix(0, 0)

// addTarEntry writes one tar entry for the file, directory, or
// symlink at filepath.Join(root, relPath).
func addTarEntry(tw *tar.Writer, root, relPath string) error {
	full := filepath.Join(root, relPath)
	fi, err := os.Lstat(full)
	if err != nil {
		return err
	}
	var link string
	if fi.Mode()&os.ModeSymlink != 0 {
		link, err = os.Readlink(full)
		if err != nil {
			return err
		}
	}
	hdr, err := tar.FileInfoHeader(fi, link)
	if err != nil {
		return err
	}
	hdr.Name = filepath.ToSlash(relPath)
	if fi.IsDir() && !strings.HasSuffix(hdr.Name, "/") {
		hdr.Name += "/"
	}
	hdr.ModTime = tarEpoch
	hdr.AccessTime = time.Time{}
	hdr.ChangeTime = time.Time{}
	hdr.Uid = 0
	hdr.Gid = 0
	hdr.Uname = ""
	hdr.Gname = ""
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if fi.Mode().IsRegular() {
		f, err := os.Open(full)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := io.Copy(tw, f); err != nil {
			return err
		}
	}
	return nil
}

// unsafePathErr is the fixed error a traversal attempt in a snapshot
// gets: the archive-relative name, never the resolved disk path,
// since the resolved path is exactly what the check refused to
// create.
func unsafePathErr(name string) error {
	return fmt.Errorf("%s: the snapshot holds an unsafe path. Fix: do not sync this snapshot; report it.", name)
}

// safeJoin resolves a tar entry's name against dir, refusing an
// absolute name or one that climbs above dir with "..".
func safeJoin(dir, name string) (string, error) {
	slash := filepath.ToSlash(name)
	if slash == "" || strings.HasPrefix(slash, "/") {
		return "", errors.New("unsafe path")
	}
	cleaned := path.Clean(slash)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("unsafe path")
	}
	return filepath.Join(dir, filepath.FromSlash(cleaned)), nil
}

// safeSymlinkTarget reports whether a symlink at entryPath (already
// resolved inside dir) pointing at linkname would still resolve
// inside dir, AND inside the SyncedSet (skills/, memory/,
// devices.toml). An absolute linkname is refused outright; a relative
// one is resolved against the symlink's own directory before the
// check, so a target that stays inside the synced set via a
// "../sibling" hop is still accepted.
//
// The synced-set check matters beyond the plain inside-dir check: dir
// is only ever a throwaway render/sync-pull-* directory, not the real
// vault, but mergeInto later recreates every accepted symlink,
// verbatim, at the SAME relative path inside the real vault root. A
// symlink that resolves inside dir but outside the synced set — say
// skills/x/link -> ../../device.key, still comfortably inside dir —
// would therefore land pointing at the real vault's own device.key
// once merged in, ready to be followed (and its content leaked) by
// anything that later projects skills/ into an adapter's harness. An
// enrolled device can encrypt and push arbitrary content, so this
// must be refused here, not trusted to whatever reads the symlink
// later.
func safeSymlinkTarget(entryPath, dir, linkname string) error {
	if linkname == "" || filepath.IsAbs(filepath.FromSlash(linkname)) {
		return errors.New("unsafe symlink target")
	}
	resolved := filepath.Join(filepath.Dir(entryPath), filepath.FromSlash(linkname))
	rel, err := filepath.Rel(dir, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("unsafe symlink target")
	}
	if !withinSyncedSet(rel) {
		return errors.New("unsafe symlink target")
	}
	return nil
}

// withinSyncedSet reports whether rel — a slash-agnostic path already
// confirmed to resolve inside the unpack root — falls within one of
// the vault's synced paths: skills/, memory/, or devices.toml itself.
func withinSyncedSet(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, entry := range SyncedSet() {
		if rel == entry || strings.HasPrefix(rel, entry+"/") {
			return true
		}
	}
	return false
}

// hasSymlinkComponent reports whether any already-existing directory
// component between dir and full — every step the entry's own write
// passes through on its way to full, not full's own final name — is
// a symlink. os.OpenFile, os.MkdirAll, and os.Symlink all follow an
// intermediate symlink silently: an earlier entry can plant one (a
// name that resolves safely on its own, since it points inside dir),
// and a later entry named right through it then lands wherever that
// symlink really points, bypassing every check that only looked at
// the entry's own name and target. A component that does not exist
// yet is not a risk: extraction is about to create it fresh.
func hasSymlinkComponent(dir, full string) (bool, error) {
	rel, err := filepath.Rel(dir, full)
	if err != nil {
		return false, err
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	current := dir
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		fi, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return false, err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return true, nil
		}
	}
	return false, nil
}

// UnpackSnapshot decrypts blob with this device's identity and
// extracts its tar content into dir. It refuses any entry whose name
// or, for a symlink, whose target would land outside dir; nothing
// from such an entry is written.
func UnpackSnapshot(v *Vault, blob []byte, dir string) error {
	identity, err := deviceKey(v)
	if err != nil {
		return err
	}
	r, err := age.Decrypt(bytes.NewReader(blob), identity)
	if err != nil {
		var noMatch *age.NoIdentityMatchError
		if errors.As(err, &noMatch) {
			return errors.New("this device cannot decrypt the snapshot. Fix: approve this device from an enrolled device, then sync again.")
		}
		return fmt.Errorf("the snapshot cannot be decrypted: %v", err)
	}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("the snapshot cannot be read: %v", err)
		}
		if err := extractTarEntry(dir, hdr, tr); err != nil {
			return err
		}
	}
	return nil
}

// extractTarEntry writes one tar entry under dir, after checking
// that doing so cannot land outside dir.
func extractTarEntry(dir string, hdr *tar.Header, r io.Reader) error {
	full, err := safeJoin(dir, hdr.Name)
	if err != nil {
		return unsafePathErr(hdr.Name)
	}
	if bad, err := hasSymlinkComponent(dir, full); err != nil {
		return err
	} else if bad {
		return unsafePathErr(hdr.Name)
	}
	mode := os.FileMode(hdr.Mode).Perm()
	switch hdr.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(full, mode|0o700)
	case tar.TypeSymlink:
		if err := safeSymlinkTarget(full, dir, hdr.Linkname); err != nil {
			return unsafePathErr(hdr.Name)
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.RemoveAll(full); err != nil {
			return err
		}
		return os.Symlink(hdr.Linkname, full)
	case tar.TypeReg:
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(full, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(f, r)
		return err
	default:
		// The vault tree never produces a hard link, device, or other
		// exotic entry. Skipping one is safer than failing the whole
		// unpack over content that cannot appear in a real snapshot.
		return nil
	}
}
