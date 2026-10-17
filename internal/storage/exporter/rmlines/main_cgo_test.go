//go:build cgo && rmlines

package rmlines

import (
	"os"
	"testing"
)

// The test binary doubles as the render worker: the client spawns
// os.Executable() with "render-worker".
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == WorkerArg {
		os.Exit(WorkerMain(os.Stdin, os.Stdout, os.Stderr))
	}
	os.Setenv(debugEnv, "1") // inherited by the workers: enables the debug ops
	os.Exit(m.Run())
}
