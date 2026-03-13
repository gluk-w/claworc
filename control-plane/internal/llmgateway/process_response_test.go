package llmgateway

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// processResponseCase drives a single processResponse assertion.
type processResponseCase struct {
	name        string
	body        string
	isStreaming bool
	apiType     string
	statusCode  int
	wantIn      int
	wantOut     int
	wantCached  int
}

func runProcessResponseCase(t *testing.T, tc processResponseCase) {
	t.Helper()
	rr := httptest.NewRecorder()
	in, out, cached, _, errMsg := processResponse(
		rr,
		strings.NewReader(tc.body),
		tc.isStreaming,
		GetAPIType(tc.apiType),
		tc.apiType,
		tc.statusCode,
		nil, // no cost model needed for token assertions
		"test-model",
	)
	if in != tc.wantIn || out != tc.wantOut || cached != tc.wantCached {
		t.Errorf("tokens: got (%d, %d, %d), want (%d, %d, %d)",
			in, out, cached, tc.wantIn, tc.wantOut, tc.wantCached)
	}
	if rr.Body.String() != tc.body {
		t.Errorf("body not forwarded to writer: got %q, want %q", rr.Body.String(), tc.body)
	}
	_ = errMsg
}

// --- Non-streaming ---

func TestProcessResponse_OpenAICompletions(t *testing.T) {
	runProcessResponseCase(t, processResponseCase{
		name:        "openai-completions non-streaming",
		body:        `{"usage":{"prompt_tokens":23,"completion_tokens":30,"prompt_tokens_details":{"cached_tokens":5}}}`,
		isStreaming: false,
		apiType:     "openai-completions",
		statusCode:  200,
		wantIn:      18,
		wantOut:     30,
		wantCached:  5,
	})
}

func TestProcessResponse_OpenAIResponses(t *testing.T) {
	runProcessResponseCase(t, processResponseCase{
		name:        "openai-responses non-streaming",
		body:        `{"object":"response","usage":{"input_tokens":24,"input_tokens_details":{"cached_tokens":0},"output_tokens":8}}`,
		isStreaming: false,
		apiType:     "openai-responses",
		statusCode:  200,
		wantIn:      24,
		wantOut:     8,
		wantCached:  0,
	})
}

func TestProcessResponse_AnthropicMessages(t *testing.T) {
	runProcessResponseCase(t, processResponseCase{
		name:        "anthropic-messages non-streaming",
		body:        `{"usage":{"input_tokens":20,"output_tokens":10,"cache_read_input_tokens":0}}`,
		isStreaming: false,
		apiType:     "anthropic-messages",
		statusCode:  200,
		wantIn:      20,
		wantOut:     10,
		wantCached:  0,
	})
}

func TestProcessResponse_GoogleGenerativeAI(t *testing.T) {
	runProcessResponseCase(t, processResponseCase{
		name:        "google-generative-ai non-streaming",
		body:        `{"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":12,"cachedContentTokenCount":4}}`,
		isStreaming: false,
		apiType:     "google-generative-ai",
		statusCode:  200,
		wantIn:      8,
		wantOut:     12,
		wantCached:  4,
	})
}

func TestProcessResponse_Ollama(t *testing.T) {
	runProcessResponseCase(t, processResponseCase{
		name:        "ollama non-streaming",
		body:        `{"model":"llama3","done":true,"prompt_eval_count":15,"eval_count":28}`,
		isStreaming: false,
		apiType:     "ollama",
		statusCode:  200,
		wantIn:      15,
		wantOut:     28,
		wantCached:  0,
	})
}

func TestProcessResponse_BedrockConverse(t *testing.T) {
	runProcessResponseCase(t, processResponseCase{
		name:        "bedrock-converse",
		body:        `{"metadata":{"usage":{"inputTokens":15,"outputTokens":28,"totalTokens":43},"metrics":{"latencyMs":1230}}}`,
		isStreaming: false,
		apiType:     "bedrock-converse",
		statusCode:  200,
		wantIn:      15,
		wantOut:     28,
		wantCached:  0,
	})
}

// --- Streaming ---

func TestProcessResponse_OpenAICompletionsStream(t *testing.T) {
	body := "data: {\"id\":\"chatcmpl-x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"Hi\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chatcmpl-x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"id\":\"chatcmpl-x\",\"object\":\"chat.completion.chunk\",\"choices\":[],\"usage\":{\"prompt_tokens\":23,\"completion_tokens\":30,\"prompt_tokens_details\":{\"cached_tokens\":5},\"total_tokens\":53}}\n\n" +
		"data: [DONE]\n"
	runProcessResponseCase(t, processResponseCase{
		name:        "openai-completions streaming with include_usage",
		body:        body,
		isStreaming: true,
		apiType:     "openai-completions",
		statusCode:  200,
		wantIn:      18,
		wantOut:     30,
		wantCached:  5,
	})
}

func TestProcessResponse_OpenAIResponsesStream(t *testing.T) {
	body := "event: response.created\n" +
		"data: {\"type\":\"response.created\",\"response\":{\"status\":\"in_progress\",\"usage\":null}}\n\n" +
		"event: response.output_text.delta\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello!\"}\n\n" +
		"event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":13,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":17}}}\n"
	runProcessResponseCase(t, processResponseCase{
		name:        "openai-responses streaming",
		body:        body,
		isStreaming: true,
		apiType:     "openai-responses",
		statusCode:  200,
		wantIn:      13,
		wantOut:     17,
		wantCached:  0,
	})
}

func TestProcessResponse_AnthropicMessagesStream(t *testing.T) {
	body := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":20,\"cache_read_input_tokens\":0,\"output_tokens\":1}}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":10}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n"
	runProcessResponseCase(t, processResponseCase{
		name:        "anthropic-messages streaming",
		body:        body,
		isStreaming: true,
		apiType:     "anthropic-messages",
		statusCode:  200,
		wantIn:      20,
		wantOut:     10,
		wantCached:  0,
	})
}

func TestProcessResponse_OllamaStream(t *testing.T) {
	body := "{\"model\":\"llama3\",\"message\":{\"content\":\"Hello\"},\"done\":false}\n" +
		"{\"model\":\"llama3\",\"message\":{\"content\":\"!\"},\"done\":false}\n" +
		"{\"model\":\"llama3\",\"message\":{\"content\":\"\"},\"done\":true,\"prompt_eval_count\":15,\"eval_count\":28}\n"
	runProcessResponseCase(t, processResponseCase{
		name:        "ollama streaming",
		body:        body,
		isStreaming: true,
		apiType:     "ollama",
		statusCode:  200,
		wantIn:      15,
		wantOut:     28,
		wantCached:  0,
	})
}

// --- Error response ---

func TestProcessResponse_ErrorBodyCaptured(t *testing.T) {
	errBody := `{"error":{"message":"invalid api key","type":"authentication_error"}}`
	rr := httptest.NewRecorder()
	_, _, _, _, errMsg := processResponse(rr, strings.NewReader(errBody), false, GetAPIType("openai-completions"), "openai-completions", 401, nil, "")
	if errMsg != errBody {
		t.Errorf("errMsg: got %q, want %q", errMsg, errBody)
	}
	if rr.Body.String() != errBody {
		t.Errorf("body not forwarded: got %q", rr.Body.String())
	}
}

func TestProcessResponse_ErrorBodyTruncated(t *testing.T) {
	errBody := strings.Repeat("x", 600)
	rr := httptest.NewRecorder()
	_, _, _, _, errMsg := processResponse(rr, strings.NewReader(errBody), false, GetAPIType("openai-completions"), "openai-completions", 500, nil, "")
	if len(errMsg) != 500 {
		t.Errorf("errMsg len: got %d, want 500", len(errMsg))
	}
}

// --- Cost calculation ---

func TestProcessResponse_CostCalculated(t *testing.T) {
	models := []database.ProviderModel{
		{ID: "gpt-4o", Cost: &database.ProviderModelCost{Input: 2.5, Output: 10.0, CacheRead: 1.25}},
	}
	body := `{"usage":{"prompt_tokens":1000000,"completion_tokens":500000,"prompt_tokens_details":{"cached_tokens":200000}}}`
	rr := httptest.NewRecorder()
	_, _, _, costUSD, _ := processResponse(rr, bytes.NewReader([]byte(body)), false, GetAPIType("openai-completions"), "openai-completions", 200, models, "gpt-4o")
	// non-cached input: 800000 * 2.5/1M = 2.0
	// cached input: 200000 * 1.25/1M = 0.25
	// output: 500000 * 10.0/1M = 5.0  → total = 7.25
	want := 7.25
	if costUSD != want {
		t.Errorf("costUSD: got %f, want %f", costUSD, want)
	}
}
