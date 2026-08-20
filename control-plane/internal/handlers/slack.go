package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
	"github.com/go-chi/chi/v5"
)

// Slack credentials are stored in the instance's encrypted EnvVars map under
// these names. OpenClaw reads them from the environment for the default Slack
// account whenever channels.slack.botToken/appToken are unset, so the rendered
// config block never contains a token.
const (
	slackBotTokenEnvVar = "SLACK_BOT_TOKEN"
	slackAppTokenEnvVar = "SLACK_APP_TOKEN"
)

// slackChannelIDRegex matches raw Slack channel IDs (public C…, private G…):
// an uppercase alphanumeric string that always contains at least one digit —
// which distinguishes IDs from channel names like "general" that would
// otherwise slip through after uppercasing. OpenClaw routes by ID under
// groupPolicy=allowlist; channel *names* silently fail to match, so reject
// anything that doesn't look like an ID up front.
var slackChannelIDRegex = regexp.MustCompile(`^[CG][A-Z0-9]*[0-9][A-Z0-9]*$`)

// slackChannelEntry is one allowlisted channel in the per-instance config.
type slackChannelEntry struct {
	ID string `json:"id"`
	// RequireMention nil means OpenClaw's default (true): the bot only
	// responds when @-mentioned in the channel.
	RequireMention *bool `json:"require_mention,omitempty"`
}

// instanceSlackConfig is the JSON shape persisted in Instance.SlackConfig.
type instanceSlackConfig struct {
	Enabled  bool                `json:"enabled"`
	Channels []slackChannelEntry `json:"channels"`
	// DMPolicy: "" (OpenClaw default, pairing), "pairing", "open", "disabled".
	DMPolicy string `json:"dm_policy,omitempty"`
}

func parseSlackConfig(raw string) (instanceSlackConfig, bool) {
	var cfg instanceSlackConfig
	if raw == "" {
		return cfg, false
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return instanceSlackConfig{}, false
	}
	if cfg.Channels == nil {
		cfg.Channels = []slackChannelEntry{}
	}
	return cfg, true
}

// validateSlackConfig normalizes channel IDs (trim, strip a leading '#',
// uppercase) and rejects anything that isn't a raw Slack channel ID.
func validateSlackConfig(cfg *instanceSlackConfig) error {
	seen := make(map[string]bool, len(cfg.Channels))
	normalized := make([]slackChannelEntry, 0, len(cfg.Channels))
	for _, ch := range cfg.Channels {
		id := strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(ch.ID), "#"))
		if !slackChannelIDRegex.MatchString(id) {
			return fmt.Errorf("invalid Slack channel ID %q: use the channel ID (e.g. C0123456789), not the channel name", ch.ID)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		ch.ID = id
		normalized = append(normalized, ch)
	}
	cfg.Channels = normalized
	switch cfg.DMPolicy {
	case "", "pairing", "open", "disabled":
	default:
		return fmt.Errorf("invalid dm_policy %q: must be one of pairing, open, disabled", cfg.DMPolicy)
	}
	return nil
}

// renderSlackChannelsJSON renders the stored config into the OpenClaw
// `channels.slack` block. Tokens are deliberately omitted — OpenClaw falls
// back to SLACK_BOT_TOKEN/SLACK_APP_TOKEN from the environment for the
// default account, keeping secrets out of the config file on the PVC.
func renderSlackChannelsJSON(cfg instanceSlackConfig) (string, error) {
	block := map[string]interface{}{"enabled": cfg.Enabled}
	if cfg.Enabled {
		block["groupPolicy"] = "allowlist"
		if len(cfg.Channels) > 0 {
			channels := make(map[string]interface{}, len(cfg.Channels))
			for _, ch := range cfg.Channels {
				channels[ch.ID] = map[string]interface{}{
					"enabled":        true,
					"requireMention": ch.RequireMention == nil || *ch.RequireMention,
				}
			}
			block["channels"] = channels
		}
		switch cfg.DMPolicy {
		case "open":
			// OpenClaw requires an explicit wildcard sender allowlist for open DMs.
			block["dmPolicy"] = "open"
			block["allowFrom"] = []string{"*"}
		case "pairing", "disabled":
			block["dmPolicy"] = cfg.DMPolicy
		}
	}
	b, err := json.Marshal(block)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// renderInitialSlackEnv returns the OPENCLAW_INITIAL_SLACK value for an
// instance, or "" when Slack has never been configured through Claworc (so a
// manually-edited channels.slack block in the agent's config is left alone).
func renderInitialSlackEnv(inst database.Instance) string {
	cfg, ok := parseSlackConfig(inst.SlackConfig)
	if !ok {
		return ""
	}
	rendered, err := renderSlackChannelsJSON(cfg)
	if err != nil {
		log.Printf("Failed to render Slack config for instance %d: %v", inst.ID, err)
		return ""
	}
	return rendered
}

// applySlackConfig writes the channels.slack block into the agent's OpenClaw
// config over an established SSH connection and restarts the gateway so it
// takes effect. The path is cleared first because `config set` deep-merges
// map values — channels removed in Claworc would otherwise linger.
func applySlackConfig(ctx context.Context, agent sshproxy.Instance, name, channelsJSON string) {
	_, _, _, _ = agent.ExecOpenclaw(ctx, "config", "unset", "channels.slack")
	_, stderr, code, err := agent.ExecOpenclaw(ctx, "config", "set", "channels.slack", channelsJSON, "--json")
	if err != nil {
		log.Printf("Error setting channels.slack for %s: %v", utils.SanitizeForLog(name), err)
		return
	}
	if code != 0 {
		log.Printf("Failed to set channels.slack for %s: %s", utils.SanitizeForLog(name), utils.SanitizeForLog(stderr))
		return
	}
	if _, _, _, err := agent.ExecOpenclaw(ctx, "gateway", "stop"); err != nil {
		log.Printf("Error restarting gateway for %s after Slack config change: %v", utils.SanitizeForLog(name), err)
	}
}

// pushSlackConfig is the async best-effort wrapper around applySlackConfig
// for a running instance (mirrors pushBrowserEnabledConfig). A stopped or
// unreachable instance picks the config up at next boot via
// OPENCLAW_INITIAL_SLACK.
func pushSlackConfig(instanceID uint, name, channelsJSON string) {
	if SSHMgr == nil || channelsJSON == "" {
		return
	}
	go func() {
		ctx := context.Background()
		sshClient, err := SSHMgr.WaitForSSH(ctx, instanceID, 30*time.Second)
		if err != nil {
			log.Printf("slack-config: no SSH connection for instance %d, skipping OpenClaw config push: %v", instanceID, err)
			return
		}
		applySlackConfig(ctx, sshproxy.NewSSHInstance(sshClient), name, channelsJSON)
	}()
}

// instanceSlackCreateRequest is the create-time Slack payload (structured
// config plus tokens) accepted by CreateInstance, so a new agent connects to
// Slack on first boot.
type instanceSlackCreateRequest struct {
	instanceSlackConfig
	BotToken string `json:"bot_token"`
	AppToken string `json:"app_token"`
}

type instanceSlackResponse struct {
	Configured     bool                `json:"configured"`
	Enabled        bool                `json:"enabled"`
	Channels       []slackChannelEntry `json:"channels"`
	DMPolicy       string              `json:"dm_policy"`
	HasBotToken    bool                `json:"has_bot_token"`
	HasAppToken    bool                `json:"has_app_token"`
	BotTokenMasked string              `json:"bot_token_masked,omitempty"`
	AppTokenMasked string              `json:"app_token_masked,omitempty"`
	Restarting     bool                `json:"restarting,omitempty"`
}

func slackResponseFor(inst database.Instance) instanceSlackResponse {
	cfg, configured := parseSlackConfig(inst.SlackConfig)
	resp := instanceSlackResponse{
		Configured: configured,
		Enabled:    cfg.Enabled,
		Channels:   cfg.Channels,
		DMPolicy:   cfg.DMPolicy,
	}
	if resp.Channels == nil {
		resp.Channels = []slackChannelEntry{}
	}
	// Token presence reflects what the container actually receives: global
	// defaults merged with per-instance overrides.
	envVars := map[string]string{}
	MergeUserEnvVars(envVars, LoadGlobalEnvVars(), LoadInstanceEnvVars(inst))
	if v, ok := envVars[slackBotTokenEnvVar]; ok && v != "" {
		resp.HasBotToken = true
		resp.BotTokenMasked = utils.Mask(v)
	}
	if v, ok := envVars[slackAppTokenEnvVar]; ok && v != "" {
		resp.HasAppToken = true
		resp.AppTokenMasked = utils.Mask(v)
	}
	return resp
}

// resolveInstanceForChannelSettings loads the instance from the URL id
// parameter for the chat-channel settings endpoints (Slack, Discord) and
// enforces per-instance access (same model as the webhook admin endpoints).
func resolveInstanceForChannelSettings(w http.ResponseWriter, r *http.Request) (*database.Instance, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return nil, false
	}
	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return nil, false
	}
	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return nil, false
	}
	return &inst, true
}

// GET /api/v1/instances/{id}/slack
func GetInstanceSlack(w http.ResponseWriter, r *http.Request) {
	inst, ok := resolveInstanceForChannelSettings(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, slackResponseFor(*inst))
}

type instanceSlackUpdateRequest struct {
	Enabled  *bool                `json:"enabled"`
	Channels *[]slackChannelEntry `json:"channels"`
	DMPolicy *string              `json:"dm_policy"`
	// Tokens: nil = keep current value, "" = remove, non-empty = set.
	BotToken *string `json:"bot_token"`
	AppToken *string `json:"app_token"`
}

// PUT /api/v1/instances/{id}/slack
//
// Persists the structured Slack config and token env vars, then propagates:
// a token change restarts the container (env vars are injected at create
// time; the boot script re-applies channels.slack from OPENCLAW_INITIAL_SLACK),
// while a config-only change is pushed live over SSH with just a gateway
// restart.
func UpdateInstanceSlack(w http.ResponseWriter, r *http.Request) {
	inst, ok := resolveInstanceForChannelSettings(w, r)
	if !ok {
		return
	}

	var body instanceSlackUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	cfg, _ := parseSlackConfig(inst.SlackConfig)
	if body.Enabled != nil {
		cfg.Enabled = *body.Enabled
	}
	if body.Channels != nil {
		cfg.Channels = *body.Channels
	}
	if body.DMPolicy != nil {
		cfg.DMPolicy = *body.DMPolicy
	}
	if cfg.Channels == nil {
		cfg.Channels = []slackChannelEntry{}
	}
	if err := validateSlackConfig(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to serialize Slack config")
		return
	}
	configChanged := string(cfgJSON) != inst.SlackConfig
	if configChanged {
		if err := database.DB.Model(inst).Update("slack_config", string(cfgJSON)).Error; err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to save Slack config")
			return
		}
		inst.SlackConfig = string(cfgJSON)
	}

	// Apply token changes through the encrypted env-vars store.
	set := map[string]string{}
	var unset []string
	applyToken := func(field *string, name string) {
		if field == nil {
			return
		}
		if *field == "" {
			unset = append(unset, name)
		} else {
			set[name] = *field
		}
	}
	applyToken(body.BotToken, slackBotTokenEnvVar)
	applyToken(body.AppToken, slackAppTokenEnvVar)

	envVarsChanged := false
	if len(set) > 0 || len(unset) > 0 {
		updated, changed, err := ApplyEnvVarsDelta(inst.EnvVars, set, unset)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if changed {
			if err := database.DB.Model(inst).Update("env_vars", updated).Error; err != nil {
				writeError(w, http.StatusInternalServerError, "Failed to save Slack tokens")
				return
			}
			inst.EnvVars = updated
			envVarsChanged = true
		}
	}

	resp := slackResponseFor(*inst)

	// Propagate to the running container. Token changes need a container
	// restart (env vars only apply at create); the boot script then
	// re-applies channels.slack from OPENCLAW_INITIAL_SLACK, so the SSH push
	// is only needed for config-only edits.
	if inst.Status == "running" {
		if envVarsChanged {
			restartInstanceAsync(*inst, callerID(r))
			resp.Restarting = true
		} else if configChanged {
			if rendered := renderInitialSlackEnv(*inst); rendered != "" {
				pushSlackConfig(inst.ID, inst.Name, rendered)
			}
		}
	}

	writeJSON(w, http.StatusOK, resp)
}
