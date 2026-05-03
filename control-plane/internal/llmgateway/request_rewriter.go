package llmgateway

import "encoding/json"

// rewriteCodexRequestBody translates an openai-responses request body into the
// shape expected by ChatGPT's /codex/responses endpoint.
//
// Differences (per pi-ai source: openai-codex-responses.js:193-221):
//
//   - Strip: max_output_tokens, service_tier (codex rejects these)
//   - Force: store=false, stream=true
//   - Inject: text.verbosity="medium", tool_choice="auto",
//     parallel_tool_calls=true, include=["reasoning.encrypted_content"]
//   - Extract first system/developer message from input[] into top-level
//     "instructions" string and remove it from input[]
//
// On any JSON parse error or unexpected shape the original body is returned
// unchanged — translation is best-effort, not a validator.
func rewriteCodexRequestBody(body []byte) []byte {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil || doc == nil {
		return body
	}

	delete(doc, "max_output_tokens")
	delete(doc, "service_tier")

	doc["store"] = false
	doc["stream"] = true
	doc["tool_choice"] = "auto"
	doc["parallel_tool_calls"] = true

	// text.verbosity defaults to "medium" — preserve any caller-supplied value.
	if existing, ok := doc["text"].(map[string]any); ok {
		if _, hasVerbosity := existing["verbosity"]; !hasVerbosity {
			existing["verbosity"] = "medium"
		}
		doc["text"] = existing
	} else {
		doc["text"] = map[string]any{"verbosity": "medium"}
	}

	doc["include"] = mergeInclude(doc["include"])

	if input, ok := doc["input"].([]any); ok {
		if instructions, remaining, found := extractSystemInstructions(input); found {
			if _, alreadySet := doc["instructions"].(string); !alreadySet {
				doc["instructions"] = instructions
			}
			doc["input"] = remaining
		}
	}

	out, err := json.Marshal(doc)
	if err != nil {
		return body
	}
	return out
}

// mergeInclude returns an include[] that contains "reasoning.encrypted_content"
// plus any pre-existing values, with no duplicates.
func mergeInclude(existing any) []any {
	const required = "reasoning.encrypted_content"
	out := []any{required}
	seen := map[string]bool{required: true}
	if arr, ok := existing.([]any); ok {
		for _, v := range arr {
			s, ok := v.(string)
			if !ok || seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// extractSystemInstructions removes the first input item with role "system" or
// "developer" and returns its text content as a string. The remaining input
// items are returned unchanged. found=false when no such item exists.
func extractSystemInstructions(input []any) (instructions string, remaining []any, found bool) {
	remaining = make([]any, 0, len(input))
	for _, item := range input {
		if found {
			remaining = append(remaining, item)
			continue
		}
		obj, ok := item.(map[string]any)
		if !ok {
			remaining = append(remaining, item)
			continue
		}
		role, _ := obj["role"].(string)
		if role != "system" && role != "developer" {
			remaining = append(remaining, item)
			continue
		}
		text, ok := contentToText(obj["content"])
		if !ok {
			remaining = append(remaining, item)
			continue
		}
		instructions = text
		found = true
	}
	return
}

// contentToText flattens a Responses-API content field — which may be a plain
// string OR an array of typed parts like {type:"input_text",text:"..."} — into
// a single string. Returns ok=false when the shape is unrecognized so the
// caller leaves the message intact.
func contentToText(content any) (string, bool) {
	switch v := content.(type) {
	case string:
		return v, true
	case []any:
		var out string
		for _, part := range v {
			obj, ok := part.(map[string]any)
			if !ok {
				return "", false
			}
			text, ok := obj["text"].(string)
			if !ok {
				continue
			}
			if out != "" {
				out += "\n"
			}
			out += text
		}
		return out, true
	default:
		return "", false
	}
}
