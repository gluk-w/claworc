package providers

import "encoding/json"

// ParseGeminiChunk extracts token counts from Gemini NDJSON/SSE responses.
// Gemini sends usageMetadata with promptTokenCount / candidatesTokenCount.
func ParseGeminiChunk(data []byte) (model string, inputTokens, outputTokens int64) {
	var chunk struct {
		ModelVersion  string `json:"modelVersion"`
		UsageMetadata *struct {
			PromptTokenCount     int64 `json:"promptTokenCount"`
			CandidatesTokenCount int64 `json:"candidatesTokenCount"`
			TotalTokenCount      int64 `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}
	if json.Unmarshal(data, &chunk) == nil && chunk.UsageMetadata != nil {
		return chunk.ModelVersion, chunk.UsageMetadata.PromptTokenCount, chunk.UsageMetadata.CandidatesTokenCount
	}
	return "", 0, 0
}

// ParseGeminiBody extracts token usage from a non-streaming Gemini response.
func ParseGeminiBody(body []byte) (model string, inputTokens, outputTokens int64) {
	return ParseGeminiChunk(body)
}
