// composio_skill.go generates an OpenClaw skill for a Composio connection. When
// a connection becomes ACTIVE the control plane builds a "claworc-<toolkit>"
// skill — a SKILL.md describing the toolkit and how the agent can call its tools
// through the local /connections/ broker — and deploys it into the instance.
//
// The Composio read calls (toolkit description, tool list) and the pure SKILL.md
// builder live here so they can be unit-tested without any SSH dependency. The
// actual deployment over SSH is driven from the handlers package, which owns the
// SSH manager and the skill-deploy primitives.

package internalproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ToolkitDetail is a Composio toolkit's display metadata.
type ToolkitDetail struct {
	Slug        string
	Name        string
	Description string
}

// ToolInfo is one tool exposed by a toolkit, including its input/output schema.
type ToolInfo struct {
	Slug         string
	Name         string
	Description  string
	InputParams  []ToolParam
	OutputParams []ToolParam
}

// ToolParam is a single input or output parameter of a tool.
type ToolParam struct {
	Name        string
	Type        string
	Description string
	Required    bool
}

// ConnectionSkillName returns the skill (and on-disk directory) name for a
// toolkit: "claworc-<slug>", lowercased.
func ConnectionSkillName(toolkitSlug string) string {
	return "claworc-" + strings.ToLower(strings.TrimSpace(toolkitSlug))
}

// GetToolkit fetches a toolkit's display metadata (name + description). Composio
// is inconsistent about where the description lives, so both the top level and a
// nested "meta" object are checked.
func (c *ComposioClient) GetToolkit(ctx context.Context, slug string) (*ToolkitDetail, error) {
	_, body, err := c.do(ctx, http.MethodGet, "/toolkits/"+url.PathEscape(slug), nil)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	// Some responses wrap the toolkit under "data".
	if data, ok := raw["data"].(map[string]any); ok {
		raw = data
	}
	meta, _ := raw["meta"].(map[string]any)
	detail := &ToolkitDetail{
		Slug:        firstNonEmpty(stringField(raw, "slug"), slug),
		Name:        firstNonEmpty(stringField(raw, "name"), metaField(meta, "name"), slug),
		Description: firstNonEmpty(stringField(raw, "description"), metaField(meta, "description")),
	}
	return detail, nil
}

// ListToolkitTools returns the tools available for a toolkit.
func (c *ComposioClient) ListToolkitTools(ctx context.Context, slug string) ([]ToolInfo, error) {
	q := url.Values{"toolkit_slugs": {slug}}
	_, body, err := c.do(ctx, http.MethodGet, "/tools?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	// Tolerate {items:[...]} or {data:[...]} or a bare array.
	var env struct {
		Items []json.RawMessage `json:"items"`
		Data  []json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(body, &env)
	raws := env.Items
	if len(raws) == 0 {
		raws = env.Data
	}
	if len(raws) == 0 {
		_ = json.Unmarshal(body, &raws)
	}
	out := make([]ToolInfo, 0, len(raws))
	for _, r := range raws {
		var t struct {
			Slug        string `json:"slug"`
			Name        string `json:"name"`
			Description string `json:"description"`
			// Composio is inconsistent about casing; accept both.
			InputParameters    json.RawMessage `json:"input_parameters"`
			InputParametersCC  json.RawMessage `json:"inputParameters"`
			OutputParameters   json.RawMessage `json:"output_parameters"`
			OutputParametersCC json.RawMessage `json:"outputParameters"`
		}
		if err := json.Unmarshal(r, &t); err != nil {
			continue
		}
		if t.Slug == "" {
			continue
		}
		out = append(out, ToolInfo{
			Slug:         t.Slug,
			Name:         firstNonEmpty(t.Name, t.Slug),
			Description:  t.Description,
			InputParams:  parseSchemaParams(firstNonEmptyRaw(t.InputParameters, t.InputParametersCC)),
			OutputParams: parseSchemaParams(firstNonEmptyRaw(t.OutputParameters, t.OutputParametersCC)),
		})
	}
	return out, nil
}

// parseSchemaParams extracts the ordered parameter list from a JSON-Schema
// object ({type:object, properties:{…}, required:[…]}). Property order is
// preserved from the raw JSON so the rendered skill is deterministic.
func parseSchemaParams(raw json.RawMessage) []ToolParam {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var schema struct {
		Properties json.RawMessage `json:"properties"`
		Required   []string        `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil
	}
	if len(bytes.TrimSpace(schema.Properties)) == 0 {
		return nil
	}
	required := map[string]bool{}
	for _, r := range schema.Required {
		required[r] = true
	}
	dec := json.NewDecoder(bytes.NewReader(schema.Properties))
	tok, err := dec.Token()
	if err != nil {
		return nil
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil
	}
	var out []ToolParam
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			break
		}
		name, _ := keyTok.(string)
		var prop struct {
			Type        any    `json:"type"`
			Description string `json:"description"`
		}
		if err := dec.Decode(&prop); err != nil {
			break
		}
		out = append(out, ToolParam{
			Name:        name,
			Type:        schemaType(prop.Type),
			Description: prop.Description,
			Required:    required[name],
		})
	}
	return out
}

// schemaType renders a JSON-Schema "type" (a string, or an array like
// ["string","null"]) as a single display string.
func schemaType(t any) string {
	switch v := t.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok && s != "null" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "|")
	}
	return ""
}

// GenerateConnectionSkill fetches the toolkit description and tool list from
// Composio and builds the SKILL.md file set. If the toolkit metadata can't be
// fetched it falls back to fallbackName and an empty description so a skill is
// still produced. connectionSecret is baked into the skill's example requests.
func GenerateConnectionSkill(ctx context.Context, client *ComposioClient, toolkitSlug, fallbackName, connectionSecret string) (string, map[string][]byte, error) {
	detail, err := client.GetToolkit(ctx, toolkitSlug)
	if err != nil || detail == nil {
		detail = &ToolkitDetail{Slug: toolkitSlug, Name: firstNonEmpty(fallbackName, toolkitSlug)}
	}
	if detail.Name == "" {
		detail.Name = firstNonEmpty(fallbackName, toolkitSlug)
	}
	tools, err := client.ListToolkitTools(ctx, toolkitSlug)
	if err != nil {
		// A missing tool list shouldn't block skill creation.
		tools = nil
	}
	name, files := BuildConnectionSkill(detail, tools, connectionSecret)
	return name, files, nil
}

// BuildConnectionSkill renders the SKILL.md for a connection. It is pure (no
// network/SSH) so it can be unit-tested directly. The connection secret's value
// is embedded into the Authorization header of every example request — the skill
// never references the CLAWORC_CONNECTION_SECRET env var.
func BuildConnectionSkill(detail *ToolkitDetail, tools []ToolInfo, connectionSecret string) (string, map[string][]byte) {
	skillName := ConnectionSkillName(detail.Slug)
	desc := fmt.Sprintf("Integration with %s.", detail.Name)
	if d := sanitizeInline(detail.Description); d != "" {
		desc = fmt.Sprintf("Integration with %s. %s", detail.Name, d)
	}

	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + skillName + "\n")
	b.WriteString("description: " + yamlDoubleQuoted(desc) + "\n")
	b.WriteString("---\n\n")

	b.WriteString("# " + detail.Name + " integration\n\n")
	b.WriteString(fmt.Sprintf("Use this skill to call **%s** tools. Requests go through the local "+
		"Claworc connections broker — you never handle the third-party credentials directly.\n\n", detail.Name))

	b.WriteString("## Discover the available tools\n\n")
	b.WriteString("```bash\n")
	b.WriteString("curl -s http://127.0.0.1:40001/connections/tools \\\n")
	b.WriteString("  -H " + shellSingleQuote("Authorization: Bearer "+connectionSecret) + "\n")
	b.WriteString("```\n\n")

	b.WriteString("## Tools\n\n")
	if len(tools) == 0 {
		b.WriteString("Call the discovery endpoint above to list the tools available for this connection.\n")
	} else {
		for i, t := range tools {
			if i > 0 {
				b.WriteString("\n")
			}
			writeToolSection(&b, t, connectionSecret)
		}
	}

	files := map[string][]byte{"SKILL.md": []byte(b.String())}
	return skillName, files
}

// writeToolSection renders one tool: description, input/output parameters, and a
// full example curl request (URL, Authorization header, and request body).
func writeToolSection(b *strings.Builder, t ToolInfo, connectionSecret string) {
	b.WriteString("### " + t.Slug + "\n\n")
	if d := sanitizeInline(t.Description); d != "" {
		b.WriteString(d + "\n\n")
	}
	if len(t.InputParams) > 0 {
		b.WriteString("**Input parameters:**\n\n")
		for _, p := range t.InputParams {
			b.WriteString(renderParam(p) + "\n")
		}
		b.WriteString("\n")
	}
	if len(t.OutputParams) > 0 {
		b.WriteString("**Output parameters:**\n\n")
		for _, p := range t.OutputParams {
			b.WriteString(renderParam(p) + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("**Example request:**\n\n")
	b.WriteString("```bash\n")
	b.WriteString("curl -s -X POST http://127.0.0.1:40001/connections/tools/execute/" + t.Slug + " \\\n")
	b.WriteString("  -H " + shellSingleQuote("Authorization: Bearer "+connectionSecret) + " \\\n")
	b.WriteString("  -H 'Content-Type: application/json' \\\n")
	b.WriteString("  -d " + shellSingleQuote(exampleBody(t.InputParams)) + "\n")
	b.WriteString("```\n")
}

// renderParam renders a single parameter as a markdown bullet:
// "- `name` (type, required) — description".
func renderParam(p ToolParam) string {
	attrs := make([]string, 0, 2)
	if p.Type != "" {
		attrs = append(attrs, p.Type)
	}
	if p.Required {
		attrs = append(attrs, "required")
	}
	line := "- `" + p.Name + "`"
	if len(attrs) > 0 {
		line += " (" + strings.Join(attrs, ", ") + ")"
	}
	if d := sanitizeInline(p.Description); d != "" {
		line += " — " + d
	}
	return line
}

// exampleBody builds a JSON request body with a placeholder value per input
// parameter, preserving parameter order: {"arguments":{"name":<placeholder>,…}}.
func exampleBody(params []ToolParam) string {
	var sb strings.Builder
	sb.WriteString(`{"arguments":{`)
	for i, p := range params {
		if i > 0 {
			sb.WriteString(",")
		}
		key, _ := json.Marshal(p.Name)
		sb.Write(key)
		sb.WriteString(":")
		sb.WriteString(examplePlaceholder(p.Type))
	}
	sb.WriteString("}}")
	return sb.String()
}

// examplePlaceholder returns a type-appropriate JSON placeholder value.
func examplePlaceholder(typ string) string {
	switch typ {
	case "integer", "number":
		return "0"
	case "boolean":
		return "false"
	case "array":
		return "[]"
	case "object":
		return "{}"
	default:
		return `"..."`
	}
}

// --- small helpers ----------------------------------------------------------

// firstNonEmptyRaw returns the first non-empty raw JSON message.
func firstNonEmptyRaw(vals ...json.RawMessage) json.RawMessage {
	for _, v := range vals {
		if len(bytes.TrimSpace(v)) > 0 {
			return v
		}
	}
	return nil
}

func metaField(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	return stringField(meta, key)
}

// sanitizeInline collapses whitespace/newlines so a description fits on one line.
func sanitizeInline(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// yamlDoubleQuoted renders s as a YAML double-quoted scalar (escaping backslash
// and double-quote) so arbitrary descriptions stay valid YAML.
func yamlDoubleQuoted(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return "\"" + s + "\""
}

// shellSingleQuote single-quotes s for safe embedding in a shell command.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
