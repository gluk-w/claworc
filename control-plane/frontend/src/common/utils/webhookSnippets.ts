// Snippet generators for the per-URL "Copy as …" dropdown menu in the
// instance settings webhook section. All variants use the literal
// placeholders `<api key>`, `<session id>`, `<message>` — we never
// prefill real values into the clipboard.

export type SnippetVariant = "url" | "fetch" | "curl" | "powershell";

export function buildWebhookSnippet(variant: SnippetVariant, url: string): string {
  switch (variant) {
    case "url":
      return url;
    case "fetch":
      return [
        `await fetch("${url}", {`,
        `  method: "POST",`,
        `  headers: {`,
        `    "Authorization": "Bearer <api key>",`,
        `    "Content-Type": "application/json",`,
        `  },`,
        `  body: JSON.stringify({ session_name: "<session name>", message: "<message>" }),`,
        `});`,
      ].join("\n");
    case "curl":
      return [
        `curl -X POST "${url}" \\`,
        `  -H "Authorization: Bearer <api key>" \\`,
        `  -H "Content-Type: application/json" \\`,
        `  -d '{"session_name":"<session name>","message":"<message>"}'`,
      ].join("\n");
    case "powershell":
      return [
        `Invoke-RestMethod -Method Post -Uri "${url}" \``,
        `  -Headers @{ "Authorization" = "Bearer <api key>" } \``,
        `  -ContentType "application/json" \``,
        `  -Body '{"session_name":"<session name>","message":"<message>"}'`,
      ].join("\n");
  }
}
