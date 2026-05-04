package llmgateway

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func readAll(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(b)
}

func TestCodexRewriter_RenamesResponseDone(t *testing.T) {
	in := "event: response.done\ndata: {\"foo\":1}\n\n"
	out := readAll(t, newCodexEventRewriter(strings.NewReader(in)))
	want := "event: response.completed\ndata: {\"foo\":1}\n\n"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestCodexRewriter_LeavesOtherEventsUnchanged(t *testing.T) {
	in := "event: response.created\ndata: {}\n\nevent: response.output_item.added\ndata: {\"x\":2}\n\n"
	out := readAll(t, newCodexEventRewriter(strings.NewReader(in)))
	if out != in {
		t.Fatalf("got %q, want %q", out, in)
	}
}

func TestCodexRewriter_HandlesCRLFLineEndings(t *testing.T) {
	in := "event: response.done\r\ndata: {}\r\n\r\n"
	out := readAll(t, newCodexEventRewriter(strings.NewReader(in)))
	want := "event: response.completed\r\ndata: {}\r\n\r\n"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestCodexRewriter_MultipleEventsInOneRead(t *testing.T) {
	in := "event: response.created\ndata: {}\n\nevent: response.done\ndata: {\"u\":1}\n\n"
	out := readAll(t, newCodexEventRewriter(strings.NewReader(in)))
	want := "event: response.created\ndata: {}\n\nevent: response.completed\ndata: {\"u\":1}\n\n"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

// chunkedReader yields its source one byte at a time, simulating worst-case
// network framing where the rewriter must stitch together bytes spanning many
// Read calls before it sees a newline.
type chunkedReader struct {
	src []byte
	i   int
}

func (c *chunkedReader) Read(p []byte) (int, error) {
	if c.i >= len(c.src) {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = c.src[c.i]
	c.i++
	return 1, nil
}

func TestCodexRewriter_HandlesByteAtATime(t *testing.T) {
	in := "event: response.done\ndata: {\"a\":\"b\"}\n\n"
	r := newCodexEventRewriter(&chunkedReader{src: []byte(in)})
	out := readAll(t, r)
	want := "event: response.completed\ndata: {\"a\":\"b\"}\n\n"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestCodexRewriter_PartialLineAtEOF(t *testing.T) {
	// Last line has no trailing newline — rewriter must still flush it.
	in := "event: response.created\ndata: {}\n\nevent: response.done"
	out := readAll(t, newCodexEventRewriter(strings.NewReader(in)))
	want := "event: response.created\ndata: {}\n\nevent: response.completed"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestCodexRewriter_DoesNotRewriteSimilarPrefix(t *testing.T) {
	// `response.done.something` should not match exactly `response.done`.
	in := "event: response.done.early\ndata: {}\n\n"
	out := readAll(t, newCodexEventRewriter(strings.NewReader(in)))
	if out != in {
		t.Fatalf("got %q, want %q", out, in)
	}
}

func TestCodexRewriter_PreservesDataPayloadContainingResponseDone(t *testing.T) {
	// The data: line happens to contain the literal "response.done" — only
	// the event: line should be rewritten.
	in := "event: response.output_item.added\ndata: {\"text\":\"event: response.done\"}\n\n"
	out := readAll(t, newCodexEventRewriter(strings.NewReader(in)))
	if out != in {
		t.Fatalf("data payload mutated: got %q", out)
	}
}

func TestWrapResponseStream_Codex(t *testing.T) {
	in := "event: response.done\ndata: {}\n\n"
	r := wrapResponseStream("openai-codex-responses", strings.NewReader(in))
	out := readAll(t, r)
	if !strings.Contains(out, "response.completed") {
		t.Fatalf("expected codex wrap to rewrite, got %q", out)
	}
}

func TestWrapResponseStream_Passthrough(t *testing.T) {
	in := "event: response.done\ndata: {}\n\n"
	r := wrapResponseStream("openai-responses", strings.NewReader(in))
	if r != strings.NewReader(in) {
		// Different reader instance is fine — what matters is identity, but
		// strings.NewReader returns a value type, so check by reading instead.
	}
	out := readAll(t, r)
	if out != in {
		t.Fatalf("non-codex apiType should passthrough; got %q", out)
	}
}

// Sanity: rewriter does not alter total byte content for streams that have no
// rewriteable events.
func TestCodexRewriter_IdentityForUnrelatedStream(t *testing.T) {
	in := bytes.Repeat([]byte("event: foo\ndata: bar\n\n"), 10)
	out := readAll(t, newCodexEventRewriter(bytes.NewReader(in)))
	if out != string(in) {
		t.Fatalf("identity stream mutated")
	}
}

func TestCodexRewritePath(t *testing.T) {
	at := openAICodexResponses{}
	cases := []struct {
		in, want string
	}{
		{"/responses", "/codex/responses"},
		{"/v1/responses", "/codex/responses"},
		{"/codex/responses", "/codex/responses"},
		{"/something/else", "/something/else"},
	}
	for _, tc := range cases {
		got := at.RewritePath("https://chatgpt.com/backend-api", tc.in)
		if got != tc.want {
			t.Errorf("RewritePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
