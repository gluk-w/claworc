package internalproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// fakeComposio spins up a fake Composio upstream and points the client at it.
func fakeComposio(t *testing.T, h http.HandlerFunc) *ComposioClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := NewComposioClient("test-key")
	c.baseURL = srv.URL
	return c
}

func TestGetToolkit_ParsesDescription(t *testing.T) {
	c := fakeComposio(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/toolkits/gmail" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"slug":"gmail","name":"Gmail","description":"Send and read email."}`))
	})
	d, err := c.GetToolkit(context.Background(), "gmail")
	if err != nil {
		t.Fatalf("GetToolkit: %v", err)
	}
	if d.Name != "Gmail" || d.Description != "Send and read email." {
		t.Errorf("got %+v", d)
	}
}

func TestGetToolkit_MetaDescriptionFallback(t *testing.T) {
	c := fakeComposio(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"slug":"gmail","meta":{"name":"Gmail","description":"Email toolkit."}}`))
	})
	d, err := c.GetToolkit(context.Background(), "gmail")
	if err != nil {
		t.Fatalf("GetToolkit: %v", err)
	}
	if d.Name != "Gmail" || d.Description != "Email toolkit." {
		t.Errorf("got %+v", d)
	}
}

func TestListToolkitTools_Parses(t *testing.T) {
	c := fakeComposio(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("toolkit_slugs"); got != "gmail" {
			t.Errorf("toolkit_slugs = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[
			{"slug":"GMAIL_SEND_EMAIL","name":"Send Email","description":"Send an email.",
				"input_parameters":{"type":"object","properties":{
					"recipient_email":{"type":"string","description":"Recipient address."},
					"subject":{"type":["string","null"],"description":"Subject."}
				},"required":["recipient_email"]},
				"output_parameters":{"type":"object","properties":{
					"message_id":{"type":"string","description":"Sent message id."}
				}}},
			{"slug":"GMAIL_FETCH_EMAILS","description":"Fetch emails."},
			{"description":"missing slug, skipped"}
		]}`))
	})
	tools, err := c.ListToolkitTools(context.Background(), "gmail")
	if err != nil {
		t.Fatalf("ListToolkitTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d: %+v", len(tools), tools)
	}
	if tools[0].Slug != "GMAIL_SEND_EMAIL" || tools[1].Slug != "GMAIL_FETCH_EMAILS" {
		t.Errorf("got %+v", tools)
	}

	// Input parameters parsed in order, with type/required/description.
	in := tools[0].InputParams
	if len(in) != 2 {
		t.Fatalf("expected 2 input params, got %d: %+v", len(in), in)
	}
	if in[0] != (ToolParam{Name: "recipient_email", Type: "string", Description: "Recipient address.", Required: true}) {
		t.Errorf("input[0] = %+v", in[0])
	}
	// type ["string","null"] collapses to "string"; not in required list.
	if in[1] != (ToolParam{Name: "subject", Type: "string", Description: "Subject.", Required: false}) {
		t.Errorf("input[1] = %+v", in[1])
	}
	out := tools[0].OutputParams
	if len(out) != 1 || out[0].Name != "message_id" || out[0].Type != "string" {
		t.Errorf("output params = %+v", out)
	}
	// Tool without schemas has no params.
	if len(tools[1].InputParams) != 0 || len(tools[1].OutputParams) != 0 {
		t.Errorf("expected no params for second tool, got %+v", tools[1])
	}
}

func TestBuildConnectionSkill(t *testing.T) {
	detail := &ToolkitDetail{Slug: "Gmail", Name: "Gmail", Description: "Send and\nread email."}
	tools := []ToolInfo{
		{Slug: "GMAIL_SEND_EMAIL", Description: "Send an email."},
		{Slug: "GMAIL_FETCH_EMAILS", Description: "Fetch emails."},
	}
	secret := "claworc-cs-deadbeef"

	name, files := BuildConnectionSkill(detail, tools, secret)
	if name != "claworc-gmail" {
		t.Errorf("name = %q, want claworc-gmail", name)
	}
	md, ok := files["SKILL.md"]
	if !ok {
		t.Fatal("SKILL.md not produced")
	}
	content := string(md)

	// Frontmatter must parse and carry the expected name/description.
	fm := parseFrontmatter(t, content)
	if fm.Name != "claworc-gmail" {
		t.Errorf("frontmatter name = %q", fm.Name)
	}
	if fm.Description != "Integration with Gmail. Send and read email." {
		t.Errorf("frontmatter description = %q", fm.Description)
	}

	// Secret value embedded in the Authorization header; env var never named.
	if !strings.Contains(content, "Authorization: Bearer "+secret) {
		t.Error("secret value not embedded in Authorization header")
	}
	if strings.Contains(content, "CLAWORC_CONNECTION_SECRET") {
		t.Error("skill must not mention CLAWORC_CONNECTION_SECRET")
	}

	// Every tool slug appears in the body.
	for _, tl := range tools {
		if !strings.Contains(content, tl.Slug) {
			t.Errorf("tool %q missing from skill body", tl.Slug)
		}
	}
}

func TestBuildConnectionSkill_NoDescriptionOrTools(t *testing.T) {
	name, files := BuildConnectionSkill(&ToolkitDetail{Slug: "slack", Name: "Slack"}, nil, "secret")
	if name != "claworc-slack" {
		t.Errorf("name = %q", name)
	}
	fm := parseFrontmatter(t, string(files["SKILL.md"]))
	if fm.Description != "Integration with Slack." {
		t.Errorf("description = %q", fm.Description)
	}
}

func TestGenerateConnectionSkill_EndToEnd(t *testing.T) {
	c := fakeComposio(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/toolkits/gmail":
			_, _ = w.Write([]byte(`{"slug":"gmail","name":"Gmail","description":"Email."}`))
		case r.URL.Path == "/tools":
			_, _ = w.Write([]byte(`{"items":[{"slug":"GMAIL_SEND_EMAIL","description":"Send."}]}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	})
	name, files, err := GenerateConnectionSkill(context.Background(), c, "gmail", "Gmail", "sek")
	if err != nil {
		t.Fatalf("GenerateConnectionSkill: %v", err)
	}
	if name != "claworc-gmail" {
		t.Errorf("name = %q", name)
	}
	content := string(files["SKILL.md"])
	if !strings.Contains(content, "GMAIL_SEND_EMAIL") {
		t.Error("tool missing from generated skill")
	}
}

func TestGenerateConnectionSkill_ToolkitFetchFallback(t *testing.T) {
	c := fakeComposio(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/toolkits/gmail" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	})
	name, files, err := GenerateConnectionSkill(context.Background(), c, "gmail", "Gmail Fallback", "sek")
	if err != nil {
		t.Fatalf("GenerateConnectionSkill: %v", err)
	}
	if name != "claworc-gmail" {
		t.Errorf("name = %q", name)
	}
	fm := parseFrontmatter(t, string(files["SKILL.md"]))
	if fm.Description != "Integration with Gmail Fallback." {
		t.Errorf("description = %q", fm.Description)
	}
}

// TestGenerateConnectionSkill_FullContent drives GenerateConnectionSkill against
// mocked Composio toolkit + tools endpoints (with full input/output parameter
// schemas) and asserts the entire generated SKILL.md byte-for-byte.
func TestGenerateConnectionSkill_FullContent(t *testing.T) {
	c := fakeComposio(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/toolkits/gmail":
			_, _ = w.Write([]byte(`{
				"slug": "gmail",
				"name": "Gmail",
				"description": "Send, read, and manage email from Gmail.",
				"meta": {"logo": "https://example.com/gmail.png"}
			}`))
		case r.URL.Path == "/tools":
			if got := r.URL.Query().Get("toolkit_slugs"); got != "gmail" {
				t.Errorf("toolkit_slugs = %q, want gmail", got)
			}
			_, _ = w.Write([]byte(`{"items": [
				{
					"slug": "GMAIL_SEND_EMAIL",
					"name": "Send Email",
					"description": "Send an email to one or more recipients.",
					"input_parameters": {
						"type": "object",
						"properties": {
							"recipient_email": {"type": "string", "description": "The recipient's email address."},
							"subject": {"type": "string", "description": "The email subject line."},
							"body": {"type": "string", "description": "The plain-text email body."}
						},
						"required": ["recipient_email", "subject"]
					},
					"output_parameters": {
						"type": "object",
						"properties": {
							"message_id": {"type": "string", "description": "The ID of the sent message."},
							"thread_id": {"type": "string", "description": "The thread the message belongs to."}
						}
					}
				},
				{
					"slug": "GMAIL_FETCH_EMAILS",
					"name": "Fetch Emails",
					"description": "Fetch a list of emails from the mailbox.",
					"input_parameters": {
						"type": "object",
						"properties": {
							"max_results": {"type": "integer", "description": "Maximum number of emails to return."},
							"query": {"type": "string", "description": "Gmail search query."}
						}
					},
					"output_parameters": {
						"type": "object",
						"properties": {
							"messages": {"type": "array", "description": "The matching email messages."}
						}
					}
				}
			]}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	})

	name, files, err := GenerateConnectionSkill(context.Background(), c, "gmail", "Gmail", "claworc-cs-abc123")
	if err != nil {
		t.Fatalf("GenerateConnectionSkill: %v", err)
	}
	if name != "claworc-gmail" {
		t.Errorf("name = %q, want claworc-gmail", name)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	const want = "---\n" +
		"name: claworc-gmail\n" +
		"description: \"Integration with Gmail. Send, read, and manage email from Gmail.\"\n" +
		"---\n" +
		"\n" +
		"# Gmail integration\n" +
		"\n" +
		"Use this skill to call **Gmail** tools. Requests go through the local Claworc connections broker — you never handle the third-party credentials directly.\n" +
		"\n" +
		"## Discover the available tools\n" +
		"\n" +
		"```bash\n" +
		"curl -s http://127.0.0.1:40001/connections/tools \\\n" +
		"  -H 'Authorization: Bearer claworc-cs-abc123'\n" +
		"```\n" +
		"\n" +
		"## Tools\n" +
		"\n" +
		"### GMAIL_SEND_EMAIL\n" +
		"\n" +
		"Send an email to one or more recipients.\n" +
		"\n" +
		"**Input parameters:**\n" +
		"\n" +
		"- `recipient_email` (string, required) — The recipient's email address.\n" +
		"- `subject` (string, required) — The email subject line.\n" +
		"- `body` (string) — The plain-text email body.\n" +
		"\n" +
		"**Output parameters:**\n" +
		"\n" +
		"- `message_id` (string) — The ID of the sent message.\n" +
		"- `thread_id` (string) — The thread the message belongs to.\n" +
		"\n" +
		"**Example request:**\n" +
		"\n" +
		"```bash\n" +
		"curl -s -X POST http://127.0.0.1:40001/connections/tools/execute/GMAIL_SEND_EMAIL \\\n" +
		"  -H 'Authorization: Bearer claworc-cs-abc123' \\\n" +
		"  -H 'Content-Type: application/json' \\\n" +
		"  -d '{\"arguments\":{\"recipient_email\":\"...\",\"subject\":\"...\",\"body\":\"...\"}}'\n" +
		"```\n" +
		"\n" +
		"### GMAIL_FETCH_EMAILS\n" +
		"\n" +
		"Fetch a list of emails from the mailbox.\n" +
		"\n" +
		"**Input parameters:**\n" +
		"\n" +
		"- `max_results` (integer) — Maximum number of emails to return.\n" +
		"- `query` (string) — Gmail search query.\n" +
		"\n" +
		"**Output parameters:**\n" +
		"\n" +
		"- `messages` (array) — The matching email messages.\n" +
		"\n" +
		"**Example request:**\n" +
		"\n" +
		"```bash\n" +
		"curl -s -X POST http://127.0.0.1:40001/connections/tools/execute/GMAIL_FETCH_EMAILS \\\n" +
		"  -H 'Authorization: Bearer claworc-cs-abc123' \\\n" +
		"  -H 'Content-Type: application/json' \\\n" +
		"  -d '{\"arguments\":{\"max_results\":0,\"query\":\"...\"}}'\n" +
		"```\n"

	got := string(files["SKILL.md"])
	if got != want {
		t.Errorf("SKILL.md mismatch.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// parseFrontmatter mirrors handlers.parseSkillFrontmatter for assertions.
func parseFrontmatter(t *testing.T, content string) struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
} {
	t.Helper()
	if !strings.HasPrefix(content, "---") {
		t.Fatal("missing frontmatter opening ---")
	}
	rest := content[3:]
	end := strings.Index(rest, "\n---")
	if end == -1 {
		t.Fatal("missing frontmatter closing ---")
	}
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(rest[:end]), &fm); err != nil {
		t.Fatalf("parse frontmatter: %v", err)
	}
	if fm.Name == "" || fm.Description == "" {
		t.Fatalf("frontmatter missing name/description: %+v", fm)
	}
	return fm
}
