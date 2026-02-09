# Claworc Dashboard

Claworc gives you a clean, web-based dashboard to manage all your OpenClaw agent instances from one place. 
Launch new isolated agents, monitor their status, access their browser and terminal sessions, 
tweak configurations, and stream logs -- all without touching a command line.

## Dashboard

The dashboard is your home base. It shows every agent instance at a glance in a simple table with live status updates.

Each instance displays:

- **Name** -- click to dive into the full detail view.
- **Status** -- a color-coded badge so you can tell what's happening instantly: green for running, yellow for creating, gray for stopped, red for error.
- **Quick actions** -- start, stop, restart, open Chrome, open Terminal, or delete -- all one click away, right from the table row.

Status badges refresh automatically every few seconds, so you always see the latest state without hitting reload.

## Creating a New Instance

Spinning up a new agent takes seconds. Just give it a name, and Claworc handles the rest with sensible defaults.

**What you can configure:**

- **Display Name** -- a human-friendly name for your agent (required).
- **Resource Limits** -- fine-tune CPU, memory, and storage allocations if you need to. Defaults are already set for most workloads, so you can skip this entirely if you're just getting started.
- **API Key Overrides** -- each instance inherits your global API keys automatically. Need a specific instance to use a different Anthropic, OpenAI, or Brave key? Just expand this section and enter them. Leave it blank to use the global keys.
- **Initial Configuration** -- power users can provide a custom `clawdbot.json` configuration using the built-in code editor with full JSON syntax highlighting and validation.

Hit "New Instance" and you'll be taken straight to your new instance's detail page.

## Instance Detail

Click any instance name to view all details, organized into three tabs.

### Overview

A clear summary of the instance's current state:

- Status, resource allocations (CPU, memory, storage), and connection details.
- Direct links to open the Chrome browser session or Terminal session.
- Timestamps showing when the instance was created and last updated.
- A quick view of which API key overrides are active (values are always kept hidden for security).

### Configuration

Edit your agent's configuration directly in the browser using a full-featured code editor with JSON syntax highlighting. Make your changes, hit "Save," and Claworc applies the new configuration automatically. If you make a mistake, the "Reset" button reverts to the last saved version.

### Live Logs

Stream your agent's logs in real time with a terminal-style viewer. Logs auto-scroll as new lines arrive, and you can pause scrolling to inspect something specific. The viewer loads the most recent 100 lines on open, then keeps streaming as the agent runs.

## Chrome and Terminal Access

Every agent instance comes with its own isolated Chrome browser and terminal, accessible directly from the dashboard. Clicking the Chrome or Terminal button opens a full-screen remote session in a new browser tab -- no extra software or VNC clients needed.

Each session opens in its own dedicated window, giving you maximum screen space to work alongside your agent.

## Settings

The Settings page is where you manage your global configuration.

### API Keys

Set your Anthropic, OpenAI, and Brave API keys once, and every instance picks them up automatically. Keys are encrypted at rest and displayed masked in the UI for security. You can reveal, edit, or update them at any time.

Changing a global key automatically propagates to all instances that don't have their own override -- no need to update each instance individually.

### Default Resource Limits

Set the default CPU, memory, and storage allocations for new instances. These defaults apply whenever you create an instance without specifying custom values, keeping your workflow fast while still giving you full control when you need it.
