package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteXrayPIDFileReplacesRestartIdentity(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "xray.pid")
	for _, pid := range []string{"123", "456"} {
		if err := writeXrayPIDFile(pidFile, pid); err != nil {
			t.Fatalf("writeXrayPIDFile(%s): %v", pid, err)
		}
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if string(raw) != pid+"\n" {
			t.Fatalf("PID file = %q, want %q", raw, pid+"\n")
		}
	}
	info, err := os.Stat(pidFile)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
		t.Fatalf("PID file mode = %o, want 644", info.Mode().Perm())
	}
}

func TestWriteXrayPIDFileRejectsUnsafeInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xray.pid")
	for _, pid := range []string{"", "0", "-1", "12 3", strings.Repeat("1", 32)} {
		if err := writeXrayPIDFile(path, pid); err == nil {
			t.Fatalf("unsafe PID %q accepted", pid)
		}
	}
}

func TestCommercialProfileAllowsOnlyDocumentedAPIAndPIDPaths(t *testing.T) {
	t.Setenv("MAESTRO_RELEASE_ID", "release-test")
	t.Setenv("MAESTRO_CONFIG_DIGEST", strings.Repeat("0", 64))
	t.Setenv("MAESTRO_XRAY_API_ADDRESS", "127.0.0.1:28082")
	t.Setenv("MAESTRO_XRAY_PID_FILE", "/run/maestro-xray-cdn-commercial-pid/xray.pid")
	configuration, err := loadConfiguration()
	if err != nil {
		t.Fatalf("loadConfiguration: %v", err)
	}
	if configuration.xrayAPIAddress != "127.0.0.1:28082" || configuration.xrayPIDFile != "/run/maestro-xray-cdn-commercial-pid/xray.pid" {
		t.Fatalf("commercial profile = %#v", configuration)
	}

	t.Setenv("MAESTRO_XRAY_API_ADDRESS", "127.0.0.1:29999")
	if _, err := loadConfiguration(); err == nil {
		t.Fatal("undocumented Xray API address accepted")
	}
}
