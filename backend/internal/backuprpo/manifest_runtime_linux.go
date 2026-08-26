//go:build linux

package backuprpo

import (
	"archive/tar"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	manifestOwnerMarker       = ".maestro-manifest-owner"
	manifestEncryptedName     = "encrypted.bundle"
	manifestArchiveName       = "archive.tar"
	manifestPayloadName       = "payload"
	manifestResultName        = "result.json"
	manifestTaskPrefix        = "verify-"
	manifestRandomAttempts    = 8
	manifestRandomIdentifier  = 16
	manifestExtractedFileMode = int64(0o600)
)

var manifestPayloadFiles = []string{
	"application-keys.json",
	"control-plane.sqlite3",
	"manifest.json",
	"manifest.sig",
}

type linuxManifestVerificationRuntime struct {
	rootPath     string
	root         linuxDirectoryIdentity
	uid          uint32
	pinnedRootFD int
}

type linuxManifestVerificationTask struct {
	runtime     *linuxManifestVerificationRuntime
	expectation ManifestExpectation
	limits      ManifestVerifierLimits

	taskName    string
	taskPath    string
	payloadPath string

	taskIdentity    *unix.Stat_t
	payloadInitial  *unix.Stat_t
	payloadSealed   *unix.Stat_t
	extractedFiles  map[string]unix.Stat_t
	encrypted       *os.File
	encryptedStat   *unix.Stat_t
	encryptedWriter *linuxBoundedPinnedWriter
	archive         *os.File
	archiveInitial  *unix.Stat_t
	archiveWriter   *linuxBoundedPinnedWriter
	archiveSealed   *unix.Stat_t
	payload         *os.File
	result          *os.File
	resultInitial   *unix.Stat_t
	resultWriter    *linuxBoundedPinnedWriter
	extracted       bool
	resultRead      bool
	aborted         bool
}

type linuxBoundedPinnedWriter struct {
	file    *os.File
	uid     uint32
	maximum int64
	written int64
	baseDev uint64
	baseIno uint64
	last    *unix.Stat_t
	failed  bool
}

func newSecureManifestVerificationRuntime(rootPath string) (manifestVerificationRuntime, error) {
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
	return &linuxManifestVerificationRuntime{
		rootPath:     rootPath,
		root:         linuxDirectoryIdentityFromStat(&stat),
		uid:          uid,
		pinnedRootFD: -1,
	}, nil
}

func newPinnedManifestVerificationRuntime(root *os.File, rootPath string) (manifestVerificationRuntime, error) {
	if root == nil || !validPOSIXAbsolute(rootPath) || rootPath == "/" {
		return nil, ErrUnsafeRuntime
	}
	var stat unix.Stat_t
	uid := uint32(os.Geteuid())
	if unix.Fstat(int(root.Fd()), &stat) != nil || !trustedLinuxDirectory(&stat, uid) {
		return nil, ErrUnsafeRuntime
	}
	return &linuxManifestVerificationRuntime{
		rootPath:     rootPath,
		root:         linuxDirectoryIdentityFromStat(&stat),
		uid:          uid,
		pinnedRootFD: int(root.Fd()),
	}, nil
}

func (runtime *linuxManifestVerificationRuntime) Prepare(
	reader io.Reader,
	expectation ManifestExpectation,
	limits ManifestVerifierLimits,
) (manifestVerificationTask, error) {
	if runtime == nil || reader == nil ||
		!validManifestRuntimeExpectation(expectation) ||
		!validManifestRuntimeLimits(limits) {
		return nil, ErrUnsafeRuntime
	}
	rootFD, _, err := runtime.openRoot()
	if err != nil {
		return nil, ErrUnsafeRuntime
	}
	defer unix.Close(rootFD)

	taskName, err := createRandomManifestTask(rootFD)
	if err != nil {
		return nil, ErrUnsafeRuntime
	}
	task := &linuxManifestVerificationTask{
		runtime:     runtime,
		expectation: expectation,
		limits:      limits,
		taskName:    taskName,
		taskPath:    filepath.Join(runtime.rootPath, taskName),
	}
	task.payloadPath = filepath.Join(task.taskPath, manifestPayloadName)
	cleanup := true
	defer func() {
		if cleanup {
			task.Abort()
		}
	}()

	taskFD, _, err := runtime.openTask(rootFD, taskName)
	if err != nil {
		return nil, ErrUnsafeRuntime
	}
	defer unix.Close(taskFD)

	if err := createManifestOwnerMarker(taskFD, expectation.Metadata.BackupID, runtime.uid); err != nil {
		return nil, ErrUnsafeRuntime
	}

	encrypted, encryptedInitial, err := createPinnedManifestFile(taskFD, manifestEncryptedName, runtime.uid)
	if err != nil {
		return nil, ErrUnsafeRuntime
	}
	task.encrypted = encrypted
	task.encryptedStat = cloneLinuxStat(&encryptedInitial)
	task.encryptedWriter = newLinuxBoundedPinnedWriter(encrypted, encryptedInitial, runtime.uid, limits.MaxBundleBytes)
	copied, copyErr := io.Copy(task.encryptedWriter, reader)
	if copyErr != nil || copied <= 0 || task.encryptedWriter.failed || task.encryptedWriter.last == nil {
		return nil, ErrUnsafeRuntime
	}
	if unix.Fdatasync(int(encrypted.Fd())) != nil ||
		validatePinnedManifestFile(taskFD, manifestEncryptedName, encrypted, task.encryptedWriter.last, runtime.uid) != nil {
		return nil, ErrUnsafeRuntime
	}
	task.encryptedStat = cloneLinuxStat(task.encryptedWriter.last)
	if _, err := encrypted.Seek(0, io.SeekStart); err != nil {
		return nil, ErrUnsafeRuntime
	}

	archive, archiveInitial, err := createPinnedManifestFile(taskFD, manifestArchiveName, runtime.uid)
	if err != nil {
		return nil, ErrUnsafeRuntime
	}
	task.archive = archive
	task.archiveInitial = cloneLinuxStat(&archiveInitial)
	task.archiveWriter = newLinuxBoundedPinnedWriter(archive, archiveInitial, runtime.uid, limits.MaxArchiveBytes)

	result, resultInitial, err := createPinnedManifestFile(taskFD, manifestResultName, runtime.uid)
	if err != nil {
		return nil, ErrUnsafeRuntime
	}
	task.result = result
	task.resultInitial = cloneLinuxStat(&resultInitial)
	task.resultWriter = newLinuxBoundedPinnedWriter(
		result,
		resultInitial,
		runtime.uid,
		int64(maximumCommandOutputBytes),
	)

	if err := unix.Mkdirat(taskFD, manifestPayloadName, 0o700); err != nil {
		return nil, ErrUnsafeRuntime
	}
	payloadFD, payloadStat, err := openManifestDirectoryAt(taskFD, manifestPayloadName, runtime.uid)
	if err != nil {
		return nil, ErrUnsafeRuntime
	}
	if unix.Fsync(payloadFD) != nil {
		unix.Close(payloadFD)
		return nil, ErrUnsafeRuntime
	}
	if unix.Close(payloadFD) != nil {
		return nil, ErrUnsafeRuntime
	}
	task.payloadInitial = cloneLinuxStat(&payloadStat)

	if unix.Fdatasync(int(archive.Fd())) != nil ||
		unix.Fdatasync(int(result.Fd())) != nil ||
		unix.Fsync(taskFD) != nil ||
		unix.Fsync(rootFD) != nil {
		return nil, ErrUnsafeRuntime
	}
	var taskStat unix.Stat_t
	var taskPathStat unix.Stat_t
	if unix.Fstat(taskFD, &taskStat) != nil ||
		unix.Fstatat(rootFD, taskName, &taskPathStat, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!trustedLinuxDirectory(&taskStat, runtime.uid) ||
		!sameLinuxFileStat(&taskStat, &taskPathStat) {
		return nil, ErrUnsafeRuntime
	}
	task.taskIdentity = cloneLinuxStat(&taskStat)
	cleanup = false
	return task, nil
}

func validManifestRuntimeExpectation(expectation ManifestExpectation) bool {
	return isValidObjectMetadata(expectation.Metadata) &&
		expectation.Metadata.ManifestVersion == 2 &&
		expectation.RestoreEpoch == expectation.Metadata.RestoreEpoch &&
		validVersionID(expectation.VersionID.String())
}

func validManifestRuntimeLimits(limits ManifestVerifierLimits) bool {
	return limits.MaxBundleBytes > 0 &&
		limits.MaxBundleBytes <= MaxObjectBytes &&
		limits.MaxArchiveBytes > 0 &&
		limits.MaxArchiveBytes <= MaxObjectBytes &&
		limits.MaxExtractedBytes > 0 &&
		limits.MaxExtractedBytes <= MaxObjectBytes
}

func createRandomManifestTask(rootFD int) (string, error) {
	randomBytes := make([]byte, manifestRandomIdentifier)
	for attempt := 0; attempt < manifestRandomAttempts; attempt++ {
		if _, err := io.ReadFull(rand.Reader, randomBytes); err != nil {
			return "", ErrUnsafeRuntime
		}
		taskName := manifestTaskPrefix + hex.EncodeToString(randomBytes)
		err := unix.Mkdirat(rootFD, taskName, 0o700)
		if err == nil {
			return taskName, nil
		}
		if !errors.Is(err, unix.EEXIST) {
			return "", ErrUnsafeRuntime
		}
	}
	return "", ErrUnsafeRuntime
}

func createManifestOwnerMarker(taskFD int, backupID string, uid uint32) error {
	fd, err := unix.Openat(
		taskFD,
		manifestOwnerMarker,
		unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0o600,
	)
	if err != nil {
		return ErrUnsafeRuntime
	}
	defer unix.Close(fd)
	payload := []byte(backupID + "\n")
	if err := writeAllLinux(fd, payload); err != nil ||
		unix.Fdatasync(fd) != nil {
		return ErrUnsafeRuntime
	}
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil ||
		!trustedLinuxFile(&stat, uid) ||
		stat.Size != int64(len(payload)) {
		return ErrUnsafeRuntime
	}
	return nil
}

func createPinnedManifestFile(taskFD int, name string, uid uint32) (*os.File, unix.Stat_t, error) {
	fd, err := unix.Openat(
		taskFD,
		name,
		unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0o600,
	)
	if err != nil {
		return nil, unix.Stat_t{}, ErrUnsafeRuntime
	}
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil ||
		!trustedLinuxFile(&stat, uid) ||
		stat.Size != 0 {
		unix.Close(fd)
		return nil, unix.Stat_t{}, ErrUnsafeRuntime
	}
	var pathStat unix.Stat_t
	if unix.Fstatat(taskFD, name, &pathStat, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!sameLinuxFileStat(&stat, &pathStat) {
		unix.Close(fd)
		return nil, unix.Stat_t{}, ErrUnsafeRuntime
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		unix.Close(fd)
		return nil, unix.Stat_t{}, ErrUnsafeRuntime
	}
	return file, stat, nil
}

func newLinuxBoundedPinnedWriter(
	file *os.File,
	initial unix.Stat_t,
	uid uint32,
	maximum int64,
) *linuxBoundedPinnedWriter {
	return &linuxBoundedPinnedWriter{
		file:    file,
		uid:     uid,
		maximum: maximum,
		baseDev: uint64(initial.Dev),
		baseIno: initial.Ino,
	}
}

func (writer *linuxBoundedPinnedWriter) Write(payload []byte) (int, error) {
	if writer == nil || writer.file == nil || writer.failed ||
		writer.maximum <= 0 || writer.written < 0 ||
		int64(len(payload)) > writer.maximum-writer.written {
		if writer != nil {
			writer.failed = true
		}
		return 0, ErrUnsafeRuntime
	}
	if len(payload) == 0 {
		return 0, nil
	}
	count, err := writer.file.Write(payload)
	if count < 0 {
		count = 0
	}
	if count > 0 {
		writer.written += int64(count)
		var stat unix.Stat_t
		if unix.Fstat(int(writer.file.Fd()), &stat) != nil ||
			!trustedLinuxFile(&stat, writer.uid) ||
			uint64(stat.Dev) != writer.baseDev ||
			stat.Ino != writer.baseIno ||
			stat.Size != writer.written {
			writer.failed = true
			return count, ErrUnsafeRuntime
		}
		writer.last = cloneLinuxStat(&stat)
	}
	if err != nil || count != len(payload) {
		writer.failed = true
		return count, ErrUnsafeRuntime
	}
	return count, nil
}

func cloneLinuxStat(stat *unix.Stat_t) *unix.Stat_t {
	if stat == nil {
		return nil
	}
	cloned := *stat
	return &cloned
}

func (runtime *linuxManifestVerificationRuntime) openRoot() (int, unix.Stat_t, error) {
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

func (runtime *linuxManifestVerificationRuntime) openTask(
	rootFD int,
	taskName string,
) (int, unix.Stat_t, error) {
	return openManifestDirectoryAt(rootFD, taskName, runtime.uid)
}

func openManifestDirectoryAt(parentFD int, name string, uid uint32) (int, unix.Stat_t, error) {
	var before unix.Stat_t
	if unix.Fstatat(parentFD, name, &before, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!trustedLinuxDirectory(&before, uid) {
		return -1, unix.Stat_t{}, ErrUnsafeRuntime
	}
	fd, err := unix.Openat(
		parentFD,
		name,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return -1, unix.Stat_t{}, ErrUnsafeRuntime
	}
	var opened unix.Stat_t
	var after unix.Stat_t
	if unix.Fstat(fd, &opened) != nil ||
		!trustedLinuxDirectory(&opened, uid) ||
		!sameLinuxFileStat(&before, &opened) ||
		unix.Fstatat(parentFD, name, &after, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!sameLinuxFileStat(&opened, &after) {
		unix.Close(fd)
		return -1, unix.Stat_t{}, ErrUnsafeRuntime
	}
	return fd, opened, nil
}

func (task *linuxManifestVerificationTask) openSafeTask() (int, int, error) {
	if task == nil || task.runtime == nil || task.aborted || task.taskIdentity == nil {
		return -1, -1, ErrUnsafeRuntime
	}
	rootFD, _, err := task.runtime.openRoot()
	if err != nil {
		return -1, -1, ErrUnsafeRuntime
	}
	taskFD, taskStat, err := task.runtime.openTask(rootFD, task.taskName)
	if err != nil || !sameLinuxFileStat(task.taskIdentity, &taskStat) {
		unix.Close(rootFD)
		if taskFD >= 0 {
			unix.Close(taskFD)
		}
		return -1, -1, ErrUnsafeRuntime
	}
	names, err := linuxDirectoryNames(taskFD)
	if err != nil ||
		!exactLinuxNames(
			names,
			manifestOwnerMarker,
			manifestEncryptedName,
			manifestArchiveName,
			manifestPayloadName,
			manifestResultName,
		) ||
		validateManifestOwnerMarker(
			taskFD,
			task.expectation.Metadata.BackupID,
			task.runtime.uid,
		) != nil {
		unix.Close(taskFD)
		unix.Close(rootFD)
		return -1, -1, ErrUnsafeRuntime
	}
	return rootFD, taskFD, nil
}

func validatePinnedManifestFile(
	parentFD int,
	name string,
	file *os.File,
	expected *unix.Stat_t,
	uid uint32,
) error {
	if file == nil || expected == nil {
		return ErrUnsafeRuntime
	}
	var before unix.Stat_t
	var opened unix.Stat_t
	var after unix.Stat_t
	if unix.Fstatat(parentFD, name, &before, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!sameLinuxFileStat(expected, &before) ||
		unix.Fstat(int(file.Fd()), &opened) != nil ||
		!trustedLinuxFile(&opened, uid) ||
		!sameLinuxFileStat(expected, &opened) ||
		unix.Fstatat(parentFD, name, &after, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!sameLinuxFileStat(&opened, &after) {
		return ErrUnsafeRuntime
	}
	return nil
}

func validateStoredManifestFile(
	parentFD int,
	name string,
	expected *unix.Stat_t,
	uid uint32,
) error {
	if expected == nil {
		return ErrUnsafeRuntime
	}
	var before unix.Stat_t
	if unix.Fstatat(parentFD, name, &before, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!trustedLinuxFile(&before, uid) ||
		!sameLinuxFileStat(expected, &before) {
		return ErrUnsafeRuntime
	}
	fd, err := unix.Openat(
		parentFD,
		name,
		unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return ErrUnsafeRuntime
	}
	defer unix.Close(fd)
	var opened unix.Stat_t
	var after unix.Stat_t
	if unix.Fstat(fd, &opened) != nil ||
		!sameLinuxFileStat(expected, &opened) ||
		unix.Fstatat(parentFD, name, &after, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!sameLinuxFileStat(&opened, &after) {
		return ErrUnsafeRuntime
	}
	return nil
}

func validateManifestOwnerMarker(taskFD int, backupID string, uid uint32) error {
	var before unix.Stat_t
	if unix.Fstatat(taskFD, manifestOwnerMarker, &before, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!trustedLinuxFile(&before, uid) {
		return ErrUnsafeRuntime
	}
	expected := []byte(backupID + "\n")
	if before.Size != int64(len(expected)) {
		return ErrUnsafeRuntime
	}
	fd, err := unix.Openat(
		taskFD,
		manifestOwnerMarker,
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
	buffer := make([]byte, len(expected)+1)
	count, readErr := unix.Pread(fd, buffer, 0)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return ErrUnsafeRuntime
	}
	if count != len(expected) || !bytes.Equal(buffer[:count], expected) {
		return ErrUnsafeRuntime
	}
	var after unix.Stat_t
	if unix.Fstatat(taskFD, manifestOwnerMarker, &after, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!sameLinuxFileStat(&opened, &after) {
		return ErrUnsafeRuntime
	}
	return nil
}

func (task *linuxManifestVerificationTask) currentEncryptedStat() *unix.Stat_t {
	if task.encryptedWriter != nil && task.encryptedWriter.last != nil {
		return task.encryptedWriter.last
	}
	return task.encryptedStat
}

func (task *linuxManifestVerificationTask) currentArchiveStat() *unix.Stat_t {
	if task.archiveWriter != nil && task.archiveWriter.last != nil {
		return task.archiveWriter.last
	}
	return task.archiveInitial
}

func (task *linuxManifestVerificationTask) currentResultStat() *unix.Stat_t {
	if task.resultWriter != nil && task.resultWriter.last != nil {
		return task.resultWriter.last
	}
	return task.resultInitial
}

func (task *linuxManifestVerificationTask) EncryptedReader() io.ReadSeeker {
	rootFD, taskFD, err := task.openSafeTask()
	if err != nil {
		return nil
	}
	defer unix.Close(rootFD)
	defer unix.Close(taskFD)
	if validatePinnedManifestFile(
		taskFD,
		manifestEncryptedName,
		task.encrypted,
		task.currentEncryptedStat(),
		task.runtime.uid,
	) != nil {
		return nil
	}
	if _, err := task.encrypted.Seek(0, io.SeekStart); err != nil {
		return nil
	}
	return task.encrypted
}

func (task *linuxManifestVerificationTask) ArchiveWriter() io.Writer {
	if task == nil || task.archiveSealed != nil || task.extracted ||
		task.archiveWriter == nil || task.archiveWriter.failed {
		return nil
	}
	rootFD, taskFD, err := task.openSafeTask()
	if err != nil {
		return nil
	}
	defer unix.Close(rootFD)
	defer unix.Close(taskFD)
	if validatePinnedManifestFile(
		taskFD,
		manifestEncryptedName,
		task.encrypted,
		task.currentEncryptedStat(),
		task.runtime.uid,
	) != nil ||
		validatePinnedManifestFile(
			taskFD,
			manifestArchiveName,
			task.archive,
			task.currentArchiveStat(),
			task.runtime.uid,
		) != nil ||
		validatePinnedManifestFile(
			taskFD,
			manifestResultName,
			task.result,
			task.currentResultStat(),
			task.runtime.uid,
		) != nil ||
		task.validatePayload(taskFD, task.payloadInitial, nil) != nil {
		return nil
	}
	return task.archiveWriter
}

func (task *linuxManifestVerificationTask) SealArchive() error {
	if task == nil || task.aborted || task.archiveSealed != nil || task.extracted ||
		task.archiveWriter == nil || task.archiveWriter.failed ||
		task.archiveWriter.last == nil || task.archiveWriter.written <= 0 {
		return ErrUnsafeRuntime
	}
	if unix.Fdatasync(int(task.archive.Fd())) != nil {
		return ErrUnsafeRuntime
	}
	rootFD, taskFD, err := task.openSafeTask()
	if err != nil {
		return ErrUnsafeRuntime
	}
	defer unix.Close(rootFD)
	defer unix.Close(taskFD)
	if validatePinnedManifestFile(
		taskFD,
		manifestArchiveName,
		task.archive,
		task.archiveWriter.last,
		task.runtime.uid,
	) != nil {
		return ErrUnsafeRuntime
	}
	task.archiveSealed = cloneLinuxStat(task.archiveWriter.last)
	if _, err := task.archive.Seek(0, io.SeekStart); err != nil {
		task.archiveSealed = nil
		return ErrUnsafeRuntime
	}
	return nil
}

func (task *linuxManifestVerificationTask) Extract() error {
	if task == nil || task.aborted || task.extracted || task.archiveSealed == nil {
		return ErrUnsafeRuntime
	}
	rootFD, taskFD, err := task.openSafeTask()
	if err != nil {
		return ErrUnsafeRuntime
	}
	defer unix.Close(rootFD)
	defer unix.Close(taskFD)
	if validatePinnedManifestFile(
		taskFD,
		manifestArchiveName,
		task.archive,
		task.archiveSealed,
		task.runtime.uid,
	) != nil {
		return ErrUnsafeRuntime
	}
	payloadFD, payloadStat, err := openManifestDirectoryAt(taskFD, manifestPayloadName, task.runtime.uid)
	if err != nil || !sameLinuxFileStat(task.payloadInitial, &payloadStat) {
		if payloadFD >= 0 {
			unix.Close(payloadFD)
		}
		return ErrUnsafeRuntime
	}
	defer func() {
		if payloadFD >= 0 {
			_ = unix.Close(payloadFD)
		}
	}()
	names, err := linuxDirectoryNames(payloadFD)
	if err != nil || len(names) != 0 {
		return ErrUnsafeRuntime
	}
	if _, err := task.archive.Seek(0, io.SeekStart); err != nil {
		return ErrUnsafeRuntime
	}

	allowed := make(map[string]bool, len(manifestPayloadFiles))
	for _, name := range manifestPayloadFiles {
		allowed[name] = true
	}
	seen := make(map[string]bool, len(manifestPayloadFiles))
	extracted := make(map[string]unix.Stat_t, len(manifestPayloadFiles))
	var total int64
	reader := tar.NewReader(task.archive)
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil || header == nil ||
			(header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA) ||
			header.Linkname != "" ||
			!allowed[header.Name] ||
			seen[header.Name] ||
			strings.ContainsAny(header.Name, "/\\") ||
			header.Mode != manifestExtractedFileMode ||
			header.Size <= 0 ||
			header.Size > task.limits.MaxExtractedBytes-total {
			return ErrUnsafeRuntime
		}
		fd, openErr := unix.Openat(
			payloadFD,
			header.Name,
			unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC,
			0o600,
		)
		if openErr != nil {
			return ErrUnsafeRuntime
		}
		file := os.NewFile(uintptr(fd), header.Name)
		if file == nil {
			unix.Close(fd)
			return ErrUnsafeRuntime
		}
		count, copyErr := io.CopyN(file, reader, header.Size)
		syncErr := unix.Fdatasync(fd)
		var fileStat unix.Stat_t
		statErr := unix.Fstat(fd, &fileStat)
		var pathStat unix.Stat_t
		pathErr := unix.Fstatat(payloadFD, header.Name, &pathStat, unix.AT_SYMLINK_NOFOLLOW)
		closeErr := file.Close()
		if copyErr != nil || count != header.Size || syncErr != nil || statErr != nil ||
			pathErr != nil || closeErr != nil ||
			!trustedLinuxFile(&fileStat, task.runtime.uid) ||
			fileStat.Size != header.Size ||
			!sameLinuxFileStat(&fileStat, &pathStat) {
			return ErrUnsafeRuntime
		}
		total += header.Size
		seen[header.Name] = true
		extracted[header.Name] = fileStat
	}
	if !zeroManifestArchiveRemainder(task.archive) {
		return ErrUnsafeRuntime
	}
	if len(seen) != len(manifestPayloadFiles) ||
		total <= 0 ||
		total > task.limits.MaxExtractedBytes {
		return ErrUnsafeRuntime
	}
	if _, err := unix.Seek(payloadFD, 0, io.SeekStart); err != nil {
		return ErrUnsafeRuntime
	}
	names, err = linuxDirectoryNames(payloadFD)
	if err != nil || !exactLinuxNames(names, manifestPayloadFiles...) ||
		unix.Fsync(payloadFD) != nil {
		return ErrUnsafeRuntime
	}
	var sealedPayload unix.Stat_t
	var pathPayload unix.Stat_t
	if unix.Fstat(payloadFD, &sealedPayload) != nil ||
		unix.Fstatat(taskFD, manifestPayloadName, &pathPayload, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!trustedLinuxDirectory(&sealedPayload, task.runtime.uid) ||
		!sameLinuxFileStat(&sealedPayload, &pathPayload) ||
		validatePinnedManifestFile(
			taskFD,
			manifestArchiveName,
			task.archive,
			task.archiveSealed,
			task.runtime.uid,
		) != nil {
		return ErrUnsafeRuntime
	}
	payloadFile := os.NewFile(uintptr(payloadFD), manifestPayloadName)
	if payloadFile == nil {
		return ErrUnsafeRuntime
	}
	task.payloadSealed = cloneLinuxStat(&sealedPayload)
	task.extractedFiles = extracted
	task.payload = payloadFile
	payloadFD = -1
	task.extracted = true
	return nil
}

func zeroManifestArchiveRemainder(file *os.File) bool {
	if file == nil {
		return false
	}
	buffer := make([]byte, 32*1024)
	for {
		count, err := file.Read(buffer)
		for _, value := range buffer[:count] {
			if value != 0 {
				return false
			}
		}
		if errors.Is(err, io.EOF) {
			return true
		}
		if err != nil || count == 0 {
			return false
		}
	}
}

func (task *linuxManifestVerificationTask) validatePayload(
	taskFD int,
	expectedDirectory *unix.Stat_t,
	expectedFiles map[string]unix.Stat_t,
) error {
	if expectedDirectory == nil {
		return ErrUnsafeRuntime
	}
	payloadFD, payloadStat, err := openManifestDirectoryAt(taskFD, manifestPayloadName, task.runtime.uid)
	if err != nil || !sameLinuxFileStat(expectedDirectory, &payloadStat) {
		if payloadFD >= 0 {
			unix.Close(payloadFD)
		}
		return ErrUnsafeRuntime
	}
	defer unix.Close(payloadFD)
	names, err := linuxDirectoryNames(payloadFD)
	if expectedFiles == nil {
		if err != nil || len(names) != 0 {
			return ErrUnsafeRuntime
		}
		return nil
	}
	if err != nil || !exactLinuxNames(names, manifestPayloadFiles...) ||
		len(expectedFiles) != len(manifestPayloadFiles) {
		return ErrUnsafeRuntime
	}
	for _, name := range manifestPayloadFiles {
		expected, ok := expectedFiles[name]
		if !ok || validateStoredManifestFile(payloadFD, name, &expected, task.runtime.uid) != nil {
			return ErrUnsafeRuntime
		}
	}
	var after unix.Stat_t
	if unix.Fstat(payloadFD, &after) != nil ||
		!sameLinuxFileStat(expectedDirectory, &after) {
		return ErrUnsafeRuntime
	}
	return nil
}

func (task *linuxManifestVerificationTask) validateExtractedState() error {
	if task == nil || !task.extracted || task.payloadSealed == nil {
		return ErrUnsafeRuntime
	}
	if task.payload == nil {
		return ErrUnsafeRuntime
	}
	var pinnedPayload unix.Stat_t
	if unix.Fstat(int(task.payload.Fd()), &pinnedPayload) != nil ||
		!sameLinuxFileStat(task.payloadSealed, &pinnedPayload) {
		return ErrUnsafeRuntime
	}
	rootFD, taskFD, err := task.openSafeTask()
	if err != nil {
		return ErrUnsafeRuntime
	}
	defer unix.Close(rootFD)
	defer unix.Close(taskFD)
	if validatePinnedManifestFile(
		taskFD,
		manifestEncryptedName,
		task.encrypted,
		task.currentEncryptedStat(),
		task.runtime.uid,
	) != nil ||
		validatePinnedManifestFile(
			taskFD,
			manifestArchiveName,
			task.archive,
			task.archiveSealed,
			task.runtime.uid,
		) != nil ||
		validatePinnedManifestFile(
			taskFD,
			manifestResultName,
			task.result,
			task.currentResultStat(),
			task.runtime.uid,
		) != nil ||
		task.validatePayload(taskFD, task.payloadSealed, task.extractedFiles) != nil {
		return ErrUnsafeRuntime
	}
	return nil
}

func (task *linuxManifestVerificationTask) DirectoryPath() string {
	if task.validateExtractedState() != nil {
		return ""
	}
	return task.payloadPath
}

func (task *linuxManifestVerificationTask) VerificationTarget() (string, []*os.File) {
	if task == nil || task.payload == nil || task.validateExtractedState() != nil {
		return "", nil
	}
	return "/proc/self/fd/3", []*os.File{task.payload}
}

func (task *linuxManifestVerificationTask) ResultWriter() io.Writer {
	if task == nil || task.aborted || task.resultRead ||
		task.resultWriter == nil || task.resultWriter.failed ||
		task.validateExtractedState() != nil {
		return nil
	}
	return task.resultWriter
}

func (task *linuxManifestVerificationTask) ReadResult() ([]byte, error) {
	if task == nil || task.aborted || task.resultRead ||
		task.resultWriter == nil || task.resultWriter.failed ||
		task.resultWriter.last == nil ||
		task.resultWriter.written <= 0 ||
		task.resultWriter.written > int64(maximumCommandOutputBytes) {
		return nil, ErrUnsafeRuntime
	}
	if unix.Fdatasync(int(task.result.Fd())) != nil ||
		task.validateExtractedState() != nil {
		return nil, ErrUnsafeRuntime
	}
	if _, err := task.result.Seek(0, io.SeekStart); err != nil {
		return nil, ErrUnsafeRuntime
	}
	raw, err := io.ReadAll(io.LimitReader(task.result, int64(maximumCommandOutputBytes)+1))
	if err != nil ||
		int64(len(raw)) != task.resultWriter.written ||
		len(raw) > int(maximumCommandOutputBytes) ||
		task.validateExtractedState() != nil {
		return nil, ErrUnsafeRuntime
	}
	task.resultRead = true
	return append([]byte(nil), raw...), nil
}

func (task *linuxManifestVerificationTask) Abort() {
	if task == nil || task.aborted {
		return
	}
	task.aborted = true
	for _, file := range []*os.File{task.encrypted, task.archive, task.payload, task.result} {
		if file != nil {
			_ = file.Close()
		}
	}
	if task.runtime == nil || task.taskName == "" {
		return
	}
	rootFD, _, err := task.runtime.openRoot()
	if err != nil {
		return
	}
	defer unix.Close(rootFD)
	taskFD, taskStat, err := task.runtime.openTask(rootFD, task.taskName)
	if err != nil {
		return
	}
	if task.taskIdentity != nil && !sameLinuxFileStat(task.taskIdentity, &taskStat) {
		unix.Close(taskFD)
		return
	}
	names, err := linuxDirectoryNames(taskFD)
	if err != nil || !safeManifestTopLevelSubset(names) {
		unix.Close(taskFD)
		return
	}
	if manifestNamePresent(names, manifestOwnerMarker) &&
		validateManifestOwnerMarker(taskFD, task.expectation.Metadata.BackupID, task.runtime.uid) != nil {
		unix.Close(taskFD)
		return
	}
	for _, name := range names {
		if name == manifestPayloadName {
			continue
		}
		var stat unix.Stat_t
		if unix.Fstatat(taskFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW) != nil ||
			!trustedLinuxFile(&stat, task.runtime.uid) ||
			!task.safeKnownTopFile(name, &stat) {
			unix.Close(taskFD)
			return
		}
	}
	var payloadFD int = -1
	var payloadNames []string
	if manifestNamePresent(names, manifestPayloadName) {
		payloadFD, payloadStat, openErr := openManifestDirectoryAt(taskFD, manifestPayloadName, task.runtime.uid)
		if openErr != nil ||
			(task.payloadInitial != nil &&
				linuxDirectoryIdentityFromStat(&payloadStat) != linuxDirectoryIdentityFromStat(task.payloadInitial)) {
			if payloadFD >= 0 {
				unix.Close(payloadFD)
			}
			unix.Close(taskFD)
			return
		}
		payloadNames, err = linuxDirectoryNames(payloadFD)
		if err != nil || !safeManifestPayloadSubset(payloadNames) {
			unix.Close(payloadFD)
			unix.Close(taskFD)
			return
		}
		for _, name := range payloadNames {
			var stat unix.Stat_t
			if unix.Fstatat(payloadFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW) != nil ||
				!trustedLinuxFile(&stat, task.runtime.uid) {
				unix.Close(payloadFD)
				unix.Close(taskFD)
				return
			}
			if expected, ok := task.extractedFiles[name]; ok &&
				!sameLinuxFileStat(&expected, &stat) {
				unix.Close(payloadFD)
				unix.Close(taskFD)
				return
			}
		}
	}
	if payloadFD >= 0 {
		for _, name := range payloadNames {
			if unix.Unlinkat(payloadFD, name, 0) != nil {
				unix.Close(payloadFD)
				unix.Close(taskFD)
				return
			}
		}
		_ = unix.Fsync(payloadFD)
		unix.Close(payloadFD)
		if unix.Unlinkat(taskFD, manifestPayloadName, unix.AT_REMOVEDIR) != nil {
			unix.Close(taskFD)
			return
		}
	}
	for _, name := range names {
		if name == manifestPayloadName {
			continue
		}
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

func (task *linuxManifestVerificationTask) safeKnownTopFile(name string, stat *unix.Stat_t) bool {
	var expected *unix.Stat_t
	switch name {
	case manifestOwnerMarker:
		return true
	case manifestEncryptedName:
		expected = task.currentEncryptedStat()
	case manifestArchiveName:
		expected = task.currentArchiveStat()
	case manifestResultName:
		expected = task.currentResultStat()
	default:
		return false
	}
	return expected == nil || sameLinuxFileStat(expected, stat)
}

func safeManifestTopLevelSubset(names []string) bool {
	allowed := map[string]bool{
		manifestOwnerMarker:   true,
		manifestEncryptedName: true,
		manifestArchiveName:   true,
		manifestPayloadName:   true,
		manifestResultName:    true,
	}
	for _, name := range names {
		if !allowed[name] {
			return false
		}
	}
	return true
}

func safeManifestPayloadSubset(names []string) bool {
	allowed := make(map[string]bool, len(manifestPayloadFiles))
	for _, name := range manifestPayloadFiles {
		allowed[name] = true
	}
	for _, name := range names {
		if !allowed[name] {
			return false
		}
	}
	return true
}

func manifestNamePresent(names []string, expected string) bool {
	for _, name := range names {
		if name == expected {
			return true
		}
	}
	return false
}
