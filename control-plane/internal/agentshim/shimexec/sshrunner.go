package shimexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	gossh "golang.org/x/crypto/ssh"
)

// SSHRunner is the production Runner: each Run/Start opens one exec channel
// on an established SSH connection resolved lazily per call (connections are
// managed and re-established by sshproxy; resolving late means a reconnect
// between verbs is picked up transparently).
//
// Verbs run as the SSH user (root — SSH is the authentication boundary,
// exactly like the terminal and file browser; shims themselves drop to the
// claworc user, per docs/shim.md).
type SSHRunner struct {
	resolve func(ctx context.Context) (*gossh.Client, error)
}

var _ Runner = (*SSHRunner)(nil)

// NewSSHRunner builds a Runner over SSH connections resolved by the given
// function (typically a closure over the sshproxy connection manager).
func NewSSHRunner(resolve func(ctx context.Context) (*gossh.Client, error)) *SSHRunner {
	return &SSHRunner{resolve: resolve}
}

// shellJoin quotes each argv element and joins them into one command line
// for the remote shell.
func shellJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = sshproxy.ShellQuote(a)
	}
	return strings.Join(parts, " ")
}

// newSSHSession resolves a connection and opens one exec session on it.
// Failures at this layer are transport failures.
func (r *SSHRunner) newSSHSession(ctx context.Context) (*gossh.Session, error) {
	if r.resolve == nil {
		return nil, &agentshim.TransportError{Err: errors.New("shimexec: no SSH client resolver")}
	}
	client, err := r.resolve(ctx)
	if err != nil {
		return nil, &agentshim.TransportError{Err: err}
	}
	sess, err := client.NewSession()
	if err != nil {
		return nil, &agentshim.TransportError{Err: err}
	}
	return sess, nil
}

// mapWaitErr translates ssh Session.Wait errors into (exit code, transport
// error). A missing exit status (channel torn down, e.g. by Terminate) maps
// to code -1 with no transport error so callers treat it as an abnormal exit
// rather than an unreachable instance.
func mapWaitErr(werr error) (int, error) {
	if werr == nil {
		return 0, nil
	}
	var exitErr *gossh.ExitError
	if errors.As(werr, &exitErr) {
		return exitErr.ExitStatus(), nil
	}
	var missing *gossh.ExitMissingError
	if errors.As(werr, &missing) {
		return -1, nil
	}
	return -1, &agentshim.TransportError{Err: werr}
}

// Run implements Runner over one SSH exec channel.
func (r *SSHRunner) Run(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	sess, err := r.newSSHSession(ctx)
	if err != nil {
		return -1, err
	}
	defer sess.Close()

	sess.Stdin = stdin
	sess.Stdout = stdout
	sess.Stderr = stderr

	if err := sess.Start(shellJoin(argv)); err != nil {
		return -1, &agentshim.TransportError{Err: err}
	}

	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()

	select {
	case <-ctx.Done():
		// Best-effort SIGTERM (not all sshds honor signal requests), then
		// tear the channel down — the contract treats channel teardown as
		// abort.
		_ = sess.Signal(gossh.SIGTERM)
		_ = sess.Close()
		<-done
		return -1, ctx.Err()
	case werr := <-done:
		return mapWaitErr(werr)
	}
}

// Start implements Runner: it begins a streaming exec (chat-send) whose
// lifetime is bound to ctx.
func (r *SSHRunner) Start(ctx context.Context, argv []string, stdin io.Reader) (StreamHandle, error) {
	sess, err := r.newSSHSession(ctx)
	if err != nil {
		return nil, err
	}

	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		return nil, &agentshim.TransportError{Err: err}
	}
	tail := newTailBuffer(stderrTailCap)
	sess.Stderr = tail
	sess.Stdin = stdin

	if err := sess.Start(shellJoin(argv)); err != nil {
		sess.Close()
		return nil, &agentshim.TransportError{Err: err}
	}

	h := &sshStream{sess: sess, stdout: stdout, tail: tail, done: make(chan struct{})}
	go func() {
		werr := sess.Wait()
		h.mu.Lock()
		h.code, h.err = mapWaitErr(werr)
		h.mu.Unlock()
		close(h.done)
		sess.Close()
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

// ReadFile implements Runner by cat-ing the remote path over an exec
// channel (same transport sshproxy's file helpers use).
func (r *SSHRunner) ReadFile(ctx context.Context, path string) ([]byte, error) {
	var out bytes.Buffer
	tail := newTailBuffer(stderrTailCap)
	code, err := r.Run(ctx, []string{"cat", path}, nil, &out, tail)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("read %s: exit %d: %s", path, code, strings.TrimSpace(tail.String()))
	}
	return out.Bytes(), nil
}

// sshStream is the StreamHandle for one streaming SSH exec.
type sshStream struct {
	sess   *gossh.Session
	stdout io.Reader
	tail   *tailBuffer

	done chan struct{}

	mu   sync.Mutex
	code int
	err  error

	termOnce sync.Once
}

func (h *sshStream) Stdout() io.Reader  { return h.stdout }
func (h *sshStream) StderrTail() string { return h.tail.String() }

// Terminate sends a best-effort SIGTERM and tears the exec channel down.
// Channel teardown is the contract's documented abort path ("on SIGTERM (or
// when the SSH channel is torn down), the shim SHOULD abort").
func (h *sshStream) Terminate() error {
	h.termOnce.Do(func() {
		_ = h.sess.Signal(gossh.SIGTERM)
		_ = h.sess.Close()
	})
	return nil
}

func (h *sshStream) Wait() (int, error) {
	<-h.done
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.code, h.err
}
