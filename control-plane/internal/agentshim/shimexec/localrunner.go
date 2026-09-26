package shimexec

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// LocalRunner implements Runner via os/exec against a directory of shim
// scripts on the local filesystem. It exists for the conformance harness:
// the same adapter code that drives a remote shim over SSH is exercised
// against real scripts (the package testdata set and agent/template/shim)
// without needing a container or SSH server. Verb paths under ShimDir are
// remapped into Dir; identity files are read from Dir too.
type LocalRunner struct {
	// Dir is the local directory containing the shim verbs.
	Dir string
	// Env is extra KEY=VALUE entries appended to the inherited environment
	// (e.g. CLAWORC_SHIM_ENV_FILE overrides for the template scripts).
	Env []string
}

var _ Runner = (*LocalRunner)(nil)

// mapPath rewrites contract paths (/opt/claworc/shim/...) into Dir.
func (r *LocalRunner) mapPath(p string) string {
	if rest, ok := strings.CutPrefix(p, ShimDir+"/"); ok {
		return filepath.Join(r.Dir, rest)
	}
	if !filepath.IsAbs(p) {
		return filepath.Join(r.Dir, p)
	}
	return p
}

func (r *LocalRunner) command(argv []string) (*exec.Cmd, error) {
	if len(argv) == 0 {
		return nil, errors.New("shimexec: empty argv")
	}
	cmd := exec.Command(r.mapPath(argv[0]), argv[1:]...)
	cmd.Env = append(os.Environ(), r.Env...)
	return cmd, nil
}

// exitCodeOf maps an os/exec Wait error to (exit code, transport error).
func exitCodeOf(werr error) (int, error) {
	if werr == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(werr, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return -1, werr
}

// Run implements Runner.
func (r *LocalRunner) Run(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	cmd, err := r.command(argv)
	if err != nil {
		return -1, err
	}
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	// Bind to ctx manually: cancellation delivers SIGTERM (the contract's
	// abort signal) with a SIGKILL escalation, which exec.CommandContext's
	// default hard-kill would not provide.
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-ctx.Done():
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		return -1, ctx.Err()
	case werr := <-done:
		return exitCodeOf(werr)
	}
}

// Start implements Runner: it launches a streaming command whose lifetime is
// bound to ctx. Stdout is bridged through an io.Pipe (closed only after Wait
// returns) so readers always observe EOF exactly at process exit.
func (r *LocalRunner) Start(ctx context.Context, argv []string, stdin io.Reader) (StreamHandle, error) {
	cmd, err := r.command(argv)
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	tail := newTailBuffer(stderrTailCap)
	cmd.Stdin = stdin
	cmd.Stdout = pw
	cmd.Stderr = tail

	if err := cmd.Start(); err != nil {
		pw.Close()
		return nil, err
	}
	h := &localStream{cmd: cmd, pr: pr, tail: tail, done: make(chan struct{})}
	go func() {
		werr := cmd.Wait()
		h.mu.Lock()
		h.code, h.err = exitCodeOf(werr)
		h.mu.Unlock()
		pw.Close()
		close(h.done)
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = h.Terminate()
		case <-h.done:
		}
	}()
	return h, nil
}

// ReadFile implements Runner from the local filesystem.
func (r *LocalRunner) ReadFile(_ context.Context, path string) ([]byte, error) {
	return os.ReadFile(r.mapPath(path))
}

// localStream is the StreamHandle for one local streaming command.
type localStream struct {
	cmd  *exec.Cmd
	pr   *io.PipeReader
	tail *tailBuffer

	done chan struct{}

	mu   sync.Mutex
	code int
	err  error

	termOnce sync.Once
}

func (h *localStream) Stdout() io.Reader  { return h.pr }
func (h *localStream) StderrTail() string { return h.tail.String() }

// Terminate delivers SIGTERM (the contract's abort signal) and escalates to
// SIGKILL if the process is still alive shortly after.
func (h *localStream) Terminate() error {
	h.termOnce.Do(func() {
		if h.cmd.Process != nil {
			_ = h.cmd.Process.Signal(syscall.SIGTERM)
		}
		go func() {
			select {
			case <-h.done:
			case <-time.After(3 * time.Second):
				if h.cmd.Process != nil {
					_ = h.cmd.Process.Kill()
				}
			}
		}()
	})
	return nil
}

func (h *localStream) Wait() (int, error) {
	<-h.done
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.code, h.err
}
