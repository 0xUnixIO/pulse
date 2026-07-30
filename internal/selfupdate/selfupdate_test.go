package selfupdate

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRestartOpenRCDetached(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("OpenRC is only supported on Unix systems")
	}

	tmp := t.TempDir()
	marker := filepath.Join(tmp, "called")
	rcService := filepath.Join(tmp, "rc-service")
	script := "#!/bin/sh\nsleep 0.1\nprintf '%s' \"$*\" > \"$PULSE_TEST_MARKER\"\n"
	if err := os.WriteFile(rcService, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake rc-service: %v", err)
	}
	t.Setenv("PULSE_TEST_MARKER", marker)

	started := time.Now()
	if err := restartOpenRC(rcService, "pulse-node"); err != nil {
		t.Fatalf("restartOpenRC: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 100*time.Millisecond {
		t.Fatalf("restartOpenRC blocked for %v", elapsed)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(marker)
		if err == nil {
			if got := strings.TrimSpace(string(body)); got != "pulse-node restart" {
				t.Fatalf("rc-service args = %q", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("detached rc-service command was not executed")
}
