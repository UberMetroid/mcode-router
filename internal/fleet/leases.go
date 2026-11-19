package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

const leasePollInterval = 100 * time.Millisecond

var (
	relResetRe = regexp.MustCompile(
		`(?i)(?:resets?\s+(?:in|after)|try\s+again\s+in|retry\s+(?:in|after))\s*:?\s*` +
			`(?:(\d+)\s*d(?:ays?)?\s*,?\s*)?(?:(\d+)\s*h(?:ours?|rs?)?\s*,?\s*)?` +
			`(?:(\d+)\s*m(?:in(?:ute)?s?)?\s*,?\s*)?(?:(\d+)\s*s(?:ec(?:ond)?s?)?)?`,
	)
	retryAfterSecRe = regexp.MustCompile(`(?i)\bretry-after\s*[:=]\s*(\d+)\b`)
)

func leaseDir() string {
	base := ScratchDirPath()
	ldir := filepath.Join(base, "leases")
	_ = os.MkdirAll(ldir, 0o700)
	_ = os.Chmod(ldir, 0o700)
	return ldir
}

// CountActiveLeases counts currently locked parallel slot files for provider.
func CountActiveLeases(provider string, maxPar int) int {
	if maxPar <= 0 {
		return 0
	}
	maxPar = min(maxPar, MaxParallelCap)
	ldir, busy := leaseDir(), 0
	for i := 0; i < maxPar; i++ {
		path := filepath.Join(ldir, fmt.Sprintf("%s-%d.lock", SafeID(provider), i))
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if fh, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600); err == nil {
			if serr := syscall.Flock(int(fh.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); serr == nil {
				_ = syscall.Flock(int(fh.Fd()), syscall.LOCK_UN)
			} else {
				busy++
			}
			_ = fh.Close()
		}
	}
	return busy
}

// AcquireParallelLease acquires an flock slot in 0..maxPar-1.
func AcquireParallelLease(provider string, maxPar int, blocking bool, timeoutSec int) (*os.File, bool, error) {
	if maxPar <= 0 {
		return nil, false, nil
	}
	maxPar = min(maxPar, MaxParallelCap)
	if blocking && timeoutSec <= 0 {
		timeoutSec = DefaultTimeout
	}
	ldir, pid, start := leaseDir(), os.Getpid(), time.Now()
	base, jitter := pid%maxPar, time.Duration(pid%7)*15*time.Millisecond
	for {
		for i := 0; i < maxPar; i++ {
			idx := (base + i) % maxPar
			path := filepath.Join(ldir, fmt.Sprintf("%s-%d.lock", SafeID(provider), idx))
			fh, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
			if err != nil {
				return nil, false, err
			}
			if ferr := syscall.Flock(int(fh.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); ferr == nil {
				_ = os.Chmod(path, 0o600)
				return fh, false, nil
			}
			_ = fh.Close()
		}
		if !blocking {
			return nil, false, nil
		}
		if timeoutSec > 0 && time.Since(start) >= time.Duration(timeoutSec)*time.Second {
			return nil, true, nil
		}
		time.Sleep(leasePollInterval + jitter)
	}
}

// ReleaseParallelLease unlocks and closes an acquired parallel slot lease.
func ReleaseParallelLease(lease *os.File) {
	if lease == nil {
		return
	}
	_ = syscall.Flock(int(lease.Fd()), syscall.LOCK_UN)
	_ = lease.Close()
}
