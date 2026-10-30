// Package shimexec implements the agentshim Client/Session interfaces for
// images that ship the exec-based agent shim contract (docs/shim.md): the
// control plane invokes well-known executables under /opt/claworc/shim over
// the instance's SSH connection. All transport goes through the small Runner
// interface so tests can drive the adapter against local scripts or fakes.
package shimexec

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
)

// Type is the adapter identifier for the exec-based shim adapter.
const Type = "shimexec"

// ShimDir is where the shim contract mandates verbs and identity files live
// inside the instance (docs/shim.md, "Image layout").
const ShimDir = "/opt/claworc/shim"

// SupportedContract is the shim contract version this adapter implements.
const SupportedContract = 1

// Shim contract exit codes (docs/shim.md, "Common conventions").
const (
	ExitOK          = 0
	ExitInternal    = 1
	ExitUsage       = 2
	ExitUnsupported = 3
	ExitNotReady    = 4
	ExitTimeout     = 5
	ExitValidation  = 6
)

// stderrTailCap bounds how much captured stderr is carried into error
// messages and synthesized chat error events.
const stderrTailCap = 2048

// ErrUnsupported reports a verb/capability the agent's shim does not
// implement (contract exit code 3).
var ErrUnsupported = errors.New("capability unsupported by agent shim")

// ErrBooting reports that the agent is not ready yet (contract exit code 4).
var ErrBooting = errors.New("agent not ready (booting)")

// ErrSessionClosed is returned by Session methods after Close.
var ErrSessionClosed = errors.New("shimexec: session closed")

// ValidationError carries the {"error":"..."} payload a verb printed on
// stdout when exiting with the validation-failure code 6.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return "validation failed: " + e.Message }

// Runner executes shim verbs on the instance. The production implementation
// runs over an established SSH connection (NewSSHRunner); tests substitute a
// LocalRunner (os/exec against a directory of scripts) or a scripted fake.
type Runner interface {
	// Run executes argv on the instance, wiring stdin/stdout/stderr, and
	// returns the process exit code. A non-nil error means the transport
	// itself failed (the exit code is then meaningless).
	Run(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) (int, error)
	// Start begins a streaming command (chat-send, chat-stream) and returns
	// a handle to its stdio. The command's lifetime is bound to ctx:
	// cancellation terminates it with SIGTERM semantics. A nil stdin makes
	// the command interactive: StreamHandle.Stdin then returns a writer
	// whose Close delivers EOF to the command.
	Start(ctx context.Context, argv []string, stdin io.Reader) (StreamHandle, error)
	// ReadFile reads a remote file (agent.txt / agent.svg identity files).
	ReadFile(ctx context.Context, path string) ([]byte, error)
}

// StreamHandle is a running streaming command started by Runner.Start.
type StreamHandle interface {
	// Stdin is the command's writable stdin when Start was called with a
	// nil stdin reader; nil otherwise.
	Stdin() io.WriteCloser
	// Stdout streams the command's stdout. It reaches EOF when the command
	// exits (or is terminated).
	Stdout() io.Reader
	// StderrTail returns the tail (up to ~2KB) of stderr captured so far.
	StderrTail() string
	// Terminate signals the command with SIGTERM semantics (best-effort)
	// and tears down its transport. Safe to call multiple times.
	Terminate() error
	// Wait blocks until the command exits and returns its exit code. A
	// non-nil error means the transport failed before an exit status was
	// observed.
	Wait() (int, error)
}

// verbPath returns the absolute path of a shim verb executable.
func verbPath(verb string) string { return ShimDir + "/" + verb }

// programPattern is the set of programs a Runner may exec: a shim verb under
// ShimDir, or cat (Runner.ReadFile). Arguments are always shell-quoted, so
// pinning the program closes the remaining command-injection surface.
var programPattern = regexp.MustCompile(`^(/opt/claworc/shim/[a-z][a-z0-9-]*|cat)$`)

// execScript is the one command every Runner execs (the SSH exec request
// and the local `sh -c`). It is a constant: argv never appears in a command
// string, so nothing a caller passes can be parsed by a shell. The argv
// travels on stdin instead (see frameArgv) — a decimal count line, then one
// base64 line per argument — and the script rebuilds "$@" from it and execs
// the program, leaving the rest of stdin to the verb. `read` consumes a pipe
// byte by byte, so it never swallows the verb's input. The trailing "x"
// keeps $(...) from stripping trailing newlines off an argument.
const execScript = `IFS= read -r n || exit 125
case "$n" in ''|*[!0-9]*) exit 125 ;; esac
set --
while [ "$n" -gt 0 ]; do
  IFS= read -r a || exit 125
  a=$(printf '%s' "$a" | base64 -d && printf x) || exit 125
  set -- "$@" "${a%x}"
  n=$((n - 1))
done
exec "$@"`

// frameArgv encodes argv as the stdin header execScript reads.
func frameArgv(argv []string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%d\n", len(argv))
	for _, a := range argv {
		b.WriteString(base64.StdEncoding.EncodeToString([]byte(a)))
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// framedStdin prefixes the caller's stdin (which may be nil) with the argv
// header.
func framedStdin(argv []string, stdin io.Reader) io.Reader {
	header := bytes.NewReader(frameArgv(argv))
	if stdin == nil {
		return header
	}
	return io.MultiReader(header, stdin)
}

// checkProgram rejects an argv whose program isn't a shim verb or cat.
func checkProgram(argv []string) error {
	if len(argv) == 0 {
		return errors.New("shimexec: empty argv")
	}
	if !programPattern.MatchString(argv[0]) {
		return fmt.Errorf("shimexec: refusing to exec %q: not a shim verb", argv[0])
	}
	return nil
}

// mapExit translates a shim verb's exit code into a Go error per the
// contract's exit-code table.
func mapExit(verb string, code int, stdout []byte, stderrTail string) error {
	switch code {
	case ExitOK:
		return nil
	case ExitUnsupported:
		return fmt.Errorf("%s: %w", verb, ErrUnsupported)
	case ExitNotReady:
		return fmt.Errorf("%s: %w", verb, ErrBooting)
	case ExitTimeout:
		return fmt.Errorf("%s: timed out waiting on the agent: %s", verb, tailDetail(stdout, stderrTail))
	case ExitValidation:
		return &ValidationError{Message: validationMessage(stdout, stderrTail)}
	default:
		return fmt.Errorf("%s: exit %d: %s", verb, code, tailDetail(stdout, stderrTail))
	}
}

// validationMessage extracts the message from the {"error":"..."} document a
// verb prints on stdout when exiting 6, falling back to raw output.
func validationMessage(stdout []byte, stderrTail string) string {
	var doc struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(stdout, &doc); err == nil && doc.Error != "" {
		return doc.Error
	}
	if msg := strings.TrimSpace(string(stdout)); msg != "" {
		return capString(msg, stderrTailCap)
	}
	if msg := strings.TrimSpace(stderrTail); msg != "" {
		return msg
	}
	return "invalid payload"
}

// tailDetail picks the most useful diagnostic text for a generic failure:
// the stderr tail when present, else a capped stdout excerpt.
func tailDetail(stdout []byte, stderrTail string) string {
	if d := strings.TrimSpace(stderrTail); d != "" {
		return d
	}
	return capString(strings.TrimSpace(string(stdout)), stderrTailCap)
}

func capString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}

// tailBuffer is an io.Writer keeping only the last max bytes written.
// Safe for concurrent use.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func newTailBuffer(max int) *tailBuffer { return &tailBuffer{max: max} }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.max:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
