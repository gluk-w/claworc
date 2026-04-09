package moderator

import (
	"path/filepath"
	"regexp"
	"strings"
)

// ExtractMentionedPaths scans agent transcript text for file paths the agent
// explicitly references and returns a deduplicated list of paths relative to
// the given workspace root. It looks for:
//   - backtick-quoted code spans containing path-like tokens with extensions
//   - phrases like "saved to X", "created X", "wrote X", "writing X"
//   - bare workspace-rooted paths (e.g. "/home/claworc/.openclaw/workspace/foo.py")
//
// Paths that don't resolve under workspaceRoot are dropped.
func ExtractMentionedPaths(transcript, workspaceRoot string) []string {
	candidates := map[string]struct{}{}

	for _, m := range backtickRe.FindAllStringSubmatch(transcript, -1) {
		if isPathLike(m[1]) {
			candidates[m[1]] = struct{}{}
		}
	}
	for _, m := range verbRe.FindAllStringSubmatch(transcript, -1) {
		token := strings.Trim(m[2], "`'\".,;:()[]{}")
		if isPathLike(token) {
			candidates[token] = struct{}{}
		}
	}
	for _, m := range bareRe.FindAllString(transcript, -1) {
		candidates[m] = struct{}{}
	}

	out := make([]string, 0, len(candidates))
	for raw := range candidates {
		rel, ok := relativizeToWorkspace(raw, workspaceRoot)
		if !ok {
			continue
		}
		out = append(out, rel)
	}
	return out
}

var (
	backtickRe = regexp.MustCompile("`([^`\\s][^`]{0,200})`")
	verbRe     = regexp.MustCompile(`(?i)\b(saved|wrote|writing|written|created|generated)\s+(?:to\s+)?([^\s,;]{1,300})`)
	bareRe     = regexp.MustCompile(`/[^\s'"` + "`" + `]+\.[A-Za-z0-9]{1,8}\b`)
	extRe      = regexp.MustCompile(`\.[A-Za-z0-9]{1,8}$`)
)

func isPathLike(s string) bool {
	if s == "" || strings.ContainsAny(s, " \n\t") {
		return false
	}
	return extRe.MatchString(s)
}

func relativizeToWorkspace(raw, workspace string) (string, bool) {
	clean := filepath.Clean(raw)
	if strings.HasPrefix(clean, workspace+"/") {
		rel := strings.TrimPrefix(clean, workspace+"/")
		if rel == "" || strings.Contains(rel, "..") {
			return "", false
		}
		return rel, true
	}
	if filepath.IsAbs(clean) {
		// Absolute path outside workspace — skip.
		return "", false
	}
	if strings.Contains(clean, "..") {
		return "", false
	}
	return clean, true
}
