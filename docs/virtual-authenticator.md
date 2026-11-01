# Virtual authenticator (simulated fingerprint / security key)

An Agent's on-demand browser can present a **simulated WebAuthn device** to websites, so sites
that offer passkeys or security-key 2FA work without any physical hardware. The device is a
Chromium virtual authenticator driven by the control plane over CDP. Passkeys it creates are
persisted (encrypted) by Claworc and re-installed on every browser start, so they survive browser pod
restarts, idle reaping, and control plane restarts.

Code: `control-plane/internal/browserauthn/` (manager), `internal/database/browser_passkeys.go`
(persistence), `internal/handlers/passkeys.go` (API), `frontend/src/common/components/VirtualAuthenticatorSection.tsx` (UI).

## Settings

Per Agent, `Instance.VirtualAuthenticator` (`virtual_authenticator` in the API):

| Value          | UI label                     | Simulated device                                        |
|----------------|------------------------------|---------------------------------------------------------|
| `""` (default) | Off                          | none                                                    |
| `platform`     | Fingerprint (built-in)       | platform authenticator, transport `internal` (Touch ID-like) |
| `security_key` | Security key (USB)           | roaming authenticator, transport `usb` (YubiKey-like)   |
| `both`         | Fingerprint and security key | both, side by side                                      |

All devices are CTAP 2.1, support resident keys (passkeys), large blobs and credBlob, and always
pass user presence and user verification automatically (`isUserVerified`,
`automaticPresenceSimulation`). Changing the setting applies to a running browser immediately (no restart needed).
Turning it off keeps stored passkeys; they come back when it is turned on again.

Legacy embedded-browser Agents (`openclaw-vnc-*` images) are not supported. The UI asks the user to migrate first.

## How it works

Chromium virtual authenticators belong to the CDP session that created them and exist per tab.
The `browserauthn.Manager` therefore keeps one long-lived, browser-level CDP connection for each
running browser session:

1. **Spawn hook.** `BrowserBridge.doSpawn` calls `Manager.Start` (via `SetOnSessionReady`) after CDP
   answers and *before* the session is marked `running`. OpenClaw blocks on `EnsureSession`, so the
   authenticator is already in place when the agent sends its first CDP command.
2. **Auto-attach.** `Target.setAutoAttach{waitForDebuggerOnStart, flatten, filter:[page]}` pauses every
   new tab or popup. For each one the manager calls `WebAuthn.enable`, then
   `WebAuthn.addVirtualAuthenticator` for each configured kind, then `WebAuthn.addCredential` for every
   stored passkey of that kind, and finally `Runtime.runIfWaitingForDebugger`. Tabs that already exist
   attach without pausing, and `Start` waits until each one is configured. The manager always resumes
   the tab, even if setup fails, so a tab can never hang.
3. **Write-through.**
   - `WebAuthn.credentialAdded` / `credentialUpdated` → upsert the row, then copy the credential into
     every other tab.
   - `WebAuthn.credentialAsserted` → advance `sign_count` and `last_used_at`, then refresh the other
     tabs' copies, so the counter never goes backwards across tabs or restarts.
   - `WebAuthn.credentialDeleted` → delete the row.
4. **Reconcile.** Every 30 s, and once more on stop, `WebAuthn.getCredentials` runs on every tab. Any
   unknown credential, or any sign-count increase, is persisted. This catches missed events.
5. **Reconnect and supervision.**
   - If the CDP connection drops while the browser is still running (for example, Chromium is
     restarted by s6), the manager reconnects with backoff and reinstalls everything.
   - If the browser session ended, the manager exits, and the next spawn starts it again.
   - `Manager.Run` checks every 30 s for running browsers that have the setting on but no manager.
     This covers control plane restarts.

The manager dials CDP through `BrowserBridge.DialCDPIfRunning`. That path never spawns a browser
and does not count as activity, so this long-lived connection doesn't keep an idle browser alive.

## Persistence

Table `browser_passkeys` (model `BrowserPasskey`), one row per credential, unique on
`(instance_id, credential_id)`:

- `private_key` (PKCS#8 P-256, as CDP returns it) and `large_blob` are **Fernet-encrypted** with the
  global key (`utils.Encrypt`).
- Metadata: `rp_id`, `user_name`, `user_display_name`, `user_handle`, `is_resident`, `sign_count`,
  backup flags, `created_at`, and `last_used_at`. Writes never lower `sign_count`.

Lifecycle:

- **Deleting an Agent** deletes its passkeys.
- **Cloning an Agent** copies the setting and the passkeys, the same way the browser profile is cloned.
- **Instance backups** (`docs/backups.md`) archive the container filesystem only, so they do *not*
  include passkeys. Passkeys live in the control plane database, so back up `CLAWORC_DATA_PATH`
  (SQLite) or your PostgreSQL database to keep them. Restoring needs the same Fernet key (`settings.fernet_key`).

## API

- `GET /api/v1/instances/{id}/passkeys` lists passkeys: `id`, `authenticator`, `rp_id`, `user_name`,
  `user_display_name`, `is_resident`, `sign_count`, `created_at`, `last_used_at`. Key material is never returned.
- `DELETE /api/v1/instances/{id}/passkeys/{passkeyId}` deletes the row and evicts the credential from every open tab.
- `virtual_authenticator` is accepted on instance create and update, and returned on instance responses.

All of these use the standard instance access check (`middleware.CanAccessInstance`).

## Limits

- **Software keys.** Sites that require attestation from a specific vendor (some enterprise IdPs,
  FIDO MDS allow-lists) will reject them. Most consumer sites ask for `none` attestation and accept them.
- **Top-level pages only.** The manager attaches to page targets. WebAuthn calls from out-of-process
  iframes depend on Chromium resolving the virtual authenticator through the parent frame.
- **"Both" on registration.** If a site doesn't constrain `authenticatorAttachment`, either device may
  answer a registration. Only the credential reported in `credentialAdded` is persisted.
- **Agent-side WebAuthn calls.** If the agent itself calls `WebAuthn.enable` /
  `addVirtualAuthenticator` over its own CDP session, its authenticators are separate and are not persisted.

## Security model

These passkeys are **not** hardware-bound. Anyone who can drive the Agent's browser can use them:
the agent itself, users with access to the Agent, and anything with CDP access. Treat them like
stored passwords. The UI hint says this explicitly. Private keys are encrypted at rest, never leave the
control plane over the API, and go only to the Agent's own browser over the existing
SSH-tunnelled CDP channel.

## Testing

- Unit tests run against a fake CDP server and cover setup order, encryption, fan-out, sign-count
  propagation, reconcile, reconnect, and not reconnecting once the session stopped:
  `go test -vet=off -race ./internal/browserauthn/`
- An end-to-end test against a real headless Chrome registers a passkey, replaces the browser with a fresh
  profile, and signs in again:
  `CLAWORC_TEST_CHROME="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" go test -vet=off -run TestRealChrome ./internal/browserauthn/`
