package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// lockPollInterval sets how often Lock retries a held lock.
const lockPollInterval = 100 * time.Millisecond

// lockTimeout sets how long Lock waits before it gives up.
const lockTimeout = 10 * time.Second

// Lock takes an exclusive lock on the vault, so two loadout commands
// do not write to it at the same time. It polls every 100ms and
// gives up after 10s. Call the returned release function to free the
// lock, typically in a defer.
func Lock(v *Vault) (release func(), err error) {
	path := filepath.Join(v.Root, "loadout.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	ensureLockIgnored(v.Root)

	deadline := time.Now().Add(lockTimeout)
	for {
		flockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if flockErr == nil {
			return func() {
				syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			}, nil
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("the vault at %s is locked by another loadout command. Fix: wait for it to finish, or remove loadout.lock if no loadout process runs.", v.Root)
		}
		time.Sleep(lockPollInterval)
	}
}

// ensureLockIgnored adds a loadout.lock entry to the vault's
// .gitignore file, so the lock file never enters history. It does
// nothing when no .gitignore file exists yet; Init creates that file
// (see Task 3) and lists loadout.lock itself. It also does nothing
// when the entry is already present.
func ensureLockIgnored(root string) {
	path := filepath.Join(root, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "loadout.lock" {
			return
		}
	}
	content := string(data)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += "loadout.lock\n"
	_ = os.WriteFile(path, []byte(content), 0o644)
}
