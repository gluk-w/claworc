---
name: doc_conventions
description: Documentation structure and conventions for website_docs/ (Mintlify-based end-user docs)
type: project
---

## website_docs/ structure

- Mintlify-based docs site, pages are `.mdx` with YAML frontmatter
- Navigation defined in `docs.json` under `navigation.tabs[0].groups`
- Main instance docs live in `instances.mdx` (top-level)
- Models docs under `models/` subdirectory
- Security/access docs: `authentication.mdx`, `ssh.mdx`, `environment-variables.mdx`
- `essentials/settings.mdx` is Mintlify boilerplate (NOT Claworc settings docs)
- CLAUDE.md in website_docs/ has terminology guide and style preferences

## Key conventions

- Use "instance" not "bot" or "agent container"
- Bold for UI elements: **Settings**, **Edit**, **Save**
- Code formatting for values: `500m`, `1920x1080`, `:latest`
- Mintlify components: `<Note>`, `<Warning>` for callouts
- No API reference sections — docs are for end users only
- Second person ("you"), active voice, sentence case headings

**Why:** Per user feedback, docs should focus on UI-visible behavior and user workflows.
**How to apply:** Always check instances.mdx first for instance-related changes. Check docs.json if adding new pages.
