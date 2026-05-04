package llmgateway

import (
	"bytes"
	"io"
)

// codexEventRewriter is a line-aware io.Reader that rewrites SSE event names
// emitted by ChatGPT's /codex/responses endpoint into the names pi-ai's
// openai-responses parser expects.
//
// Specifically: `event: response.done` → `event: response.completed`. Without
// this rewrite, pi-ai's processResponsesStream silently drops the terminal
// usage/stop-reason block because it only listens for `response.completed`.
//
// The rewriter is line-buffered: it accumulates bytes from the underlying
// reader until it sees a `\n`, rewrites the complete line if needed, and only
// then emits it downstream. This makes the rewrite robust to arbitrary chunk
// boundaries from the upstream HTTP body.
type codexEventRewriter struct {
	src io.Reader
	// buf holds bytes already read from src that have not yet been delivered to p.
	// Two regions: [pendingLine] (accumulating, no newline yet) followed by
	// [readyOut] (fully formed lines waiting to be delivered). We keep them in
	// one buffer with an offset for simplicity.
	out     bytes.Buffer // bytes ready to be delivered
	pending []byte       // partial line buffered while waiting for newline
	srcEOF  bool
	srcErr  error
}

func newCodexEventRewriter(r io.Reader) io.Reader {
	return &codexEventRewriter{src: r}
}

func (c *codexEventRewriter) Read(p []byte) (int, error) {
	for c.out.Len() == 0 {
		if c.srcEOF {
			// Flush any leftover partial line (no trailing newline).
			if len(c.pending) > 0 {
				c.out.Write(rewriteCodexLine(c.pending))
				c.pending = nil
				break
			}
			if c.srcErr != nil && c.srcErr != io.EOF {
				return 0, c.srcErr
			}
			return 0, io.EOF
		}
		buf := make([]byte, 4096)
		n, err := c.src.Read(buf)
		if n > 0 {
			c.consume(buf[:n])
		}
		if err != nil {
			c.srcEOF = true
			c.srcErr = err
		}
	}
	return c.out.Read(p)
}

// consume appends chunk to the pending line buffer and flushes any complete
// (newline-terminated) lines into the out buffer with rewriting applied.
func (c *codexEventRewriter) consume(chunk []byte) {
	c.pending = append(c.pending, chunk...)
	for {
		idx := bytes.IndexByte(c.pending, '\n')
		if idx < 0 {
			return
		}
		line := c.pending[:idx+1]
		c.out.Write(rewriteCodexLine(line))
		c.pending = c.pending[idx+1:]
	}
}

// rewriteCodexLine rewrites a single SSE line. It preserves the original line
// terminator (`\n` or `\r\n`) and any leading/trailing whitespace inside the
// value portion is treated literally — SSE line is `event: <name>`.
func rewriteCodexLine(line []byte) []byte {
	// Strip terminator for comparison; remember it to re-attach.
	body := line
	terminator := []byte{}
	if n := len(body); n > 0 && body[n-1] == '\n' {
		if n >= 2 && body[n-2] == '\r' {
			terminator = []byte{'\r', '\n'}
			body = body[:n-2]
		} else {
			terminator = []byte{'\n'}
			body = body[:n-1]
		}
	}
	const prefix = "event:"
	if !bytes.HasPrefix(body, []byte(prefix)) {
		return line
	}
	value := bytes.TrimSpace(body[len(prefix):])
	if !bytes.Equal(value, []byte("response.done")) {
		return line
	}
	out := make([]byte, 0, len("event: response.completed")+len(terminator))
	out = append(out, "event: response.completed"...)
	out = append(out, terminator...)
	return out
}

// wrapResponseStream returns r wrapped in any apiType-specific stream rewriter,
// or r unchanged when no rewriting is needed. Callers should use this only for
// streaming responses; the wrapper is a passthrough for non-streaming bodies
// but adds a small per-Read allocation cost so it's better avoided.
func wrapResponseStream(apiType string, r io.Reader) io.Reader {
	if apiType == APITypeOpenAICodexResponses {
		return newCodexEventRewriter(r)
	}
	return r
}
