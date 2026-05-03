// PKCE helpers and authorize-URL builder for the openai-codex-responses
// (ChatGPT subscription) login flow. These constants mirror those in
// control-plane/internal/llmgateway/oauth_codex.go — keep them in sync.
//
// The control plane never binds the redirect URI port; the user pastes the
// redirect URL back into the modal and the backend extracts the `code` from
// it. The verifier and state are held in React component state — close the
// modal mid-flow and nothing is persisted anywhere.

export const CODEX_OAUTH = {
  client_id: "app_EMoamEEZ73f0CkXaXp7hrann",
  redirect_uri: "http://localhost:1455/auth/callback",
  authorize_url: "https://auth.openai.com/oauth/authorize",
  scope: "openid profile email offline_access",
} as const;

function bytesToBase64Url(bytes: Uint8Array): string {
  let binary = "";
  for (let i = 0; i < bytes.length; i++) binary += String.fromCharCode(bytes[i]!);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export function randomBase64Url(byteLen: number): string {
  const buf = new Uint8Array(byteLen);
  crypto.getRandomValues(buf);
  return bytesToBase64Url(buf);
}

export async function pkceChallenge(verifier: string): Promise<string> {
  const data = new TextEncoder().encode(verifier);
  const digest = await crypto.subtle.digest("SHA-256", data);
  return bytesToBase64Url(new Uint8Array(digest));
}

export function buildCodexAuthorizeURL(state: string, challenge: string): string {
  const params = new URLSearchParams({
    response_type: "code",
    client_id: CODEX_OAUTH.client_id,
    redirect_uri: CODEX_OAUTH.redirect_uri,
    scope: CODEX_OAUTH.scope,
    state,
    code_challenge: challenge,
    code_challenge_method: "S256",
    id_token_add_organizations: "true",
    codex_cli_simplified_flow: "true",
    originator: "pi",
  });
  return `${CODEX_OAUTH.authorize_url}?${params.toString()}`;
}

export interface ParsedRedirect {
  code: string;
  state: string;
}

// extractCodeAndState parses what the user pasted: a full URL, a leading
// "?code=..." query string, or just "code=...&state=...". Throws on missing
// fields or an OAuth error response.
export function extractCodeAndState(pasted: string): ParsedRedirect {
  const trimmed = pasted.trim();
  if (!trimmed) throw new Error("redirect URL is empty");
  let q: URLSearchParams;
  if (trimmed.includes("://")) {
    const u = new URL(trimmed);
    q = u.searchParams;
  } else {
    q = new URLSearchParams(trimmed.replace(/^\?/, ""));
  }
  const oauthErr = q.get("error");
  if (oauthErr) throw new Error(`OpenAI returned error: ${oauthErr}`);
  const code = q.get("code") ?? "";
  const state = q.get("state") ?? "";
  if (!code) throw new Error("redirect URL does not contain an auth code");
  if (!state) throw new Error("redirect URL does not contain a state value");
  return { code, state };
}
