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

There are two roles: **admin** and **user**. The `user` role additionally has a per-user **Can create instances** flag that grants self-service instance creation and backup restore.

| Capability | Admin | User | User w/ "Can create instances" |
|---|---|---|---|
| View instances | All | Assigned only | Assigned only |
| Start / stop / restart instances | All | Assigned only | Assigned only |
| Access instance chat, terminal, VNC, files, logs, config | All | Assigned only | Assigned only |
| Create / list / download / delete backups | All instances | Assigned only | Assigned only |
| Manage backup schedules | All | Schedules whose instances are all assigned | Schedules whose instances are all assigned |
| Create instances (creator auto-assigned to the new instance) | Yes | No | Yes |
| Restore from backup | Yes | No | Yes (assigned target only) |
| Delete instances | Yes | No | No |
| Manage settings | Yes | No | No |
| Manage users | Yes | No | No |
| Register passkeys | Yes | Yes | Yes |

## User Management

Admins can manage users from the **Users** page in the dashboard:

- **Create User** — set username, password, role (admin or user), and (for the "user" role) the **Can create instances** flag.
- **Delete User** — removes the user and invalidates their sessions.
- **Change Role** — promote a user to admin or demote to user.
- **Toggle Can-create-instances** — grant or revoke self-service instance creation and backup restore for a non-admin user.
- **Reset Password** — set a new password and invalidate all sessions for that user.
- **Assign Instances** — for users with the "user" role, assign which instances they can access. New instances created by a user with **Can create instances** are auto-assigned to the creator.

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
| `CLAWORC_RP_ORIGINS` | `http://localhost:8000` | WebAuthn relying party origins (your dashboard URL). Comma-separated for multiple values. |
| `CLAWORC_RP_ID` | `localhost` | WebAuthn relying party ID (your domain name) |

For production deployments, set these to match your actual domain:

```bash
CLAWORC_RP_ORIGINS=https://claworc.example.com
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

### Authenticated (per-instance access enforced)

Backup endpoints require authentication; non-admin callers must be assigned to the instance referenced (directly or via the backup row). `POST /api/v1/backups/{id}/restore` and `POST /api/v1/instances` additionally require the **Can create instances** flag (or admin).

| Method | Endpoint | Description |
|---|---|---|
| POST | `/api/v1/instances` | Create a new instance (creator auto-assigned) |
| POST | `/api/v1/instances/{id}/clone` | Clone an instance |
| POST | `/api/v1/instances/{id}/backups` | Create a backup |
| GET | `/api/v1/instances/{id}/backups` | List backups for an instance |
| GET | `/api/v1/backups` | List backups (admins see all; users see assigned only) |
| GET | `/api/v1/backups/{id}` | Get backup details |
| DELETE | `/api/v1/backups/{id}` | Delete a backup |
| POST | `/api/v1/backups/{id}/cancel` | Cancel a running backup |
| POST | `/api/v1/backups/{id}/restore` | Restore a backup (requires "Can create instances") |
| GET | `/api/v1/backups/{id}/download` | Download the backup archive |
| POST | `/api/v1/backup-schedules` | Create a backup schedule |
| GET | `/api/v1/backup-schedules` | List schedules accessible to the caller |
| PUT | `/api/v1/backup-schedules/{id}` | Update a schedule |
| DELETE | `/api/v1/backup-schedules/{id}` | Delete a schedule |

### Admin Only

| Method | Endpoint | Description |
|---|---|---|
| DELETE | `/api/v1/instances/{id}` | Delete an instance |
| GET | `/api/v1/users` | List all users |
| POST | `/api/v1/users` | Create a user |
| DELETE | `/api/v1/users/{id}` | Delete a user |
| PUT | `/api/v1/users/{id}/role` | Update user role |
| PUT | `/api/v1/users/{id}/permissions` | Update user permission flags (e.g. `can_create_instances`) |
| GET | `/api/v1/users/{id}/instances` | Get assigned instances |
| PUT | `/api/v1/users/{id}/instances` | Set assigned instances |
| POST | `/api/v1/users/{id}/reset-password` | Reset user password |
