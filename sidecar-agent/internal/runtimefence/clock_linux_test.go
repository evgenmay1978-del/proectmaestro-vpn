package runtimefence

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"testing"
)

func TestLinuxLeaseClockIsPositiveAndReportsStableDomain(t *testing.T) {
	domain, first, err := ReadLeaseClock()
	if err != nil {
		t.Fatal(err)
	}
	nextDomain, next, err := ReadLeaseClock()
	if err != nil || !hexDigest.MatchString(domain) || nextDomain != domain || first <= 0 || next < first {
		t.Fatal("Linux BOOTTIME/domain unavailable or inconsistent")
	}
}

func TestClockDomainSeparatesBootTimeNamespaceAndClockKind(t *testing.T) {
	const boot = "11111111-2222-3333-4444-555555555555"
	const namespace = "time:[123]"
	domain, err := leaseClockDomain(boot, namespace)
	if err != nil {
		t.Fatal(err)
	}
	otherNamespace, err := leaseClockDomain(boot, "time:[124]")
	if err != nil || otherNamespace == domain {
		t.Fatal("time namespaces share a clock domain")
	}
	otherBoot, err := leaseClockDomain("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", namespace)
	if err != nil || otherBoot == domain {
		t.Fatal("kernel boots share a clock domain")
	}
	monotonic := sha256.Sum256([]byte("linux:CLOCK_MONOTONIC\x00" + boot + "\x00" + namespace))
	if domain == hex.EncodeToString(monotonic[:]) {
		t.Fatal("clock kind is not bound")
	}
	for _, value := range []struct{ boot, namespace string }{
		{"", namespace}, {boot, ""}, {boot, "time:[0]"}, {boot, "time:[123]\n"}, {boot, "mnt:[123]"},
	} {
		if _, err := leaseClockDomain(value.boot, value.namespace); err == nil {
			t.Fatal("unverified boot/namespace accepted")
		}
	}
}

func TestBoottimeConversionChecksPositivityAndOverflow(t *testing.T) {
	const second = int64(1_000_000_000)
	for _, value := range []struct{ sec, nsec int64 }{
		{-1, 1}, {0, -1}, {0, 0}, {1, second}, {math.MaxInt64, 0},
		{math.MaxInt64 / second, math.MaxInt64%second + 1},
	} {
		if _, err := checkedBoottimeNS(value.sec, value.nsec); err == nil {
			t.Fatal("invalid/overflowed BOOTTIME accepted")
		}
	}
	if now, err := checkedBoottimeNS(0, 1); err != nil || now != 1 {
		t.Fatal("positive subsecond BOOTTIME rejected")
	}
	if now, err := checkedBoottimeNS(math.MaxInt64/second, math.MaxInt64%second); err != nil || now != math.MaxInt64 {
		t.Fatal("valid maximum BOOTTIME rejected")
	}
}
