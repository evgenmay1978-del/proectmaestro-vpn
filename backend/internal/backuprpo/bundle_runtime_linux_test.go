//go:build linux

package backuprpo

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func linuxRuntimeFixture(t *testing.T) (string, bundleRuntime, BundleRequest) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("mkdir runtime: %v", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("chmod runtime: %v", err)
	}
	runtime, err := newSecureBundleRuntime(root)
	if err != nil {
		t.Fatalf("newSecureBundleRuntime: %v", err)
	}
	return root, runtime, BundleRequest{
		RestoreEpoch:       2,
		CapturedGeneration: 5,
		AttemptSequence:    3,
		BackupID:           "0123456789abcdef0123456789abcdef",
		ObjectKey:          "backup-rpo/g-5/a-3-0123456789abcdef0123456789abcdef.tar.gpg",
		LeaseFence:         7,
	}
}

func createLinuxCandidate(t *testing.T, runtime bundleRuntime, request BundleRequest, payload []byte) preparedTask {
	t.Helper()
	task, err := runtime.Prepare(request)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if _, err := task.ImageWriter().Write([]byte("SQLite format 3\x00database-image")); err != nil {
		t.Fatalf("write image: %v", err)
	}
	if err := task.SealImage(1 << 20); err != nil {
		t.Fatalf("SealImage: %v", err)
	}
	output, err := os.OpenFile(task.OutputPath(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("create output: %v", err)
	}
	if _, err := output.Write(payload); err != nil {
		output.Close()
		t.Fatalf("write output: %v", err)
	}
	if err := output.Sync(); err != nil {
		output.Close()
		t.Fatalf("sync output: %v", err)
	}
	if err := output.Close(); err != nil {
		t.Fatalf("close output: %v", err)
	}
	if err := os.Chmod(task.OutputPath(), 0o600); err != nil {
		t.Fatalf("chmod output: %v", err)
	}
	if err := task.RemoveImage(); err != nil {
		t.Fatalf("RemoveImage: %v", err)
	}
	return task
}

func TestLinuxBundleRuntimePinsDescriptorAcrossPathReplacement(t *testing.T) {
	_, runtime, request := linuxRuntimeFixture(t)
	task := createLinuxCandidate(t, runtime, request, []byte("original-encrypted-bundle"))

	bundle, err := runtime.Pin(request, 1<<20)
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}
	defer bundle.Close()

	moved := task.OutputPath() + ".moved"
	if err := os.Rename(task.OutputPath(), moved); err != nil {
		t.Fatalf("rename pinned path: %v", err)
	}
	if err := os.WriteFile(task.OutputPath(), []byte("replacement"), 0o600); err != nil {
		t.Fatalf("replacement: %v", err)
	}
	if _, err := bundle.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("seek pinned: %v", err)
	}
	got, err := io.ReadAll(bundle)
	if err != nil {
		t.Fatalf("read pinned: %v", err)
	}
	if string(got) != "original-encrypted-bundle" {
		t.Fatalf("pinned content=%q", got)
	}
}

func TestLinuxBundleRuntimeRejectsUnsafeCandidateFiles(t *testing.T) {
	tests := []struct {
		name    string
		maximum int64
		mutate  func(*testing.T, string, preparedTask)
	}{
		{
			name:    "group readable",
			maximum: 1 << 20,
			mutate: func(t *testing.T, _ string, task preparedTask) {
				if err := os.Chmod(task.OutputPath(), 0o640); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:    "hard link",
			maximum: 1 << 20,
			mutate: func(t *testing.T, root string, task preparedTask) {
				if err := os.Link(task.OutputPath(), filepath.Join(root, "outside-link")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:    "symlink",
			maximum: 1 << 20,
			mutate: func(t *testing.T, _ string, task preparedTask) {
				target := task.OutputPath() + ".target"
				if err := os.Rename(task.OutputPath(), target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, task.OutputPath()); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:    "unexpected file",
			maximum: 1 << 20,
			mutate: func(t *testing.T, _ string, task preparedTask) {
				if err := os.WriteFile(filepath.Join(filepath.Dir(task.OutputPath()), "unexpected"), []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:    "oversize",
			maximum: 4,
			mutate:  func(*testing.T, string, preparedTask) {},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, runtime, request := linuxRuntimeFixture(t)
			task := createLinuxCandidate(t, runtime, request, []byte("encrypted-bundle"))
			test.mutate(t, root, task)
			bundle, err := runtime.Pin(request, test.maximum)
			if bundle != nil || !errors.Is(err, ErrUnsafeRuntime) || strings.Contains(err.Error(), root) {
				t.Fatalf("bundle=%v error=%q", bundle, err)
			}
		})
	}
}

func TestLinuxBundleRuntimeRejectsUnsafeRootAndInvalidImage(t *testing.T) {
	t.Run("symlink root", func(t *testing.T) {
		parent := t.TempDir()
		target := filepath.Join(parent, "target")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(parent, "runtime")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		runtime, err := newSecureBundleRuntime(link)
		if runtime != nil || !errors.Is(err, ErrUnsafeRuntime) {
			t.Fatalf("runtime=%v error=%v", runtime, err)
		}
	})

	t.Run("invalid image", func(t *testing.T) {
		_, runtime, request := linuxRuntimeFixture(t)
		task, err := runtime.Prepare(request)
		if err != nil {
			t.Fatal(err)
		}
		defer task.Abort()
		if _, err := task.ImageWriter().Write([]byte("not sqlite")); err != nil {
			t.Fatal(err)
		}
		if err := task.SealImage(1 << 20); !errors.Is(err, ErrUnsafeRuntime) {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("no clobber", func(t *testing.T) {
		_, runtime, request := linuxRuntimeFixture(t)
		first, err := runtime.Prepare(request)
		if err != nil {
			t.Fatal(err)
		}
		defer first.Abort()
		second, err := runtime.Prepare(request)
		if second != nil || !errors.Is(err, ErrUnsafeRuntime) {
			t.Fatalf("second=%v error=%v", second, err)
		}
	})
}

func TestLinuxProcessRunnerKillsProcessGroupAndRedactsOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess boundary")
	}
	runner := osCommandRunner{}
	err := runner.Run(context.Background(), CommandSpec{
		Path:    "/bin/sh",
		Args:    []string{"-c", "printf 'credential=secret-value' >&2; sleep 30 & wait"},
		Env:     []string{"PATH=/usr/bin:/bin", "LANG=C"},
		Timeout: 50 * time.Millisecond,
	})
	if !errors.Is(err, ErrCommandFailed) || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("error=%q", err)
	}

	started := time.Now()
	err = runner.Run(context.Background(), CommandSpec{
		Path: "/bin/sh",
		Args: []string{
			"-c",
			"setsid /bin/sh -c 'sleep 30' & printf 'credential=detached-secret' >&2; wait",
		},
		Env:     []string{"PATH=/usr/bin:/bin", "LANG=C"},
		Timeout: 50 * time.Millisecond,
	})
	elapsed := time.Since(started)
	if !errors.Is(err, ErrCommandFailed) || strings.Contains(err.Error(), "detached-secret") {
		t.Fatalf("detached error=%q", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("detached pipe holder blocked for %s", elapsed)
	}
}
