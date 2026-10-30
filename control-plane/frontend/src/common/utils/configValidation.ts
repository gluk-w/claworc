/**
 * Validates agent config content by the language the shim declares for the
 * file. The shim's config-set stores content verbatim, so syntax checking
 * happens here before saving. Resolves to an error message, or null when the
 * content is valid (or the language has no validator).
 */
export async function validateConfig(content: string, language: string | undefined): Promise<string | null> {
  switch ((language || "json").toLowerCase()) {
    case "json":
      try {
        JSON.parse(content);
        return null;
      } catch (e) {
        return `Invalid JSON: ${e instanceof Error ? e.message : String(e)}`;
      }
    case "yaml":
    case "yml": {
      // Loaded on demand: keeps the YAML parser out of the main bundle.
      const { parseDocument } = await import("yaml");
      const err = parseDocument(content).errors[0];
      return err ? `Invalid YAML: ${err.message}` : null;
    }
    default:
      return null;
  }
}
