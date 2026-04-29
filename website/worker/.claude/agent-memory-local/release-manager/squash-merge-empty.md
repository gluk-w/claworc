---
name: Squash merge can land as no-op when branch contains revert + reapply
description: If a feature branch has commits that revert and then re-apply changes, GitHub squash merge can produce an empty commit on main when main has independently moved past those changes
type: feedback
---

If a PR branch was based on an old commit (before some intervening merge to main) and contains a sequence like "revert X, then re-add X", the squash merge into current main may land as an empty commit. The squash applies the net diff vs the merge-base; if main already has the revert applied, the re-add can cancel against the revert in the squash diff producing no change for those files.

**Why:** Encountered on 2026-04-28. PR #109 was based on commit before #108 merged. Branch contained: (1) website/ revert matching #108, (2) re-apply of #105's worker analytics. After merging, blob hashes for website/worker/index.ts on main were identical pre- and post-merge — squash commit was effectively empty. Path-filtered workflows (Website Worker) didn't even trigger. Had to cut a fresh branch from current main and re-checkout the worker files there (PR #110).

**How to apply:** After merging a PR, always verify the merge commit actually changed the expected files. Check `git diff <pre-merge> <merge-commit> -- <expected-paths>` or `git diff-tree -r`. If empty: cut a fresh branch from current main and re-apply only the intended changes. Don't trust the PR diff view alone — it shows branch-vs-base, not what landed.
