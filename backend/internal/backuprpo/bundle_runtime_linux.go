//go:build linux

package backuprpo

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	bundleOwnerMarker   = ".maestro-backup-owner"
	bundleImageName     = "control-plane.sqlite3"
	bundleOutputName    = "backup.bundle"
	bundleCleanupPrefix = ".cleanup-"
)

type linuxBundleRuntime struct {
	rootPath     string
	root         linuxDirectoryIdentity
	uid          uint32
	pinnedRootFD int
}

type linuxDirectoryIdentity struct {
	dev  uint64
	ino  uint64
	uid  uint32
	gid  uint32
	mode uint32
}

type linuxPreparedTask struct {
	runtime       *linuxBundleRuntime
	request       BundleRequest
	taskName      string
	taskPath      string
	imagePath     string
	outputPath    string
	image         *os.File
	imageIdentity unix.Stat_t
	sealed        *unix.Stat_t
	removed       bool
	aborted       bool
}

type rejectedImageWriter struct{}

func (rejectedImageWriter) Write([]byte) (int, error) { return 0, ErrUnsafeRuntime }

func newSecureBundleRuntime(rootPath string) (bundleRuntime, error) {
	if !validPOSIXAbsolute(rootPath) || rootPath == "/" {
		return nil, ErrUnsafeRuntime
	}
	fd, stat, err := openAbsoluteDirectoryNoSymlink(rootPath)
	if err != nil {
		return nil, ErrUnsafeRuntime
	}
	defer unix.Close(fd)
	uid := uint32(os.Geteuid())
	if !trustedLinuxDirectory(&stat, uid) {
		return nil, ErrUnsafeRuntime
	}
	return &linuxBundleRuntime{
		rootPath:     rootPath,
		root:         linuxDirectoryIdentityFromStat(&stat),
		uid:          uid,
		pinnedRootFD: -1,
	}, nil
}

func newPinnedBundleRuntime(root *os.File, rootPath string) (bundleRuntime, error) {
	if root == nil || !validPOSIXAbsolute(rootPath) || rootPath == "/" {
		return nil, ErrUnsafeRuntime
	}
	var stat unix.Stat_t
	uid := uint32(os.Geteuid())
	if unix.Fstat(int(root.Fd()), &stat) != nil || !trustedLinuxDirectory(&stat, uid) {
		return nil, ErrUnsafeRuntime
	}
	return &linuxBundleRuntime{
		rootPath:     rootPath,
		root:         linuxDirectoryIdentityFromStat(&stat),
		uid:          uid,
		pinnedRootFD: int(root.Fd()),
	}, nil
}

func (runtime *linuxBundleRuntime) Prepare(request BundleRequest) (preparedTask, error) {
	if runtime == nil || !canonicalLowerHex(request.BackupID, 32) {
		return nil, ErrUnsafeRuntime
	}
	rootFD, _, err := runtime.openRoot()
	if err != nil {
		return nil, ErrUnsafeRuntime
	}
	defer unix.Close(rootFD)

	taskName := "task-" + request.BackupID
	if err := unix.Mkdirat(rootFD, taskName, 0o700); err != nil {
		return nil, ErrUnsafeRuntime
	}
	taskCreated := true
	taskFD := -1
	markerFD := -1
	imageFD := -1
	cleanup := func() {
		if imageFD >= 0 {
			unix.Close(imageFD)
		}
		if markerFD >= 0 {
			unix.Close(markerFD)
		}
		if taskFD >= 0 {
			unix.Unlinkat(taskFD, bundleImageName, 0)
			unix.Unlinkat(taskFD, bundleOwnerMarker, 0)
			unix.Close(taskFD)
		}
		if taskCreated {
			unix.Unlinkat(rootFD, taskName, unix.AT_REMOVEDIR)
		}
	}
	taskFD, _, err = runtime.openTask(rootFD, taskName)
	if err != nil {
		cleanup()
		return nil, ErrUnsafeRuntime
	}
	markerFD, err = unix.Openat(
		taskFD,
		bundleOwnerMarker,
		unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0o600,
	)
	if err != nil {
		cleanup()
		return nil, ErrUnsafeRuntime
	}
	markerPayload := []byte(request.BackupID + "\n")
	if err := writeAllLinux(markerFD, markerPayload); err != nil ||
		unix.Fsync(markerFD) != nil {
		cleanup()
		return nil, ErrUnsafeRuntime
	}
	var markerStat unix.Stat_t
	if unix.Fstat(markerFD, &markerStat) != nil ||
		!trustedLinuxFile(&markerStat, runtime.uid) ||
		markerStat.Size != int64(len(markerPayload)) {
		cleanup()
		return nil, ErrUnsafeRuntime
	}
	if unix.Close(markerFD) != nil {
		markerFD = -1
		cleanup()
		return nil, ErrUnsafeRuntime
	}
	markerFD = -1

	imageFD, err = unix.Openat(
		taskFD,
		bundleImageName,
		unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0o600,
	)
	if err != nil {
		cleanup()
		return nil, ErrUnsafeRuntime
	}
	var imageStat unix.Stat_t
	if unix.Fstat(imageFD, &imageStat) != nil ||
		!trustedLinuxFile(&imageStat, runtime.uid) ||
		imageStat.Size != 0 {
		cleanup()
		return nil, ErrUnsafeRuntime
	}
	if unix.Fsync(taskFD) != nil || unix.Fsync(rootFD) != nil {
		cleanup()
		return nil, ErrUnsafeRuntime
	}
	imageFile := os.NewFile(uintptr(imageFD), bundleImageName)
	if imageFile == nil {
		cleanup()
		return nil, ErrUnsafeRuntime
	}
	imageFD = -1
	taskCreated = false
	unix.Close(taskFD)
	taskFD = -1

	taskPath := filepath.Join(runtime.rootPath, taskName)
	return &linuxPreparedTask{
		runtime:       runtime,
		request:       request,
		taskName:      taskName,
		taskPath:      taskPath,
		imagePath:     filepath.Join(taskPath, bundleImageName),
		outputPath:    filepath.Join(taskPath, bundleOutputName),
		image:         imageFile,
		imageIdentity: imageStat,
	}, nil
}

func (runtime *linuxBundleRuntime) Pin(request BundleRequest, maximum int64) (Bundle, error) {
	if runtime == nil ||
		maximum <= 0 ||
		maximum > MaxObjectBytes ||
		!canonicalLowerHex(request.BackupID, 32) {
		return nil, ErrUnsafeRuntime
	}
	rootFD, _, err := runtime.openRoot()
	if err != nil {
		return nil, ErrUnsafeRuntime
	}
	defer unix.Close(rootFD)
	taskName := "task-" + request.BackupID
	taskFD, taskStat, err := runtime.openTask(rootFD, taskName)
	if err != nil {
		return nil, ErrUnsafeRuntime
	}
	defer unix.Close(taskFD)

	names, err := linuxDirectoryNames(taskFD)
	if err != nil || !exactLinuxNames(names, bundleOwnerMarker, bundleOutputName) {
		return nil, ErrUnsafeRuntime
	}
	if err := validateLinuxOwnerMarker(taskFD, request.BackupID, runtime.uid); err != nil {
		return nil, ErrUnsafeRuntime
	}

	var before unix.Stat_t
	if unix.Fstatat(taskFD, bundleOutputName, &before, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!trustedLinuxFile(&before, runtime.uid) ||
		before.Size <= 0 ||
		before.Size > maximum {
		return nil, ErrUnsafeRuntime
	}
	bundleFD, err := unix.Openat(
		taskFD,
		bundleOutputName,
		unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return nil, ErrUnsafeRuntime
	}
	var opened unix.Stat_t
	if unix.Fstat(bundleFD, &opened) != nil ||
		!sameLinuxFileStat(&before, &opened) ||
		!trustedLinuxFile(&opened, runtime.uid) {
		unix.Close(bundleFD)
		return nil, ErrUnsafeRuntime
	}
	var after unix.Stat_t
	if unix.Fstatat(taskFD, bundleOutputName, &after, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!sameLinuxFileStat(&opened, &after) {
		unix.Close(bundleFD)
		return nil, ErrUnsafeRuntime
	}
	var currentTask unix.Stat_t
	if unix.Fstatat(rootFD, taskName, &currentTask, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!sameLinuxFileStat(&taskStat, &currentTask) {
		unix.Close(bundleFD)
		return nil, ErrUnsafeRuntime
	}
	file := os.NewFile(uintptr(bundleFD), bundleOutputName)
	if file == nil {
		unix.Close(bundleFD)
		return nil, ErrUnsafeRuntime
	}
	return file, nil
}

func (runtime *linuxBundleRuntime) RemoveExisting(backupID string) error {
	if runtime == nil || !canonicalLowerHex(backupID, 32) {
		return ErrUnsafeRuntime
	}
	rootFD, _, err := runtime.openRoot()
	if err != nil {
		return ErrUnsafeRuntime
	}
	defer unix.Close(rootFD)

	sourceName := "task-" + backupID
	cleanupName := bundleCleanupPrefix + backupID
	sourceExists, err := linuxPathExistsAt(rootFD, sourceName)
	if err != nil {
		return ErrUnsafeRuntime
	}
	cleanupExists, err := linuxPathExistsAt(rootFD, cleanupName)
	if err != nil || sourceExists && cleanupExists {
		return ErrUnsafeRuntime
	}
	if !sourceExists && !cleanupExists {
		return nil
	}

	taskName := cleanupName
	if sourceExists {
		taskFD, _, openErr := runtime.openTask(rootFD, sourceName)
		if openErr != nil {
			return ErrUnsafeRuntime
		}
		if validateErr := validateLinuxRemovalCandidate(taskFD, backupID, runtime.uid, false); validateErr != nil {
			unix.Close(taskFD)
			return ErrUnsafeRuntime
		}
		if renameErr := unix.Renameat2(
			rootFD,
			sourceName,
			rootFD,
			cleanupName,
			unix.RENAME_NOREPLACE,
		); renameErr != nil || unix.Fsync(rootFD) != nil {
			unix.Close(taskFD)
			return ErrUnsafeRuntime
		}
		if !linuxDirectoryEntryMatches(rootFD, cleanupName, taskFD, runtime.uid) {
			unix.Close(taskFD)
			return ErrUnsafeRuntime
		}
		stillExists, existsErr := linuxPathExistsAt(rootFD, sourceName)
		if existsErr != nil || stillExists {
			unix.Close(taskFD)
			return ErrUnsafeRuntime
		}
		removeErr := runtime.removeQuarantinedTask(rootFD, taskName, taskFD, backupID)
		closeErr := unix.Close(taskFD)
		if removeErr != nil || closeErr != nil {
			return ErrUnsafeRuntime
		}
		return nil
	}

	taskFD, _, err := runtime.openTask(rootFD, taskName)
	if err != nil {
		return ErrUnsafeRuntime
	}
	removeErr := runtime.removeQuarantinedTask(rootFD, taskName, taskFD, backupID)
	closeErr := unix.Close(taskFD)
	if removeErr != nil || closeErr != nil {
		return ErrUnsafeRuntime
	}
	return nil
}

func (runtime *linuxBundleRuntime) removeQuarantinedTask(
	rootFD int,
	taskName string,
	taskFD int,
	backupID string,
) error {
	if runtime == nil || !linuxDirectoryEntryMatches(rootFD, taskName, taskFD, runtime.uid) {
		return ErrUnsafeRuntime
	}
	if err := validateLinuxRemovalCandidate(taskFD, backupID, runtime.uid, true); err != nil {
		return ErrUnsafeRuntime
	}
	names, err := linuxDirectoryNames(taskFD)
	if err != nil {
		return ErrUnsafeRuntime
	}
	if exactLinuxNames(names, bundleOwnerMarker, bundleOutputName) {
		if err := validateLinuxRemovalBundle(taskFD, runtime.uid); err != nil ||
			unix.Unlinkat(taskFD, bundleOutputName, 0) != nil ||
			unix.Fsync(taskFD) != nil {
			return ErrUnsafeRuntime
		}
		names = []string{bundleOwnerMarker}
	}
	if exactLinuxNames(names, bundleOwnerMarker) {
		if err := validateLinuxOwnerMarker(taskFD, backupID, runtime.uid); err != nil ||
			unix.Unlinkat(taskFD, bundleOwnerMarker, 0) != nil ||
			unix.Fsync(taskFD) != nil {
			return ErrUnsafeRuntime
		}
		names = nil
	}
	currentNames, err := linuxDirectoryNames(taskFD)
	if err != nil || len(names) != 0 || len(currentNames) != 0 ||
		!linuxDirectoryEntryMatches(rootFD, taskName, taskFD, runtime.uid) {
		return ErrUnsafeRuntime
	}
	if unix.Unlinkat(rootFD, taskName, unix.AT_REMOVEDIR) != nil || unix.Fsync(rootFD) != nil {
		return ErrUnsafeRuntime
	}
	return nil
}

func linuxPathExistsAt(parentFD int, name string) (bool, error) {
	var stat unix.Stat_t
	err := unix.Fstatat(parentFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	return false, ErrUnsafeRuntime
}

func validateLinuxRemovalCandidate(
	taskFD int,
	backupID string,
	uid uint32,
	allowResidue bool,
) error {
	names, err := linuxDirectoryNames(taskFD)
	if err != nil {
		return ErrUnsafeRuntime
	}
	if exactLinuxNames(names, bundleOwnerMarker, bundleOutputName) {
		if validateLinuxOwnerMarker(taskFD, backupID, uid) != nil ||
			validateLinuxRemovalBundle(taskFD, uid) != nil {
			return ErrUnsafeRuntime
		}
		return nil
	}
	if allowResidue && exactLinuxNames(names, bundleOwnerMarker) {
		return validateLinuxOwnerMarker(taskFD, backupID, uid)
	}
	if allowResidue && len(names) == 0 {
		return nil
	}
	return ErrUnsafeRuntime
}

func validateLinuxRemovalBundle(taskFD int, uid uint32) error {
	var before unix.Stat_t
	if unix.Fstatat(taskFD, bundleOutputName, &before, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!trustedLinuxFile(&before, uid) ||
		before.Size <= 0 ||
		before.Size > MaxObjectBytes {
		return ErrUnsafeRuntime
	}
	fd, err := unix.Openat(
		taskFD,
		bundleOutputName,
		unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return ErrUnsafeRuntime
	}
	defer unix.Close(fd)
	var opened unix.Stat_t
	if unix.Fstat(fd, &opened) != nil || !sameLinuxFileStat(&before, &opened) {
		return ErrUnsafeRuntime
	}
	var after unix.Stat_t
	if unix.Fstatat(taskFD, bundleOutputName, &after, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!sameLinuxFileStat(&opened, &after) {
		return ErrUnsafeRuntime
	}
	return nil
}

func linuxDirectoryEntryMatches(parentFD int, name string, directoryFD int, uid uint32) bool {
	var opened unix.Stat_t
	var current unix.Stat_t
	return unix.Fstat(directoryFD, &opened) == nil &&
		unix.Fstatat(parentFD, name, &current, unix.AT_SYMLINK_NOFOLLOW) == nil &&
		trustedLinuxDirectory(&opened, uid) &&
		trustedLinuxDirectory(&current, uid) &&
		opened.Dev == current.Dev &&
		opened.Ino == current.Ino &&
		opened.Mode == current.Mode &&
		opened.Uid == current.Uid &&
		opened.Gid == current.Gid
}

func (runtime *linuxBundleRuntime) openRoot() (int, unix.Stat_t, error) {
	if runtime == nil {
		return -1, unix.Stat_t{}, ErrUnsafeRuntime
	}
	if runtime.pinnedRootFD >= 0 {
		fd, err := unix.FcntlInt(uintptr(runtime.pinnedRootFD), unix.F_DUPFD_CLOEXEC, 0)
		if err != nil {
			return -1, unix.Stat_t{}, ErrUnsafeRuntime
		}
		var stat unix.Stat_t
		if unix.Fstat(fd, &stat) != nil ||
			!trustedLinuxDirectory(&stat, runtime.uid) ||
			linuxDirectoryIdentityFromStat(&stat) != runtime.root {
			unix.Close(fd)
			return -1, unix.Stat_t{}, ErrUnsafeRuntime
		}
		return fd, stat, nil
	}
	fd, stat, err := openAbsoluteDirectoryNoSymlink(runtime.rootPath)
	if err != nil ||
		!trustedLinuxDirectory(&stat, runtime.uid) ||
		linuxDirectoryIdentityFromStat(&stat) != runtime.root {
		if fd >= 0 {
			unix.Close(fd)
		}
		return -1, unix.Stat_t{}, ErrUnsafeRuntime
	}
	return fd, stat, nil
}

func (runtime *linuxBundleRuntime) openTask(rootFD int, taskName string) (int, unix.Stat_t, error) {
	var before unix.Stat_t
	if unix.Fstatat(rootFD, taskName, &before, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!trustedLinuxDirectory(&before, runtime.uid) {
		return -1, unix.Stat_t{}, ErrUnsafeRuntime
	}
	fd, err := unix.Openat(
		rootFD,
		taskName,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return -1, unix.Stat_t{}, ErrUnsafeRuntime
	}
	var opened unix.Stat_t
	if unix.Fstat(fd, &opened) != nil ||
		!trustedLinuxDirectory(&opened, runtime.uid) ||
		!sameLinuxFileStat(&before, &opened) {
		unix.Close(fd)
		return -1, unix.Stat_t{}, ErrUnsafeRuntime
	}
	return fd, opened, nil
}

func (task *linuxPreparedTask) ImageWriter() io.Writer {
	if task == nil || task.image == nil || task.sealed != nil || task.aborted {
		return rejectedImageWriter{}
	}
	return task.image
}

func (task *linuxPreparedTask) ImagePath() string {
	if task == nil {
		return ""
	}
	return task.imagePath
}

func (task *linuxPreparedTask) OutputPath() string {
	if task == nil {
		return ""
	}
	return task.outputPath
}

func (task *linuxPreparedTask) SealImage(maximum int64) error {
	if task == nil ||
		task.image == nil ||
		task.sealed != nil ||
		task.aborted ||
		maximum <= 0 ||
		maximum > MaxObjectBytes {
		return ErrUnsafeRuntime
	}
	if err := task.image.Sync(); err != nil {
		return ErrUnsafeRuntime
	}
	var opened unix.Stat_t
	if unix.Fstat(int(task.image.Fd()), &opened) != nil ||
		!trustedLinuxFile(&opened, task.runtime.uid) ||
		opened.Dev != task.imageIdentity.Dev ||
		opened.Ino != task.imageIdentity.Ino ||
		opened.Size < 16 ||
		opened.Size > maximum {
		return ErrUnsafeRuntime
	}
	header := make([]byte, 16)
	count, err := unix.Pread(int(task.image.Fd()), header, 0)
	if err != nil || count != len(header) ||
		!bytes.Equal(header, []byte("SQLite format 3\x00")) {
		return ErrUnsafeRuntime
	}
	rootFD, _, err := task.runtime.openRoot()
	if err != nil {
		return ErrUnsafeRuntime
	}
	defer unix.Close(rootFD)
	taskFD, _, err := task.runtime.openTask(rootFD, task.taskName)
	if err != nil {
		return ErrUnsafeRuntime
	}
	defer unix.Close(taskFD)
	var pathStat unix.Stat_t
	if unix.Fstatat(taskFD, bundleImageName, &pathStat, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!sameLinuxFileStat(&opened, &pathStat) {
		return ErrUnsafeRuntime
	}
	if err := task.image.Close(); err != nil {
		task.image = nil
		return ErrUnsafeRuntime
	}
	task.image = nil
	task.sealed = &opened
	return nil
}

func (task *linuxPreparedTask) RemoveImage() error {
	if task == nil ||
		task.sealed == nil ||
		task.removed ||
		task.aborted ||
		task.image != nil {
		return ErrUnsafeRuntime
	}
	rootFD, _, err := task.runtime.openRoot()
	if err != nil {
		return ErrUnsafeRuntime
	}
	defer unix.Close(rootFD)
	taskFD, _, err := task.runtime.openTask(rootFD, task.taskName)
	if err != nil {
		return ErrUnsafeRuntime
	}
	defer unix.Close(taskFD)
	names, err := linuxDirectoryNames(taskFD)
	if err != nil || !exactLinuxNames(names, bundleOwnerMarker, bundleImageName, bundleOutputName) {
		return ErrUnsafeRuntime
	}
	var current unix.Stat_t
	if unix.Fstatat(taskFD, bundleImageName, &current, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!sameLinuxFileStat(task.sealed, &current) {
		return ErrUnsafeRuntime
	}
	if unix.Unlinkat(taskFD, bundleImageName, 0) != nil ||
		unix.Fsync(taskFD) != nil {
		return ErrUnsafeRuntime
	}
	task.removed = true
	return nil
}

func (task *linuxPreparedTask) Abort() {
	if task == nil || task.aborted {
		return
	}
	task.aborted = true
	if task.image != nil {
		_ = task.image.Close()
		task.image = nil
	}
	rootFD, _, err := task.runtime.openRoot()
	if err != nil {
		return
	}
	defer unix.Close(rootFD)
	taskFD, _, err := task.runtime.openTask(rootFD, task.taskName)
	if err != nil {
		return
	}
	names, err := linuxDirectoryNames(taskFD)
	if err != nil {
		unix.Close(taskFD)
		return
	}
	allowed := map[string]bool{
		bundleOwnerMarker: true,
		bundleImageName:   true,
		bundleOutputName:  true,
	}
	for _, name := range names {
		if !allowed[name] {
			unix.Close(taskFD)
			return
		}
		var stat unix.Stat_t
		if unix.Fstatat(taskFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW) != nil ||
			!trustedLinuxFile(&stat, task.runtime.uid) {
			unix.Close(taskFD)
			return
		}
	}
	for _, name := range names {
		if unix.Unlinkat(taskFD, name, 0) != nil {
			unix.Close(taskFD)
			return
		}
	}
	_ = unix.Fsync(taskFD)
	unix.Close(taskFD)
	if unix.Unlinkat(rootFD, task.taskName, unix.AT_REMOVEDIR) == nil {
		_ = unix.Fsync(rootFD)
	}
}

func openAbsoluteDirectoryNoSymlink(value string) (int, unix.Stat_t, error) {
	if !validPOSIXAbsolute(value) {
		return -1, unix.Stat_t{}, ErrUnsafeRuntime
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, unix.Stat_t{}, ErrUnsafeRuntime
	}
	for _, component := range strings.Split(strings.TrimPrefix(value, "/"), "/") {
		if component == "" {
			continue
		}
		next, openErr := unix.Openat(
			fd,
			component,
			unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
			0,
		)
		unix.Close(fd)
		if openErr != nil {
			return -1, unix.Stat_t{}, ErrUnsafeRuntime
		}
		fd = next
	}
	var opened unix.Stat_t
	var pathStat unix.Stat_t
	if unix.Fstat(fd, &opened) != nil ||
		unix.Lstat(value, &pathStat) != nil ||
		!sameLinuxFileStat(&opened, &pathStat) {
		unix.Close(fd)
		return -1, unix.Stat_t{}, ErrUnsafeRuntime
	}
	return fd, opened, nil
}

func trustedLinuxDirectory(stat *unix.Stat_t, uid uint32) bool {
	return stat != nil &&
		stat.Mode&unix.S_IFMT == unix.S_IFDIR &&
		stat.Mode&0o7777 == 0o700 &&
		stat.Uid == uid
}

func trustedLinuxFile(stat *unix.Stat_t, uid uint32) bool {
	return stat != nil &&
		stat.Mode&unix.S_IFMT == unix.S_IFREG &&
		stat.Mode&0o7777 == 0o600 &&
		stat.Uid == uid &&
		stat.Nlink == 1
}

func sameLinuxFileStat(left, right *unix.Stat_t) bool {
	return left != nil &&
		right != nil &&
		left.Dev == right.Dev &&
		left.Ino == right.Ino &&
		left.Mode == right.Mode &&
		left.Uid == right.Uid &&
		left.Gid == right.Gid &&
		left.Nlink == right.Nlink &&
		left.Size == right.Size &&
		left.Mtim == right.Mtim &&
		left.Ctim == right.Ctim
}

func linuxDirectoryIdentityFromStat(stat *unix.Stat_t) linuxDirectoryIdentity {
	return linuxDirectoryIdentity{
		dev:  uint64(stat.Dev),
		ino:  stat.Ino,
		uid:  stat.Uid,
		gid:  stat.Gid,
		mode: stat.Mode,
	}
}

func writeAllLinux(fd int, payload []byte) error {
	for len(payload) > 0 {
		count, err := unix.Write(fd, payload)
		if err != nil || count <= 0 {
			return ErrUnsafeRuntime
		}
		payload = payload[count:]
	}
	return nil
}

func linuxDirectoryNames(fd int) ([]string, error) {
	duplicate, err := unix.Openat(
		fd,
		".",
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return nil, ErrUnsafeRuntime
	}
	file := os.NewFile(uintptr(duplicate), "directory")
	if file == nil {
		unix.Close(duplicate)
		return nil, ErrUnsafeRuntime
	}
	names, readErr := file.Readdirnames(-1)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return nil, ErrUnsafeRuntime
	}
	sort.Strings(names)
	return names, nil
}

func exactLinuxNames(actual []string, expected ...string) bool {
	if len(actual) != len(expected) {
		return false
	}
	copyExpected := append([]string(nil), expected...)
	sort.Strings(copyExpected)
	for index := range actual {
		if actual[index] != copyExpected[index] {
			return false
		}
	}
	return true
}

func validateLinuxOwnerMarker(taskFD int, backupID string, uid uint32) error {
	var before unix.Stat_t
	if unix.Fstatat(taskFD, bundleOwnerMarker, &before, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!trustedLinuxFile(&before, uid) {
		return ErrUnsafeRuntime
	}
	expected := []byte(backupID + "\n")
	if before.Size != int64(len(expected)) {
		return ErrUnsafeRuntime
	}
	fd, err := unix.Openat(
		taskFD,
		bundleOwnerMarker,
		unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return ErrUnsafeRuntime
	}
	defer unix.Close(fd)
	var opened unix.Stat_t
	if unix.Fstat(fd, &opened) != nil ||
		!sameLinuxFileStat(&before, &opened) {
		return ErrUnsafeRuntime
	}
	buffer := make([]byte, len(expected)+1)
	count, readErr := unix.Pread(fd, buffer, 0)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return ErrUnsafeRuntime
	}
	if count != len(expected) || !bytes.Equal(buffer[:count], expected) {
		return ErrUnsafeRuntime
	}
	var after unix.Stat_t
	if unix.Fstatat(taskFD, bundleOwnerMarker, &after, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!sameLinuxFileStat(&opened, &after) {
		return ErrUnsafeRuntime
	}
	return nil
}
