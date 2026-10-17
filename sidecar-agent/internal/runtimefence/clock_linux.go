package runtimefence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"os"
	"regexp"
	"strings"

	"golang.org/x/sys/unix"
)

const leaseClockKind = "linux:CLOCK_BOOTTIME"

var kernelBootPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var timeNamespacePattern = regexp.MustCompile(`^time:\[[1-9][0-9]{0,19}\]$`)

// ReadLeaseClock is shared by the local caller and runtime. The domain must be
// independently computed on both sides and compared with the runtime receipt;
// a process boot digest alone does not establish a shared time namespace.
func ReadLeaseClock() (domain string, boottimeNS int64, err error) {
	f, err := os.Open("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", 0, errors.New("lease clock boot identity unavailable")
	}
	b, readErr := io.ReadAll(io.LimitReader(f, 129))
	closeErr := f.Close()
	if readErr != nil || closeErr != nil || len(b) > 128 {
		return "", 0, errors.New("lease clock boot identity unavailable")
	}
	namespace, err := os.Readlink("/proc/self/ns/time")
	if err != nil {
		return "", 0, errors.New("lease clock time namespace unavailable")
	}
	domain, err = leaseClockDomain(strings.TrimSpace(string(b)), namespace)
	if err != nil {
		return "", 0, err
	}
	boottimeNS, err = readBoottime()
	if err != nil {
		return "", 0, err
	}
	return domain, boottimeNS, nil
}

func leaseClockDomain(boot, namespace string) (string, error) {
	if !kernelBootPattern.MatchString(boot) || !timeNamespacePattern.MatchString(namespace) {
		return "", errors.New("invalid lease clock domain")
	}
	digest := sha256.Sum256([]byte(leaseClockKind + "\x00" + boot + "\x00" + namespace))
	return hex.EncodeToString(digest[:]), nil
}

func readBoottime() (int64, error) {
	var ts unix.Timespec
	if unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts) != nil {
		return 0, errors.New("lease BOOTTIME clock unavailable")
	}
	return checkedBoottimeNS(int64(ts.Sec), int64(ts.Nsec))
}

func checkedBoottimeNS(seconds, nanos int64) (int64, error) {
	const second = int64(1_000_000_000)
	if seconds < 0 || nanos < 0 || nanos >= second || seconds > (math.MaxInt64-nanos)/second {
		return 0, errors.New("invalid lease BOOTTIME value")
	}
	now := seconds*second + nanos
	if now <= 0 {
		return 0, errors.New("invalid lease BOOTTIME value")
	}
	return now, nil
}
