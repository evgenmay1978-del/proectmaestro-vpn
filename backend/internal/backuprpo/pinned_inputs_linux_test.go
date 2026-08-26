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

type pinnedPreparedTaskFixture struct{}

func (pinnedPreparedTaskFixture) ImageWriter() io.Writer { return io.Discard }
func (pinnedPreparedTaskFixture) ImagePath() string      { return "/proc/self/fd/3/task/image" }
func (pinnedPreparedTaskFixture) OutputPath() string     { return "/proc/self/fd/3/task/output" }
func (pinnedPreparedTaskFixture) SealImage(int64) error  { return nil }
func (pinnedPreparedTaskFixture) RemoveImage() error     { return nil }
func (pinnedPreparedTaskFixture) Abort()                 {}

func openPinnedInputFixture(t *testing.T, path string, directory bool) *os.File {
	t.Helper()
	if directory {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	} else if err := os.WriteFile(path, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func openPinnedCommandFixture(t *testing.T, path, body string) *os.File {
	t.Helper()
	script := "#!/bin/sh\nset -eu\n" +
		"for descriptor in 3 4 5 6 7; do test -e \"/proc/self/fd/$descriptor\"; done\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func TestPinnedConstructorsUseStableCommandDescriptors(t *testing.T) {
	base := t.TempDir()
	runtimePath := filepath.Join(base, "runtime")
	runtimeFile := openPinnedInputFixture(t, runtimePath, true)
	backupScript := openPinnedInputFixture(t, filepath.Join(base, "backup"), false)
	verifyScript := openPinnedInputFixture(t, filepath.Join(base, "verify"), false)
	keys := openPinnedInputFixture(t, filepath.Join(base, "keys"), false)
	gpg := openPinnedInputFixture(t, filepath.Join(base, "gpg"), false)
	python := openPinnedInputFixture(t, filepath.Join(base, "python"), false)
	gpgHome := openPinnedInputFixture(t, filepath.Join(base, "gnupg"), true)
	payload := openPinnedInputFixture(t, filepath.Join(base, "payload"), true)

	creatorConfig := creatorConfigFixture()
	creatorConfig.RuntimeDir = runtimePath
	order := []string{}
	creator, err := NewPinnedShellBundleCreator(
		creatorConfig,
		&recordingBackupSource{order: &order},
		PinnedBundleInputs{
			RuntimeDir: runtimeFile, VerifyScript: verifyScript, Script: backupScript,
			Keys: keys, GPGHome: gpgHome, GPG: gpg, Python: python,
		},
	)
	if err != nil {
		t.Fatalf("new pinned creator: %v", err)
	}
	if creator.config.ScriptPath != "/proc/self/fd/4" ||
		creator.config.VerifyScriptPath != "/proc/self/fd/5" ||
		creator.config.KeysPath != "/proc/self/fd/6" ||
		creator.config.GPGHome != "/proc/self/fd/7" ||
		creator.config.GPGPath != "/proc/self/fd/8" ||
		creator.config.PythonPath != "/proc/self/fd/9" {
		t.Fatalf("creator paths = %#v", creator.config)
	}
	creatorSpec := creator.commandSpec(pinnedPreparedTaskFixture{}, BundleRequest{})
	if len(creatorSpec.ExtraFiles) != 7 || creatorSpec.ExtraFiles[0] != runtimeFile ||
		creatorSpec.ExtraFiles[1] != backupScript || creatorSpec.ExtraFiles[4] != gpgHome ||
		creatorSpec.ExtraFiles[5] != gpg || creatorSpec.ExtraFiles[6] != python {
		t.Fatalf("creator extra files = %v", creatorSpec.ExtraFiles)
	}
	creatorEnv := strings.Join(creatorSpec.Env, "\n")
	if !strings.Contains(creatorEnv, "MAESTRO_BACKUP_GPG=/proc/self/fd/8") ||
		!strings.Contains(creatorEnv, "MAESTRO_BACKUP_PYTHON=/proc/self/fd/9") ||
		!strings.Contains(creatorEnv, "GNUPGHOME=/proc/self/fd/7") {
		t.Fatalf("creator env = %q", creatorSpec.Env)
	}

	verifierConfig := manifestVerifierConfigFixture()
	verifierConfig.RuntimeDir = runtimePath
	verifier, err := NewPinnedShellManifestVerifier(
		verifierConfig,
		PinnedManifestInputs{
			RuntimeDir: runtimeFile, VerifyScript: verifyScript, GPG: gpg,
			Python: python, GPGHome: gpgHome,
		},
	)
	if err != nil {
		t.Fatalf("new pinned verifier: %v", err)
	}
	if verifier.config.VerifyScriptPath != "/proc/self/fd/4" ||
		verifier.config.GPGPath != "/proc/self/fd/5" ||
		verifier.config.PythonPath != "/proc/self/fd/6" ||
		verifier.config.GPGHome != "/proc/self/fd/7" {
		t.Fatalf("verifier paths = %#v", verifier.config)
	}
	decryptSpec := verifier.decryptSpec(nil, io.Discard)
	if len(decryptSpec.ExtraFiles) != 5 || decryptSpec.ExtraFiles[0] != runtimeFile ||
		decryptSpec.ExtraFiles[1] != verifyScript {
		t.Fatalf("decrypt extra files = %v", decryptSpec.ExtraFiles)
	}
	verifySpec := verifier.verifySpec("/proc/self/fd/3", []*os.File{payload}, io.Discard)
	if len(verifySpec.ExtraFiles) != 5 || verifySpec.ExtraFiles[0] != payload ||
		verifySpec.ExtraFiles[1] != verifyScript {
		t.Fatalf("verify extra files = %v", verifySpec.ExtraFiles)
	}

	movedRuntime := filepath.Join(base, "runtime-moved")
	if err := os.Rename(runtimePath, movedRuntime); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(runtimePath, 0o700); err != nil {
		t.Fatal(err)
	}
	pinnedRuntime, ok := creator.runtime.(*linuxBundleRuntime)
	if !ok {
		t.Fatalf("creator runtime = %T", creator.runtime)
	}
	rootFD, stat, err := pinnedRuntime.openRoot()
	if err != nil {
		t.Fatalf("open pinned root after replacement: %v", err)
	}
	defer unixCloseForTest(rootFD)
	if stat.Ino != pinnedRuntime.root.ino {
		t.Fatalf("opened inode = %d, want %d", stat.Ino, pinnedRuntime.root.ino)
	}
}

func TestPinnedManifestSpecsResolveEveryDescriptorWithRealProcesses(t *testing.T) {
	base := t.TempDir()
	runtimePath := filepath.Join(base, "runtime")
	gpgHomePath := filepath.Join(base, "gnupg")
	runtimeFile := openPinnedInputFixture(t, runtimePath, true)
	gpgHome := openPinnedInputFixture(t, gpgHomePath, true)
	verifyScript := openPinnedInputFixture(t, filepath.Join(base, "verify"), false)
	gpgPath := filepath.Join(base, "gpg")
	pythonPath := filepath.Join(base, "python")
	gpg := openPinnedCommandFixture(t, gpgPath, "cat")
	python := openPinnedCommandFixture(t, pythonPath, "printf verified")
	payload := openPinnedInputFixture(t, filepath.Join(base, "payload"), true)

	for _, path := range []string{gpgPath, pythonPath} {
		if err := os.Rename(path, path+"-pinned"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 99\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	verifier := &ShellManifestVerifier{
		config: ManifestVerifierConfig{
			VerifyScriptPath: "/proc/self/fd/4",
			GPGPath:          "/proc/self/fd/5",
			PythonPath:       "/proc/self/fd/6",
			GPGHome:          "/proc/self/fd/7",
			CommandTimeout:   5 * time.Second,
		},
		decryptLeadFile: runtimeFile,
		commandFiles:    []*os.File{verifyScript, gpg, python, gpgHome},
	}

	var archive strings.Builder
	if err := (osCommandRunner{}).Run(
		context.Background(),
		verifier.decryptSpec(strings.NewReader("ciphertext"), &archive),
	); err != nil {
		t.Fatalf("decrypt pinned process: %v", err)
	}
	if archive.String() != "ciphertext" {
		t.Fatalf("archive = %q", archive.String())
	}

	var result strings.Builder
	if err := (osCommandRunner{}).Run(
		context.Background(),
		verifier.verifySpec("/proc/self/fd/3", []*os.File{payload}, &result),
	); err != nil {
		t.Fatalf("verify pinned process: %v", err)
	}
	if result.String() != "verified" {
		t.Fatalf("result = %q", result.String())
	}
}

func TestPinnedBundleCreatorLaunchesWorkerWithFD3ThroughFD9(t *testing.T) {
	base := t.TempDir()
	runtimePath := filepath.Join(base, "runtime")
	runtimeFile := openPinnedInputFixture(t, runtimePath, true)
	gpgHome := openPinnedInputFixture(t, filepath.Join(base, "gnupg"), true)
	verifyScript := openPinnedInputFixture(t, filepath.Join(base, "verify.py"), false)
	keysPath := filepath.Join(base, "keys.json")
	keys := openPinnedInputFixture(t, keysPath, false)
	if err := os.Chmod(keysPath, 0o600); err != nil {
		t.Fatal(err)
	}
	gpg := openPinnedCommandFixture(t, filepath.Join(base, "gpg"), "exit 97")
	marker := filepath.Join(base, "fd-contract-reached")
	python := openPinnedCommandFixture(
		t,
		filepath.Join(base, "python"),
		"for descriptor in 8 9; do test -e \"/proc/self/fd/$descriptor\"; done\n"+
			": > "+shellSingleQuoteForTest(marker)+"\nexit 91",
	)
	repositoryScript, err := os.ReadFile(filepath.Join("..", "..", "..", "ops", "ha", "backup-rqlite.sh"))
	if err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(base, "backup-rqlite.sh")
	if err := os.WriteFile(scriptPath, repositoryScript, 0o700); err != nil {
		t.Fatal(err)
	}
	backupScript, err := os.Open(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backupScript.Close() })

	config := creatorConfigFixture()
	config.RuntimeDir = runtimePath
	config.CommandTimeout = 5 * time.Second
	order := []string{}
	creator, err := NewPinnedShellBundleCreator(
		config,
		&recordingBackupSource{order: &order},
		PinnedBundleInputs{
			RuntimeDir: runtimeFile, Script: backupScript, VerifyScript: verifyScript,
			Keys: keys, GPGHome: gpgHome, GPG: gpg, Python: python,
		},
	)
	if err != nil {
		t.Fatalf("new pinned creator: %v", err)
	}

	if _, err := creator.Create(context.Background(), bundleRequestFixture()); !errors.Is(err, ErrCommandFailed) {
		t.Fatalf("Create error = %v, want controlled fake-python failure", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("worker did not accept and preserve fd3..fd9 before invoking python: %v", err)
	}
}

func shellSingleQuoteForTest(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func unixCloseForTest(fd int) {
	_ = os.NewFile(uintptr(fd), "pinned-root").Close()
}
