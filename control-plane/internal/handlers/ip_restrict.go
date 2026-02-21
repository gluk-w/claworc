package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
	"github.com/go-chi/chi/v5"
)

type ipRestrictResponse struct {
	InstanceID     uint   `json:"instance_id"`
	AllowedIPs     string `json:"allowed_ips"`
	NormalizedList string `json:"normalized_list"`
}

type ipRestrictUpdateRequest struct {
	AllowedIPs string `json:"allowed_ips"`
}

// GetAllowedSourceIPs returns the current allowed source IP list for an instance.
func GetAllowedSourceIPs(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	normalized, _ := sshmanager.NormalizeAllowList(inst.AllowedSourceIPs)

	writeJSON(w, http.StatusOK, ipRestrictResponse{
		InstanceID:     inst.ID,
		AllowedIPs:     inst.AllowedSourceIPs,
		NormalizedList: normalized,
	})
}

// UpdateAllowedSourceIPs updates the allowed source IP list for an instance.
// Validates that each entry is a valid IP address or CIDR range.
func UpdateAllowedSourceIPs(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	var body ipRestrictUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate and normalize the IP list
	normalized, err := sshmanager.NormalizeAllowList(body.AllowedIPs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := database.DB.Model(&inst).Update("allowed_source_ips", normalized).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update allowed source IPs")
		return
	}

	writeJSON(w, http.StatusOK, ipRestrictResponse{
		InstanceID:     inst.ID,
		AllowedIPs:     normalized,
		NormalizedList: normalized,
	})
}
