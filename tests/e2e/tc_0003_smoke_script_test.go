package e2e

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestTC0003SmokeScriptExecutable(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("failed to get current file path")
	}

	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	scriptPath := filepath.Join(repoRoot, "scripts", "test", "test.sh")

	cmd := exec.Command("bash", scriptPath)
	cmd.Dir = repoRoot
	cmd.Env = append(cmd.Environ(), "COIN_SKIP_E2E_SMOKE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test script failed: %v, output: %s", err, string(out))
	}
}
