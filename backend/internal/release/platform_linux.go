//go:build linux

package release

import (
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func requireSealedPlatform() error { return nil }

func currentTrustedOwnerUID() (uint32, error) {
	return uint32(unix.Geteuid()), nil
}

func captureTrustedReleaseDirectory(path string, trustedOwnerUID uint32) (filesystemIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR ||
		stat.Uid != trustedOwnerUID || !safeLinuxReleaseDirectoryMode(stat.Mode) {
		return filesystemIdentity{}, invalid("release_root_unsealed")
	}
	return linuxIdentity(stat), nil
}

func captureTrustedParentDirectory(path string, trustedOwnerUID uint32) (filesystemIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR ||
		stat.Uid != trustedOwnerUID || !safeLinuxParentMode(stat.Mode) {
		return filesystemIdentity{}, invalid("promotion_parent_untrusted")
	}
	return linuxIdentity(stat), nil
}

func platformFileOwnedBy(info os.FileInfo, trustedOwnerUID uint32) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == trustedOwnerUID
}

func platformRenameNoReplace(staging, published string, expectedParent, expectedStaging filesystemIdentity, trustedOwnerUID uint32) (filesystemIdentity, error) {
	parent := filepath.Dir(staging)
	parentFD, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return filesystemIdentity{}, invalid("promotion_parent_changed")
	}
	defer unix.Close(parentFD)
	var parentStat unix.Stat_t
	if err := unix.Fstat(parentFD, &parentStat); err != nil || linuxIdentity(parentStat) != expectedParent ||
		parentStat.Uid != trustedOwnerUID || !safeLinuxParentMode(parentStat.Mode) {
		return filesystemIdentity{}, invalid("promotion_parent_changed")
	}
	var stagingStat unix.Stat_t
	if err := unix.Fstatat(parentFD, filepath.Base(staging), &stagingStat, unix.AT_SYMLINK_NOFOLLOW); err != nil ||
		linuxIdentity(stagingStat) != expectedStaging || stagingStat.Uid != trustedOwnerUID ||
		!safeLinuxReleaseDirectoryMode(stagingStat.Mode) {
		return filesystemIdentity{}, invalid("promotion_source_changed")
	}
	if err := unix.Renameat2(parentFD, filepath.Base(staging), parentFD, filepath.Base(published), unix.RENAME_NOREPLACE); err != nil {
		if err == unix.EEXIST {
			return filesystemIdentity{}, invalid("promotion_destination_exists")
		}
		return filesystemIdentity{}, invalid("promotion_rename_failed")
	}
	var publishedStat unix.Stat_t
	if err := unix.Fstatat(parentFD, filepath.Base(published), &publishedStat, unix.AT_SYMLINK_NOFOLLOW); err != nil ||
		linuxIdentity(publishedStat) != expectedStaging || publishedStat.Uid != trustedOwnerUID ||
		!safeLinuxReleaseDirectoryMode(publishedStat.Mode) {
		return filesystemIdentity{}, invalid("promotion_inode_changed")
	}
	return linuxIdentity(publishedStat), nil
}

func platformSyncSealedRelease(root string, names []string, expectedParent filesystemIdentity, trustedOwnerUID uint32) error {
	for _, name := range names {
		path := filepath.Join(root, name)
		fileFD, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return invalid("promotion_sync_failed")
		}
		var stat unix.Stat_t
		valid := unix.Fstat(fileFD, &stat) == nil && stat.Mode&unix.S_IFMT == unix.S_IFREG && stat.Uid == trustedOwnerUID &&
			stat.Nlink == 1 && safeReleaseFileMode(name, linuxFileMode(stat.Mode))
		syncErr := unix.Fsync(fileFD)
		closeErr := unix.Close(fileFD)
		if !valid || syncErr != nil || closeErr != nil {
			return invalid("promotion_sync_failed")
		}
	}
	rootFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return invalid("promotion_sync_failed")
	}
	rootSyncErr := unix.Fsync(rootFD)
	rootCloseErr := unix.Close(rootFD)
	if rootSyncErr != nil || rootCloseErr != nil {
		return invalid("promotion_sync_failed")
	}
	parent := filepath.Dir(root)
	parentFD, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return invalid("promotion_sync_failed")
	}
	var parentStat unix.Stat_t
	validParent := unix.Fstat(parentFD, &parentStat) == nil && linuxIdentity(parentStat) == expectedParent &&
		parentStat.Uid == trustedOwnerUID && safeLinuxParentMode(parentStat.Mode)
	parentSyncErr := unix.Fsync(parentFD)
	parentCloseErr := unix.Close(parentFD)
	if !validParent || parentSyncErr != nil || parentCloseErr != nil {
		return invalid("promotion_sync_failed")
	}
	return nil
}

func linuxIdentity(stat unix.Stat_t) filesystemIdentity {
	return filesystemIdentity{device: uint64(stat.Dev), inode: stat.Ino}
}

func safeLinuxReleaseDirectoryMode(mode uint32) bool {
	permissions := mode & 0o777
	return mode&unix.S_IFMT == unix.S_IFDIR && mode&(unix.S_ISUID|unix.S_ISGID|unix.S_ISVTX) == 0 &&
		permissions&0o100 != 0 && permissions&0o222 == 0
}

func safeLinuxParentMode(mode uint32) bool {
	permissions := mode & 0o777
	return mode&unix.S_IFMT == unix.S_IFDIR && mode&(unix.S_ISUID|unix.S_ISGID|unix.S_ISVTX) == 0 &&
		permissions&0o700 == 0o700 && permissions&0o022 == 0
}

func linuxFileMode(mode uint32) os.FileMode {
	result := os.FileMode(mode & 0o777)
	if mode&unix.S_ISUID != 0 {
		result |= os.ModeSetuid
	}
	if mode&unix.S_ISGID != 0 {
		result |= os.ModeSetgid
	}
	if mode&unix.S_ISVTX != 0 {
		result |= os.ModeSticky
	}
	return result
}
