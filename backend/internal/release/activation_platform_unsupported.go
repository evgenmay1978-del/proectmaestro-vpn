//go:build !linux

package release

import "time"

type activationLockedRoot struct{}

func activationPlatformSupported() bool { return false }

func activationWithRootLock(_ string, _ uint32, _ *activationFilesystemAnchor, _ func(*activationLockedRoot) error) error {
	return invalid("unsupported_platform")
}

func (*activationLockedRoot) activationAnchor() activationFilesystemAnchor {
	return activationFilesystemAnchor{}
}

func (*activationLockedRoot) activationReadRegular(_ string, _ int) ([]byte, bool, error) {
	return nil, false, invalid("unsupported_platform")
}

func (*activationLockedRoot) activationEntryExists(_ string) (bool, error) {
	return false, invalid("unsupported_platform")
}

func (*activationLockedRoot) activationReadCurrent() (string, bool, error) {
	return "", false, invalid("unsupported_platform")
}

func (*activationLockedRoot) activationReleaseDirExists(_ string) (bool, error) {
	return false, invalid("unsupported_platform")
}

func (*activationLockedRoot) activationInspectSealedRelease(_ string, _ EvidenceTrust, _ *time.Time) (sealedReleaseIdentity, error) {
	return sealedReleaseIdentity{}, invalid("unsupported_platform")
}

func (*activationLockedRoot) activationPrepareSealedRelease(_ string, _ EvidenceTrust, _ *time.Time) (sealedReleaseIdentity, error) {
	return sealedReleaseIdentity{}, invalid("unsupported_platform")
}

func (*activationLockedRoot) activationPromoteSealedRelease(_, _ string, _ EvidenceTrust, _ time.Time, _ func() error) (sealedReleaseIdentity, error) {
	return sealedReleaseIdentity{}, invalid("unsupported_platform")
}

func (*activationLockedRoot) activationWriteNoReplace(_, _ string, _ []byte) error {
	return invalid("unsupported_platform")
}

func (*activationLockedRoot) activationWriteReplaceExpected(_, _ string, _, _ []byte) error {
	return invalid("unsupported_platform")
}

func (*activationLockedRoot) activationSwapCurrent(_ string, _ bool, _ string) error {
	return invalid("unsupported_platform")
}

func (*activationLockedRoot) activationRemoveExact(_ string, _ []byte) error {
	return invalid("unsupported_platform")
}

func (*activationLockedRoot) activationRepairTemps(_ *activationIntent) error {
	return invalid("unsupported_platform")
}

func (*activationLockedRoot) activationSyncSealedRelease(_ string) error {
	return invalid("unsupported_platform")
}
