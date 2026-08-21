package release_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/release"
)

type taskCReasoned interface {
	ReasonCode() string
}

func taskCRequireReason(t *testing.T, err error, expected string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected reason %q, got nil", expected)
	}
	reasoned, ok := err.(taskCReasoned)
	if !ok || reasoned.ReasonCode() != expected {
		t.Fatalf("reason = %T %v, want %q", err, err, expected)
	}
}

func TestTaskCFilesystemAPIsFailClosedOutsideLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("unsupported-platform contract is exercised on non-Linux")
	}

	trust := release.EvidenceTrust{}
	taskCRequireReason(t, release.ValidateReleaseDirectory("must-not-be-read"), "unsupported_platform")
	taskCRequireReason(t, release.ValidateReleaseDirectoryWithTrust("must-not-be-read", trust), "unsupported_platform")
	taskCRequireReason(t, release.ValidateReleaseDirectoryForPromotionWithTrust("must-not-be-read", trust, time.Now().UTC()), "unsupported_platform")
	taskCRequireReason(t, release.PromoteSealedDirectory("must-not-be-read", "must-not-be-written"), "unsupported_platform")
	taskCRequireReason(t, release.PromoteSealedDirectoryWithTrust("must-not-be-read", "must-not-be-written", trust), "unsupported_platform")

	root := filepath.Join(t.TempDir(), "must-not-be-created")
	_, err := release.NewActivationStore(release.ActivationStoreConfig{Root: root})
	taskCRequireReason(t, err, "unsupported_platform")
	if _, statErr := os.Lstat(root); !os.IsNotExist(statErr) {
		t.Fatalf("unsupported constructor mutated root: %v", statErr)
	}
}

func TestTaskCPureConfigParsingRemainsCrossPlatform(t *testing.T) {
	if err := release.ValidateConfigTemplate(release.DefaultConfigTemplate()); err != nil {
		t.Fatalf("ValidateConfigTemplate: %v", err)
	}
	if err := release.ValidateSystemdTemplate(release.DefaultSystemdTemplate()); err != nil {
		t.Fatalf("ValidateSystemdTemplate: %v", err)
	}
	if err := release.ValidateRollbackTemplate(release.DefaultRollbackTemplate()); err != nil {
		t.Fatalf("ValidateRollbackTemplate: %v", err)
	}
}
