# User Interface

## Tech Stack

- **React** with TypeScript
- **Vite** for build tooling and development server
- **TailwindCSS** for styling
- **React Router** for client-side routing
- **TanStack React Query** for server state management and data fetching
- **Monaco Editor** (@monaco-editor/react) for JSON configuration editing
- **Axios** for HTTP requests
- **Lucide React** for icons

## Pages

### Dashboard (`/`)

The main landing page showing all bot instances in a table.

**Table Columns**:
| Column | Content |
|--------|---------|
| Name | Display name (clickable, navigates to detail page) |
| Status | Color-coded badge: green (running), yellow (creating), gray (stopped), red (error) |
| NodePorts | Chrome and Terminal port numbers with copy buttons |
| Created | Relative timestamp (e.g., "2 hours ago") |
| Actions | Button group: Chrome VNC, Terminal VNC, Start/Stop, Restart, Delete |

**Action Buttons**:
- **Chrome VNC** (monitor icon): Opens `window.open('http://192.168.1.104:<nodeport_chrome>/vnc.html?autoconnect=true')` — Chrome kiosk session. Disabled when instance is stopped.
- **Terminal VNC** (terminal icon): Opens `window.open('http://192.168.1.104:<nodeport_terminal>/vnc.html?autoconnect=true')` — xterm session. Disabled when instance is stopped.
- **Start** (play icon): Visible when status is `stopped`. Calls `POST /instances/{id}/start`.
- **Stop** (square icon): Visible when status is `running`. Calls `POST /instances/{id}/stop`.
- **Restart** (refresh icon): Visible when status is `running`. Calls `POST /instances/{id}/restart`.
- **Delete** (trash icon): Shows confirmation dialog before calling `DELETE /instances/{id}`.

**Header Area**:
- Page title "Openclaw Orchestrator"
- "New Instance" button (navigates to create page)
- "Settings" link/button (navigates to settings page)

**Polling**: The instance list auto-refreshes every 5 seconds via React Query's `refetchInterval` to keep status badges current.

---

### Create Instance (`/instances/new`)

A form page for creating a new bot instance.

**Form Fields**:

| Field | Type | Required | Default |
|-------|------|----------|---------|
| Display Name | Text input | Yes | -- |
| CPU Request | Text input | No | 500m |
| CPU Limit | Text input | No | 2000m |
| Memory Request | Text input | No | 1Gi |
| Memory Limit | Text input | No | 4Gi |
| Clawdbot Storage | Text input | No | 5Gi |
| Homebrew Storage | Text input | No | 10Gi |
| Clawd Storage | Text input | No | 5Gi |

**API Key Overrides Section** (collapsed by default):

| Field | Type | Description |
|-------|------|-------------|
| Anthropic API Key | Password input | Leave empty to use global key |
| OpenAI API Key | Password input | Leave empty to use global key |
| Brave API Key | Password input | Leave empty to use global key |

**Initial Config Section** (collapsed by default):
- Monaco editor for entering the initial clawdbot.json content
- Defaults to `{}` (empty JSON object)

**Form Actions**:
- "Create" button: Submits the form, navigates to instance detail on success
- "Cancel" button: Navigates back to dashboard

**Validation**:
- Display name is required and must be non-empty
- Resource values should match K8s format patterns (e.g., digits + unit suffix)
- JSON config must be valid JSON

---

### Instance Detail (`/instances/:id`)

A detail page with tabbed content.

**Header**:
- Instance display name as page title
- Status badge
- Action buttons (Chrome VNC, Terminal VNC, Start/Stop, Restart, Delete) -- same as dashboard row
- Back link to dashboard

**Tabs**:

#### Overview Tab

| Field | Value |
|-------|-------|
| Name | K8s-safe name (e.g., "bot-alpha") |
| Display Name | Human name |
| Status | Current status with badge |
| Chrome NodePort | Port number |
| Terminal NodePort | Port number |
| Chrome VNC URL | Clickable link |
| Terminal VNC URL | Clickable link |
| CPU | Request / Limit |
| Memory | Request / Limit |
| Storage (Clawdbot) | Size |
| Storage (Homebrew) | Size |
| Storage (Clawd) | Size |
| API Key Overrides | Shows which keys have overrides (without revealing values) |
| Created | Timestamp |
| Updated | Timestamp |

#### Config Tab

- Full-height Monaco editor with JSON syntax highlighting
- Loads current config from `GET /instances/{id}/config`
- "Save" button: Validates JSON, calls `PUT /instances/{id}/config`
- "Reset" button: Reverts editor content to last saved state
- Shows a warning that saving will restart the pod
- JSON validation errors displayed inline below the editor

#### Logs Tab

- Terminal-style log viewer with dark background and monospace font
- Connects to `GET /instances/{id}/logs` via EventSource (SSE)
- Auto-scrolls to bottom as new lines arrive
- "Clear" button to clear the display (does not clear actual logs)
- "Pause/Resume" toggle to stop/resume auto-scroll
- Shows most recent 100 lines on initial load, then streams new lines

---

### Settings (`/settings`)

Global settings page.

**API Keys Section**:

| Field | Type | Description |
|-------|------|-------------|
| Anthropic API Key | Password input | Shows masked value (****abcd), editable |
| OpenAI API Key | Password input | Shows masked value, editable |
| Brave API Key | Password input | Shows masked value, editable |

Each key field has a "Show/Hide" toggle button and a "Save" button (or a single "Save All" button).

**Default Resource Limits Section**:

| Field | Type | Default |
|-------|------|---------|
| Default CPU Request | Text input | 500m |
| Default CPU Limit | Text input | 2000m |
| Default Memory Request | Text input | 1Gi |
| Default Memory Limit | Text input | 4Gi |
| Default Clawdbot Storage | Text input | 5Gi |
| Default Homebrew Storage | Text input | 10Gi |
| Default Clawd Storage | Text input | 5Gi |

**Save**: Single "Save Settings" button that updates all changed fields via `PUT /api/v1/settings`.

**Note**: A banner at the top warns that changing global API keys will update all instances that don't have overrides.

---

## Component Hierarchy

```
App
  +-- Layout (header with nav)
  |     +-- Header ("Claworc", nav links)
  |
  +-- Routes
        +-- DashboardPage
        |     +-- InstanceTable
        |           +-- InstanceRow (per instance)
        |                 +-- StatusBadge
        |                 +-- ActionButtons
        |                       +-- VncChromeButton
        |                       +-- VncTerminalButton
        |                       +-- StartStopButton
        |                       +-- RestartButton
        |                       +-- DeleteButton (with ConfirmDialog)
        |
        +-- CreateInstancePage
        |     +-- InstanceForm
        |           +-- ResourceFields
        |           +-- ApiKeyFields (collapsible)
        |           +-- ConfigEditor (collapsible, Monaco)
        |
        +-- InstanceDetailPage
        |     +-- InstanceHeader (name, status, actions)
        |     +-- TabNav (Overview | Config | Logs)
        |     +-- OverviewTab
        |     +-- ConfigTab
        |     |     +-- MonacoEditor
        |     +-- LogsTab
        |           +-- LogViewer (SSE consumer)
        |
        +-- SettingsPage
              +-- ApiKeySettings
              +-- DefaultResourceSettings
```

## VNC Integration

The VNC viewers are **not** embedded in the Claworc UI. Instead, they open as separate browser windows/tabs:

```javascript
// Chrome VNC (kiosk mode)
window.open(
  `http://192.168.1.104:${instance.nodeport_chrome}/vnc.html?autoconnect=true`,
  `vnc-chrome-${instance.name}`
);

// Terminal VNC (xterm)
window.open(
  `http://192.168.1.104:${instance.nodeport_terminal}/vnc.html?autoconnect=true`,
  `vnc-term-${instance.name}`
);
```

This approach was chosen because:
- noVNC's web client works standalone without modification
- Embedding in an iframe would require CSP configuration
- Separate windows give the user full screen real estate for each session
- The `autoconnect=true` parameter skips the noVNC connection dialog
- Each session has a unique window name to prevent duplicates

## Responsive Design

The UI is designed for desktop use (internal tool). Minimum supported viewport is 1280px wide. The interface uses TailwindCSS utility classes with a clean, functional aesthetic suitable for an admin dashboard.

## Project Structure

```
frontend/
  package.json
  vite.config.ts
  tsconfig.json
  index.html
  src/
    main.tsx
    App.tsx
    api/
      client.ts              -- Axios instance with base URL
      instances.ts           -- Instance API functions
      settings.ts            -- Settings API functions
    pages/
      DashboardPage.tsx
      CreateInstancePage.tsx
      InstanceDetailPage.tsx
      SettingsPage.tsx
    components/
      Layout.tsx
      InstanceTable.tsx
      InstanceRow.tsx
      StatusBadge.tsx
      ActionButtons.tsx
      ConfirmDialog.tsx
      InstanceForm.tsx
      MonacoConfigEditor.tsx
      LogViewer.tsx
      ApiKeySettings.tsx
      ResourceSettings.tsx
    hooks/
      useInstances.ts        -- React Query hooks for instance data
      useSettings.ts         -- React Query hooks for settings
      useInstanceLogs.ts     -- SSE hook for log streaming
    types/
      instance.ts            -- TypeScript interfaces
      settings.ts
```
