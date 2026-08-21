//go:build linux

package release

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const activationOPath = 0x200000

type activationLockedRoot struct {
	fd           int
	releasesFD   int
	trustedUID   uint32
	rootStat     syscall.Stat_t
	releasesStat syscall.Stat_t
}

type activationAnchoredArtifact struct {
	name   string
	file   *os.File
	before unix.Stat_t
	raw    []byte
}

func activationPlatformSupported() bool { return true }

// The owner-only root and releases directory are the trust boundary. Processes
// running as root, or as the same UID while ignoring activation.lock, are out of
// scope; protecting against those actors requires fs-verity or a separate UID.
func activationWithRootLock(rootPath string, trustedUID uint32, expected *activationFilesystemAnchor, callback func(*activationLockedRoot) error) error {
	rootFD, rootStat, err := activationOpenAbsoluteDirectory(rootPath, trustedUID)
	if err != nil || !activationWritableDirectory(&rootStat, trustedUID) {
		if rootFD >= 0 {
			_ = syscall.Close(rootFD)
		}
		return invalid("activation_root_untrusted")
	}
	defer syscall.Close(rootFD)
	if expected != nil && activationSyscallIdentity(rootStat) != expected.root {
		return invalid("activation_root_changed")
	}

	lockFD, lockCreated, err := activationOpenLock(rootFD, trustedUID)
	if err != nil {
		return err
	}
	defer syscall.Close(lockFD)
	if lockCreated {
		if err := syscall.Fsync(lockFD); err != nil || syscall.Fsync(rootFD) != nil {
			return invalid("activation_durability_failed")
		}
	}
	if err := syscall.Flock(lockFD, syscall.LOCK_EX); err != nil {
		return invalid("activation_lock_failed")
	}
	defer syscall.Flock(lockFD, syscall.LOCK_UN)

	checkFD, checkStat, err := activationOpenAbsoluteDirectory(rootPath, trustedUID)
	if err != nil {
		return invalid("activation_root_changed")
	}
	_ = syscall.Close(checkFD)
	if !activationSameDirectory(rootStat, checkStat) || !activationWritableDirectory(&checkStat, trustedUID) {
		return invalid("activation_root_changed")
	}

	releasesFD, created, err := activationOpenOrCreateReleases(rootFD, expected == nil)
	if err != nil {
		return err
	}
	defer syscall.Close(releasesFD)
	if created {
		if err := syscall.Fsync(rootFD); err != nil {
			return invalid("activation_durability_failed")
		}
	}
	var releasesStat syscall.Stat_t
	if err := syscall.Fstat(releasesFD, &releasesStat); err != nil ||
		!activationWritableDirectory(&releasesStat, trustedUID) {
		return invalid("activation_release_parent_untrusted")
	}
	if expected != nil && activationSyscallIdentity(releasesStat) != expected.releases {
		return invalid("activation_release_parent_changed")
	}
	locked := &activationLockedRoot{
		fd: rootFD, releasesFD: releasesFD, trustedUID: trustedUID,
		rootStat: rootStat, releasesStat: releasesStat,
	}
	if err := locked.activationRevalidate(); err != nil {
		return err
	}
	return callback(locked)
}

func (root *activationLockedRoot) activationAnchor() activationFilesystemAnchor {
	return activationFilesystemAnchor{
		root:     activationSyscallIdentity(root.rootStat),
		releases: activationSyscallIdentity(root.releasesStat),
	}
}

func activationOpenLock(rootFD int, trustedUID uint32) (int, bool, error) {
	flags := syscall.O_RDWR | syscall.O_NOFOLLOW | syscall.O_CLOEXEC
	lockFD, err := syscall.Openat(rootFD, "activation.lock", flags|syscall.O_CREAT|syscall.O_EXCL, 0o600)
	created := err == nil
	if errors.Is(err, syscall.EEXIST) {
		lockFD, err = syscall.Openat(rootFD, "activation.lock", flags, 0)
	}
	if err != nil {
		return -1, false, invalid("activation_lock_failed")
	}
	closeInvalid := func(code string) (int, bool, error) {
		_ = syscall.Close(lockFD)
		return -1, false, invalid(code)
	}
	var lockStat syscall.Stat_t
	if err := syscall.Fstat(lockFD, &lockStat); err != nil ||
		!activationTrustedLockInode(&lockStat, trustedUID) {
		return closeInvalid("activation_lock_untrusted")
	}
	if !created {
		if !activationMutableRegular(&lockStat, trustedUID) {
			return closeInvalid("activation_lock_untrusted")
		}
		return lockFD, false, nil
	}
	if err := syscall.Fchmod(lockFD, 0o600); err != nil {
		return closeInvalid("activation_lock_failed")
	}
	if err := syscall.Fstat(lockFD, &lockStat); err != nil ||
		!activationMutableRegular(&lockStat, trustedUID) {
		return closeInvalid("activation_lock_untrusted")
	}
	return lockFD, true, nil
}

func activationTrustedLockInode(stat *syscall.Stat_t, trustedUID uint32) bool {
	return stat != nil && stat.Mode&syscall.S_IFMT == syscall.S_IFREG &&
		stat.Uid == trustedUID && stat.Nlink == 1 && stat.Mode&0o7000 == 0
}

func activationOpenAbsoluteDirectory(value string, trustedUID uint32) (int, syscall.Stat_t, error) {
	var zero syscall.Stat_t
	if !filepath.IsAbs(value) || filepath.Clean(value) != value || value == "/" ||
		strings.IndexByte(value, 0) >= 0 {
		return -1, zero, syscall.EINVAL
	}
	fd, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return -1, zero, err
	}
	components := strings.Split(strings.TrimPrefix(value, "/"), "/")
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			_ = syscall.Close(fd)
			return -1, zero, syscall.EINVAL
		}
		var ancestorStat syscall.Stat_t
		if err := syscall.Fstat(fd, &ancestorStat); err != nil ||
			!activationTrustedAncestor(&ancestorStat, trustedUID) {
			_ = syscall.Close(fd)
			return -1, zero, syscall.EPERM
		}
		next, openErr := syscall.Openat(
			fd, component,
			syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
			0,
		)
		_ = syscall.Close(fd)
		if openErr != nil {
			return -1, zero, openErr
		}
		fd = next
	}
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		_ = syscall.Close(fd)
		return -1, zero, err
	}
	return fd, stat, nil
}

func activationOpenOrCreateReleases(rootFD int, allowCreate bool) (int, bool, error) {
	fd, err := syscall.Openat(
		rootFD, "releases",
		syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
		0,
	)
	if err == nil {
		return fd, false, nil
	}
	if !errors.Is(err, syscall.ENOENT) {
		return -1, false, invalid("activation_release_parent_untrusted")
	}
	if !allowCreate {
		return -1, false, invalid("activation_release_parent_changed")
	}
	if err := syscall.Mkdirat(rootFD, "releases", 0o700); err != nil {
		return -1, false, invalid("activation_layout_failed")
	}
	fd, err = syscall.Openat(
		rootFD, "releases",
		syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
		0,
	)
	if err != nil {
		return -1, false, invalid("activation_layout_failed")
	}
	return fd, true, nil
}

func (root *activationLockedRoot) activationRevalidate() error {
	var rootStat syscall.Stat_t
	if err := syscall.Fstat(root.fd, &rootStat); err != nil ||
		!activationSameDirectory(root.rootStat, rootStat) ||
		!activationWritableDirectory(&rootStat, root.trustedUID) {
		return invalid("activation_root_changed")
	}
	releasesFD, err := syscall.Openat(
		root.fd, "releases",
		syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
		0,
	)
	if err != nil {
		return invalid("activation_release_parent_changed")
	}
	defer syscall.Close(releasesFD)
	var releasesStat syscall.Stat_t
	if err := syscall.Fstat(releasesFD, &releasesStat); err != nil ||
		!activationSameDirectory(root.releasesStat, releasesStat) ||
		!activationWritableDirectory(&releasesStat, root.trustedUID) {
		return invalid("activation_release_parent_changed")
	}
	var retainedStat syscall.Stat_t
	if err := syscall.Fstat(root.releasesFD, &retainedStat); err != nil ||
		!activationSameDirectory(root.releasesStat, retainedStat) ||
		!activationWritableDirectory(&retainedStat, root.trustedUID) {
		return invalid("activation_release_parent_changed")
	}
	return nil
}

func (root *activationLockedRoot) activationReadRegular(name string, limit int) ([]byte, bool, error) {
	if err := root.activationRevalidate(); err != nil {
		return nil, false, err
	}
	fd, err := syscall.Openat(
		root.fd, name,
		syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
		0,
	)
	if errors.Is(err, syscall.ENOENT) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, invalid("activation_file_invalid")
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = syscall.Close(fd)
		return nil, false, invalid("activation_file_invalid")
	}
	defer file.Close()
	var before syscall.Stat_t
	if err := syscall.Fstat(fd, &before); err != nil ||
		!activationMutableRegular(&before, root.trustedUID) ||
		before.Size <= 0 || limit <= 0 || before.Size > int64(limit) {
		return nil, false, invalid("activation_file_invalid")
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil || int64(len(raw)) != before.Size || len(raw) > limit {
		return nil, false, invalid("activation_file_read_failed")
	}
	var after syscall.Stat_t
	if err := syscall.Fstat(fd, &after); err != nil || !activationSameFile(before, after) {
		return nil, false, invalid("activation_file_changed")
	}
	pathFD, err := syscall.Openat(
		root.fd, name,
		activationOPath|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
		0,
	)
	if err != nil {
		return nil, false, invalid("activation_file_changed")
	}
	var pathStat syscall.Stat_t
	statErr := syscall.Fstat(pathFD, &pathStat)
	_ = syscall.Close(pathFD)
	if statErr != nil || !activationSameFile(before, pathStat) {
		return nil, false, invalid("activation_file_changed")
	}
	return raw, true, nil
}

func (root *activationLockedRoot) activationEntryExists(name string) (bool, error) {
	if err := root.activationRevalidate(); err != nil {
		return false, err
	}
	fd, err := syscall.Openat(
		root.fd, name,
		activationOPath|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
		0,
	)
	if errors.Is(err, syscall.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, invalid("activation_entry_invalid")
	}
	_ = syscall.Close(fd)
	return true, nil
}

func (root *activationLockedRoot) activationReadCurrent() (string, bool, error) {
	if err := root.activationRevalidate(); err != nil {
		return "", false, err
	}
	fd, err := syscall.Openat(
		root.fd, "current",
		activationOPath|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
		0,
	)
	if errors.Is(err, syscall.ENOENT) {
		return "", false, nil
	}
	if err != nil {
		return "", false, invalid("activation_current_invalid")
	}
	var before syscall.Stat_t
	statErr := syscall.Fstat(fd, &before)
	_ = syscall.Close(fd)
	if statErr != nil || before.Mode&syscall.S_IFMT != syscall.S_IFLNK ||
		before.Uid != root.trustedUID || before.Nlink != 1 {
		return "", false, invalid("activation_current_invalid")
	}
	buffer := make([]byte, 4096)
	size, err := activationReadlinkAt(root.fd, "current", buffer)
	if err != nil || size <= 0 || size == len(buffer) {
		return "", false, invalid("activation_current_invalid")
	}
	pathFD, err := syscall.Openat(
		root.fd, "current",
		activationOPath|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
		0,
	)
	if err != nil {
		return "", false, invalid("activation_current_changed")
	}
	var after syscall.Stat_t
	statErr = syscall.Fstat(pathFD, &after)
	_ = syscall.Close(pathFD)
	if statErr != nil || !activationSameFile(before, after) {
		return "", false, invalid("activation_current_changed")
	}
	return string(buffer[:size]), true, nil
}

func (root *activationLockedRoot) activationReleaseDirExists(name string) (bool, error) {
	if !validID(name) || name != filepath.Base(name) {
		return false, invalid("activation_release_path_invalid")
	}
	if err := root.activationRevalidate(); err != nil {
		return false, err
	}
	fd, err := syscall.Openat(
		root.releasesFD, name,
		activationOPath|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
		0,
	)
	if errors.Is(err, syscall.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, invalid("activation_release_path_invalid")
	}
	defer syscall.Close(fd)
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil ||
		!activationSealedDirectory(&stat, root.trustedUID) {
		return false, invalid("activation_release_path_invalid")
	}
	return true, nil
}

func (root *activationLockedRoot) activationInspectSealedRelease(name string, trust EvidenceTrust, admissionTime *time.Time) (sealedReleaseIdentity, error) {
	if err := trust.validate(); err != nil {
		return sealedReleaseIdentity{}, err
	}
	if !validID(name) || name != filepath.Base(name) {
		return sealedReleaseIdentity{}, invalid("activation_release_path_invalid")
	}
	if err := root.activationRevalidate(); err != nil {
		return sealedReleaseIdentity{}, err
	}
	dirFD, err := unix.Openat(root.releasesFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return sealedReleaseIdentity{}, invalid("release_root_unsealed")
	}
	dir := os.NewFile(uintptr(dirFD), name)
	if dir == nil {
		_ = unix.Close(dirFD)
		return sealedReleaseIdentity{}, invalid("release_root_unsealed")
	}
	opened := make([]activationAnchoredArtifact, 0, len(allowedArtifactPaths())+1)
	defer func() {
		for index := range opened {
			if opened[index].file != nil {
				_ = opened[index].file.Close()
			}
		}
		if dir != nil {
			_ = dir.Close()
		}
	}()

	var beforeDir unix.Stat_t
	if err := unix.Fstat(dirFD, &beforeDir); err != nil ||
		beforeDir.Uid != root.trustedUID || !safeLinuxReleaseDirectoryMode(beforeDir.Mode) {
		return sealedReleaseIdentity{}, invalid("release_root_unsealed")
	}
	names := append([]string{"manifest.json"}, allowedArtifactPaths()...)
	entries, err := dir.ReadDir(-1)
	if err != nil || len(entries) != len(names) {
		return sealedReleaseIdentity{}, invalid("release_entry_set_invalid")
	}
	expected := make(map[string]struct{}, len(names))
	for _, artifactName := range names {
		expected[artifactName] = struct{}{}
	}
	for _, entry := range entries {
		if _, ok := expected[entry.Name()]; !ok {
			return sealedReleaseIdentity{}, invalid("release_entry_invalid")
		}
	}

	byName := make(map[string][]byte, len(names))
	for _, artifactName := range names {
		limit := int64(maxManifestBytes)
		if artifactName != "manifest.json" {
			limit = artifactSizeLimit(artifactName)
		}
		artifact, err := activationOpenAnchoredArtifact(dirFD, artifactName, limit, root.trustedUID)
		if err != nil {
			return sealedReleaseIdentity{}, err
		}
		opened = append(opened, artifact)
		byName[artifactName] = artifact.raw
	}
	manifestBytes := byName["manifest.json"]
	manifest, err := ParseManifest(manifestBytes)
	if err != nil {
		return sealedReleaseIdentity{}, err
	}
	artifacts := make(map[string][]byte, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		artifacts[artifact.Path] = byName[artifact.Path]
	}
	value := Release{manifest: manifest}
	if err := value.verifyArtifactsWithTrustAt(artifacts, trust, admissionTime); err != nil {
		return sealedReleaseIdentity{}, err
	}
	for index := range opened {
		var handleStat, pathStat unix.Stat_t
		if err := unix.Fstat(int(opened[index].file.Fd()), &handleStat); err != nil ||
			unix.Fstatat(dirFD, opened[index].name, &pathStat, unix.AT_SYMLINK_NOFOLLOW) != nil ||
			!activationUnixSameFile(opened[index].before, handleStat) ||
			!activationUnixSameFile(opened[index].before, pathStat) {
			return sealedReleaseIdentity{}, invalid("artifact_inode_changed")
		}
	}
	var afterDir, pathDir unix.Stat_t
	if err := unix.Fstat(dirFD, &afterDir); err != nil ||
		unix.Fstatat(root.releasesFD, name, &pathDir, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!activationUnixSameDirectory(beforeDir, afterDir) ||
		!activationUnixSameDirectory(beforeDir, pathDir) {
		return sealedReleaseIdentity{}, invalid("release_root_changed")
	}
	if err := root.activationRevalidate(); err != nil {
		return sealedReleaseIdentity{}, err
	}
	for index := range opened {
		if err := opened[index].file.Close(); err != nil {
			opened[index].file = nil
			return sealedReleaseIdentity{}, invalid("artifact_read_invalid")
		}
		opened[index].file = nil
	}
	if err := dir.Close(); err != nil {
		dir = nil
		return sealedReleaseIdentity{}, invalid("artifact_read_invalid")
	}
	dir = nil
	return sealedReleaseIdentity{Manifest: cloneManifest(manifest), ManifestSHA256: digestBytes(manifestBytes)}, nil
}

func (root *activationLockedRoot) activationPrepareSealedRelease(name string, trust EvidenceTrust, admissionTime *time.Time) (sealedReleaseIdentity, error) {
	if err := root.activationSyncSealedRelease(name); err != nil {
		return sealedReleaseIdentity{}, err
	}
	return root.activationInspectSealedRelease(name, trust, admissionTime)
}

func activationOpenAnchoredArtifact(dirFD int, name string, limit int64, trustedUID uint32) (activationAnchoredArtifact, error) {
	fd, err := unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return activationAnchoredArtifact{}, invalid("artifact_open_failed")
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return activationAnchoredArtifact{}, invalid("artifact_open_failed")
	}
	closeInvalid := func(code string) (activationAnchoredArtifact, error) {
		_ = file.Close()
		return activationAnchoredArtifact{}, invalid(code)
	}
	var before unix.Stat_t
	if err := unix.Fstat(fd, &before); err != nil || limit <= 0 || before.Size <= 0 || before.Size > limit ||
		before.Mode&unix.S_IFMT != unix.S_IFREG || before.Uid != trustedUID || before.Nlink != 1 ||
		!safeReleaseFileMode(name, linuxFileMode(before.Mode)) {
		return closeInvalid("artifact_stat_invalid")
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(raw)) != before.Size || int64(len(raw)) > limit {
		return closeInvalid("artifact_read_invalid")
	}
	var after, pathStat unix.Stat_t
	if err := unix.Fstat(fd, &after); err != nil ||
		unix.Fstatat(dirFD, name, &pathStat, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!activationUnixSameFile(before, after) || !activationUnixSameFile(before, pathStat) {
		return closeInvalid("artifact_inode_changed")
	}
	return activationAnchoredArtifact{name: name, file: file, before: before, raw: raw}, nil
}

func (root *activationLockedRoot) activationPromoteSealedRelease(stagingName, publishedName string, trust EvidenceTrust, admissionTime time.Time, beforeRename func() error) (sealedReleaseIdentity, error) {
	if admissionTime.IsZero() {
		return sealedReleaseIdentity{}, invalid("promotion_time_invalid")
	}
	if !validID(stagingName) || stagingName != filepath.Base(stagingName) ||
		!validID(publishedName) || publishedName != filepath.Base(publishedName) || stagingName == publishedName {
		return sealedReleaseIdentity{}, invalid("promotion_path_invalid")
	}
	if err := root.activationRevalidate(); err != nil {
		return sealedReleaseIdentity{}, err
	}
	var stagingStat unix.Stat_t
	if err := unix.Fstatat(root.releasesFD, stagingName, &stagingStat, unix.AT_SYMLINK_NOFOLLOW); err != nil ||
		stagingStat.Uid != root.trustedUID || !safeLinuxReleaseDirectoryMode(stagingStat.Mode) {
		return sealedReleaseIdentity{}, invalid("promotion_source_changed")
	}
	admissionTime = admissionTime.UTC()
	inspected, err := root.activationInspectSealedRelease(stagingName, trust, &admissionTime)
	if err != nil {
		return sealedReleaseIdentity{}, err
	}
	if beforeRename != nil {
		if err := beforeRename(); err != nil {
			return sealedReleaseIdentity{}, err
		}
	}
	if err := root.activationRevalidate(); err != nil {
		return sealedReleaseIdentity{}, err
	}
	var immediateStat unix.Stat_t
	if err := unix.Fstatat(root.releasesFD, stagingName, &immediateStat, unix.AT_SYMLINK_NOFOLLOW); err != nil ||
		!activationUnixSameDirectory(stagingStat, immediateStat) {
		return sealedReleaseIdentity{}, invalid("promotion_source_changed")
	}
	if err := unix.Renameat2(root.releasesFD, stagingName, root.releasesFD, publishedName, unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return sealedReleaseIdentity{}, invalid("promotion_destination_exists")
		}
		return sealedReleaseIdentity{}, invalid("promotion_rename_failed")
	}
	var publishedStat unix.Stat_t
	if err := unix.Fstatat(root.releasesFD, publishedName, &publishedStat, unix.AT_SYMLINK_NOFOLLOW); err != nil ||
		!activationUnixSameDirectory(stagingStat, publishedStat) {
		return sealedReleaseIdentity{}, invalid("promotion_inode_changed")
	}
	promoted, err := root.activationPrepareSealedRelease(publishedName, trust, &admissionTime)
	if err != nil {
		return sealedReleaseIdentity{}, err
	}
	if promoted.Manifest.ReleaseID != inspected.Manifest.ReleaseID ||
		!equalDigest(promoted.ManifestSHA256, inspected.ManifestSHA256) {
		return sealedReleaseIdentity{}, invalid("promotion_release_changed")
	}
	var finalStat unix.Stat_t
	if err := unix.Fstatat(root.releasesFD, publishedName, &finalStat, unix.AT_SYMLINK_NOFOLLOW); err != nil ||
		!activationUnixSameDirectory(stagingStat, finalStat) {
		return sealedReleaseIdentity{}, invalid("promotion_inode_changed")
	}
	if err := root.activationRevalidate(); err != nil {
		return sealedReleaseIdentity{}, err
	}
	return promoted, nil
}

func (root *activationLockedRoot) activationWriteNoReplace(tempName, targetName string, raw []byte) error {
	return root.activationWriteAtomic(tempName, targetName, nil, raw, false)
}

func (root *activationLockedRoot) activationWriteReplaceExpected(tempName, targetName string, expected, raw []byte) error {
	return root.activationWriteAtomic(tempName, targetName, expected, raw, true)
}

func (root *activationLockedRoot) activationWriteAtomic(tempName, targetName string, expected, raw []byte, replace bool) error {
	if len(raw) == 0 || len(raw) > maxActivationTransactionBytes ||
		!activationSafeMutableName(tempName) || !activationSafeMutableName(targetName) {
		return invalid("activation_file_invalid")
	}
	if err := root.activationRevalidate(); err != nil {
		return err
	}
	fd, err := syscall.Openat(
		root.fd, tempName,
		syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
		0o600,
	)
	if err != nil {
		return invalid("activation_temp_exists")
	}
	file := os.NewFile(uintptr(fd), tempName)
	if file == nil {
		_ = syscall.Close(fd)
		return invalid("activation_file_write_failed")
	}
	written := 0
	for written < len(raw) {
		count, writeErr := file.Write(raw[written:])
		if writeErr != nil || count <= 0 {
			_ = file.Close()
			root.activationDiscardTemp(tempName)
			return invalid("activation_file_write_failed")
		}
		written += count
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		root.activationDiscardTemp(tempName)
		return invalid("activation_durability_failed")
	}
	if err := file.Close(); err != nil {
		root.activationDiscardTemp(tempName)
		return invalid("activation_file_write_failed")
	}
	if err := root.activationRevalidate(); err != nil {
		root.activationDiscardTemp(tempName)
		return err
	}
	if replace {
		actual, exists, err := root.activationReadRegular(targetName, maxActivationTransactionBytes)
		if err != nil || !exists || !bytes.Equal(actual, expected) {
			root.activationDiscardTemp(tempName)
			if err != nil {
				return err
			}
			return invalid("activation_file_changed")
		}
		err = syscall.Renameat(root.fd, tempName, root.fd, targetName)
	} else {
		err = activationRenameNoReplace(root.fd, tempName, root.fd, targetName)
	}
	if err != nil {
		root.activationDiscardTemp(tempName)
		if errors.Is(err, syscall.EEXIST) {
			return invalid("activation_destination_exists")
		}
		return invalid("activation_rename_failed")
	}
	if err := syscall.Fsync(root.fd); err != nil {
		return invalid("activation_durability_failed")
	}
	actual, exists, err := root.activationReadRegular(targetName, maxActivationTransactionBytes)
	if err != nil || !exists || !bytes.Equal(actual, raw) {
		if err != nil {
			return err
		}
		return invalid("activation_file_changed")
	}
	return nil
}

func (root *activationLockedRoot) activationSwapCurrent(expected string, expectedExists bool, target string) error {
	if _, err := activationReleaseIDFromTarget(target); err != nil {
		return err
	}
	actual, exists, err := root.activationReadCurrent()
	if err != nil {
		return err
	}
	if !activationPointerEquals(actual, exists, expected, expectedExists) {
		return invalid("activation_current_changed")
	}
	tempExists, err := root.activationEntryExists("current.new")
	if err != nil {
		return err
	}
	if tempExists {
		return invalid("activation_temp_exists")
	}
	if err := activationSymlinkAt(target, root.fd, "current.new"); err != nil {
		return invalid("activation_current_write_failed")
	}
	if err := root.activationRevalidate(); err != nil {
		root.activationDiscardTemp("current.new")
		return err
	}
	actual, exists, err = root.activationReadCurrent()
	if err != nil || !activationPointerEquals(actual, exists, expected, expectedExists) {
		root.activationDiscardTemp("current.new")
		if err != nil {
			return err
		}
		return invalid("activation_current_changed")
	}
	if expectedExists {
		err = syscall.Renameat(root.fd, "current.new", root.fd, "current")
	} else {
		err = activationRenameNoReplace(root.fd, "current.new", root.fd, "current")
	}
	if err != nil {
		root.activationDiscardTemp("current.new")
		return invalid("activation_current_swap_failed")
	}
	if err := syscall.Fsync(root.fd); err != nil {
		return invalid("activation_durability_failed")
	}
	actual, exists, err = root.activationReadCurrent()
	if err != nil || !exists || actual != target {
		if err != nil {
			return err
		}
		return invalid("activation_current_changed")
	}
	return nil
}

func (root *activationLockedRoot) activationRemoveExact(name string, expected []byte) error {
	actual, exists, err := root.activationReadRegular(name, maxActivationTransactionBytes)
	if err != nil {
		return err
	}
	if !exists || !bytes.Equal(actual, expected) {
		return invalid("activation_file_changed")
	}
	if err := root.activationRevalidate(); err != nil {
		return err
	}
	actual, exists, err = root.activationReadRegular(name, maxActivationTransactionBytes)
	if err != nil {
		return err
	}
	if !exists || !bytes.Equal(actual, expected) {
		return invalid("activation_file_changed")
	}
	if err := activationUnlinkAt(root.fd, name); err != nil {
		return invalid("activation_remove_failed")
	}
	if err := syscall.Fsync(root.fd); err != nil {
		return invalid("activation_durability_failed")
	}
	exists, err = root.activationEntryExists(name)
	if err != nil {
		return err
	}
	if exists {
		return invalid("activation_remove_failed")
	}
	return nil
}

func (root *activationLockedRoot) activationRepairTemps(_ *activationIntent) error {
	for _, name := range []string{"transaction.new", "journal.new", "current.new"} {
		exists, err := root.activationEntryExists(name)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if name == "current.new" {
			target, ok, err := root.activationReadNamedSymlink(name)
			if err != nil || !ok {
				return invalid("activation_transient_invalid")
			}
			if _, err := activationReleaseIDFromTarget(target); err != nil {
				return invalid("activation_transient_invalid")
			}
		} else {
			if err := root.activationValidateTempRegular(name); err != nil {
				return invalid("activation_transient_invalid")
			}
		}
		if err := root.activationRemoveTemp(name); err != nil {
			return err
		}
	}
	return nil
}

func (root *activationLockedRoot) activationValidateTempRegular(name string) error {
	if err := root.activationRevalidate(); err != nil {
		return err
	}
	fd, err := syscall.Openat(
		root.fd, name,
		activationOPath|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
		0,
	)
	if err != nil {
		return invalid("activation_transient_invalid")
	}
	defer syscall.Close(fd)
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil || !activationMutableRegular(&stat, root.trustedUID) {
		return invalid("activation_transient_invalid")
	}
	return nil
}

func (root *activationLockedRoot) activationSyncSealedRelease(name string) error {
	if !validID(name) || name != filepath.Base(name) {
		return invalid("activation_release_path_invalid")
	}
	if err := root.activationRevalidate(); err != nil {
		return err
	}
	dirFD, err := unix.Openat(root.releasesFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return invalid("promotion_sync_failed")
	}
	dir := os.NewFile(uintptr(dirFD), name)
	if dir == nil {
		_ = unix.Close(dirFD)
		return invalid("promotion_sync_failed")
	}
	defer dir.Close()
	var beforeDir unix.Stat_t
	if err := unix.Fstat(dirFD, &beforeDir); err != nil || beforeDir.Uid != root.trustedUID ||
		!safeLinuxReleaseDirectoryMode(beforeDir.Mode) {
		return invalid("promotion_sync_failed")
	}
	names := append([]string{"manifest.json"}, allowedArtifactPaths()...)
	entries, err := dir.ReadDir(-1)
	if err != nil || len(entries) != len(names) {
		return invalid("promotion_sync_failed")
	}
	expected := make(map[string]struct{}, len(names))
	for _, artifactName := range names {
		expected[artifactName] = struct{}{}
	}
	for _, entry := range entries {
		if _, ok := expected[entry.Name()]; !ok {
			return invalid("promotion_sync_failed")
		}
	}
	for _, artifactName := range names {
		fd, err := unix.Openat(dirFD, artifactName, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return invalid("promotion_sync_failed")
		}
		var before, after, pathStat unix.Stat_t
		valid := unix.Fstat(fd, &before) == nil && before.Mode&unix.S_IFMT == unix.S_IFREG &&
			before.Uid == root.trustedUID && before.Nlink == 1 &&
			safeReleaseFileMode(artifactName, linuxFileMode(before.Mode))
		syncErr := unix.Fsync(fd)
		afterErr := unix.Fstat(fd, &after)
		pathErr := unix.Fstatat(dirFD, artifactName, &pathStat, unix.AT_SYMLINK_NOFOLLOW)
		closeErr := unix.Close(fd)
		if !valid || syncErr != nil || afterErr != nil || pathErr != nil || closeErr != nil ||
			!activationUnixSameFile(before, after) || !activationUnixSameFile(before, pathStat) {
			return invalid("promotion_sync_failed")
		}
	}
	if err := unix.Fsync(dirFD); err != nil || unix.Fsync(root.releasesFD) != nil {
		return invalid("promotion_sync_failed")
	}
	var afterDir, pathDir unix.Stat_t
	if err := unix.Fstat(dirFD, &afterDir); err != nil ||
		unix.Fstatat(root.releasesFD, name, &pathDir, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!activationUnixSameDirectory(beforeDir, afterDir) ||
		!activationUnixSameDirectory(beforeDir, pathDir) {
		return invalid("promotion_sync_failed")
	}
	return root.activationRevalidate()
}

func (root *activationLockedRoot) activationReadNamedSymlink(name string) (string, bool, error) {
	if err := root.activationRevalidate(); err != nil {
		return "", false, err
	}
	fd, err := syscall.Openat(
		root.fd, name,
		activationOPath|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
		0,
	)
	if errors.Is(err, syscall.ENOENT) {
		return "", false, nil
	}
	if err != nil {
		return "", false, invalid("activation_transient_invalid")
	}
	var stat syscall.Stat_t
	statErr := syscall.Fstat(fd, &stat)
	_ = syscall.Close(fd)
	if statErr != nil || stat.Mode&syscall.S_IFMT != syscall.S_IFLNK ||
		stat.Uid != root.trustedUID || stat.Nlink != 1 {
		return "", false, invalid("activation_transient_invalid")
	}
	buffer := make([]byte, 4096)
	size, err := activationReadlinkAt(root.fd, name, buffer)
	if err != nil || size <= 0 || size == len(buffer) {
		return "", false, invalid("activation_transient_invalid")
	}
	return string(buffer[:size]), true, nil
}

func (root *activationLockedRoot) activationRemoveTemp(name string) error {
	if err := root.activationRevalidate(); err != nil {
		return err
	}
	if err := activationUnlinkAt(root.fd, name); err != nil {
		return invalid("activation_remove_failed")
	}
	if err := syscall.Fsync(root.fd); err != nil {
		return invalid("activation_durability_failed")
	}
	return nil
}

func (root *activationLockedRoot) activationDiscardTemp(name string) {
	_ = activationUnlinkAt(root.fd, name)
	_ = syscall.Fsync(root.fd)
}

func activationTrustedAncestor(stat *syscall.Stat_t, trustedUID uint32) bool {
	if stat == nil || stat.Mode&syscall.S_IFMT != syscall.S_IFDIR ||
		(stat.Uid != 0 && stat.Uid != trustedUID) ||
		stat.Mode&(syscall.S_ISUID|syscall.S_ISGID) != 0 {
		return false
	}
	if stat.Mode&0o022 == 0 {
		return true
	}
	return stat.Uid == 0 && stat.Mode&syscall.S_ISVTX != 0
}

func activationSafeMutableName(name string) bool {
	switch name {
	case "transaction.new", "transaction.json", "journal.new", "journal.json":
		return true
	default:
		return false
	}
}

func activationWritableDirectory(stat *syscall.Stat_t, trustedUID uint32) bool {
	return stat != nil && stat.Mode&syscall.S_IFMT == syscall.S_IFDIR &&
		stat.Uid == trustedUID && stat.Mode&0o700 == 0o700 &&
		stat.Mode&0o022 == 0 && stat.Mode&0o7000 == 0
}

func activationSealedDirectory(stat *syscall.Stat_t, trustedUID uint32) bool {
	return stat != nil && stat.Mode&syscall.S_IFMT == syscall.S_IFDIR &&
		stat.Uid == trustedUID && stat.Mode&0o100 != 0 &&
		stat.Mode&0o222 == 0 && stat.Mode&0o7000 == 0
}

func activationMutableRegular(stat *syscall.Stat_t, trustedUID uint32) bool {
	return stat != nil && stat.Mode&syscall.S_IFMT == syscall.S_IFREG &&
		stat.Uid == trustedUID && stat.Nlink == 1 &&
		stat.Mode&0o777 == 0o600 && stat.Mode&0o7000 == 0
}

func activationSameDirectory(left, right syscall.Stat_t) bool {
	return left.Dev == right.Dev && left.Ino == right.Ino &&
		left.Mode == right.Mode && left.Uid == right.Uid && left.Gid == right.Gid
}

func activationSameFile(left, right syscall.Stat_t) bool {
	return left.Dev == right.Dev && left.Ino == right.Ino &&
		left.Mode == right.Mode && left.Uid == right.Uid && left.Gid == right.Gid &&
		left.Nlink == right.Nlink && left.Size == right.Size &&
		left.Mtim == right.Mtim && left.Ctim == right.Ctim
}

func activationSyscallIdentity(stat syscall.Stat_t) filesystemIdentity {
	return filesystemIdentity{device: uint64(stat.Dev), inode: stat.Ino}
}

func activationUnixSameDirectory(left, right unix.Stat_t) bool {
	return left.Dev == right.Dev && left.Ino == right.Ino &&
		left.Mode == right.Mode && left.Uid == right.Uid && left.Gid == right.Gid
}

func activationUnixSameFile(left, right unix.Stat_t) bool {
	return activationUnixSameDirectory(left, right) && left.Nlink == right.Nlink &&
		left.Size == right.Size && left.Mtim == right.Mtim && left.Ctim == right.Ctim
}

func activationRenameNoReplace(oldDirFD int, oldName string, newDirFD int, newName string) error {
	return unix.Renameat2(oldDirFD, oldName, newDirFD, newName, unix.RENAME_NOREPLACE)
}

func activationSymlinkAt(target string, dirFD int, name string) error {
	return unix.Symlinkat(target, dirFD, name)
}

func activationReadlinkAt(dirFD int, name string, buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, syscall.EINVAL
	}
	return unix.Readlinkat(dirFD, name, buffer)
}

func activationUnlinkAt(dirFD int, name string) error {
	return unix.Unlinkat(dirFD, name, 0)
}
