package importer

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestShadowVerifierContract(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shadow verifier contract runs in the Linux GitHub job")
	}
	script := filepath.Join("..", "..", "..", "ops", "ha", "test-shadow-verify.sh")
	command := exec.Command("bash", script)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("shadow verifier contract: %v\n%s", err, output)
	}
}
