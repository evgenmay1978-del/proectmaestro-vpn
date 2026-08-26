//go:build linux

package backuprpo

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	manifestTestEncryptedName = "encrypted.bundle"
	manifestTestArchiveName   = "archive.tar"
	manifestTestPayloadName   = "payload"
	manifestTestResultName    = "result.json"
	manifestTestMarkerName    = ".maestro-manifest-owner"
)

type manifestTarEntry struct {
	name     string
	typeflag byte
	mode     int64
	linkname string
	data     []byte
}

type failingManifestReader struct{}

func (failingManifestReader) Read([]byte) (int, error) {
	return 0, errors.New("sensitive reader failure")
}

func validManifestTarEntries() []manifestTarEntry {
	return []manifestTarEntry{
		{name: "application-keys.json", typeflag: tar.TypeReg, mode: 0o600, data: []byte("{\"keys\":[]}")},
		{name: "control-plane.sqlite3", typeflag: tar.TypeReg, mode: 0o600, data: []byte("SQLite format 3\x00database")},
		{name: "manifest.json", typeflag: tar.TypeReg, mode: 0o600, data: []byte("{\"format_version\":2}")},
		{name: "manifest.sig", typeflag: tar.TypeReg, mode: 0o600, data: []byte("detached-signature")},
	}
}

func manifestTarBytes(t *testing.T, entries []manifestTarEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, entry := range entries {
		header := &tar.Header{
			Name:     entry.name,
			Mode:     entry.mode,
			Size:     int64(len(entry.data)),
			Typeflag: entry.typeflag,
			Linkname: entry.linkname,
			ModTime:  time.Unix(1, 0),
		}
		if entry.typeflag != tar.TypeReg {
			header.Size = 0
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatalf("WriteHeader(%s): %v", entry.name, err)
		}
		if entry.typeflag == tar.TypeReg {
			if _, err := writer.Write(entry.data); err != nil {
				t.Fatalf("Write(%s): %v", entry.name, err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close tar: %v", err)
	}
	return buffer.Bytes()
}

func manifestLinuxLimits() ManifestVerifierLimits {
	return ManifestVerifierLimits{
		MaxBundleBytes:    1 << 20,
		MaxArchiveBytes:   1 << 20,
		MaxExtractedBytes: 1 << 20,
	}
}

func newLinuxManifestRuntimeFixture(t *testing.T) (string, manifestVerificationRuntime) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("Mkdir runtime: %v", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("Chmod runtime: %v", err)
	}
	runtime, err := newSecureManifestVerificationRuntime(root)
	if err != nil {
		t.Fatalf("newSecureManifestVerificationRuntime: %v", err)
	}
	return root, runtime
}

func prepareLinuxManifestTask(
	t *testing.T,
	runtime manifestVerificationRuntime,
	ciphertext string,
	limits ManifestVerifierLimits,
) manifestVerificationTask {
	t.Helper()
	task, err := runtime.Prepare(strings.NewReader(ciphertext), manifestExpectationFixture(t), limits)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if task == nil {
		t.Fatal("Prepare returned nil task")
	}
	return task
}

func onlyManifestTaskDirectory(t *testing.T, root string) string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", root, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "verify-") {
			names = append(names, entry.Name())
		}
	}
	if len(names) != 1 {
		t.Fatalf("verify task names=%v all=%v", names, entries)
	}
	return filepath.Join(root, names[0])
}

func exactDirectoryNames(t *testing.T, directory string, expected ...string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", directory, err)
	}
	actual := make([]string, 0, len(entries))
	for _, entry := range entries {
		actual = append(actual, entry.Name())
	}
	sort.Strings(actual)
	sort.Strings(expected)
	if strings.Join(actual, ",") != strings.Join(expected, ",") {
		t.Fatalf("names=%v want=%v", actual, expected)
	}
}

func requireMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat(%s): %v", path, err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("mode(%s)=%#o want=%#o", path, info.Mode().Perm(), want)
	}
}

func requireUnsafeManifestError(t *testing.T, err error, sensitive string) {
	t.Helper()
	if !errors.Is(err, ErrUnsafeRuntime) || err.Error() != ErrUnsafeRuntime.Error() ||
		(sensitive != "" && strings.Contains(err.Error(), sensitive)) {
		t.Fatalf("error=%q", err)
	}
}

func sealAndExtractManifestTask(t *testing.T, task manifestVerificationTask, archive []byte) {
	t.Helper()
	writer := task.ArchiveWriter()
	if writer == nil {
		t.Fatal("ArchiveWriter returned nil")
	}
	if _, err := writer.Write(archive); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	if err := task.SealArchive(); err != nil {
		t.Fatalf("SealArchive: %v", err)
	}
	if err := task.Extract(); err != nil {
		t.Fatalf("Extract: %v", err)
	}
}

func TestLinuxManifestRuntimePinsBoundedFilesAndExtractsExactPayload(t *testing.T) {
	root, runtime := newLinuxManifestRuntimeFixture(t)
	task := prepareLinuxManifestTask(t, runtime, "original-ciphertext", manifestLinuxLimits())
	taskDirectory := onlyManifestTaskDirectory(t, root)

	exactDirectoryNames(
		t,
		taskDirectory,
		manifestTestMarkerName,
		manifestTestEncryptedName,
		manifestTestArchiveName,
		manifestTestPayloadName,
		manifestTestResultName,
	)
	requireMode(t, taskDirectory, 0o700)
	requireMode(t, filepath.Join(taskDirectory, manifestTestMarkerName), 0o600)
	requireMode(t, filepath.Join(taskDirectory, manifestTestEncryptedName), 0o600)
	requireMode(t, filepath.Join(taskDirectory, manifestTestArchiveName), 0o600)
	requireMode(t, filepath.Join(taskDirectory, manifestTestPayloadName), 0o700)
	requireMode(t, filepath.Join(taskDirectory, manifestTestResultName), 0o600)

	encrypted := task.EncryptedReader()
	if encrypted == nil {
		t.Fatal("EncryptedReader returned nil")
	}
	if _, err := encrypted.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("seek encrypted: %v", err)
	}
	gotCiphertext, err := io.ReadAll(encrypted)
	if err != nil || string(gotCiphertext) != "original-ciphertext" {
		t.Fatalf("ciphertext=%q error=%v", gotCiphertext, err)
	}

	entries := validManifestTarEntries()
	sealAndExtractManifestTask(t, task, manifestTarBytes(t, entries))
	payload := task.DirectoryPath()
	if payload != filepath.Join(taskDirectory, manifestTestPayloadName) {
		t.Fatalf("payload=%q", payload)
	}
	exactDirectoryNames(t, payload, "application-keys.json", "control-plane.sqlite3", "manifest.json", "manifest.sig")
	for _, entry := range entries {
		path := filepath.Join(payload, entry.name)
		requireMode(t, path, 0o600)
		got, readErr := os.ReadFile(path)
		if readErr != nil || !bytes.Equal(got, entry.data) {
			t.Fatalf("payload %s=%q error=%v", entry.name, got, readErr)
		}
	}

	result := []byte("{\"status\":\"verified\"}")
	if _, err := task.ResultWriter().Write(result); err != nil {
		t.Fatalf("write result: %v", err)
	}
	gotResult, err := task.ReadResult()
	if err != nil || !bytes.Equal(gotResult, result) {
		t.Fatalf("result=%q error=%v", gotResult, err)
	}

	task.Abort()
	if _, err := os.Lstat(taskDirectory); !errors.Is(err, os.ErrNotExist) {
		survivors := make([]string, 0)
		topEntries, topReadErr := os.ReadDir(taskDirectory)
		if topReadErr == nil {
			for _, entry := range topEntries {
				survivors = append(survivors, entry.Name())
				if entry.Name() != manifestTestPayloadName {
					continue
				}
				payloadEntries, payloadReadErr := os.ReadDir(filepath.Join(taskDirectory, entry.Name()))
				if payloadReadErr != nil {
					survivors = append(survivors, manifestTestPayloadName+"/<unreadable>")
					continue
				}
				for _, payloadEntry := range payloadEntries {
					survivors = append(survivors, manifestTestPayloadName+"/"+payloadEntry.Name())
				}
			}
		}
		t.Fatalf("task directory survived safe Abort: %v; top_read_ok=%t; survivors=%v", err, topReadErr == nil, survivors)
	}
}

func TestLinuxManifestRuntimeUsesRandomNoClobberTasksAndEnforcesAllBounds(t *testing.T) {
	t.Run("random no clobber", func(t *testing.T) {
		root, runtime := newLinuxManifestRuntimeFixture(t)
		first := prepareLinuxManifestTask(t, runtime, "first", manifestLinuxLimits())
		firstDirectory := onlyManifestTaskDirectory(t, root)
		second := prepareLinuxManifestTask(t, runtime, "second", manifestLinuxLimits())
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 2 {
			t.Fatalf("entries=%v error=%v", entries, err)
		}
		secondDirectory := ""
		for _, entry := range entries {
			path := filepath.Join(root, entry.Name())
			if path != firstDirectory {
				secondDirectory = path
			}
		}
		if secondDirectory == "" || secondDirectory == firstDirectory {
			t.Fatalf("first=%q second=%q", firstDirectory, secondDirectory)
		}
		first.Abort()
		second.Abort()
	})

	t.Run("encrypted input", func(t *testing.T) {
		root, runtime := newLinuxManifestRuntimeFixture(t)
		limits := manifestLinuxLimits()
		limits.MaxBundleBytes = 8
		task, err := runtime.Prepare(strings.NewReader("secret-123"), manifestExpectationFixture(t), limits)
		if task != nil {
			task.Abort()
		}
		requireUnsafeManifestError(t, err, "secret-123")
		entries, readErr := os.ReadDir(root)
		if readErr != nil || len(entries) != 0 {
			t.Fatalf("partial tasks=%v error=%v", entries, readErr)
		}
	})

	t.Run("partial encrypted reader error cleanup", func(t *testing.T) {
		root, runtime := newLinuxManifestRuntimeFixture(t)
		reader := io.MultiReader(strings.NewReader("first-chunk"), failingManifestReader{})
		task, err := runtime.Prepare(reader, manifestExpectationFixture(t), manifestLinuxLimits())
		if task != nil {
			task.Abort()
		}
		requireUnsafeManifestError(t, err, "sensitive reader failure")
		entries, readErr := os.ReadDir(root)
		if readErr != nil || len(entries) != 0 {
			t.Fatalf("partial reader tasks=%v error=%v", entries, readErr)
		}
	})

	t.Run("multi chunk encrypted overflow cleanup", func(t *testing.T) {
		root, runtime := newLinuxManifestRuntimeFixture(t)
		limits := manifestLinuxLimits()
		limits.MaxBundleBytes = 8
		reader := io.MultiReader(strings.NewReader("1234"), strings.NewReader("56789"))
		task, err := runtime.Prepare(reader, manifestExpectationFixture(t), limits)
		if task != nil {
			task.Abort()
		}
		requireUnsafeManifestError(t, err, "")
		entries, readErr := os.ReadDir(root)
		if readErr != nil || len(entries) != 0 {
			t.Fatalf("overflow tasks=%v error=%v", entries, readErr)
		}
	})

	t.Run("archive writer", func(t *testing.T) {
		_, runtime := newLinuxManifestRuntimeFixture(t)
		limits := manifestLinuxLimits()
		limits.MaxArchiveBytes = 8
		task := prepareLinuxManifestTask(t, runtime, "cipher", limits)
		defer task.Abort()
		if _, err := task.ArchiveWriter().Write([]byte("123456789")); err == nil {
			t.Fatal("oversize archive write succeeded")
		} else {
			requireUnsafeManifestError(t, err, "")
		}
		if err := task.SealArchive(); err == nil {
			t.Fatal("oversize archive sealed")
		}
	})

	t.Run("result writer", func(t *testing.T) {
		_, runtime := newLinuxManifestRuntimeFixture(t)
		task := prepareLinuxManifestTask(t, runtime, "cipher", manifestLinuxLimits())
		defer task.Abort()
		sealAndExtractManifestTask(t, task, manifestTarBytes(t, validManifestTarEntries()))
		if _, err := task.ResultWriter().Write(bytes.Repeat([]byte("x"), 4097)); err == nil {
			t.Fatal("oversize result write succeeded")
		} else {
			requireUnsafeManifestError(t, err, "")
		}
		if _, err := task.ReadResult(); err == nil {
			t.Fatal("oversize result readback succeeded")
		}
	})
}

func TestLinuxManifestRuntimeRejectsUnsafeRootAndPinnedPathReplacement(t *testing.T) {
	t.Run("root symlink", func(t *testing.T) {
		parent := t.TempDir()
		target := filepath.Join(parent, "target")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		root := filepath.Join(parent, "runtime")
		if err := os.Symlink(target, root); err != nil {
			t.Fatal(err)
		}
		runtime, err := newSecureManifestVerificationRuntime(root)
		if runtime != nil {
			t.Fatalf("runtime=%v", runtime)
		}
		requireUnsafeManifestError(t, err, root)
	})

	t.Run("root mode", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "runtime")
		if err := os.Mkdir(root, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(root, 0o755); err != nil {
			t.Fatal(err)
		}
		runtime, err := newSecureManifestVerificationRuntime(root)
		if runtime != nil {
			t.Fatalf("runtime=%v", runtime)
		}
		requireUnsafeManifestError(t, err, root)
	})

	t.Run("encrypted descriptor remains pinned but replacement is rejected", func(t *testing.T) {
		root, runtime := newLinuxManifestRuntimeFixture(t)
		task := prepareLinuxManifestTask(t, runtime, "pinned-original", manifestLinuxLimits())
		taskDirectory := onlyManifestTaskDirectory(t, root)
		reader := task.EncryptedReader()
		encryptedPath := filepath.Join(taskDirectory, manifestTestEncryptedName)
		moved := encryptedPath + ".moved"
		if err := os.Rename(encryptedPath, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(encryptedPath, []byte("replacement"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(reader)
		if err != nil || string(got) != "pinned-original" {
			t.Fatalf("pinned read=%q error=%v", got, err)
		}
		if task.ArchiveWriter() != nil {
			t.Fatal("replacement path accepted")
		}
		task.Abort()
		if _, err := os.Lstat(taskDirectory); err != nil {
			t.Fatalf("unsafe evidence was removed: %v", err)
		}
	})
}

func TestLinuxManifestRuntimeRejectsArchiveMetadataAndPathTampering(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string, string)
	}{
		{
			name: "mode",
			mutate: func(t *testing.T, _ string, path string) {
				if err := os.Chmod(path, 0o640); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "hard link",
			mutate: func(t *testing.T, root string, path string) {
				if err := os.Link(path, filepath.Join(root, "archive-link")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "mtime",
			mutate: func(t *testing.T, _ string, path string) {
				future := time.Now().Add(time.Hour)
				if err := os.Chtimes(path, future, future); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "path replacement",
			mutate: func(t *testing.T, _ string, path string) {
				if err := os.Rename(path, path+".moved"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, runtime := newLinuxManifestRuntimeFixture(t)
			task := prepareLinuxManifestTask(t, runtime, "cipher", manifestLinuxLimits())
			taskDirectory := onlyManifestTaskDirectory(t, root)
			archivePath := filepath.Join(taskDirectory, manifestTestArchiveName)
			if _, err := task.ArchiveWriter().Write(manifestTarBytes(t, validManifestTarEntries())); err != nil {
				t.Fatal(err)
			}
			test.mutate(t, root, archivePath)
			requireUnsafeManifestError(t, task.SealArchive(), root)
			task.Abort()
		})
	}
}

func TestLinuxManifestRuntimeRejectsEveryUnsafeTarShape(t *testing.T) {
	tests := []struct {
		name    string
		entries func() []manifestTarEntry
		limit   int64
	}{
		{
			name: "symlink",
			entries: func() []manifestTarEntry {
				entries := validManifestTarEntries()
				entries[3] = manifestTarEntry{name: "manifest.sig", typeflag: tar.TypeSymlink, mode: 0o600, linkname: "manifest.json"}
				return entries
			},
		},
		{
			name: "hard link",
			entries: func() []manifestTarEntry {
				entries := validManifestTarEntries()
				entries[3] = manifestTarEntry{name: "manifest.sig", typeflag: tar.TypeLink, mode: 0o600, linkname: "manifest.json"}
				return entries
			},
		},
		{
			name: "special",
			entries: func() []manifestTarEntry {
				entries := validManifestTarEntries()
				entries[3] = manifestTarEntry{name: "manifest.sig", typeflag: tar.TypeChar, mode: 0o600}
				return entries
			},
		},
		{
			name: "traversal",
			entries: func() []manifestTarEntry {
				entries := validManifestTarEntries()
				entries[0].name = "../application-keys.json"
				return entries
			},
		},
		{
			name: "nested",
			entries: func() []manifestTarEntry {
				entries := validManifestTarEntries()
				entries[2].name = "payload/manifest.json"
				return entries
			},
		},
		{
			name: "duplicate",
			entries: func() []manifestTarEntry {
				entries := validManifestTarEntries()
				return append(entries, entries[2])
			},
		},
		{
			name: "unexpected",
			entries: func() []manifestTarEntry {
				return append(validManifestTarEntries(), manifestTarEntry{name: "secret.txt", typeflag: tar.TypeReg, mode: 0o600, data: []byte("secret")})
			},
		},
		{
			name: "missing",
			entries: func() []manifestTarEntry {
				return validManifestTarEntries()[:3]
			},
		},
		{
			name: "unsafe mode",
			entries: func() []manifestTarEntry {
				entries := validManifestTarEntries()
				entries[1].mode = 0o644
				return entries
			},
		},
		{
			name:    "extracted bytes",
			entries: validManifestTarEntries,
			limit:   8,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, runtime := newLinuxManifestRuntimeFixture(t)
			limits := manifestLinuxLimits()
			if test.limit != 0 {
				limits.MaxExtractedBytes = test.limit
			}
			task := prepareLinuxManifestTask(t, runtime, "cipher", limits)
			defer task.Abort()
			writer := task.ArchiveWriter()
			if _, err := writer.Write(manifestTarBytes(t, test.entries())); err != nil {
				t.Fatalf("write archive: %v", err)
			}
			if err := task.SealArchive(); err != nil {
				t.Fatalf("SealArchive: %v", err)
			}
			requireUnsafeManifestError(t, task.Extract(), "secret")
		})
	}
}

func TestLinuxManifestRuntimeRejectsTrailingAndConcatenatedTarData(t *testing.T) {
	valid := manifestTarBytes(t, validManifestTarEntries())
	tests := map[string][]byte{
		"trailing garbage":     append(append([]byte(nil), valid...), []byte("nonzero-trailing-data")...),
		"concatenated archive": append(append([]byte(nil), valid...), manifestTarBytes(t, validManifestTarEntries())...),
	}
	for name, archive := range tests {
		t.Run(name, func(t *testing.T) {
			_, runtime := newLinuxManifestRuntimeFixture(t)
			task := prepareLinuxManifestTask(t, runtime, "cipher", manifestLinuxLimits())
			defer task.Abort()
			if _, err := task.ArchiveWriter().Write(archive); err != nil {
				t.Fatalf("write archive: %v", err)
			}
			if err := task.SealArchive(); err != nil {
				t.Fatalf("SealArchive: %v", err)
			}
			requireUnsafeManifestError(t, task.Extract(), "nonzero-trailing-data")
		})
	}
}

func TestLinuxManifestRuntimeExposesPinnedPayloadTarget(t *testing.T) {
	root, runtime := newLinuxManifestRuntimeFixture(t)
	task := prepareLinuxManifestTask(t, runtime, "cipher", manifestLinuxLimits())
	defer task.Abort()
	sealAndExtractManifestTask(t, task, manifestTarBytes(t, validManifestTarEntries()))
	target, files := task.VerificationTarget()
	if target != "/proc/self/fd/3" || len(files) != 1 || files[0] == nil {
		t.Fatalf("target=%q files=%v", target, files)
	}
	pinnedManifest := fmt.Sprintf("/proc/self/fd/%d/manifest.json", files[0].Fd())
	before, err := os.ReadFile(pinnedManifest)
	if err != nil || string(before) != "{\"format_version\":2}" {
		t.Fatalf("pinned manifest=%q error=%v", before, err)
	}
	taskDirectory := onlyManifestTaskDirectory(t, root)
	if err := os.Rename(taskDirectory, taskDirectory+".moved"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(pinnedManifest)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("pinned after ancestor move=%q error=%v", after, err)
	}
	if unsafeTarget, unsafeFiles := task.VerificationTarget(); unsafeTarget != "" || len(unsafeFiles) != 0 {
		t.Fatalf("unsafe target=%q files=%v", unsafeTarget, unsafeFiles)
	}
}

func TestLinuxManifestRuntimeRejectsPayloadAndResultReplacement(t *testing.T) {
	t.Run("payload", func(t *testing.T) {
		root, runtime := newLinuxManifestRuntimeFixture(t)
		task := prepareLinuxManifestTask(t, runtime, "cipher", manifestLinuxLimits())
		defer task.Abort()
		sealAndExtractManifestTask(t, task, manifestTarBytes(t, validManifestTarEntries()))
		payload := task.DirectoryPath()
		manifestPath := filepath.Join(payload, "manifest.json")
		if err := os.Rename(manifestPath, manifestPath+".moved"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(manifestPath, []byte("replacement"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := task.DirectoryPath(); got != "" {
			t.Fatalf("unsafe payload path=%q root=%q", got, root)
		}
	})

	tests := []struct {
		name   string
		mutate func(*testing.T, string, string)
	}{
		{
			name: "mode",
			mutate: func(t *testing.T, _ string, path string) {
				if err := os.Chmod(path, 0o640); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "hard link",
			mutate: func(t *testing.T, root string, path string) {
				if err := os.Link(path, filepath.Join(root, "result-link")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "mtime",
			mutate: func(t *testing.T, _ string, path string) {
				future := time.Now().Add(time.Hour)
				if err := os.Chtimes(path, future, future); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "path replacement",
			mutate: func(t *testing.T, _ string, path string) {
				if err := os.Rename(path, path+".moved"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run("result "+test.name, func(t *testing.T) {
			root, runtime := newLinuxManifestRuntimeFixture(t)
			task := prepareLinuxManifestTask(t, runtime, "cipher", manifestLinuxLimits())
			defer task.Abort()
			sealAndExtractManifestTask(t, task, manifestTarBytes(t, validManifestTarEntries()))
			if _, err := task.ResultWriter().Write([]byte("{\"status\":\"verified\"}")); err != nil {
				t.Fatal(err)
			}
			taskDirectory := filepath.Dir(task.DirectoryPath())
			resultPath := filepath.Join(taskDirectory, manifestTestResultName)
			test.mutate(t, root, resultPath)
			_, err := task.ReadResult()
			requireUnsafeManifestError(t, err, root)
		})
	}
}

func TestLinuxManifestRuntimeAbortLeavesUnsafeEvidence(t *testing.T) {
	root, runtime := newLinuxManifestRuntimeFixture(t)
	task := prepareLinuxManifestTask(t, runtime, "cipher", manifestLinuxLimits())
	taskDirectory := onlyManifestTaskDirectory(t, root)
	unexpected := filepath.Join(taskDirectory, "unexpected")
	if err := os.WriteFile(unexpected, []byte("evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	task.Abort()
	task.Abort()
	got, err := os.ReadFile(unexpected)
	if err != nil || string(got) != "evidence" {
		t.Fatalf("unsafe evidence=%q error=%v", got, err)
	}
}
