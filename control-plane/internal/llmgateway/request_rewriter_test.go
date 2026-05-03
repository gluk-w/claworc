package llmgateway

import (
	"encoding/json"
	"reflect"
	"testing"
)

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, b)
	}
	return m
}

func TestCodexBody_StripsForbiddenFields(t *testing.T) {
	in := []byte(`{"model":"gpt-5.3-codex","input":[],"max_output_tokens":1024,"service_tier":"auto"}`)
	got := decode(t, rewriteCodexRequestBody(in))
	if _, ok := got["max_output_tokens"]; ok {
		t.Errorf("max_output_tokens should be stripped")
	}
	if _, ok := got["service_tier"]; ok {
		t.Errorf("service_tier should be stripped")
	}
}

func TestCodexBody_InjectsRequiredFields(t *testing.T) {
	in := []byte(`{"model":"gpt-5.3-codex","input":[]}`)
	got := decode(t, rewriteCodexRequestBody(in))

	if got["store"] != false {
		t.Errorf("store: want false, got %v", got["store"])
	}
	if got["stream"] != true {
		t.Errorf("stream: want true, got %v", got["stream"])
	}
	if got["tool_choice"] != "auto" {
		t.Errorf("tool_choice: want auto, got %v", got["tool_choice"])
	}
	if got["parallel_tool_calls"] != true {
		t.Errorf("parallel_tool_calls: want true, got %v", got["parallel_tool_calls"])
	}

	text, ok := got["text"].(map[string]any)
	if !ok {
		t.Fatalf("text missing or wrong type: %v", got["text"])
	}
	if text["verbosity"] != "medium" {
		t.Errorf("text.verbosity: want medium, got %v", text["verbosity"])
	}

	include, ok := got["include"].([]any)
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Errorf("include: want [reasoning.encrypted_content], got %v", got["include"])
	}
}

func TestCodexBody_PreservesExistingTextVerbosity(t *testing.T) {
	in := []byte(`{"model":"x","input":[],"text":{"verbosity":"high"}}`)
	got := decode(t, rewriteCodexRequestBody(in))
	text := got["text"].(map[string]any)
	if text["verbosity"] != "high" {
		t.Errorf("caller-supplied verbosity should be preserved, got %v", text["verbosity"])
	}
}

func TestCodexBody_MergesIncludeWithoutDuplicates(t *testing.T) {
	in := []byte(`{"input":[],"include":["reasoning.encrypted_content","other.thing"]}`)
	got := decode(t, rewriteCodexRequestBody(in))
	include := got["include"].([]any)
	if len(include) != 2 {
		t.Fatalf("want 2 include entries, got %d: %v", len(include), include)
	}
	if include[0] != "reasoning.encrypted_content" {
		t.Errorf("required entry must be first, got %v", include[0])
	}
	if include[1] != "other.thing" {
		t.Errorf("pre-existing entry lost, got %v", include[1])
	}
}

func TestCodexBody_ExtractsSystemMessageToInstructions_StringContent(t *testing.T) {
	in := []byte(`{"input":[
		{"role":"system","content":"you are helpful"},
		{"role":"user","content":"hi"}
	]}`)
	got := decode(t, rewriteCodexRequestBody(in))
	if got["instructions"] != "you are helpful" {
		t.Errorf("instructions: want \"you are helpful\", got %v", got["instructions"])
	}
	input := got["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("system message should be removed, got %d items", len(input))
	}
	first := input[0].(map[string]any)
	if first["role"] != "user" {
		t.Errorf("expected user message remaining, got %v", first)
	}
}

func TestCodexBody_ExtractsDeveloperRole(t *testing.T) {
	in := []byte(`{"input":[
		{"role":"developer","content":"system-ish prompt"},
		{"role":"user","content":"hi"}
	]}`)
	got := decode(t, rewriteCodexRequestBody(in))
	if got["instructions"] != "system-ish prompt" {
		t.Errorf("developer role should map to instructions, got %v", got["instructions"])
	}
}

func TestCodexBody_ExtractsTypedContentParts(t *testing.T) {
	in := []byte(`{"input":[
		{"role":"system","content":[
			{"type":"input_text","text":"first"},
			{"type":"input_text","text":"second"}
		]},
		{"role":"user","content":"hi"}
	]}`)
	got := decode(t, rewriteCodexRequestBody(in))
	if got["instructions"] != "first\nsecond" {
		t.Errorf("typed parts should be flattened, got %v", got["instructions"])
	}
}

func TestCodexBody_NoSystemMessage(t *testing.T) {
	in := []byte(`{"input":[{"role":"user","content":"hi"}]}`)
	got := decode(t, rewriteCodexRequestBody(in))
	if _, ok := got["instructions"]; ok {
		t.Errorf("instructions should not be set when no system message")
	}
	input := got["input"].([]any)
	if len(input) != 1 {
		t.Errorf("input should be unchanged when no system message")
	}
}

func TestCodexBody_OnlyFirstSystemMessageExtracted(t *testing.T) {
	// Two system messages — only the first should be lifted.
	in := []byte(`{"input":[
		{"role":"system","content":"first system"},
		{"role":"system","content":"second system"},
		{"role":"user","content":"hi"}
	]}`)
	got := decode(t, rewriteCodexRequestBody(in))
	if got["instructions"] != "first system" {
		t.Errorf("first system should be extracted, got %v", got["instructions"])
	}
	input := got["input"].([]any)
	if len(input) != 2 {
		t.Fatalf("only first system extracted, got %d items: %v", len(input), input)
	}
	if input[0].(map[string]any)["content"] != "second system" {
		t.Errorf("second system message should remain in input")
	}
}

func TestCodexBody_PreservesPassthroughFields(t *testing.T) {
	in := []byte(`{"model":"gpt-5.3-codex","input":[{"role":"user","content":"hi"}],"temperature":0.7,"prompt_cache_key":"sess-1","tools":[{"type":"function","name":"x"}]}`)
	got := decode(t, rewriteCodexRequestBody(in))
	if got["model"] != "gpt-5.3-codex" {
		t.Errorf("model not preserved")
	}
	if got["temperature"] != 0.7 {
		t.Errorf("temperature not preserved, got %v", got["temperature"])
	}
	if got["prompt_cache_key"] != "sess-1" {
		t.Errorf("prompt_cache_key not preserved")
	}
	if _, ok := got["tools"]; !ok {
		t.Errorf("tools not preserved")
	}
}

func TestCodexBody_InvalidJSONReturnsOriginal(t *testing.T) {
	in := []byte(`not json at all`)
	got := rewriteCodexRequestBody(in)
	if !reflect.DeepEqual(got, in) {
		t.Errorf("invalid JSON should pass through unchanged")
	}
}

func TestCodexBody_EmptyObjectStillNormalised(t *testing.T) {
	in := []byte(`{}`)
	got := decode(t, rewriteCodexRequestBody(in))
	if got["store"] != false || got["stream"] != true {
		t.Errorf("required constants should still be set on empty body")
	}
}
