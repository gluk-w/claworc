package handlers

import (
	"net/http"

	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// agentTypeResponse is one entry of GET /api/v1/agent-types.
type agentTypeResponse struct {
	Type         string `json:"type"`
	DisplayName  string `json:"display_name"`
	DefaultImage string `json:"default_image"`
	HasControlUI bool   `json:"has_control_ui"`
}

// agentCapabilitiesResponse serializes the static registry capabilities for
// API responses (single-instance GET).
type agentCapabilitiesResponse struct {
	Chat         bool `json:"chat"`
	ChatAbort    bool `json:"chat_abort"`
	SessionReset bool `json:"session_reset"`
	Config       bool `json:"config"`
	ConfigureLLM bool `json:"configure_llm"`
	Restart      bool `json:"restart"`
	ControlUI    bool `json:"control_ui"`
	Skills       bool `json:"skills"`
}

func toAgentCapabilitiesResponse(c agentshim.Capabilities) *agentCapabilitiesResponse {
	return &agentCapabilitiesResponse{
		Chat:         c.Chat,
		ChatAbort:    c.ChatAbort,
		SessionReset: c.SessionReset,
		Config:       c.Config,
		ConfigureLLM: c.ConfigureLLM,
		Restart:      c.Restart,
		ControlUI:    c.ControlUI,
		Skills:       c.Skills,
	}
}

// ListAgentTypes returns the static agent-type registry with each type's
// configured default image resolved from settings.
func ListAgentTypes(w http.ResponseWriter, r *http.Request) {
	entries := agentshim.Types()
	out := make([]agentTypeResponse, 0, len(entries))
	for _, e := range entries {
		out = append(out, agentTypeResponse{
			Type:         e.Type,
			DisplayName:  e.DisplayName,
			DefaultImage: agentshim.DefaultImage(e.Type, database.GetSetting),
			HasControlUI: e.HasControlUI,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
