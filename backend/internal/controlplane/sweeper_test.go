package controlplane

import (
	"testing"
	"time"
)

func TestExpirySweeperTimingContract(t *testing.T) {
	if expiryLeaseTTL != 90*time.Second {
		t.Fatalf("lease TTL=%v", expiryLeaseTTL)
	}
	if expiryLeaseRenewal != 30*time.Second {
		t.Fatalf("renewal=%v", expiryLeaseRenewal)
	}
	if expiryScanInterval != 60*time.Second {
		t.Fatalf("scan interval=%v", expiryScanInterval)
	}
	if orderUnclaimedTTL != 24*time.Hour {
		t.Fatalf("order TTL=%v", orderUnclaimedTTL)
	}
}
