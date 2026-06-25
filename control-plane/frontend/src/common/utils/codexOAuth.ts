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
  // crypto.subtle is only available in secure contexts (HTTPS or localhost).
  // Fall back to a pure-JS SHA-256 when serving over plain HTTP on a LAN IP.
  const subtle = (globalThis.crypto as Crypto | undefined)?.subtle;
  const digest = subtle
    ? new Uint8Array(await subtle.digest("SHA-256", data))
    : sha256(data);
  return bytesToBase64Url(digest);
}

// Minimal SHA-256 (FIPS 180-4) for insecure-context fallback.
function sha256(msg: Uint8Array): Uint8Array {
  const K = new Uint32Array([
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1,
    0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
    0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
    0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
    0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
    0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b,
    0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a,
    0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
    0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
  ]);
  const H = new Uint32Array([
    0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c,
    0x1f83d9ab, 0x5be0cd19,
  ]);
  const bitLen = msg.length * 8;
  const padLen = (msg.length % 64 < 56 ? 56 : 120) - (msg.length % 64);
  const buf = new Uint8Array(msg.length + padLen + 8);
  buf.set(msg);
  buf[msg.length] = 0x80;
  // 64-bit big-endian length (top 32 bits stay zero — JS bitwise is 32-bit)
  const view = new DataView(buf.buffer);
  view.setUint32(buf.length - 4, bitLen >>> 0, false);
  view.setUint32(buf.length - 8, Math.floor(bitLen / 0x100000000), false);

  const W = new Uint32Array(64);
  const rotr = (x: number, n: number) => (x >>> n) | (x << (32 - n));
  for (let off = 0; off < buf.length; off += 64) {
    for (let i = 0; i < 16; i++) W[i] = view.getUint32(off + i * 4, false);
    for (let i = 16; i < 64; i++) {
      const s0 = rotr(W[i - 15]!, 7) ^ rotr(W[i - 15]!, 18) ^ (W[i - 15]! >>> 3);
      const s1 = rotr(W[i - 2]!, 17) ^ rotr(W[i - 2]!, 19) ^ (W[i - 2]! >>> 10);
      W[i] = (W[i - 16]! + s0 + W[i - 7]! + s1) >>> 0;
    }
    let [a, b, c, d, e, f, g, h] = [H[0]!, H[1]!, H[2]!, H[3]!, H[4]!, H[5]!, H[6]!, H[7]!];
    for (let i = 0; i < 64; i++) {
      const S1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
      const ch = (e & f) ^ (~e & g);
      const t1 = (h + S1 + ch + K[i]! + W[i]!) >>> 0;
      const S0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
      const mj = (a & b) ^ (a & c) ^ (b & c);
      const t2 = (S0 + mj) >>> 0;
      h = g; g = f; f = e;
      e = (d + t1) >>> 0;
      d = c; c = b; b = a;
      a = (t1 + t2) >>> 0;
    }
    H[0] = (H[0]! + a) >>> 0; H[1] = (H[1]! + b) >>> 0;
    H[2] = (H[2]! + c) >>> 0; H[3] = (H[3]! + d) >>> 0;
    H[4] = (H[4]! + e) >>> 0; H[5] = (H[5]! + f) >>> 0;
    H[6] = (H[6]! + g) >>> 0; H[7] = (H[7]! + h) >>> 0;
  }
  const out = new Uint8Array(32);
  const ov = new DataView(out.buffer);
  for (let i = 0; i < 8; i++) ov.setUint32(i * 4, H[i]!, false);
  return out;
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
