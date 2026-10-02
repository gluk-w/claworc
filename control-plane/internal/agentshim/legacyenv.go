package agentshim

import "sort"

// LegacyEnvFunc returns extra, adapter-specific env vars injected into an
// instance's container on top of the universal CLAWORC_* contract variables
// (docs/shim.md). It exists only for backward compatibility with images that
// predate the shim contract (e.g. OpenClaw's OPENCLAW_INITIAL_* seeds).
type LegacyEnvFunc func(routing LLMRouting, agentToken string) map[string]string

type legacyEnvEntry struct {
	names []string
	fn    LegacyEnvFunc
}

var legacyEnv = map[string]legacyEnvEntry{}

// RegisterLegacyEnv registers the legacy env provider for an agent type,
// together with the names it may set (reserved so users cannot shadow them).
// Adapters call this from init(); the map is not mutated afterwards.
func RegisterLegacyEnv(agentType string, names []string, fn LegacyEnvFunc) {
	legacyEnv[agentType] = legacyEnvEntry{names: names, fn: fn}
}

// LegacyEnv returns the legacy env vars for an agent type, or nil when the
// type registers none.
func LegacyEnv(agentType string, routing LLMRouting, agentToken string) map[string]string {
	e, ok := legacyEnv[agentType]
	if !ok || e.fn == nil {
		return nil
	}
	return e.fn(routing, agentToken)
}

// LegacyEnvNames returns every env var name any registered legacy provider
// may set, sorted.
func LegacyEnvNames() []string {
	var out []string
	for _, e := range legacyEnv {
		out = append(out, e.names...)
	}
	sort.Strings(out)
	return out
}
