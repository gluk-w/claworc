package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

// The Discord bot token is stored in the instance's encrypted EnvVars map
// under this name. OpenClaw reads it from the environment for the default
// Discord account whenever channels.discord.token is unset, so the rendered
// config block never contains the token. Discord needs no app-level token —
// OpenClaw only connects via the Gateway/WebSocket transport.
const discordBotTokenEnvVar = "DISCORD_BOT_TOKEN"

// discordSnowflakeRegex matches raw Discord guild/channel IDs (numeric
// snowflakes). Names silently fail to route, so reject them up front.
var discordSnowflakeRegex = regexp.MustCompile(`^[0-9]{15,22}$`)

// discordChannelRule is one allowlist entry in the per-instance config: a
// guild plus optionally one of its channels. An entry with no channel ID
// allows the whole guild.
type discordChannelRule struct {
	GuildID string `json:"guild_id"`
	// ChannelID empty means the entire guild is allowed.
	ChannelID string `json:"channel_id,omitempty"`
	// RequireMention nil means OpenClaw's default (true): the bot only
	// responds when @-mentioned.
	RequireMention *bool `json:"require_mention,omitempty"`
}

// instanceDiscordConfig is the JSON shape persisted in Instance.DiscordConfig.
type instanceDiscordConfig struct {
	Enabled  bool                 `json:"enabled"`
	Channels []discordChannelRule `json:"channels"`
	// DMPolicy: "" (OpenClaw default, pairing), "pairing", "open", "disabled".
	DMPolicy string `json:"dm_policy,omitempty"`
}

func parseDiscordConfig(raw string) (instanceDiscordConfig, bool) {
	var cfg instanceDiscordConfig
	if raw == "" {
		return cfg, false
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return instanceDiscordConfig{}, false
	}
	if cfg.Channels == nil {
		cfg.Channels = []discordChannelRule{}
	}
	return cfg, true
}

// validateDiscordConfig normalizes IDs (trim, strip a leading '#') and
// rejects anything that isn't a numeric snowflake. Mixing a whole-guild
// entry with specific-channel entries for the same guild is rejected because
// the rendered config could only express one of the two.
func validateDiscordConfig(cfg *instanceDiscordConfig) error {
	type guildState struct{ whole, channels bool }
	guilds := map[string]*guildState{}
	seen := map[string]bool{}
	normalized := make([]discordChannelRule, 0, len(cfg.Channels))
	for _, rule := range cfg.Channels {
		gid := strings.TrimSpace(rule.GuildID)
		if !discordSnowflakeRegex.MatchString(gid) {
			return fmt.Errorf("invalid Discord server ID %q: use the numeric server (guild) ID, not its name", rule.GuildID)
		}
		cid := strings.TrimPrefix(strings.TrimSpace(rule.ChannelID), "#")
		if cid != "" && !discordSnowflakeRegex.MatchString(cid) {
			return fmt.Errorf("invalid Discord channel ID %q: use the numeric channel ID, not the channel name", rule.ChannelID)
		}
		st := guilds[gid]
		if st == nil {
			st = &guildState{}
			guilds[gid] = st
		}
		if cid == "" {
			st.whole = true
		} else {
			st.channels = true
		}
		if st.whole && st.channels {
			return fmt.Errorf("server %s mixes a whole-server entry with specific channels: keep either the whole server or a channel list", gid)
		}
		key := gid + "/" + cid
		if seen[key] {
			continue
		}
		seen[key] = true
		rule.GuildID = gid
		rule.ChannelID = cid
		normalized = append(normalized, rule)
	}
	cfg.Channels = normalized
	switch cfg.DMPolicy {
	case "", "pairing", "open", "disabled":
	default:
		return fmt.Errorf("invalid dm_policy %q: must be one of pairing, open, disabled", cfg.DMPolicy)
	}
	return nil
}

// renderDiscordChannelsJSON renders the stored config into the OpenClaw
// `channels.discord` block. The token is deliberately omitted — OpenClaw
// falls back to DISCORD_BOT_TOKEN from the environment for the default
// account. groupPolicy is always written explicitly: unlike Slack, OpenClaw's
// Discord runtime defaults to "open" when unset.
func renderDiscordChannelsJSON(cfg instanceDiscordConfig) (string, error) {
	block := map[string]interface{}{"enabled": cfg.Enabled}
	if cfg.Enabled {
		block["groupPolicy"] = "allowlist"
		if len(cfg.Channels) > 0 {
			guilds := map[string]map[string]interface{}{}
			for _, rule := range cfg.Channels {
				guild := guilds[rule.GuildID]
				if guild == nil {
					guild = map[string]interface{}{}
					guilds[rule.GuildID] = guild
				}
				requireMention := rule.RequireMention == nil || *rule.RequireMention
				if rule.ChannelID == "" {
					// Whole guild: mention gating lives at the guild level.
					guild["requireMention"] = requireMention
					continue
				}
				channels, _ := guild["channels"].(map[string]interface{})
				if channels == nil {
					channels = map[string]interface{}{}
					guild["channels"] = channels
				}
				channels[rule.ChannelID] = map[string]interface{}{
					"allow":          true,
					"requireMention": requireMention,
				}
			}
			block["guilds"] = guilds
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

// renderInitialDiscordEnv returns the OPENCLAW_INITIAL_DISCORD value for an
// instance, or "" when Discord has never been configured through Claworc (so
// a manually-edited channels.discord block in the agent's config is left
// alone).
func renderInitialDiscordEnv(inst database.Instance) string {
	cfg, ok := parseDiscordConfig(inst.DiscordConfig)
	if !ok {
		return ""
	}
	rendered, err := renderDiscordChannelsJSON(cfg)
	if err != nil {
		log.Printf("Failed to render Discord config for instance %d: %v", inst.ID, err)
		return ""
	}
	return rendered
}

// applyDiscordConfig writes the channels.discord block into the agent's
// OpenClaw config over an established SSH connection and restarts the gateway
// so it takes effect. The path is cleared first because `config set`
// deep-merges map values — guilds/channels removed in Claworc would otherwise
// linger.
func applyDiscordConfig(ctx context.Context, agent sshproxy.Instance, name, channelsJSON string) {
	_, _, _, _ = agent.ExecOpenclaw(ctx, "config", "unset", "channels.discord")
	_, stderr, code, err := agent.ExecOpenclaw(ctx, "config", "set", "channels.discord", channelsJSON, "--json")
	if err != nil {
		log.Printf("Error setting channels.discord for %s: %v", utils.SanitizeForLog(name), err)
		return
	}
	if code != 0 {
		log.Printf("Failed to set channels.discord for %s: %s", utils.SanitizeForLog(name), utils.SanitizeForLog(stderr))
		return
	}
	if _, _, _, err := agent.ExecOpenclaw(ctx, "gateway", "stop"); err != nil {
		log.Printf("Error restarting gateway for %s after Discord config change: %v", utils.SanitizeForLog(name), err)
	}
}

// pushDiscordConfig is the async best-effort wrapper around
// applyDiscordConfig for a running instance (mirrors pushSlackConfig). A
// stopped or unreachable instance picks the config up at next boot via
// OPENCLAW_INITIAL_DISCORD.
func pushDiscordConfig(instanceID uint, name, channelsJSON string) {
	if SSHMgr == nil || channelsJSON == "" {
		return
	}
	go func() {
		ctx := context.Background()
		sshClient, err := SSHMgr.WaitForSSH(ctx, instanceID, 30*time.Second)
		if err != nil {
			log.Printf("discord-config: no SSH connection for instance %d, skipping OpenClaw config push: %v", instanceID, err)
			return
		}
		applyDiscordConfig(ctx, sshproxy.NewSSHInstance(sshClient), name, channelsJSON)
	}()
}

// instanceDiscordCreateRequest is the create-time Discord payload (structured
// config plus the bot token) accepted by CreateInstance, so a new agent
// connects to Discord on first boot.
type instanceDiscordCreateRequest struct {
	instanceDiscordConfig
	BotToken string `json:"bot_token"`
}

type instanceDiscordResponse struct {
	Configured     bool                 `json:"configured"`
	Enabled        bool                 `json:"enabled"`
	Channels       []discordChannelRule `json:"channels"`
	DMPolicy       string               `json:"dm_policy"`
	HasBotToken    bool                 `json:"has_bot_token"`
	BotTokenMasked string               `json:"bot_token_masked,omitempty"`
	Restarting     bool                 `json:"restarting,omitempty"`
}

func discordResponseFor(inst database.Instance) instanceDiscordResponse {
	cfg, configured := parseDiscordConfig(inst.DiscordConfig)
	resp := instanceDiscordResponse{
		Configured: configured,
		Enabled:    cfg.Enabled,
		Channels:   cfg.Channels,
		DMPolicy:   cfg.DMPolicy,
	}
	if resp.Channels == nil {
		resp.Channels = []discordChannelRule{}
	}
	// Token presence reflects what the container actually receives: global
	// defaults merged with per-instance overrides.
	envVars := map[string]string{}
	MergeUserEnvVars(envVars, LoadGlobalEnvVars(), LoadInstanceEnvVars(inst))
	if v, ok := envVars[discordBotTokenEnvVar]; ok && v != "" {
		resp.HasBotToken = true
		resp.BotTokenMasked = utils.Mask(v)
	}
	return resp
}

// GET /api/v1/instances/{id}/discord
func GetInstanceDiscord(w http.ResponseWriter, r *http.Request) {
	inst, ok := resolveInstanceForChannelSettings(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, discordResponseFor(*inst))
}

type instanceDiscordUpdateRequest struct {
	Enabled  *bool                 `json:"enabled"`
	Channels *[]discordChannelRule `json:"channels"`
	DMPolicy *string               `json:"dm_policy"`
	// Token: nil = keep current value, "" = remove, non-empty = set.
	BotToken *string `json:"bot_token"`
}

// PUT /api/v1/instances/{id}/discord
//
// Persists the structured Discord config and the token env var, then
// propagates: a token change restarts the container (env vars are injected at
// create time; the boot script re-applies channels.discord from
// OPENCLAW_INITIAL_DISCORD), while a config-only change is pushed live over
// SSH with just a gateway restart.
func UpdateInstanceDiscord(w http.ResponseWriter, r *http.Request) {
	inst, ok := resolveInstanceForChannelSettings(w, r)
	if !ok {
		return
	}

	var body instanceDiscordUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	cfg, _ := parseDiscordConfig(inst.DiscordConfig)
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
		cfg.Channels = []discordChannelRule{}
	}
	if err := validateDiscordConfig(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to serialize Discord config")
		return
	}
	configChanged := string(cfgJSON) != inst.DiscordConfig
	if configChanged {
		if err := database.DB.Model(inst).Update("discord_config", string(cfgJSON)).Error; err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to save Discord config")
			return
		}
		inst.DiscordConfig = string(cfgJSON)
	}

	// Apply the token change through the encrypted env-vars store.
	envVarsChanged := false
	if body.BotToken != nil {
		set := map[string]string{}
		var unset []string
		if *body.BotToken == "" {
			unset = append(unset, discordBotTokenEnvVar)
		} else {
			set[discordBotTokenEnvVar] = *body.BotToken
		}
		updated, changed, err := ApplyEnvVarsDelta(inst.EnvVars, set, unset)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if changed {
			if err := database.DB.Model(inst).Update("env_vars", updated).Error; err != nil {
				writeError(w, http.StatusInternalServerError, "Failed to save Discord token")
				return
			}
			inst.EnvVars = updated
			envVarsChanged = true
		}
	}

	resp := discordResponseFor(*inst)

	// Propagate to the running container. A token change needs a container
	// restart (env vars only apply at create); the boot script then
	// re-applies channels.discord from OPENCLAW_INITIAL_DISCORD, so the SSH
	// push is only needed for config-only edits.
	if inst.Status == "running" {
		if envVarsChanged {
			restartInstanceAsync(*inst, callerID(r))
			resp.Restarting = true
		} else if configChanged {
			if rendered := renderInitialDiscordEnv(*inst); rendered != "" {
				pushDiscordConfig(inst.ID, inst.Name, rendered)
			}
		}
	}

	writeJSON(w, http.StatusOK, resp)
}
