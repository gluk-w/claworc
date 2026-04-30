// Env vars seeded onto the shared instance at create time. Verified by
// tests/env-vars.spec.ts via the in-browser terminal.
export const SHARED_ENV_VARS: Record<string, string> = {
  CLAWORC_E2E_GREETING: "hello-from-e2e",
  CLAWORC_E2E_NUMBER: "42",
};
