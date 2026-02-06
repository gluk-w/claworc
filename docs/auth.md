# Authentication & Authorization

Claworc uses username/password authentication with optional passkey (WebAuthn) support. All API endpoints except `/health` require authentication.

## First-Time Setup

When Claworc starts with no users in the database, it enters **setup mode**. The first time you open the dashboard, you will see a "Create Admin Account" form instead of the login page. This creates the initial admin user.

Alternatively, you can create the admin user via CLI:

```bash
# Docker
docker exec claworc-dashboard /app/claworc --create-admin --username admin --password <password>

# Kubernetes
kubectl exec deploy/claworc -n claworc -- /app/claworc --create-admin --username admin --password <password>
```

## Roles

There are two roles: **admin** and **user**.

| Capability | Admin | User |
|---|---|---|
| View instances | All | Assigned only |
| Start / stop / restart instances | All | Assigned only |
| Access instance chat, terminal, VNC, files, logs, config | All | Assigned only |
| Create / delete instances | Yes | No |
| Manage settings | Yes | No |
| Manage users | Yes | No |
| Register passkeys | Yes | Yes |

## User Management

Admins can manage users from the **Users** page in the dashboard:

- **Create User** — set username, password, and role (admin or user).
- **Delete User** — removes the user and invalidates their sessions.
- **Change Role** — promote a user to admin or demote to user.
- **Reset Password** — set a new password and invalidate all sessions for that user.
- **Assign Instances** — for users with the "user" role, assign which instances they can access.

## Instance Assignment

Users with the "user" role can only see and interact with instances that an admin has explicitly assigned to them. Admins always have access to all instances.

To assign instances to a user, go to **Users**, and use the instance assignment feature for the target user.

## Sessions

- Sessions are stored **in-memory** using HTTP-only cookies.
- Sessions expire after **1 hour**.
- On server restart, all sessions are cleared — users must re-login.
- WebSocket connections (chat, terminal, VNC) authenticate automatically via the session cookie.

## Passkeys (WebAuthn)

Passkeys provide passwordless login using biometric or hardware security keys.

### Registering a Passkey

1. Log in with your username and password.
2. Register a passkey from your account (the browser will prompt for biometric or security key).
3. Give the passkey a name for identification.

### Logging in with a Passkey

1. On the login page, click **"Sign in with Passkey"**.
2. Follow the browser prompt to authenticate with your registered passkey.

### Managing Passkeys

- View your registered passkeys from the WebAuthn credentials endpoint.
- Delete passkeys you no longer use.

## Password Reset

### Via Admin UI

An admin can reset any user's password from the **Users** page. This immediately invalidates all sessions for that user.

### Via CLI

Use the included `reset-password.sh` script:

```bash
./reset-password.sh
```

The script auto-detects your deployment mode (Docker or Kubernetes) and prompts for username and new password.

You can also run the command directly:

```bash
# Docker
docker exec claworc-dashboard /app/claworc --reset-password --username <user> --password <new-password>

# Kubernetes
kubectl exec deploy/claworc -n claworc -- /app/claworc --reset-password --username <user> --password <new-password>
```

Note: CLI password reset cannot invalidate in-memory sessions. Existing sessions will expire naturally within 1 hour. For immediate invalidation, use the admin UI.

## Configuration

The following environment variables configure authentication behavior:

| Variable | Default | Description |
|---|---|---|
| `CLAWORC_RP_ORIGIN` | `http://localhost:8000` | WebAuthn relying party origin (your dashboard URL) |
| `CLAWORC_RP_ID` | `localhost` | WebAuthn relying party ID (your domain name) |

For production deployments, set these to match your actual domain:

```bash
CLAWORC_RP_ORIGIN=https://claworc.example.com
CLAWORC_RP_ID=claworc.example.com
```

## API Endpoints

### Public (no auth required)

| Method | Endpoint | Description |
|---|---|---|
| POST | `/api/v1/auth/login` | Login with username/password |
| GET | `/api/v1/auth/setup-required` | Check if first-time setup is needed |
| POST | `/api/v1/auth/setup` | Create initial admin (only when no users exist) |
| POST | `/api/v1/auth/webauthn/login/begin` | Begin passkey login |
| POST | `/api/v1/auth/webauthn/login/finish` | Complete passkey login |

### Authenticated

| Method | Endpoint | Description |
|---|---|---|
| POST | `/api/v1/auth/logout` | Logout (clear session) |
| GET | `/api/v1/auth/me` | Get current user info |
| POST | `/api/v1/auth/webauthn/register/begin` | Begin passkey registration |
| POST | `/api/v1/auth/webauthn/register/finish` | Complete passkey registration |
| GET | `/api/v1/auth/webauthn/credentials` | List registered passkeys |
| DELETE | `/api/v1/auth/webauthn/credentials/{id}` | Delete a passkey |

### Admin Only

| Method | Endpoint | Description |
|---|---|---|
| GET | `/api/v1/users` | List all users |
| POST | `/api/v1/users` | Create a user |
| DELETE | `/api/v1/users/{id}` | Delete a user |
| PUT | `/api/v1/users/{id}/role` | Update user role |
| GET | `/api/v1/users/{id}/instances` | Get assigned instances |
| PUT | `/api/v1/users/{id}/instances` | Set assigned instances |
| POST | `/api/v1/users/{id}/reset-password` | Reset user password |
