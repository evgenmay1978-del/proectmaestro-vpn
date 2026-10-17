//go:build linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadSecureFileAcceptsOwnerOnlyRegularFile(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "secret.json")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := readSecureFile(path, secureSecretFile, 64)
	if err != nil {
		t.Fatalf("read secure file: %v", err)
	}
	if string(data) != "payload" {
		t.Fatalf("payload = %q", data)
	}
}

func TestReadSecureFileRejectsUnsafeFilesystemShapes(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(*testing.T, string) string
	}{
		{
			name: "group readable",
			arrange: func(t *testing.T, root string) string {
				path := filepath.Join(root, "mode.json")
				if err := os.WriteFile(path, []byte("x"), 0o640); err != nil {
					t.Fatal(err)
				}
				return path
			},
		},
		{
			name: "hard link",
			arrange: func(t *testing.T, root string) string {
				path := filepath.Join(root, "source.json")
				if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(root, "hard.json")
				if err := os.Link(path, link); err != nil {
					t.Fatal(err)
				}
				return link
			},
		},
		{
			name: "symbolic link",
			arrange: func(t *testing.T, root string) string {
				target := filepath.Join(root, "target.json")
				if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(root, "symbolic.json")
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
				return link
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0o700); err != nil {
				t.Fatal(err)
			}
			path := test.arrange(t, root)
			if _, err := readSecureFile(path, secureSecretFile, 64); !errors.Is(err, errUnsafeRuntime) {
				t.Fatalf("error = %v, want errUnsafeRuntime", err)
			}
		})
	}
}

func TestVerifySecureDirectoryRejectsBroadMode(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := verifySecureDirectory(root); !errors.Is(err, errUnsafeRuntime) {
		t.Fatalf("error = %v, want errUnsafeRuntime", err)
	}
}
func TestReadSecureFileRejectsUnsafeAncestorMode(t *testing.T) {
	root := t.TempDir()
	unsafeParent := filepath.Join(root, "unsafe")
	trustedChild := filepath.Join(unsafeParent, "trusted")
	if err := os.MkdirAll(trustedChild, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unsafeParent, 0o777); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(trustedChild, "secret.json")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSecureFile(target, secureSecretFile, 64); !errors.Is(err, errUnsafeRuntime) {
		t.Fatalf("error = %v, want errUnsafeRuntime", err)
	}
}

func TestPinnedSecureFileSurvivesAncestorAndFinalReplacement(t *testing.T) {
	root := t.TempDir()
	trustedParent := filepath.Join(root, "trusted")
	if err := os.Mkdir(trustedParent, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(trustedParent, "tool")
	if err := os.WriteFile(target, []byte("original"), 0o700); err != nil {
		t.Fatal(err)
	}
	pinned, err := openPinnedSecureFile(target, secureExecutableFile)
	if err != nil {
		t.Fatalf("pin secure file: %v", err)
	}
	defer pinned.Close()

	movedParent := filepath.Join(root, "moved")
	if err := os.Rename(trustedParent, movedParent); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(trustedParent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("replacement"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(pinnedSecurePath(pinned))
	if err != nil || string(got) != "original" {
		t.Fatalf("pinned content = %q, error = %v", got, err)
	}
}
