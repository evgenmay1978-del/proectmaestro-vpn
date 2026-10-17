//go:build linux

package main

import (
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func readSecureFile(filePath string, class secureFileClass, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, errUnsafeRuntime
	}
	descriptor, before, err := openSecureRegularFile(filePath, class)
	if err != nil {
		return nil, errUnsafeRuntime
	}
	file := os.NewFile(uintptr(descriptor), "secure-file")
	if file == nil {
		_ = unix.Close(descriptor)
		return nil, errUnsafeRuntime
	}
	defer func() { _ = file.Close() }()
	if before.Size < 0 || before.Size > limit {
		return nil, errUnsafeRuntime
	}
	limited := &io.LimitedReader{R: file, N: limit + 1}
	data, err := io.ReadAll(limited)
	if err != nil || int64(len(data)) > limit || int64(len(data)) != before.Size {
		return nil, errUnsafeRuntime
	}
	var after unix.Stat_t
	if err := unix.Fstat(descriptor, &after); err != nil || !sameSecureStat(before, &after) {
		return nil, errUnsafeRuntime
	}
	var pathAfter unix.Stat_t
	if err := unix.Fstatat(unix.AT_FDCWD, filePath, &pathAfter, unix.AT_SYMLINK_NOFOLLOW); err != nil ||
		!sameSecureStat(before, &pathAfter) {
		return nil, errUnsafeRuntime
	}
	return data, nil
}

func verifySecureFile(filePath string, class secureFileClass) error {
	descriptor, before, err := openSecureRegularFile(filePath, class)
	if err != nil {
		return errUnsafeRuntime
	}
	defer func() { _ = unix.Close(descriptor) }()
	var pathAfter unix.Stat_t
	if err := unix.Fstatat(unix.AT_FDCWD, filePath, &pathAfter, unix.AT_SYMLINK_NOFOLLOW); err != nil ||
		!sameSecureStat(before, &pathAfter) {
		return errUnsafeRuntime
	}
	return nil
}

func verifySecureDirectory(directoryPath string) error {
	descriptor, stat, err := openSecurePath(directoryPath, true)
	if err != nil {
		return errUnsafeRuntime
	}
	defer func() { _ = unix.Close(descriptor) }()
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o7777 != 0o700 || !secureOwner(stat.Uid) {
		return errUnsafeRuntime
	}
	var pathAfter unix.Stat_t
	if err := unix.Fstatat(unix.AT_FDCWD, directoryPath, &pathAfter, unix.AT_SYMLINK_NOFOLLOW); err != nil ||
		!sameSecureStat(stat, &pathAfter) {
		return errUnsafeRuntime
	}
	return nil
}

func openPinnedSecureFile(filePath string, class secureFileClass) (*os.File, error) {
	descriptor, before, err := openSecureRegularFile(filePath, class)
	if err != nil {
		return nil, errUnsafeRuntime
	}
	var pathAfter unix.Stat_t
	if unix.Fstatat(unix.AT_FDCWD, filePath, &pathAfter, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!sameSecureStat(before, &pathAfter) {
		_ = unix.Close(descriptor)
		return nil, errUnsafeRuntime
	}
	file := os.NewFile(uintptr(descriptor), "pinned-secure-file")
	if file == nil {
		_ = unix.Close(descriptor)
		return nil, errUnsafeRuntime
	}
	return file, nil
}

func openPinnedSecureDirectory(directoryPath string) (*os.File, error) {
	descriptor, stat, err := openSecurePath(directoryPath, true)
	if err != nil ||
		stat.Mode&unix.S_IFMT != unix.S_IFDIR ||
		stat.Mode&0o7777 != 0o700 ||
		!secureOwner(stat.Uid) {
		if descriptor >= 0 {
			_ = unix.Close(descriptor)
		}
		return nil, errUnsafeRuntime
	}
	var pathAfter unix.Stat_t
	if unix.Fstatat(unix.AT_FDCWD, directoryPath, &pathAfter, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!sameSecureStat(stat, &pathAfter) {
		_ = unix.Close(descriptor)
		return nil, errUnsafeRuntime
	}
	file := os.NewFile(uintptr(descriptor), "pinned-secure-directory")
	if file == nil {
		_ = unix.Close(descriptor)
		return nil, errUnsafeRuntime
	}
	return file, nil
}

func pinnedSecurePath(file *os.File) string {
	if file == nil {
		return ""
	}
	return "/proc/self/fd/" + strconv.FormatUint(uint64(file.Fd()), 10)
}

func openSecureRegularFile(filePath string, class secureFileClass) (int, *unix.Stat_t, error) {
	descriptor, stat, err := openSecurePath(filePath, false)
	if err != nil {
		return -1, nil, errUnsafeRuntime
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || !secureOwner(stat.Uid) ||
		!secureMode(stat.Mode&0o7777, class) {
		_ = unix.Close(descriptor)
		return -1, nil, errUnsafeRuntime
	}
	return descriptor, stat, nil
}

func openSecurePath(target string, directory bool) (int, *unix.Stat_t, error) {
	if !validPOSIXPath(target, false) {
		return -1, nil, errUnsafeRuntime
	}
	parts := strings.Split(strings.TrimPrefix(target, "/"), "/")
	if len(parts) == 0 {
		return -1, nil, errUnsafeRuntime
	}
	current, err := unix.Open("/", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY, 0)
	if err != nil {
		return -1, nil, errUnsafeRuntime
	}
	for index, component := range parts {
		var ancestor unix.Stat_t
		if unix.Fstat(current, &ancestor) != nil || !secureAncestorDirectory(&ancestor) {
			_ = unix.Close(current)
			return -1, nil, errUnsafeRuntime
		}
		if component == "" || component == "." || component == ".." {
			_ = unix.Close(current)
			return -1, nil, errUnsafeRuntime
		}
		final := index == len(parts)-1
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW
		if !final || directory {
			flags |= unix.O_DIRECTORY
		}
		next, err := unix.Openat(current, component, flags, 0)
		_ = unix.Close(current)
		if err != nil {
			return -1, nil, errUnsafeRuntime
		}
		current = next
	}
	var stat unix.Stat_t
	if err := unix.Fstat(current, &stat); err != nil {
		_ = unix.Close(current)
		return -1, nil, errUnsafeRuntime
	}
	return current, &stat, nil
}

func secureAncestorDirectory(stat *unix.Stat_t) bool {
	if stat == nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || !secureOwner(stat.Uid) {
		return false
	}
	if stat.Mode&0o022 == 0 {
		return true
	}
	return stat.Uid == 0 && stat.Mode&unix.S_ISVTX != 0
}

func secureOwner(uid uint32) bool {
	return uid == 0 || uid == uint32(unix.Geteuid())
}

func secureMode(mode uint32, class secureFileClass) bool {
	switch class {
	case secureSecretFile:
		return mode == 0o600
	case securePublicFile:
		return mode == 0o600 || mode == 0o644
	case secureExecutableFile:
		return mode == 0o700 || mode == 0o755
	default:
		return false
	}
}

func sameSecureStat(left *unix.Stat_t, right *unix.Stat_t) bool {
	return left != nil && right != nil &&
		left.Dev == right.Dev && left.Ino == right.Ino &&
		left.Uid == right.Uid && left.Gid == right.Gid &&
		left.Mode == right.Mode && left.Nlink == right.Nlink &&
		left.Size == right.Size && left.Mtim == right.Mtim && left.Ctim == right.Ctim
}
