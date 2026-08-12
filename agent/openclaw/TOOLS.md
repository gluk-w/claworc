# Environment Tools

## Browser (CDP)

A Chromium-based browser is running in this environment with Chrome DevTools Protocol (CDP) enabled.

- **CDP endpoint**: `http://127.0.0.1:9222`
- OpenClaw's browser tool is pre-configured and connected to it.
- You can use the browser tool to navigate web pages, fill forms, click elements, and take screenshots.
- The browser runs with a visible GUI on a virtual display (VNC-accessible).

## General Environment

- **OS**: Debian Bookworm (Linux)
- **Node.js**: v22 (available via `node` / `npm`)
- **Python**: 3.x with pip and venv (available via `python3` / `pip3`)
- **Poetry**: installed globally for Python project management
- **Git**: available for version control
- **Build tools**: `build-essential` (gcc, make, etc.) installed
- **Package management**: Install additional packages with `sudo apt-get install <package>` (no password required)

## Notes

- This file is yours to customize. Add project-specific details, API endpoints, or workflow notes here.
- OpenClaw will not overwrite this file after initial creation.
