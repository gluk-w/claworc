package llmgateway

import "testing"

func TestParseUsageOpenAICompletions(t *testing.T) {
	body := []byte(`{
		"usage": {
			"prompt_tokens": 23,
			"completion_tokens": 30,
			"prompt_tokens_details": {"cached_tokens": 0}
		}
	}`)
	in, out, cached := ParseUsageOpenAICompletions(body)
	if in != 23 || out != 30 || cached != 0 {
		t.Errorf("got (%d, %d, %d), want (23, 30, 0)", in, out, cached)
	}
}

func TestParseUsageOpenAIResponses(t *testing.T) {
	body := []byte(`{
		"object": "response",
		"usage": {
			"input_tokens": 24,
			"input_tokens_details": {"cached_tokens": 0},
			"output_tokens": 8,
			"output_tokens_details": {"reasoning_tokens": 0},
			"total_tokens": 32
		}
	}`)
	in, out, cached := ParseUsageOpenAIResponses(body)
	if in != 24 || out != 8 || cached != 0 {
		t.Errorf("got (%d, %d, %d), want (24, 8, 0)", in, out, cached)
	}
}

func TestParseUsageOpenAIResponses_WithCached(t *testing.T) {
	body := []byte(`{
		"object": "response",
		"usage": {
			"input_tokens": 100,
			"input_tokens_details": {"cached_tokens": 40},
			"output_tokens": 20,
			"total_tokens": 120
		}
	}`)
	in, out, cached := ParseUsageOpenAIResponses(body)
	if in != 100 || out != 20 || cached != 40 {
		t.Errorf("got (%d, %d, %d), want (100, 20, 40)", in, out, cached)
	}
}

func TestParseUsageAnthropicMessages(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-20250514","id":"msg_013STsrWN8yxak5zXEEC51LS","type":"message","role":"assistant","content":[{"type":"text","text":"The capital of France is Paris."}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":20,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens":10,"service_tier":"standard","inference_geo":"not_available"}}`)
	in, out, cached := ParseUsageAnthropicMessages(body)
	if in != 20 || out != 10 || cached != 0 {
		t.Errorf("got (%d, %d, %d), want (20, 10, 0)", in, out, cached)
	}
}

func TestParseUsageAnthropicMessages_WithCached(t *testing.T) {
	body := []byte(`{"usage":{"input_tokens":50,"cache_read_input_tokens":30,"output_tokens":15}}`)
	in, out, cached := ParseUsageAnthropicMessages(body)
	if in != 50 || out != 15 || cached != 30 {
		t.Errorf("got (%d, %d, %d), want (50, 15, 30)", in, out, cached)
	}
}

func TestParseUsageOpenAICompletionsStream_NoUsage(t *testing.T) {
	// Stream without stream_options:{include_usage:true} — no usage chunk is emitted.
	body := []byte(
		"data: {\"id\":\"chatcmpl-DGsESSAZBMFa4WpHuyTayvACNEH0X\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"id\":\"chatcmpl-DGsESSAZBMFa4WpHuyTayvACNEH0X\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello!\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"id\":\"chatcmpl-DGsESSAZBMFa4WpHuyTayvACNEH0X\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n")
	in, out, cached := ParseUsageOpenAICompletionsStream(body)
	if in != 0 || out != 0 || cached != 0 {
		t.Errorf("got (%d, %d, %d), want (0, 0, 0)", in, out, cached)
	}
}

func TestParseUsageOpenAICompletionsStream_WithUsage(t *testing.T) {
	// Stream with stream_options:{include_usage:true} — a final usage chunk is appended before [DONE].
	body := []byte(
		"data: {\"id\":\"chatcmpl-x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hi\"},\"finish_reason\":null}]}\n\n" +
			"data: {\"id\":\"chatcmpl-x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: {\"id\":\"chatcmpl-x\",\"object\":\"chat.completion.chunk\",\"choices\":[],\"usage\":{\"prompt_tokens\":23,\"completion_tokens\":30,\"prompt_tokens_details\":{\"cached_tokens\":5},\"total_tokens\":53}}\n\n" +
			"data: [DONE]\n")
	in, out, cached := ParseUsageOpenAICompletionsStream(body)
	if in != 18 || out != 30 || cached != 5 {
		t.Errorf("got (%d, %d, %d), want (18, 30, 5)", in, out, cached)
	}
}

func TestParseUsageOpenAIResponsesStream(t *testing.T) {
	body := []byte(
		"event: response.created\n" +
			`data: {"type":"response.created","response":{"id":"resp_06f081de","object":"response","status":"in_progress","usage":null},"sequence_number":0}` + "\n\n" +
			"event: response.output_text.delta\n" +
			`data: {"type":"response.output_text.delta","delta":"Hello!","sequence_number":4}` + "\n\n" +
			"event: response.completed\n" +
			`data: {"type":"response.completed","response":{"id":"resp_06f081de","object":"response","status":"completed","usage":{"input_tokens":13,"input_tokens_details":{"cached_tokens":0},"output_tokens":17,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":30}},"sequence_number":23}` + "\n")
	in, out, cached := ParseUsageOpenAIResponsesStream(body)
	if in != 13 || out != 17 || cached != 0 {
		t.Errorf("got (%d, %d, %d), want (13, 17, 0)", in, out, cached)
	}
}

func TestParseUsageOpenAIResponsesStream_WithCached(t *testing.T) {
	body := []byte(
		"event: response.completed\n" +
			`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":60},"output_tokens":20,"total_tokens":120}},"sequence_number":10}` + "\n")
	in, out, cached := ParseUsageOpenAIResponsesStream(body)
	if in != 100 || out != 20 || cached != 60 {
		t.Errorf("got (%d, %d, %d), want (100, 20, 60)", in, out, cached)
	}
}

func TestParseUsageAnthropicMessagesStream(t *testing.T) {
	body := []byte("event: message_start\n" +
		`data: {"type":"message_start","message":{"model":"claude-sonnet-4-20250514","id":"msg_01NSyhy93LurRhSyZStUHcMJ","type":"message","role":"assistant","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":20,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens":1,"service_tier":"standard","inference_geo":"not_available"}}}` + "\n\n" +
		"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		"event: ping\n" +
		`data: {"type": "ping"}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"The"}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" capital of France is Paris."}}` + "\n\n" +
		"event: content_block_stop\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":20,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":10}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n")
	in, out, cached := ParseUsageAnthropicMessagesStream(body)
	if in != 20 || out != 10 || cached != 0 {
		t.Errorf("got (%d, %d, %d), want (20, 10, 0)", in, out, cached)
	}
}

func TestParseUsageAnthropicMessagesStream_WithCached(t *testing.T) {
	body := []byte("event: message_start\n" +
		`data: {"type":"message_start","message":{"usage":{"input_tokens":100,"cache_read_input_tokens":40,"output_tokens":1}}}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{},"usage":{"output_tokens":25}}` + "\n")
	in, out, cached := ParseUsageAnthropicMessagesStream(body)
	if in != 100 || out != 25 || cached != 40 {
		t.Errorf("got (%d, %d, %d), want (100, 25, 40)", in, out, cached)
	}
}

func TestParseUsageOllama(t *testing.T) {
	body := []byte(`{
		"model": "llama3",
		"created_at": "2024-01-15T10:30:00.000000Z",
		"message": {"role": "assistant", "content": "Hello! I'm doing well, thank you for asking. How can I help you today?"},
		"done": true,
		"total_duration": 1234567890,
		"load_duration": 123456789,
		"prompt_eval_count": 15,
		"prompt_eval_duration": 234567890,
		"eval_count": 28,
		"eval_duration": 876543210
	}`)
	in, out, cached := ParseUsageOllama(body)
	if in != 15 || out != 28 || cached != 0 {
		t.Errorf("got (%d, %d, %d), want (15, 28, 0)", in, out, cached)
	}
}

func TestParseUsageOllamaStream(t *testing.T) {
	body := []byte(
		`{"model":"llama3","created_at":"2024-01-15T10:30:00.000Z","message":{"role":"assistant","content":"Hello"},"done":false}` + "\n" +
			`{"model":"llama3","created_at":"2024-01-15T10:30:00.001Z","message":{"role":"assistant","content":"!"},"done":false}` + "\n" +
			`{"model":"llama3","created_at":"2024-01-15T10:30:00.002Z","message":{"role":"assistant","content":" How"},"done":false}` + "\n" +
			`{"model":"llama3","created_at":"2024-01-15T10:30:00.010Z","message":{"role":"assistant","content":""},"done":true,"total_duration":1234567890,"prompt_eval_count":15,"eval_count":28}` + "\n")
	in, out, cached := ParseUsageOllamaStream(body)
	if in != 15 || out != 28 || cached != 0 {
		t.Errorf("got (%d, %d, %d), want (15, 28, 0)", in, out, cached)
	}
}

func TestParseUsageOllamaStream_NoDoneChunk(t *testing.T) {
	// Truncated stream with no done:true line — all zeros.
	body := []byte(
		`{"model":"llama3","message":{"content":"Hello"},"done":false}` + "\n" +
			`{"model":"llama3","message":{"content":"!"},"done":false}` + "\n")
	in, out, cached := ParseUsageOllamaStream(body)
	if in != 0 || out != 0 || cached != 0 {
		t.Errorf("got (%d, %d, %d), want (0, 0, 0)", in, out, cached)
	}
}

func TestParseUsageAnthropicMessagesStream_WithCacheCreation(t *testing.T) {
	// Real Anthropic API stream: input_tokens=1, cache_creation_input_tokens=452,
	// cache_read_input_tokens=14751. The message_delta carries the final output_tokens=42.
	// cached should reflect cache_read_input_tokens (14751), not cache_creation.
	body := []byte("event: message_start\n" +
		`data: {"type":"message_start","message":{"model":"claude-sonnet-4-6","id":"msg_01PTjhsPobs9hA35CDDJuKgi","type":"message","role":"assistant","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"cache_creation_input_tokens":452,"cache_read_input_tokens":14751,"cache_creation":{"ephemeral_5m_input_tokens":452,"ephemeral_1h_input_tokens":0},"output_tokens":5,"service_tier":"standard","inference_geo":"global"}}}` + "\n\n" +
		"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		"event: ping\n" +
		`data: {"type": "ping"}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello!"}}` + "\n\n" +
		"event: content_block_stop\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":1,"cache_creation_input_tokens":452,"cache_read_input_tokens":14751,"output_tokens":42}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n")
	in, out, cached := ParseUsageAnthropicMessagesStream(body)
	if in != 1 || out != 42 || cached != 14751 {
		t.Errorf("got (%d, %d, %d), want (1, 42, 14751)", in, out, cached)
	}
}

func TestParseUsageBedrockConverseStream(t *testing.T) {
	body := []byte(`{
		"metadata": {
			"usage": {
				"inputTokens": 15,
				"outputTokens": 28,
				"totalTokens": 43
			},
			"metrics": {
				"latencyMs": 1230
			}
		}
	}`)
	in, out, cached := ParseUsageBedrockConverseStream(body)
	if in != 15 || out != 28 || cached != 0 {
		t.Errorf("got (%d, %d, %d), want (15, 28, 0)", in, out, cached)
	}
}

func TestParseUsageOpenAICompletions_Empty(t *testing.T) {
	in, out, cached := ParseUsageOpenAICompletions([]byte{})
	if in != 0 || out != 0 || cached != 0 {
		t.Errorf("got (%d, %d, %d), want (0, 0, 0)", in, out, cached)
	}
}
