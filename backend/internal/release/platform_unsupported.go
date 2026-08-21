//go:build !linux

package release

import "os"

func requireSealedPlatform() error { return invalid("unsupported_platform") }

func currentTrustedOwnerUID() (uint32, error) {
	return 0, invalid("unsupported_platform")
}

func captureTrustedReleaseDirectory(string, uint32) (filesystemIdentity, error) {
	return filesystemIdentity{}, invalid("unsupported_platform")
}

func captureTrustedParentDirectory(string, uint32) (filesystemIdentity, error) {
	return filesystemIdentity{}, invalid("unsupported_platform")
}

func platformFileOwnedBy(os.FileInfo, uint32) bool { return false }

func platformRenameNoReplace(string, string, filesystemIdentity, filesystemIdentity, uint32) (filesystemIdentity, error) {
	return filesystemIdentity{}, invalid("unsupported_platform")
}

func platformSyncSealedRelease(string, []string, filesystemIdentity, uint32) error {
	return invalid("unsupported_platform")
}
