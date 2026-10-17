//go:build !linux

package release

import (
	"testing"
	"time"
)

func TestSealedFilesystemAPIsFailClosedOutsideLinux(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "validate without trust", err: ValidateReleaseDirectory("")},
		{name: "validate with trust", err: ValidateReleaseDirectoryWithTrust("", EvidenceTrust{})},
		{name: "validate promotion", err: ValidateReleaseDirectoryForPromotionWithTrust("", EvidenceTrust{}, time.Unix(1, 0))},
		{name: "promote without trust", err: PromoteSealedDirectory("", "")},
		{name: "promote with trust", err: PromoteSealedDirectoryWithTrust("", "", EvidenceTrust{})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, ok := test.err.(ValidationError)
			if !ok || value.ReasonCode() != "unsupported_platform" {
				t.Fatalf("error=%T %v, want unsupported_platform", test.err, test.err)
			}
		})
	}
}
