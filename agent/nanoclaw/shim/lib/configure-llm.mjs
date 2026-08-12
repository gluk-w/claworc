// configure-llm.mjs — route NanoClaw's LLM traffic through the Claworc LLM
// proxy (docs/shim.md). Reads the generic routing document on stdin.
//
// NanoClaw's agent-runner talks to Anthropic through the Claude Agent SDK,
// which honors ANTHROPIC_BASE_URL + ANTHROPIC_AUTH_TOKEN — the exact wiring
// upstream NanoClaw's own setup uses (src/providers/claude.ts). This shim
// stores the routing state in one fully-managed JSON file
// (<state>/llm.json); the svc-agent supervisor injects the env pair into
// every agent-runner it spawns, and the default model is written into the
// agent's container.json (NanoClaw's real per-group config file). Both
// writes are deterministic full rewrites — idempotent by construction.
//
// Virtual-key selection: the provider whose dialect is Anthropic (api_type
// mentioning "anthropic", else key "anthropic", else the first entry), per
// the declared llm.styles ["anthropic"].
import fs from 'node:fs';
import path from 'node:path';

import { AGENT_DIR, LLM_CONFIG_PATH, STATE_DIR, jsonError } from './shimlib.mjs';

function fail(msg) {
  jsonError(msg);
  process.exit(6);
}

let doc;
try {
  doc = JSON.parse((await Bun.stdin.text()).toString());
} catch (err) {
  fail(`invalid JSON routing document: ${err.message}`);
}
if (typeof doc !== 'object' || doc === null || Array.isArray(doc)) {
  fail('routing document must be a JSON object');
}

const style = doc.style || 'anthropic';
if (style !== 'anthropic') {
  fail(`unsupported llm style '${style}': this image only speaks the anthropic dialect`);
}

const providers = doc.providers ?? [];
if (!Array.isArray(providers)) fail('providers must be an array');
const proxyUrl = doc.proxy_url || '';
if (providers.length > 0 && !proxyUrl) fail('proxy_url is required when providers are present');

let provider = null;
if (providers.length > 0) {
  provider =
    providers.find((p) => p && typeof p.api_type === 'string' && p.api_type.includes('anthropic')) ||
    providers.find((p) => p && p.key === 'anthropic') ||
    providers[0];
  if (typeof provider !== 'object' || provider === null) fail('providers entries must be objects');
}

const defaultModel = doc.default_model || '';
const fallbackModels = (doc.fallback_models || []).filter((m) => typeof m === 'string' && m);

fs.mkdirSync(STATE_DIR, { recursive: true });
fs.mkdirSync(AGENT_DIR, { recursive: true });

function writeAtomic(file, content) {
  const tmp = `${file}.tmp-${process.pid}`;
  fs.writeFileSync(tmp, content, { mode: 0o644 });
  fs.renameSync(tmp, file);
}

if (!proxyUrl) {
  // Routing removed — drop the managed state so runners fall back to
  // whatever ambient credentials exist (normally none). Idempotent.
  fs.rmSync(LLM_CONFIG_PATH, { force: true });
} else {
  // Fully-managed file: deterministic key order, whole-file rewrite.
  writeAtomic(
    LLM_CONFIG_PATH,
    JSON.stringify(
      {
        _comment: 'Managed by the Claworc shim configure-llm verb - do not edit.',
        style: 'anthropic',
        proxy_url: proxyUrl,
        api_key: provider?.api_key || '',
        default_model: defaultModel,
        fallback_models: fallbackModels,
      },
      null,
      2,
    ) + '\n',
  );
}

// Default model lands in NanoClaw's own per-group config (container.json,
// read by the agent-runner at startup). Only the managed keys are rewritten;
// user-added keys (mcpServers, assistantName, ...) are preserved.
const containerJsonPath = path.join(AGENT_DIR, 'container.json');
let cfg = {};
try {
  cfg = JSON.parse(fs.readFileSync(containerJsonPath, 'utf8'));
  if (typeof cfg !== 'object' || cfg === null || Array.isArray(cfg)) cfg = {};
} catch {
  // No config yet (first boot runs configure-llm before init-agent-seed):
  // start from the image's baked defaults so their non-managed keys
  // (maxMessagesPerPrompt, mcpServers) aren't lost.
  try {
    cfg = JSON.parse(fs.readFileSync('/defaults/container.json', 'utf8'));
    if (typeof cfg !== 'object' || cfg === null || Array.isArray(cfg)) cfg = {};
  } catch {
    cfg = {};
  }
}
cfg.provider = 'claude';
if (defaultModel) {
  // Model ids are passed through as-is; the Claworc LLM proxy owns the
  // mapping from routing-document ids to upstream models.
  cfg.model = defaultModel;
} else {
  delete cfg.model;
}
if (!cfg.assistantName) cfg.assistantName = 'NanoClaw';
writeAtomic(containerJsonPath, JSON.stringify(cfg, null, 2) + '\n');

process.exit(0);
