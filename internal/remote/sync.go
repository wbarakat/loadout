package remote

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"loadout.dev/loadout/internal/vault"
)

// maxMergeRetries bounds how many times Sync retries the pull-merge-
// push cycle after a conflict, before it gives up: a remote that
// never stops moving is not something a retry loop can outrun.
const maxMergeRetries = 3

// Result reports what Sync did.
type Result struct {
	// Version is the snapshot version this device is synced to when
	// Sync returns successfully.
	Version string
	// Pushed reports whether this device published a new version.
	Pushed bool
	// Merged reports whether Sync pulled and merged a remote
	// snapshot into the working tree.
	Merged bool
}

// Sync reconciles this vault with its configured remote: it
// registers this device, compares the remote's latest snapshot
// version against the last version this device saw, and either
// pushes (when this device is already caught up) or pulls, merges,
// and republishes (when the remote is ahead). It runs under the
// vault lock, so it never races another loadout command on this
// vault.
func Sync(v *vault.Vault) (Result, error) {
	release, err := vault.Lock(v)
	if err != nil {
		return Result{}, err
	}
	defer release()

	cfg, err := Load(v)
	if err != nil {
		return Result{}, err
	}
	client := NewClient(cfg.URL, cfg.Token)

	name, recipient, err := vault.DeviceIdentity(v)
	if err != nil {
		return Result{}, err
	}
	if err := client.RegisterDevice(name, recipient); err != nil {
		return Result{}, err
	}

	// A defensive snapshot: whatever is on disk right now must enter
	// history before any merge ever overwrites it, so a losing edit
	// stays reachable via git show. Every loadout command that
	// mutates the vault already snapshots on its own, so this is
	// normally a no-op; it only matters for a caller (or a test) that
	// hands Sync a dirty working tree.
	if err := vault.Snapshot(v, "sync"); err != nil {
		return Result{}, err
	}

	serverVersion, _, err := client.Latest()
	if err != nil {
		return Result{}, err
	}
	base := cfg.LastVersion

	if serverVersion == "" || serverVersion == base {
		result, pushErr := push(v, client, base)
		if pushErr == nil {
			return result, nil
		}
		var conflict *ConflictError
		if !errors.As(pushErr, &conflict) {
			return Result{}, pushErr
		}
		return pullMergePush(v, client, conflict.Latest, maxMergeRetries)
	}
	return pullMergePush(v, client, serverVersion, maxMergeRetries)
}

// push packs the vault's current content and publishes it as a new
// version built on parent, then records that version (and the commit
// it corresponds to) as this device's sync state.
func push(v *vault.Vault, client *Client, parent string) (Result, error) {
	if err := vault.Snapshot(v, "sync"); err != nil {
		return Result{}, err
	}
	blob, headHash, err := vault.PackSnapshot(v)
	if err != nil {
		return Result{}, err
	}
	version, err := client.PutSnapshot(blob, parent)
	if err != nil {
		return Result{}, err
	}
	if err := SetLastVersion(v, version); err != nil {
		return Result{}, err
	}
	if err := writeSyncState(v, syncState{Version: version, BaseCommit: headHash}); err != nil {
		return Result{}, err
	}
	return Result{Version: version, Pushed: true}, nil
}

// pullMergePush pulls remoteVersion's snapshot, merges it into the
// working tree, snapshots the result, and republishes it, so any
// local-only content the merge kept reaches the remote too. A further
// conflict retries against the new latest, up to retriesLeft times.
func pullMergePush(v *vault.Vault, client *Client, remoteVersion string, retriesLeft int) (Result, error) {
	if retriesLeft <= 0 {
		return Result{}, errors.New("the remote changed too fast. Fix: run loadout sync --remote again.")
	}

	blob, err := client.GetSnapshot(remoteVersion)
	if err != nil {
		return Result{}, err
	}
	tmp, err := os.MkdirTemp(filepath.Join(v.Root, "render"), "sync-pull-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(tmp)
	if err := vault.UnpackSnapshot(v, blob, tmp); err != nil {
		return Result{}, err
	}

	state, err := readSyncState(v)
	if err != nil {
		return Result{}, err
	}
	if err := mergeInto(v.Root, tmp, state.BaseCommit); err != nil {
		return Result{}, err
	}
	if err := vault.Snapshot(v, fmt.Sprintf("sync from %s", remoteVersion)); err != nil {
		return Result{}, err
	}

	// When the merge kept nothing beyond what remoteVersion already
	// holds, republishing would only mint a byte-different but
	// content-identical duplicate version: skip it, and record this
	// device as caught up on remoteVersion exactly as pulled. This
	// also keeps two devices that both merge the same version from
	// drifting onto different "last synced" versions for no reason,
	// which would otherwise make an actual same-parent race
	// (two pushes against one shared parent) impossible to land on
	// reliably.
	identical, err := treesIdentical(v.Root, tmp)
	if err != nil {
		return Result{}, err
	}
	if identical {
		head, err := headHash(v.Root)
		if err != nil {
			return Result{}, err
		}
		if err := SetLastVersion(v, remoteVersion); err != nil {
			return Result{}, err
		}
		if err := writeSyncState(v, syncState{Version: remoteVersion, BaseCommit: head}); err != nil {
			return Result{}, err
		}
		return Result{Version: remoteVersion, Merged: true}, nil
	}

	blobOut, headHash, err := vault.PackSnapshot(v)
	if err != nil {
		return Result{}, err
	}
	// Record the merge as this device's sync state before it even
	// tries to republish: on any failure below, the device is still
	// honestly checkpointed at remoteVersion, not left claiming an
	// older base it has already merged past.
	if err := SetLastVersion(v, remoteVersion); err != nil {
		return Result{}, err
	}
	if err := writeSyncState(v, syncState{Version: remoteVersion, BaseCommit: headHash}); err != nil {
		return Result{}, err
	}

	newVersion, err := client.PutSnapshot(blobOut, remoteVersion)
	if err != nil {
		var conflict *ConflictError
		if errors.As(err, &conflict) {
			return pullMergePush(v, client, conflict.Latest, retriesLeft-1)
		}
		return Result{}, err
	}
	if err := SetLastVersion(v, newVersion); err != nil {
		return Result{}, err
	}
	if err := writeSyncState(v, syncState{Version: newVersion, BaseCommit: headHash}); err != nil {
		return Result{}, err
	}
	return Result{Version: newVersion, Pushed: true, Merged: true}, nil
}

// treesIdentical reports whether vaultRoot's current SyncedSet content
// is byte-for-byte identical to incomingDir's: when true, nothing
// this device holds goes beyond what the incoming snapshot already
// carries, so publishing it would only mint a redundant duplicate
// version.
func treesIdentical(vaultRoot, incomingDir string) (bool, error) {
	localPaths, err := listSyncedFiles(vaultRoot)
	if err != nil {
		return false, err
	}
	incomingPaths, err := listSyncedFiles(incomingDir)
	if err != nil {
		return false, err
	}
	if len(localPaths) != len(incomingPaths) {
		return false, nil
	}
	for _, rel := range unionSorted(localPaths, incomingPaths) {
		local, err := readFileState(filepath.Join(vaultRoot, rel))
		if err != nil {
			return false, err
		}
		incoming, err := readFileState(filepath.Join(incomingDir, rel))
		if err != nil {
			return false, err
		}
		if !statesEqual(local, incoming) {
			return false, nil
		}
	}
	return true, nil
}

// fileState is one path's content as either the local working tree,
// an incoming unpacked snapshot, or the base commit holds it. exists
// is false for a path absent from that tree; isSymlink distinguishes
// a symlink's target (in target) from a regular file's bytes (in
// content), so the two are never mistaken for each other even if a
// target string happens to match some file's content.
type fileState struct {
	exists    bool
	isSymlink bool
	content   []byte
	target    string
	mode      os.FileMode
}

// statesEqual reports whether a and b describe the same content: both
// absent, or both present with the same type and bytes/target.
func statesEqual(a, b fileState) bool {
	if a.exists != b.exists {
		return false
	}
	if !a.exists {
		return true
	}
	if a.isSymlink != b.isSymlink {
		return false
	}
	if a.isSymlink {
		return a.target == b.target
	}
	return bytes.Equal(a.content, b.content)
}

// mergeInto merges the incoming unpacked snapshot at incomingDir into
// the working tree at vaultRoot, following the last-write-wins rule
// against baseCommit (vaultRoot's own git history at the point the
// last sync recorded its base; "" when this device has never synced
// before, which the rule treats as an empty base tree everywhere).
//
// For every path present in either the working tree's SyncedSet
// content or the incoming snapshot:
//   - when the local copy is unchanged since base, the incoming copy
//     wins (this also propagates a remote deletion: an incoming copy
//     that does not exist "wins" by removing the local file);
//   - otherwise, when the incoming copy is unchanged since base, the
//     local copy is kept as-is;
//   - otherwise both sides changed since base: the incoming copy
//     wins, and the local copy it replaces stays reachable in git
//     history, since the caller snapshots before this ever runs and
//     snapshots again right after.
func mergeInto(vaultRoot, incomingDir, baseCommit string) error {
	localPaths, err := listSyncedFiles(vaultRoot)
	if err != nil {
		return err
	}
	incomingPaths, err := listSyncedFiles(incomingDir)
	if err != nil {
		return err
	}
	for _, rel := range unionSorted(localPaths, incomingPaths) {
		local, err := readFileState(filepath.Join(vaultRoot, rel))
		if err != nil {
			return err
		}
		incoming, err := readFileState(filepath.Join(incomingDir, rel))
		if err != nil {
			return err
		}
		base, err := readBaseState(vaultRoot, baseCommit, rel)
		if err != nil {
			return err
		}

		switch {
		case statesEqual(local, base):
			if err := applyFileState(vaultRoot, rel, incoming); err != nil {
				return err
			}
		case statesEqual(incoming, base):
			// Local changed, incoming did not: keep local as-is.
		default:
			// Both changed since base, and differently: incoming
			// wins. When they happen to already agree, this is a
			// harmless no-op write of identical content.
			if err := applyFileState(vaultRoot, rel, incoming); err != nil {
				return err
			}
		}
	}
	return nil
}

// listSyncedFiles walks root's SyncedSet paths (skills/, memory/,
// devices.toml) that exist, returning every regular file's or
// symlink's path relative to root, forward-slash, deduplicated and
// sorted. It never descends into a symlinked directory's content: a
// symlink is a leaf entry, exactly as packTar treats it.
func listSyncedFiles(root string) ([]string, error) {
	var rels []string
	for _, rel := range vault.SyncedSet() {
		full := filepath.Join(root, rel)
		info, err := os.Lstat(full)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			rels = append(rels, filepath.ToSlash(rel))
			continue
		}
		err = filepath.WalkDir(full, func(walked string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			relPath, err := filepath.Rel(root, walked)
			if err != nil {
				return err
			}
			rels = append(rels, filepath.ToSlash(relPath))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(rels)
	return rels, nil
}

// unionSorted returns the sorted union of a and b, deduplicated.
func unionSorted(a, b []string) []string {
	set := make(map[string]bool, len(a)+len(b))
	for _, p := range a {
		set[p] = true
	}
	for _, p := range b {
		set[p] = true
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// readFileState reads one path's current state from disk: absent, a
// symlink (its target), or a regular file (its bytes and mode).
func readFileState(fullPath string) (fileState, error) {
	info, err := os.Lstat(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fileState{}, nil
		}
		return fileState{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(fullPath)
		if err != nil {
			return fileState{}, err
		}
		return fileState{exists: true, isSymlink: true, target: target}, nil
	}
	if info.IsDir() {
		// A directory carries no content of its own to compare; the
		// files inside it are what the merge walks and compares.
		return fileState{}, nil
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return fileState{}, err
	}
	return fileState{exists: true, content: data, mode: info.Mode().Perm()}, nil
}

// applyFileState writes st into the working tree at vaultRoot/rel:
// a regular file's bytes, a symlink pointing at its target, or (when
// st does not exist) removes whatever is there, propagating a
// deletion.
func applyFileState(vaultRoot, rel string, st fileState) error {
	full := filepath.Join(vaultRoot, rel)
	if !st.exists {
		if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	if st.isSymlink {
		if err := os.RemoveAll(full); err != nil {
			return err
		}
		return os.Symlink(st.target, full)
	}
	if err := os.RemoveAll(full); err != nil {
		return err
	}
	mode := st.mode
	if mode == 0 {
		mode = 0o644
	}
	return os.WriteFile(full, st.content, mode)
}

// readBaseState reads one path's state at baseCommit, vaultRoot's own
// git history. An empty baseCommit (no prior sync) reads back as
// absent for every path, without ever invoking git.
func readBaseState(vaultRoot, baseCommit, rel string) (fileState, error) {
	if baseCommit == "" {
		return fileState{}, nil
	}
	mode, exists, err := lsTreeEntry(vaultRoot, baseCommit, rel)
	if err != nil {
		return fileState{}, err
	}
	if !exists {
		return fileState{}, nil
	}
	content, err := gitShowBytes(vaultRoot, baseCommit, rel)
	if err != nil {
		return fileState{}, err
	}
	if mode == "120000" {
		return fileState{exists: true, isSymlink: true, target: string(content)}, nil
	}
	return fileState{exists: true, content: content}, nil
}

// lsTreeEntry looks up rel's git object mode at commit, reporting
// exists as false when rel is not present in that commit's tree.
func lsTreeEntry(root, commit, rel string) (mode string, exists bool, err error) {
	out, err := runGit(root, "ls-tree", commit, "--", filepath.ToSlash(rel))
	if err != nil {
		return "", false, err
	}
	line := strings.TrimSpace(out)
	if line == "" {
		return "", false, nil
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", false, nil
	}
	return fields[0], true, nil
}

// gitShowBytes returns rel's raw content at commit, exactly as git
// stored it: stdout only, never merged with stderr, so a blob's exact
// bytes are never corrupted by an incidental warning line.
func gitShowBytes(root, commit, rel string) ([]byte, error) {
	cmd := exec.Command("git", "-C", root, "show", commit+":"+filepath.ToSlash(rel))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git show failed: %s", msg)
	}
	return stdout.Bytes(), nil
}

// runGit runs a git subcommand at root, returning combined
// stdout+stderr trimmed on failure (mirroring internal/vault's own
// git helper), and plain stdout on success. It is used only for
// commands whose output is metadata (ls-tree, rev-parse), never for a
// blob's raw content — see gitShowBytes for that.
func runGit(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(out.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s failed: %s", args[0], msg)
	}
	return out.String(), nil
}

// headHash returns vaultRoot's current git HEAD commit hash.
func headHash(vaultRoot string) (string, error) {
	out, err := runGit(vaultRoot, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Status describes this vault's relationship to its configured
// remote, without registering this device or transferring anything:
// status and doctor call it to render their one remote line.
type Status struct {
	URL string
	// State is "in sync", "behind", "ahead", or "unreachable".
	State string
	// Detail adds context for State: version numbers for "behind", or
	// the network error's own text for "unreachable". Empty for "in
	// sync" and "ahead".
	Detail string
}

// LoadStatus reports the vault's remote status. ok is false when the
// vault holds no remote.toml: callers print no remote line then. It
// never mutates the vault and never registers this device.
func LoadStatus(v *vault.Vault) (st Status, ok bool, err error) {
	cfg, err := Load(v)
	if err != nil {
		if errors.Is(err, ErrNotConfigured) {
			return Status{}, false, nil
		}
		return Status{}, true, err
	}
	st.URL = cfg.URL

	client := NewClient(cfg.URL, cfg.Token)
	version, _, latestErr := client.Latest()
	if latestErr != nil {
		st.State = "unreachable"
		st.Detail = latestErr.Error()
		return st, true, nil
	}

	if version != cfg.LastVersion {
		st.State = "behind"
		st.Detail = fmt.Sprintf("the remote has %s, this device has %s", displayVersion(version), displayVersion(cfg.LastVersion))
		return st, true, nil
	}

	state, stateErr := readSyncState(v)
	if stateErr != nil {
		return Status{}, true, stateErr
	}
	if head, headErr := headHash(v.Root); headErr == nil && state.BaseCommit != "" && head != state.BaseCommit {
		st.State = "ahead"
		st.Detail = "this device has local changes not yet pushed"
		return st, true, nil
	}
	st.State = "in sync"
	return st, true, nil
}

// displayVersion renders an empty version as readable text: a remote
// or a device that has never held a snapshot has no version string to
// show as-is.
func displayVersion(version string) string {
	if version == "" {
		return "no snapshot yet"
	}
	return version
}
