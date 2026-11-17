package compose

// BUG-799 r3: the property the takeover-lock design rests on -- the kernel
// releases the advisory lock when its holder PROCESS dies, with no unlock and
// no cleanup. A re-exec'd child takes the lock and exits via os.Exit without
// unlocking or closing; the parent must then acquire it immediately.

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const lockDeathChildEnv = bug799EnvPrefix + "BUG799_" + "LOCK_CHILD"

func TestBUG799_ProcessDeathReleasesTakeoverLock(t *testing.T) {
	if path := os.Getenv(lockDeathChildEnv); path != "" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			_, _ = os.Stderr.WriteString("open: " + err.Error() + "\n")
			os.Exit(3)
		}
		held, err := tryLockFile(f)
		if err != nil || !held {
			_, _ = os.Stderr.WriteString("lock failed\n")
			os.Exit(4)
		}
		_, _ = os.Stdout.WriteString("LOCKED\n")
		os.Exit(0) // die holding the lock: no unlock, no close
	}

	lockPath := filepath.Join(t.TempDir(), pageClaimFileName+".reclaim")
	cmd := exec.Command(os.Args[0], "-test.run=^TestBUG799_ProcessDeathReleasesTakeoverLock$")
	cmd.Env = append(os.Environ(), lockDeathChildEnv+"="+lockPath)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(out)
	locked := false
	for sc.Scan() {
		if sc.Text() == "LOCKED" {
			locked = true
		}
	}
	_ = cmd.Wait()
	if !locked {
		t.Fatal("child never reported holding the lock")
	}

	// The dead child's lock must be free at once, and the file survives.
	start := time.Now()
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("lock file must survive the holder's death: %v", err)
	}
	defer func() { _ = f.Close() }()
	held, lerr := tryLockFile(f)
	if lerr != nil || !held {
		t.Fatalf("FINDING: lock still held after its process died (held=%v err=%v)", held, lerr)
	}
	t.Logf("parent acquired the dead child's lock in %s", time.Since(start))
	// Premise check: the lock really excludes a second handle while held.
	f2, err := os.OpenFile(lockPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f2.Close() }()
	if got, _ := tryLockFile(f2); got {
		t.Fatal("premise broken: a second handle acquired a lock the first still holds")
	}
	_ = unlockFile(f)
}
