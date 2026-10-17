package rmlines

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	handshakeTimeout = 10 * time.Second
	stderrHeadLines  = 3 // a crash dump starts with the signal and the reason
	stderrTailLines  = 5
)

// client is the Renderer used by the server. It owns a fixed number of worker
// slots; each slot runs at most one request at a time and lazily (re)spawns
// its worker process.
type client struct {
	cfg   Config
	slots chan *slot
	all   []*slot

	mu     sync.Mutex
	closed bool
}

type slot struct {
	c    *client
	mu   sync.Mutex
	proc *proc
	busy bool
	idle *time.Timer
}

type proc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	done   chan struct{} // closed after cmd.Wait returns
	err    error         // cmd.Wait result, valid after done

	tailMu sync.Mutex
	head   []string // first lines written to stderr
	tail   []string // last lines written to stderr after head
}

func newClient(cfg Config) (*client, error) {
	if cfg.Workers <= 0 {
		cfg.Workers = DefaultWorkers
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = DefaultIdleTimeout
	}
	if cfg.MaxPixels <= 0 {
		cfg.MaxPixels = DefaultMaxPixels
	}
	if cfg.MaxOutputPixels <= 0 {
		cfg.MaxOutputPixels = DefaultMaxOutputPixels
	}
	if cfg.WorkerPath == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("rmlines: locate worker executable: %w", err)
		}
		cfg.WorkerPath = exe
	}
	c := &client{cfg: cfg, slots: make(chan *slot, cfg.Workers)}
	for i := 0; i < cfg.Workers; i++ {
		s := &slot{c: c}
		c.all = append(c.all, s)
		c.slots <- s
	}
	return c, nil
}

func (c *client) RenderFile(ctx context.Context, rmPath string, opts Options) (*Result, error) {
	return c.render(ctx, rmPath, opts, "")
}

func (c *client) render(ctx context.Context, rmPath string, opts Options, debug string) (*Result, error) {
	wo, err := toWire(opts, c.cfg.MaxPixels, c.cfg.MaxOutputPixels).normalize()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRender, err)
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if rmPath == "" {
		return nil, fmt.Errorf("%w: empty path", ErrRender)
	}
	if abs, err := filepath.Abs(rmPath); err == nil {
		rmPath = abs
	}
	if c.isClosed() {
		return nil, ErrClosed
	}

	var s *slot
	select {
	case s = <-c.slots:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer c.release(s)
	if c.isClosed() {
		return nil, ErrClosed
	}

	req := request{Path: rmPath, Options: wo, Debug: debug}
	res, err, retry := c.attempt(ctx, s, req, timeout)
	if retry {
		// A worker that served earlier requests died before answering. It
		// may have died while idle (killed, OOM) rather than because of this
		// page, so give the page one fresh worker.
		res, err, _ = c.attempt(ctx, s, req, timeout)
	}
	if err != nil && !errors.Is(err, ErrClosed) && c.isClosed() {
		err = fmt.Errorf("%w: %w", ErrClosed, err) // Close killed the worker
	}
	return res, err
}

// attempt runs one request on the slot's worker. retry reports a crash of a
// reused worker.
func (c *client) attempt(ctx context.Context, s *slot, req request, timeout time.Duration) (_ *Result, _ error, retry bool) {
	p, reused, err := s.ensureProc()
	if err != nil {
		return nil, err, false
	}
	if err := p.send(req); err != nil {
		s.kill()
		return nil, fmt.Errorf("%w: %v", ErrWorkerCrashed, err), reused
	}

	type outcome struct {
		res *Result
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		res, err := p.receive(req, req.Options.MaxOutPixels)
		ch <- outcome{res, err}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case o := <-ch:
		if o.err == nil {
			return o.res, nil, false
		}
		var re *renderError
		if errors.As(o.err, &re) {
			return nil, o.err, false // worker is fine
		}
		desc := p.describeExit(500 * time.Millisecond) // let a crashing worker finish dying for its exit status
		s.kill()
		if desc == "" {
			desc = p.describeExit(0)
		}
		return nil, fmt.Errorf("%w: %v%s", ErrWorkerCrashed, o.err, desc), reused
	case <-timer.C:
		s.kill()
		<-ch
		return nil, fmt.Errorf("%w after %v: %s", ErrTimeout, timeout, req.Path), false
	case <-ctx.Done():
		s.kill()
		<-ch
		return nil, ctx.Err(), false
	}
}

// renderError is a clean failure reported by a healthy worker.
type renderError struct{ msg, log string }

func (e *renderError) Error() string {
	if e.log == "" {
		return ErrRender.Error() + ": " + e.msg
	}
	return ErrRender.Error() + ": " + e.msg + "; library log: " + e.log
}
func (e *renderError) Unwrap() error { return ErrRender }

func (p *proc) send(req request) error {
	select {
	case <-p.done:
		return errors.New("worker exited")
	default:
	}
	if err := writeJSONFrame(p.stdin, req); err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	return nil
}

// receive reads the response to req. maxPixels bounds the size of each image
// the worker may send.
func (p *proc) receive(req request, maxPixels int) (*Result, error) {
	var resp response
	if err := readJSONFrame(p.stdout, &resp); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.Log != "" {
		logrus.WithField("component", "rmlines").Debugf("%s: library log: %s", req.Path, resp.Log)
	}
	if resp.Error != "" {
		return nil, &renderError{msg: resp.Error, log: resp.Log}
	}
	// InkW*InkH may overflow: divide instead.
	if resp.InkW <= 0 || resp.InkH <= 0 || resp.InkW > maxPixels || resp.InkH > maxPixels/resp.InkW {
		return nil, fmt.Errorf("bad image size %dx%d", resp.InkW, resp.InkH)
	}
	n := resp.InkW * resp.InkH * 4
	res := &Result{PageW: resp.PageW, PageH: resp.PageH, Extent: resp.Extent}
	ink, err := readFrame(p.stdout, n, nil)
	if err != nil || len(ink) != n {
		return nil, fmt.Errorf("read ink pixels: %v (got %d of %d bytes)", err, len(ink), n)
	}
	res.Ink = &image.NRGBA{Pix: ink, Stride: resp.InkW * 4, Rect: image.Rect(0, 0, resp.InkW, resp.InkH)}
	hl, err := readFrame(p.stdout, n, nil)
	if err != nil {
		return nil, fmt.Errorf("read highlight pixels: %w", err)
	}
	switch {
	case resp.HasHighlights && len(hl) == n:
		res.Highlights = &image.NRGBA{Pix: hl, Stride: resp.InkW * 4, Rect: res.Ink.Rect}
	case !resp.HasHighlights && len(hl) == 0:
	default:
		return nil, fmt.Errorf("highlight frame of %d bytes, want %d", len(hl), n)
	}
	logrus.WithField("component", "rmlines").Debugf("%s rendered in %.1f ms (%dx%d)", req.Path, resp.Millis, resp.InkW, resp.InkH)
	return res, nil
}

func (c *client) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *client) release(s *slot) {
	s.mu.Lock()
	s.busy = false
	if s.proc != nil && !c.isClosed() {
		if s.idle == nil {
			s.idle = time.AfterFunc(c.cfg.IdleTimeout, s.reapIdle)
		} else {
			s.idle.Reset(c.cfg.IdleTimeout)
		}
	}
	s.mu.Unlock()
	c.slots <- s
}

func (s *slot) reapIdle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.busy && s.proc != nil {
		s.proc.stop()
		s.proc = nil
	}
}

// ensureProc marks the slot busy and returns a live worker, spawning one if
// needed. reused reports whether the worker served an earlier request. After
// Close it returns ErrClosed: Close stops the workers it finds in the slots,
// so a worker spawned later (by the retry of a request whose worker Close
// killed) would outlive the client. The check is made under s.mu, which
// Close takes for every slot after setting closed.
func (s *slot) ensureProc() (p *proc, reused bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.c.isClosed() {
		return nil, false, ErrClosed
	}
	s.busy = true
	if s.idle != nil {
		s.idle.Stop()
	}
	if s.proc != nil {
		select {
		case <-s.proc.done:
			s.proc = nil // died while idle
		default:
			return s.proc, true, nil
		}
	}
	p, err = spawn(s.c.cfg.WorkerPath)
	if err != nil {
		return nil, false, err
	}
	s.proc = p
	return p, false, nil
}

func (s *slot) kill() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.proc != nil {
		s.proc.stop()
		s.proc = nil
	}
}

func spawn(path string) (*proc, error) {
	cmd := exec.Command(path, WorkerArg)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: start %s: %v", ErrWorkerCrashed, path, err)
	}
	p := &proc{cmd: cmd, stdin: stdin, stdout: bufio.NewReaderSize(stdout, 1<<20), done: make(chan struct{})}
	stderrDone := make(chan struct{})
	go p.pumpStderr(stderr, stderrDone)
	go func() {
		<-stderrDone // Wait closes the pipes; drain stderr first
		p.err = cmd.Wait()
		close(p.done)
	}()

	hello := make(chan error, 1)
	go func() {
		var h helloMsg
		err := readJSONFrame(p.stdout, &h)
		if err == nil && (h.Hello != helloMagic || h.Version != protocolVersion) {
			err = fmt.Errorf("unexpected hello %+v", h)
		}
		hello <- err
	}()
	select {
	case err = <-hello:
	case <-time.After(handshakeTimeout):
		err = errors.New("no hello within " + handshakeTimeout.String())
	}
	if err != nil {
		p.stop()
		return nil, fmt.Errorf("%w: %s %s did not start as a render worker: %v%s", ErrWorkerCrashed, path, WorkerArg, err, p.describeExit(0))
	}
	return p, nil
}

func (p *proc) pumpStderr(r io.Reader, done chan struct{}) {
	defer close(done)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	log := logrus.WithFields(logrus.Fields{"component": "rmlines-worker", "pid": p.cmd.Process.Pid})
	for sc.Scan() {
		line := sc.Text()
		log.Warn(line)
		p.tailMu.Lock()
		if len(p.head) < stderrHeadLines {
			p.head = append(p.head, line)
		} else {
			p.tail = append(p.tail, line)
			if len(p.tail) > stderrTailLines {
				p.tail = p.tail[1:]
			}
		}
		p.tailMu.Unlock()
	}
	_, _ = io.Copy(io.Discard, r)
}

// stop kills the process and waits for it to be reaped.
func (p *proc) stop() {
	_ = p.cmd.Process.Kill()
	_ = p.stdin.Close()
	<-p.done
}

// describeExit returns the exit status and the stderr tail once the process
// has exited, waiting at most wait for that; "" if it is still running.
func (p *proc) describeExit(wait time.Duration) string {
	select {
	case <-p.done:
	default:
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-p.done:
		case <-t.C:
			return ""
		}
	}
	var b strings.Builder
	if p.err != nil {
		fmt.Fprintf(&b, " (worker exit: %v)", p.err)
	}
	p.tailMu.Lock()
	if len(p.head) > 0 {
		fmt.Fprintf(&b, " stderr: %s", strings.Join(p.head, " | "))
		if len(p.tail) > 0 {
			fmt.Fprintf(&b, " ... %s", strings.Join(p.tail, " | "))
		}
	}
	p.tailMu.Unlock()
	return b.String()
}

func (c *client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	for _, s := range c.all {
		s.mu.Lock()
		if s.idle != nil {
			s.idle.Stop()
		}
		if s.proc != nil {
			s.proc.stop()
			s.proc = nil
		}
		s.mu.Unlock()
	}
	return nil
}

// workerPIDs returns the PIDs of the running workers (tests).
func (c *client) workerPIDs() []int {
	var pids []int
	for _, s := range c.all {
		s.mu.Lock()
		if s.proc != nil {
			pids = append(pids, s.proc.cmd.Process.Pid)
		}
		s.mu.Unlock()
	}
	return pids
}
