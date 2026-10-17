//go:build unix

package rmlines

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A fake worker for client tests that need no library: when the test binary
// is started as "render-worker" with fakeWorkerEnv set, it speaks the protocol
// and answers every request with a render error, except that the first
// request for a path ending in "hang.rm" (across all fake workers sharing the
// directory in fakeWorkerEnv) blocks until the worker is killed. Each fake
// worker records its PID in that directory.
const fakeWorkerEnv = "RMLINES_FAKE_WORKER_DIR"

func init() {
	dir := os.Getenv(fakeWorkerEnv)
	if dir == "" || len(os.Args) < 2 || os.Args[1] != WorkerArg {
		return
	}
	os.Exit(fakeWorker(dir))
}

func fakeWorker(dir string) int {
	pid := strconv.Itoa(os.Getpid())
	if err := os.WriteFile(filepath.Join(dir, pid+".pid"), nil, 0o600); err != nil {
		return 1
	}
	if writeJSONFrame(os.Stdout, helloMsg{Hello: helloMagic, Version: protocolVersion}) != nil {
		return 1
	}
	for {
		var req request
		if err := readJSONFrame(os.Stdin, &req); err != nil {
			return 0
		}
		if strings.HasSuffix(req.Path, "hang.rm") {
			f, err := os.OpenFile(filepath.Join(dir, "hung"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err == nil {
				f.Close()
				time.Sleep(time.Hour) // until killed
			}
		}
		if writeJSONFrame(os.Stdout, response{Error: "fake worker"}) != nil {
			return 1
		}
	}
}

// Close during a render on a reused worker: the request's retry must not
// spawn a worker that outlives the client.
func TestCloseDuringRender(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Setenv(fakeWorkerEnv, dir)
	c, err := newClient(Config{Workers: 1, WorkerPath: exe})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	opts := Options{Timeout: time.Minute}
	ctx := context.Background()

	// First request: the worker answers, so the next request reuses it.
	if _, err := c.RenderFile(ctx, filepath.Join(dir, "ok.rm"), opts); !errors.Is(err, ErrRender) {
		t.Fatalf("first render: %v, want ErrRender", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := c.RenderFile(ctx, filepath.Join(dir, "hang.rm"), opts)
		done <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "hung")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the worker never received the request")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Errorf("render after Close: %v, want ErrClosed", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("render did not return after Close")
	}

	if pids := c.workerPIDs(); len(pids) != 0 {
		t.Errorf("workers %v still attached after Close", pids)
	}
	pidFiles, _ := filepath.Glob(filepath.Join(dir, "*.pid"))
	if len(pidFiles) == 0 {
		t.Fatal("no worker recorded its PID")
	}
	for _, f := range pidFiles {
		pid, _ := strconv.Atoi(strings.TrimSuffix(filepath.Base(f), ".pid"))
		if syscall.Kill(pid, 0) == nil {
			t.Errorf("worker %d outlived Close", pid)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}
