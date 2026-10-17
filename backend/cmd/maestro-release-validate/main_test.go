package main

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/release"
)

type taskDReasonError struct{ code string }

func (err taskDReasonError) Error() string      { return "task D validation failure" }
func (err taskDReasonError) ReasonCode() string { return err.code }

func TestRunRejectsInvalidArgumentsWithoutEcho(t *testing.T) {
	trustPath, _ := writeTaskDTrust(t)
	previous := validateReleaseDirectoryWithTrust
	validateReleaseDirectoryWithTrust = func(string, release.EvidenceTrust) error {
		t.Fatal("validator called for invalid arguments")
		return nil
	}
	t.Cleanup(func() { validateReleaseDirectoryWithTrust = previous })

	secret := "do-not-echo-this-path"
	tests := []struct {
		name string
		args []string
	}{
		{name: "empty"},
		{name: "missing release", args: []string{"--evidence-trust", trustPath}},
		{name: "missing trust", args: []string{"--release-dir", "release"}},
		{name: "missing value", args: []string{"--release-dir"}},
		{name: "unknown flag", args: []string{"--unknown=" + secret, "--release-dir", "release", "--evidence-trust", trustPath}},
		{name: "extra argument", args: []string{"--release-dir", "release", "--evidence-trust", trustPath, secret}},
		{name: "duplicate release", args: []string{"--release-dir", "one", "--release-dir", secret, "--evidence-trust", trustPath}},
		{name: "duplicate trust", args: []string{"--release-dir", "release", "--evidence-trust", trustPath, "--evidence-trust", secret}},
		{name: "empty release", args: []string{"--release-dir=", "--evidence-trust", trustPath}},
		{name: "empty trust", args: []string{"--release-dir", "release", "--evidence-trust="}},
		{name: "unsafe release text", args: []string{"--release-dir", secret + "\nnext", "--evidence-trust", trustPath}},
		{name: "unsafe trust text", args: []string{"--release-dir", "release", "--evidence-trust", secret + "\rnext"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if code := run(test.args, &stdout, &stderr); code != 1 {
				t.Fatalf("exit=%d, want 1", code)
			}
			if stdout.String() != "" {
				t.Fatalf("stdout=%q, want empty", stdout.String())
			}
			const expected = "release_validation_failed code=arguments_invalid\n"
			if stderr.String() != expected {
				t.Fatalf("stderr=%q, want %q", stderr.String(), expected)
			}
		})
	}
}

func TestRunUsesExternalTrustAndPrintsOnlySuccess(t *testing.T) {
	trustPath, expectedTrust := writeTaskDTrust(t)
	releasePath := filepath.Join(t.TempDir(), "release candidate;$HOME")
	previous := validateReleaseDirectoryWithTrust
	validateReleaseDirectoryWithTrust = func(gotPath string, gotTrust release.EvidenceTrust) error {
		if gotPath != releasePath {
			t.Fatalf("release path=%q, want exact input", gotPath)
		}
		gotSHA, gotErr := gotTrust.SHA256()
		wantSHA, wantErr := expectedTrust.SHA256()
		if gotErr != nil || wantErr != nil || gotSHA != wantSHA {
			t.Fatalf("trust mismatch: got=%q/%v want=%q/%v", gotSHA, gotErr, wantSHA, wantErr)
		}
		return nil
	}
	t.Cleanup(func() { validateReleaseDirectoryWithTrust = previous })

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"--release-dir", releasePath, "--evidence-trust", trustPath}, &stdout, &stderr)
	if code != 0 || stdout.String() != "release_validation_passed\n" || stderr.String() != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunReturnsStableNonSecretTrustAndValidationCodes(t *testing.T) {
	releasePath := filepath.Join(t.TempDir(), "release")
	t.Run("trust read", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing-do-not-echo-trust.json")
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"--release-dir", releasePath, "--evidence-trust", missing}, &stdout, &stderr)
		if code != 1 || stdout.String() != "" || stderr.String() != "release_validation_failed code=evidence_trust_read_failed\n" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})

	t.Run("trust parse", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "trust-secret-marker.json")
		if err := os.WriteFile(path, []byte(`{"secret":"must-not-echo"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"--release-dir", releasePath, "--evidence-trust", path}, &stdout, &stderr)
		if code != 1 || stdout.String() != "" || stderr.String() != "release_validation_failed code=evidence_trust_invalid\n" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})

	trustPath, _ := writeTaskDTrust(t)
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "trust mismatch", err: taskDReasonError{code: "evidence_trust_mismatch"}, want: "evidence_trust_mismatch"},
		{name: "generic", err: errors.New("do-not-echo-generic-error"), want: "validation_failed"},
		{name: "unsafe reason", err: taskDReasonError{code: "unsafe/path/do-not-echo"}, want: "validation_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			previous := validateReleaseDirectoryWithTrust
			validateReleaseDirectoryWithTrust = func(string, release.EvidenceTrust) error { return test.err }
			t.Cleanup(func() { validateReleaseDirectoryWithTrust = previous })
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run([]string{"--release-dir", releasePath, "--evidence-trust", trustPath}, &stdout, &stderr)
			expected := "release_validation_failed code=" + test.want + "\n"
			if code != 1 || stdout.String() != "" || stderr.String() != expected {
				t.Fatalf("exit=%d stdout=%q stderr=%q want=%q", code, stdout.String(), stderr.String(), expected)
			}
		})
	}
}

func TestBashWrapperForwardsOpaquePathsAsSingleArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Bash wrapper execution is covered by Linux CI")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is unavailable")
	}
	assertWrapperForwardsOpaquePaths(t, bash, []string{wrapperPath(t, "validate-yandex-cdn-release.sh")})
}

func TestPowerShellWrapperForwardsOpaquePathsAsSingleArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PowerShell wrapper execution with a POSIX fake Go is covered by Linux CI")
	}
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh is unavailable")
	}
	assertWrapperForwardsOpaquePaths(t, pwsh, []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-File", wrapperPath(t, "validate-yandex-cdn-release.ps1")})
}

func TestPowerShellWrapperSupportsWindowsPowerShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell compatibility is covered on Windows")
	}
	powershell := filepath.Join(os.Getenv("WINDIR"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(powershell); err != nil {
		t.Skip("Windows PowerShell is unavailable")
	}
	temp := t.TempDir()
	capture := filepath.Join(temp, "captured-arguments")
	fakeGo := filepath.Join(temp, "fake go.ps1")
	fake := "[IO.File]::WriteAllBytes($env:MAESTRO_CAPTURE, [Text.Encoding]::UTF8.GetBytes(($args -join [char]0)))\nexit 0\n"
	if err := os.WriteFile(fakeGo, []byte(fake), 0o600); err != nil {
		t.Fatal(err)
	}
	releasePath := filepath.Join(temp, "release candidate; false")
	trustPath := filepath.Join(temp, "trust file $(false).json")
	command := exec.Command(
		powershell,
		"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", wrapperPath(t, "validate-yandex-cdn-release.ps1"),
		"--release-dir", releasePath,
		"--evidence-trust", trustPath,
		"--go-binary", fakeGo,
	)
	command.Env = append(os.Environ(), "MAESTRO_CAPTURE="+capture)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Windows PowerShell wrapper failed: %v output=%q", err, output)
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	parts := bytes.Split(raw, []byte{0})
	got := make([]string, len(parts))
	for index := range parts {
		got[index] = string(parts[index])
	}
	want := []string{"run", "./cmd/maestro-release-validate", "--release-dir", releasePath, "--evidence-trust", trustPath}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("forwarded args=%q, want %q", got, want)
	}
}

func TestPowerShellWrapperRejectsPartiallyQualifiedWindowsPaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path qualification is covered on Windows")
	}
	powershell := filepath.Join(os.Getenv("WINDIR"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(powershell); err != nil {
		t.Skip("Windows PowerShell is unavailable")
	}
	temp := t.TempDir()
	fakeGo := filepath.Join(temp, "fake go.ps1")
	if err := os.WriteFile(fakeGo, []byte("exit 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	absoluteRelease := filepath.Join(temp, "release")
	absoluteTrust := filepath.Join(temp, "trust.json")
	tests := []struct {
		name         string
		releasePath  string
		evidencePath string
	}{
		{name: "drive relative release", releasePath: `C:relative\release`, evidencePath: absoluteTrust},
		{name: "root relative trust", releasePath: absoluteRelease, evidencePath: `\root-relative\trust.json`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command(
				powershell,
				"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
				"-File", wrapperPath(t, "validate-yandex-cdn-release.ps1"),
				"--release-dir", test.releasePath,
				"--evidence-trust", test.evidencePath,
				"--go-binary", fakeGo,
			)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("partially qualified path accepted: output=%q", output)
			}
			const expected = "release_validation_failed code=arguments_invalid\r\n"
			if string(output) != expected {
				t.Fatalf("output=%q, want %q", output, expected)
			}
		})
	}
}

func assertWrapperForwardsOpaquePaths(t *testing.T, shell string, prefix []string) {
	t.Helper()
	temp := t.TempDir()
	capture := filepath.Join(temp, "captured-arguments")
	fakeGo := filepath.Join(temp, "fake go")
	fake := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$MAESTRO_CAPTURE\"\n"
	if err := os.WriteFile(fakeGo, []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	releasePath := filepath.Join(temp, "release candidate; false")
	trustPath := filepath.Join(temp, "trust file $(false).json")
	args := append(append([]string(nil), prefix...),
		"--release-dir", releasePath,
		"--evidence-trust", trustPath,
		"--go-binary", fakeGo,
	)
	command := exec.Command(shell, args...)
	command.Env = append(os.Environ(), "MAESTRO_CAPTURE="+capture)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("wrapper failed: %v output=%q", err, output)
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	want := []string{"run", "./cmd/maestro-release-validate", "--release-dir", releasePath, "--evidence-trust", trustPath}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("forwarded args=%q, want %q", got, want)
	}
}

func wrapperPath(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "..", "ops", name))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func writeTaskDTrust(t *testing.T) (string, release.EvidenceTrust) {
	t.Helper()
	publicKey := make([]byte, ed25519.PublicKeySize)
	for index := range publicKey {
		publicKey[index] = byte(index + 1)
	}
	trust, err := release.NewEvidenceTrust([]release.EvidenceTrustKey{{
		KeyID: "task-d-test-key", PublicKey: publicKey, SourceOrigin: "https://evidence.example.test",
	}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := trust.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "evidence-trust.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, trust
}
